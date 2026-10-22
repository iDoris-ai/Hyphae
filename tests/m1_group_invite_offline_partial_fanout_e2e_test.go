//go:build integration

package integration_test

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	_ "modernc.org/sqlite"
)

type egDelivery struct {
	RecipientNpub string `json:"recipient_npub"`
	EventID       string `json:"event_id"`
	QueueID       string `json:"queue_id"`
	State         string `json:"state"`
	RelayAcks     int    `json:"relay_acks"`
}

type egFanout struct {
	Queued     int          `json:"queued"`
	Failed     int          `json:"failed"`
	Partial    bool         `json:"partial"`
	LogicalID  string       `json:"logical_id"`
	Recipients []egDelivery `json:"recipients"`
}

type egHistory struct {
	Messages []struct {
		ID        string `json:"id"`
		Plaintext string `json:"plaintext"`
	} `json:"messages"`
}

// TestM1GroupInviteOfflinePartialFanoutExactlyOnce creates the group through
// the public CLI, accepts both invites, and sends one partial fanout. The
// signed outbox event is handed to the local relay. Each member keeps one
// group history row, and a later daemon start adds no second receive line.
func TestM1GroupInviteOfflinePartialFanoutExactlyOnce(t *testing.T) {
	root := projectRoot(t)
	temp := t.TempDir()
	cliBin := filepath.Join(temp, "hyphae")
	relayBin := filepath.Join(temp, "hyphae-relay")
	buildBinary(t, root, cliBin, "./cmd/hyphae")
	buildBinary(t, root, relayBin, "./cmd/hyphae-relay")

	home := filepath.Join(temp, "home")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	alice := egIdentity(t, cliBin, home, "alice", true)
	bob := egIdentity(t, cliBin, home, "bob", false)
	carol := egIdentity(t, cliBin, home, "carol", false)

	port := freeLoopbackPort(t)
	dataDir := filepath.Join(temp, "relay-data")
	relay := startRelay(t, relayBin, dataDir, port)
	relayURL := "ws://127.0.0.1:" + strconv.Itoa(port)
	egMust(t, cliBin, home, "relay", "set", "--relay", relayURL)

	groupID := egGroup(t, egMust(t, cliBin, home, "groupchat", "create", "--name", "m1-exit", "--identity", "alice", "--invitee", bob, "--invitee", carol))
	invites := egInvites(t, cliBin, home, groupID)
	for _, member := range []struct{ nick, npub string }{{"bob", bob}, {"carol", carol}} {
		egRun(t, cliBin, home, nil, "groupchat", "accept", "--identity", member.nick, "--invite", egInvite(t, invites, member.npub, groupID))
	}
	for _, member := range []struct{ nick, npub string }{{"alice", alice}, {"bob", bob}, {"carol", carol}} {
		if got := egState(t, home, member.npub, groupID); got != "active" {
			t.Fatalf("%s group state %s", member.nick, got)
		}
	}

	const body = "m1 group fanout once"
	logical := egOpaque(t)
	stdout, stderr, code := egRun(t, cliBin, home, []string{"HYPHAE_FAIL_GROUP_RECIPIENT=" + carol},
		"groupchat", "send", "--group-id", groupID, "--logical-id", logical, "--body", body, "--identity", "alice", "--max-retries", "1")
	if code == 0 || !strings.Contains(stderr, "partial fanout queued 1 failed 1") {
		t.Fatalf("partial send code %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}

	status := egFanoutOf(t, cliBin, home, groupID, logical)
	if !status.Partial || status.Queued != 1 || status.Failed != 1 || status.LogicalID != logical || len(status.Recipients) != 2 {
		t.Fatalf("partial status %#v", status)
	}
	bobRow := egByNpub(t, status.Recipients, bob)
	carolRow := egByNpub(t, status.Recipients, carol)
	if bobRow.State != "queued" || carolRow.State != "failed" || bobRow.EventID == "" || carolRow.EventID == "" || bobRow.EventID == carolRow.EventID || bobRow.QueueID == "" || carolRow.QueueID == "" || bobRow.RelayAcks != 0 || carolRow.RelayAcks != 0 {
		t.Fatalf("fanout bob %#v carol %#v", bobRow, carolRow)
	}
	if egInt(t, home, `SELECT COUNT(*) FROM messages`) != 0 {
		t.Fatal("send wrote a DM row")
	}
	if egInt(t, home, `SELECT COUNT(*) FROM groupchat_messages WHERE local_npub=? AND group_id=? AND logical_id=?`, alice, groupID, logical) != 1 {
		t.Fatal("sender history row missing")
	}
	if egInt(t, home, `SELECT COUNT(*) FROM groupchat_messages WHERE local_npub=? AND group_id=? AND logical_id=?`, bob, groupID, logical) != 0 ||
		egInt(t, home, `SELECT COUNT(*) FROM groupchat_messages WHERE local_npub=? AND group_id=? AND logical_id=?`, carol, groupID, logical) != 0 {
		t.Fatal("a recipient stored the message before the daemon")
	}
	if got := egPlaintexts(t, cliBin, home, groupID, "alice"); len(got) != 1 || got[0] != body {
		t.Fatalf("alice history %#v", got)
	}
	if len(egPlaintexts(t, cliBin, home, groupID, "bob")) != 0 || len(egPlaintexts(t, cliBin, home, groupID, "carol")) != 0 {
		t.Fatal("recipient history was visible before the daemon")
	}

	bobEvent := egFrozen(t, home, bobRow.EventID, body)
	bobSig := egDeliver(t, relayURL, bobEvent)
	relay.stop()
	relay = startRelay(t, relayBin, dataDir, port)
	egSameEvent(t, relayURL, bobRow.EventID, bobSig)

	bobDaemon := startDaemonCLI(t, cliBin, home, "bob", relayURL)
	egWaitScan(t, bobDaemon, 1, 45*time.Second)
	bobDaemon.stopAndWait(t, 3*time.Second)
	if got := egPlaintexts(t, cliBin, home, groupID, "bob"); len(got) != 1 || got[0] != body {
		t.Fatalf("bob history %#v", got)
	}
	if egInt(t, home, `SELECT COUNT(*) FROM groupchat_messages WHERE local_npub=? AND group_id=? AND logical_id=?`, bob, groupID, logical) != 1 {
		t.Fatal("bob stored more than one row")
	}
	if len(egPlaintexts(t, cliBin, home, groupID, "carol")) != 0 {
		t.Fatal("carol observed the message before recovery")
	}

	retried := egFanoutOf(t, cliBin, home, groupID, logical, "groupchat", "retry", "--group-id", groupID, "--logical-id", logical, "--recipient", carol, "--identity", "alice", "--json")
	bobAfter := egByNpub(t, retried.Recipients, bob)
	carolAfter := egByNpub(t, retried.Recipients, carol)
	if bobAfter.EventID != bobRow.EventID || bobAfter.QueueID != bobRow.QueueID || carolAfter.EventID != carolRow.EventID || carolAfter.QueueID == carolRow.QueueID || carolAfter.State != "queued" {
		t.Fatalf("retry bob %#v carol %#v", bobAfter, carolAfter)
	}
	if egInt(t, home, `SELECT COUNT(*) FROM groupchat_messages WHERE group_id=? AND logical_id=?`, groupID, logical) != 2 {
		t.Fatal("retry changed history rows")
	}

	carolEvent := egFrozen(t, home, carolAfter.EventID, body)
	if carolEvent == bobEvent {
		t.Fatal("recipients share one signed event")
	}
	carolSig := egDeliver(t, relayURL, carolEvent)
	relay.stop()
	relay = startRelay(t, relayBin, dataDir, port)
	egSameEvent(t, relayURL, bobRow.EventID, bobSig)
	egSameEvent(t, relayURL, carolAfter.EventID, carolSig)

	carolDaemon := startDaemonCLI(t, cliBin, home, "carol", relayURL)
	egWaitScan(t, carolDaemon, 1, 45*time.Second)
	carolDaemon.stopAndWait(t, 3*time.Second)
	if got := egPlaintexts(t, cliBin, home, groupID, "carol"); len(got) != 1 || got[0] != body {
		t.Fatalf("carol history %#v", got)
	}
	for _, member := range []struct{ nick, npub string }{{"alice", alice}, {"bob", bob}, {"carol", carol}} {
		if egInt(t, home, `SELECT COUNT(*) FROM groupchat_messages WHERE local_npub=? AND group_id=? AND logical_id=?`, member.npub, groupID, logical) != 1 {
			t.Fatalf("%s history row count", member.nick)
		}
		got := egPlaintexts(t, cliBin, home, groupID, member.nick)
		if len(got) != 1 || got[0] != body {
			t.Fatalf("%s history %#v", member.nick, got)
		}
	}
	if egInt(t, home, `SELECT COUNT(*) FROM messages`) != 0 {
		t.Fatal("receive wrote a DM row")
	}

	for _, nick := range []string{"bob", "carol", "alice"} {
		again := startDaemonCLI(t, cliBin, home, nick, relayURL)
		egWaitScan(t, again, 0, 45*time.Second)
		again.stopAndWait(t, 3*time.Second)
	}
	for _, member := range []struct{ nick, npub string }{{"alice", alice}, {"bob", bob}, {"carol", carol}} {
		if egInt(t, home, `SELECT COUNT(*) FROM groupchat_messages WHERE local_npub=? AND group_id=? AND logical_id=?`, member.npub, groupID, logical) != 1 {
			t.Fatalf("restart added a %s row", member.nick)
		}
	}
	if egInt(t, home, `SELECT COUNT(*) FROM messages`) != 0 {
		t.Fatal("restart wrote a DM row")
	}
	settled := egFanoutOf(t, cliBin, home, groupID, logical)
	if egByNpub(t, settled.Recipients, carol).EventID != carolRow.EventID || egByNpub(t, settled.Recipients, bob).EventID != bobRow.EventID {
		t.Fatal("restart changed an event id")
	}
}

func egIdentity(t *testing.T, bin, home, nickname string, makeDefault bool) string {
	t.Helper()
	args := []string{"identity", "create", "--nickname", nickname, "--json"}
	if makeDefault {
		args = append(args, "--default")
	}
	created := egDecode[struct {
		Npub string `json:"npub"`
	}](t, egMust(t, bin, home, args...))
	if !strings.HasPrefix(created.Npub, "npub1") {
		t.Fatalf("identity %s", nickname)
	}
	return created.Npub
}

func egInvites(t *testing.T, bin, home, groupID string) []struct {
	InviteID, InviteeNpub, GroupID string
} {
	t.Helper()
	var payload struct {
		Invites []struct {
			InviteID    string `json:"invite_id"`
			InviteeNpub string `json:"invitee_npub"`
			GroupID     string `json:"group_id"`
		} `json:"invites"`
	}
	payload = egDecode[struct {
		Invites []struct {
			InviteID    string `json:"invite_id"`
			InviteeNpub string `json:"invitee_npub"`
			GroupID     string `json:"group_id"`
		} `json:"invites"`
	}](t, egMust(t, bin, home, "groupchat", "invite", "list", "--group", groupID, "--identity", "alice", "--json"))
	out := make([]struct {
		InviteID, InviteeNpub, GroupID string
	}, len(payload.Invites))
	for i, item := range payload.Invites {
		out[i].InviteID = item.InviteID
		out[i].InviteeNpub = item.InviteeNpub
		out[i].GroupID = item.GroupID
	}
	return out
}

func egInvite(t *testing.T, invites []struct {
	InviteID, InviteeNpub, GroupID string
}, npub, groupID string) string {
	t.Helper()
	for _, item := range invites {
		if item.InviteeNpub == npub && item.GroupID == groupID && item.InviteID != "" {
			return item.InviteID
		}
	}
	t.Fatalf("missing invite for %s", npub)
	return ""
}

func egFanoutOf(t *testing.T, bin, home, groupID, logical string, args ...string) egFanout {
	t.Helper()
	if len(args) == 0 {
		args = []string{"groupchat", "status", "--group-id", groupID, "--logical-id", logical, "--identity", "alice", "--json"}
	}
	return egDecode[egFanout](t, egMust(t, bin, home, args...))
}

func egPlaintexts(t *testing.T, bin, home, groupID, nick string) []string {
	t.Helper()
	payload := egDecode[egHistory](t, egMust(t, bin, home, "groupchat", "history", "--group-id", groupID, "--identity", nick, "--json"))
	out := make([]string, 0, len(payload.Messages))
	for _, message := range payload.Messages {
		out = append(out, message.Plaintext)
	}
	return out
}

func egByNpub(t *testing.T, rows []egDelivery, npub string) egDelivery {
	t.Helper()
	for _, row := range rows {
		if row.RecipientNpub == npub {
			return row
		}
	}
	t.Fatalf("missing recipient %s", npub)
	return egDelivery{}
}

func egFrozen(t *testing.T, home, eventID, body string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(home, ".hyphae", "outbox.json"))
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Entries []struct {
			ID        string `json:"id"`
			EventJSON string `json:"event_json"`
			Route     string `json:"route"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	found := ""
	for _, entry := range file.Entries {
		if entry.ID != eventID {
			continue
		}
		if entry.Route != "group" || entry.EventJSON == "" || strings.Contains(entry.EventJSON, body) {
			t.Fatalf("outbox entry for %s is not a ciphertext group event", eventID)
		}
		if found != "" && found != entry.EventJSON {
			t.Fatalf("event %s has two payloads", eventID)
		}
		found = entry.EventJSON
	}
	if found == "" {
		t.Fatalf("outbox missing %s", eventID)
	}
	return found
}

func egDeliver(t *testing.T, relayURL, eventJSON string) [64]byte {
	t.Helper()
	var event nostr.Event
	if err := json.Unmarshal([]byte(eventJSON), &event); err != nil {
		t.Fatal(err)
	}
	if !event.VerifySignature() {
		t.Fatal("frozen event signature did not verify")
	}
	ok, id, message := publishRelayEvent(t, relayURL, event)
	if !ok || id != event.ID.Hex() {
		t.Fatalf("relay rejected %s: %s", event.ID.Hex(), message)
	}
	egSameEvent(t, relayURL, event.ID.Hex(), event.Sig)
	return event.Sig
}

func egSameEvent(t *testing.T, relayURL, eventID string, sig [64]byte) {
	t.Helper()
	stored := queryRelayEvent(t, relayURL, eventID)
	if !stored.VerifySignature() || stored.Sig != sig {
		t.Fatalf("relay changed event %s", eventID)
	}
}

func egWaitScan(t *testing.T, proc *daemonCLIProcess, updates int, timeout time.Duration) {
	t.Helper()
	const marker = "📨 Group update from"
	const done = "Watching... (no new messages)"
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out := proc.stdout.String()
		got := strings.Count(out, marker)
		if updates == 0 && strings.Contains(out, done) && got == 0 {
			return
		}
		if updates > 0 && got == updates {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("daemon updates=%d\nstdout: %s\nstderr: %s", updates, egTail(proc.stdout.String()), egTail(proc.stderr.String()))
}

func egTail(text string) string {
	if len(text) > 1500 {
		return text[len(text)-1500:]
	}
	return text
}

func egMust(t *testing.T, bin, home string, args ...string) string {
	t.Helper()
	stdout, stderr, code := egRun(t, bin, home, nil, args...)
	if code != 0 {
		t.Fatalf("exit %d for %v\nstdout: %s\nstderr: %s", code, args, stdout, stderr)
	}
	return stdout
}

func egRun(t *testing.T, bin, home string, extra []string, args ...string) (string, string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = append(isolatedEnv(home), extra...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return stdout.String(), stderr.String(), 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return stdout.String(), stderr.String(), exitErr.ExitCode()
	}
	t.Fatalf("run %v: %v\nstderr: %s", args, err, stderr.String())
	return "", "", 1
}

func egDecode[T any](t *testing.T, stdout string) T {
	t.Helper()
	var env struct {
		OK   bool `json:"ok"`
		Data T    `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &env); err != nil || !env.OK {
		t.Fatalf("decode: %v\n%s", err, stdout)
	}
	return env.Data
}

func egGroup(t *testing.T, stdout string) string {
	t.Helper()
	id := strings.TrimSpace(stdout)
	if len(id) != 32 {
		t.Fatalf("group id %q", stdout)
	}
	for _, r := range id {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			t.Fatalf("group id %q", stdout)
		}
	}
	return id
}

func egOpaque(t *testing.T) string {
	t.Helper()
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(raw[:])
}

func egState(t *testing.T, home, npub, groupID string) string {
	t.Helper()
	db := egDB(t, home)
	defer db.Close()
	var state string
	if err := db.QueryRow(`SELECT state FROM groupchat_groups WHERE local_npub=? AND group_id=?`, npub, groupID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	return state
}

func egInt(t *testing.T, home, query string, args ...any) int {
	t.Helper()
	db := egDB(t, home)
	defer db.Close()
	var n int
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func egDB(t *testing.T, home string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(home, ".hyphae", "messages.db"))
	if err != nil {
		t.Fatal(err)
	}
	return db
}

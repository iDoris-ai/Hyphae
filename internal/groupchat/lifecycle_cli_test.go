package groupchat_test

import (
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"fiatjaf.com/nostr"
	"github.com/iDoris-ai/hyphae/internal/common"
	_ "modernc.org/sqlite"
)

func TestGroupLifecycleCLI(t *testing.T) {
	bin := buildHyphae(t)
	home := t.TempDir()

	legacy, stderr, code := runCLI(t, bin, home, "group", "chat", "--name", "legacy")
	if code != 0 || !strings.Contains(legacy, "coming soon") {
		t.Fatalf("legacy group chat code %d\nstdout: %s\nstderr: %s", code, legacy, stderr)
	}

	alice := createIdentity(t, bin, home, "alice")
	bob := createIdentity(t, bin, home, "bob")
	carol := createIdentity(t, bin, home, "carol")
	remote := common.EncodeNpub(nostr.Generate().Public())

	remoteOut := mustCLI(t, bin, home, "groupchat", "create", "--name", "remote", "--identity", "alice", "--invitee", remote)
	remoteID := oneLine(t, remoteOut)
	remoteList := listInvites(t, bin, home, "alice", remoteID)
	if !hasInvitee(remoteList, remote) {
		t.Fatalf("remote invite list = %#v", remoteList)
	}
	if queryInt(t, home, `SELECT COUNT(*) FROM groupchat_groups WHERE local_npub = ?`, remote) != 0 {
		t.Fatal("non-local invitee gained a group row")
	}
	// the invitee is on another device: its invite is queued for the relay,
	// and only that one (co-located invitees below are handed over locally).
	queuedInvites := 1
	if queryInt(t, home, `SELECT COUNT(*) FROM groupchat_fanout WHERE envelope_type = 'invite' AND recipient_npub = ? AND state = 'queued'`, remote) != queuedInvites {
		t.Fatal("remote invite was not queued for delivery")
	}
	if queryInt(t, home, `SELECT COUNT(*) FROM groupchat_fanout`) != queuedInvites {
		t.Fatal("create queued more than the remote invite")
	}

	created := mustCLI(t, bin, home, "groupchat", "create", "--name", "planning", "--identity", "alice", "--invitee", bob.Npub)
	groupID := oneLine(t, created)
	if queryState(t, home, bob.Npub, groupID) != "pending" || queryState(t, home, alice.Npub, groupID) != "pending" {
		t.Fatalf("create left an active group alice=%s bob=%s", queryState(t, home, alice.Npub, groupID), queryState(t, home, bob.Npub, groupID))
	}
	if queryInt(t, home, `SELECT COUNT(*) FROM groupchat_fanout`) != queuedInvites {
		t.Fatal("create wrote fanout")
	}
	invites := listInvites(t, bin, home, "alice", groupID)
	inviteID := inviteFor(t, invites, bob.Npub, groupID)

	accepted := mustCLI(t, bin, home, "groupchat", "accept", "--identity", "bob", "--invite", inviteID)
	if oneLine(t, accepted) != groupID {
		t.Fatalf("accept printed %q", accepted)
	}
	if queryState(t, home, bob.Npub, groupID) != "active" || queryState(t, home, alice.Npub, groupID) != "active" {
		t.Fatalf("accept states alice=%s bob=%s", queryState(t, home, alice.Npub, groupID), queryState(t, home, bob.Npub, groupID))
	}
	members := queryInt(t, home, `SELECT COUNT(*) FROM groupchat_members WHERE local_npub = ? AND group_id = ?`, bob.Npub, groupID)
	inviteRows := queryInt(t, home, `SELECT COUNT(*) FROM groupchat_invites WHERE local_npub = ? AND group_id = ?`, bob.Npub, groupID)
	if members != 2 || inviteRows != 1 {
		t.Fatalf("membership members=%d invites=%d", members, inviteRows)
	}
	if queryInt(t, home, `SELECT COUNT(*) FROM groupchat_fanout`) != queuedInvites || queryInt(t, home, `SELECT COUNT(*) FROM messages`) != 0 {
		t.Fatal("accept wrote fanout or a DM row")
	}
	if queryInt(t, home, `SELECT COUNT(*) FROM groupchat_invites WHERE local_npub = ? AND group_id = ? AND activation_event_id != ''`, alice.Npub, groupID) != 0 {
		t.Fatal("accept stored a creator activation event id")
	}

	repeated := mustCLI(t, bin, home, "groupchat", "accept", "--identity", "bob", "--invite", inviteID)
	if oneLine(t, repeated) != groupID {
		t.Fatalf("repeat printed %q", repeated)
	}
	if queryInt(t, home, `SELECT COUNT(*) FROM groupchat_members WHERE local_npub = ? AND group_id = ?`, bob.Npub, groupID) != members {
		t.Fatal("repeat added a membership")
	}
	if queryInt(t, home, `SELECT COUNT(*) FROM groupchat_invites WHERE local_npub = ? AND group_id = ?`, bob.Npub, groupID) != inviteRows {
		t.Fatal("repeat added an invite")
	}
	if queryInt(t, home, `SELECT COUNT(*) FROM groupchat_fanout`) != queuedInvites {
		t.Fatal("repeat wrote fanout")
	}
	wrongOut, wrongErr, wrongCode := runCLI(t, bin, home, "groupchat", "accept", "--identity", "alice", "--invite", inviteID)
	if wrongCode == 0 || strings.TrimSpace(wrongOut) == groupID || !strings.Contains(wrongErr, "current identity does not match group invitation") {
		t.Fatalf("creator accept code %d\nstdout: %s\nstderr: %s", wrongCode, wrongOut, wrongErr)
	}
	if queryInt(t, home, `SELECT COUNT(*) FROM groupchat_members WHERE local_npub = ? AND group_id = ?`, bob.Npub, groupID) != members {
		t.Fatal("creator accept changed membership")
	}

	partial := mustCLI(t, bin, home, "groupchat", "create", "--name", "partial", "--identity", "alice", "--invitee", bob.Npub, "--invitee", carol.Npub)
	partialID := oneLine(t, partial)
	partialInvite := inviteFor(t, listInvites(t, bin, home, "alice", partialID), bob.Npub, partialID)
	stdout, stderr, code := runCLI(t, bin, home, "--json", "groupchat", "accept", "--identity", "bob", "--invite", partialInvite)
	if code != 0 || !strings.Contains(stdout, `"state":"pending"`) || !strings.Contains(stdout, partialID) {
		t.Fatalf("partial accept must succeed as pending, code %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if queryState(t, home, bob.Npub, partialID) == "active" {
		t.Fatal("partial accept activated bob")
	}

	resumeID := oneLine(t, mustCLI(t, bin, home, "groupchat", "create", "--name", "resume", "--identity", "alice", "--invitee", bob.Npub))
	resumeInvite := inviteFor(t, listInvites(t, bin, home, "alice", resumeID), bob.Npub, resumeID)
	stdout, stderr, code = runCLIEnv(t, bin, home, []string{"HYPHAE_HALT_BEFORE_ACTIVATION_RECEIVE=" + resumeInvite}, "groupchat", "accept", "--identity", "bob", "--invite", resumeInvite)
	if code != 75 || queryState(t, home, alice.Npub, resumeID) != "active" || queryState(t, home, bob.Npub, resumeID) == "active" {
		t.Fatalf("resume halt code %d alice %s bob %s\nstdout: %s\nstderr: %s", code, queryState(t, home, alice.Npub, resumeID), queryState(t, home, bob.Npub, resumeID), stdout, stderr)
	}
	if oneLine(t, mustCLI(t, bin, home, "groupchat", "accept", "--identity", "bob", "--invite", resumeInvite)) != resumeID || queryState(t, home, bob.Npub, resumeID) != "active" {
		t.Fatal("resume rerun left bob inactive")
	}

	lowNick, highNick := "bob", "carol"
	low, high := bob, carol
	if bob.Npub > carol.Npub {
		low, high = carol, bob
		lowNick, highNick = "carol", "bob"
	}
	trioID := oneLine(t, mustCLI(t, bin, home, "groupchat", "create", "--name", "trio", "--identity", "alice", "--invitee", bob.Npub, "--invitee", carol.Npub))
	trioInvites := listInvites(t, bin, home, "alice", trioID)
	lowInvite := inviteFor(t, trioInvites, low.Npub, trioID)
	highInvite := inviteFor(t, trioInvites, high.Npub, trioID)
	stdout, stderr, code = runCLI(t, bin, home, "groupchat", "accept", "--identity", highNick, "--invite", highInvite)
	if code != 0 || !strings.Contains(stdout, "pending") {
		t.Fatalf("trio first accept code %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	stdout, stderr, code = runCLIEnv(t, bin, home, []string{"HYPHAE_HALT_BEFORE_ACTIVATION_RECEIVE=" + lowInvite}, "groupchat", "accept", "--identity", lowNick, "--invite", lowInvite)
	if code != 75 || queryState(t, home, alice.Npub, trioID) != "activating" || queryState(t, home, low.Npub, trioID) == "active" {
		t.Fatalf("trio halt code %d alice %s low %s\nstdout: %s\nstderr: %s", code, queryState(t, home, alice.Npub, trioID), queryState(t, home, low.Npub, trioID), stdout, stderr)
	}
	if oneLine(t, mustCLI(t, bin, home, "groupchat", "accept", "--identity", lowNick, "--invite", lowInvite)) != trioID {
		t.Fatal("trio rerun did not print the group id")
	}
	if queryState(t, home, alice.Npub, trioID) != "active" || queryState(t, home, low.Npub, trioID) != "active" || queryState(t, home, high.Npub, trioID) != "active" {
		t.Fatal("trio rerun left a group inactive")
	}
	if queryInt(t, home, `SELECT COUNT(*) FROM groupchat_invites WHERE local_npub = ? AND group_id = ? AND activation_event_id != ''`, alice.Npub, trioID) != 0 {
		t.Fatal("trio stored a creator activation event id")
	}

	peerID := oneLine(t, mustCLI(t, bin, home, "groupchat", "create", "--name", "peer", "--identity", "alice", "--invitee", bob.Npub, "--invitee", carol.Npub))
	peerInvites := listInvites(t, bin, home, "alice", peerID)
	peerLow := inviteFor(t, peerInvites, low.Npub, peerID)
	peerHigh := inviteFor(t, peerInvites, high.Npub, peerID)
	stdout, stderr, code = runCLI(t, bin, home, "groupchat", "accept", "--identity", highNick, "--invite", peerHigh)
	if code != 0 || !strings.Contains(stdout, "pending") {
		t.Fatalf("peer first accept code %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	stdout, stderr, code = runCLIEnv(t, bin, home, []string{"HYPHAE_HALT_BEFORE_ACTIVATION_RECEIVE=" + peerHigh}, "groupchat", "accept", "--identity", lowNick, "--invite", peerLow)
	if code != 75 || queryState(t, home, low.Npub, peerID) != "active" || queryState(t, home, high.Npub, peerID) == "active" {
		t.Fatalf("peer halt code %d low %s high %s\nstdout: %s\nstderr: %s", code, queryState(t, home, low.Npub, peerID), queryState(t, home, high.Npub, peerID), stdout, stderr)
	}
	if oneLine(t, mustCLI(t, bin, home, "groupchat", "accept", "--identity", lowNick, "--invite", peerLow)) != peerID || queryState(t, home, high.Npub, peerID) != "active" {
		t.Fatal("peer rerun by the earlier member left the later member pending")
	}

	otherID := oneLine(t, mustCLI(t, bin, home, "groupchat", "create", "--name", "other", "--identity", "alice", "--invitee", bob.Npub, "--invitee", carol.Npub))
	otherInvites := listInvites(t, bin, home, "alice", otherID)
	otherLow := inviteFor(t, otherInvites, low.Npub, otherID)
	otherHigh := inviteFor(t, otherInvites, high.Npub, otherID)
	stdout, stderr, code = runCLI(t, bin, home, "groupchat", "accept", "--identity", lowNick, "--invite", otherLow)
	if code != 0 || !strings.Contains(stdout, "pending") {
		t.Fatalf("other first accept code %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	stdout, stderr, code = runCLIEnv(t, bin, home, []string{"HYPHAE_HALT_BEFORE_ACTIVATION_RECEIVE=" + otherLow}, "groupchat", "accept", "--identity", highNick, "--invite", otherHigh)
	if code != 75 || queryState(t, home, high.Npub, otherID) == "active" {
		t.Fatalf("other halt code %d high %s\nstdout: %s\nstderr: %s", code, queryState(t, home, high.Npub, otherID), stdout, stderr)
	}
	if oneLine(t, mustCLI(t, bin, home, "groupchat", "accept", "--identity", highNick, "--invite", otherHigh)) != otherID || queryState(t, home, low.Npub, otherID) != "active" || queryState(t, home, high.Npub, otherID) != "active" {
		t.Fatal("other rerun by the later member left a group inactive")
	}

	dave := createIdentity(t, bin, home, "dave")
	quadOrder := []struct {
		nick string
		npub string
	}{{"bob", bob.Npub}, {"carol", carol.Npub}, {"dave", dave.Npub}}
	sort.Slice(quadOrder, func(i, j int) bool { return quadOrder[i].npub < quadOrder[j].npub })
	n1, n2, n3 := quadOrder[0], quadOrder[1], quadOrder[2]
	quadID := oneLine(t, mustCLI(t, bin, home, "groupchat", "create", "--name", "quad", "--identity", "alice", "--invitee", bob.Npub, "--invitee", carol.Npub, "--invitee", dave.Npub))
	quadInvites := listInvites(t, bin, home, "alice", quadID)
	n2Invite := inviteFor(t, quadInvites, n2.npub, quadID)
	for _, member := range []struct{ nick, invite string }{{n3.nick, inviteFor(t, quadInvites, n3.npub, quadID)}, {n2.nick, n2Invite}} {
		stdout, stderr, code = runCLI(t, bin, home, "groupchat", "accept", "--identity", member.nick, "--invite", member.invite)
		if code != 0 || !strings.Contains(stdout, "pending") {
			t.Fatalf("quad partial %s code %d\nstdout: %s\nstderr: %s", member.nick, code, stdout, stderr)
		}
	}
	stdout, stderr, code = runCLIEnv(t, bin, home, []string{"HYPHAE_HALT_BEFORE_ACTIVATION_RECEIVE=" + n2Invite}, "groupchat", "accept", "--identity", n1.nick, "--invite", inviteFor(t, quadInvites, n1.npub, quadID))
	if code != 75 || queryState(t, home, alice.Npub, quadID) != "activating" || queryState(t, home, n1.npub, quadID) != "active" || queryState(t, home, n2.npub, quadID) == "active" {
		t.Fatalf("quad halt code %d alice %s n1 %s n2 %s\nstdout: %s\nstderr: %s", code, queryState(t, home, alice.Npub, quadID), queryState(t, home, n1.npub, quadID), queryState(t, home, n2.npub, quadID), stdout, stderr)
	}
	if oneLine(t, mustCLI(t, bin, home, "groupchat", "accept", "--identity", n1.nick, "--invite", inviteFor(t, quadInvites, n1.npub, quadID))) != quadID || queryState(t, home, n2.npub, quadID) != "active" || queryState(t, home, n3.npub, quadID) != "active" || queryState(t, home, alice.Npub, quadID) != "active" {
		t.Fatal("quad rerun by the earlier member left a later member pending")
	}

	heldID := oneLine(t, mustCLI(t, bin, home, "groupchat", "create", "--name", "held", "--identity", "alice", "--invitee", bob.Npub, "--invitee", carol.Npub))
	heldInvites := listInvites(t, bin, home, "alice", heldID)
	stdout, stderr, code = runCLI(t, bin, home, "groupchat", "accept", "--identity", highNick, "--invite", inviteFor(t, heldInvites, high.Npub, heldID))
	if code != 0 || !strings.Contains(stdout, "pending") {
		t.Fatalf("held first accept code %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	dropIdentity(t, home, highNick)
	stdout, stderr, code = runCLI(t, bin, home, "groupchat", "accept", "--identity", lowNick, "--invite", inviteFor(t, heldInvites, low.Npub, heldID))
	// the dropped member is now treated as living on another device: low
	// activates in-process, high's activation is queued for the relay.
	if code != 0 || strings.TrimSpace(stdout) != heldID {
		t.Fatalf("held completing accept code %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if queryState(t, home, low.Npub, heldID) != "active" || queryState(t, home, alice.Npub, heldID) != "active" {
		t.Fatal("completing accept left the group inactive")
	}
	if queryInt(t, home, `SELECT COUNT(*) FROM groupchat_fanout WHERE envelope_type = 'activate' AND recipient_npub = ? AND state = 'queued'`, high.Npub) != 1 {
		t.Fatal("remote member's activation was not queued")
	}
	queuedInvites++
	if highNick == "bob" {
		bob = createIdentity(t, bin, home, "bob")
	}

	gone := mustCLI(t, bin, home, "groupchat", "create", "--name", "gone", "--identity", "alice", "--invitee", bob.Npub)
	goneID := oneLine(t, gone)
	goneInvite := inviteFor(t, listInvites(t, bin, home, "alice", goneID), bob.Npub, goneID)
	dropIdentity(t, home, "alice")
	stdout, stderr, code = runCLI(t, bin, home, "groupchat", "accept", "--identity", "bob", "--invite", goneInvite)
	// the creator is treated as living on another device: the acceptance is
	// queued for the relay and bob waits for the activation.
	if code != 0 || !strings.Contains(stdout, "pending") {
		t.Fatalf("absent creator code %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if queryState(t, home, bob.Npub, goneID) == "active" {
		t.Fatal("bob activated without the creator's activation")
	}
	if queryInt(t, home, `SELECT COUNT(*) FROM groupchat_fanout WHERE envelope_type = 'accept' AND state = 'queued'`) != 1 {
		t.Fatal("acceptance was not queued for the creator")
	}
	if queryInt(t, home, `SELECT COUNT(*) FROM groupchat_fanout`) != queuedInvites+1 {
		t.Fatal("lifecycle wrote unexpected fanout")
	}
}

func buildHyphae(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "hyphae")
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/hyphae")
	cmd.Dir = moduleRoot(t)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build hyphae: %v\n%s", err, out)
	}
	return bin
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func commandEnv(home string) []string {
	filtered := make([]string, 0, len(os.Environ())+1)
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "HOME=") || strings.HasPrefix(entry, "HYPHAE_OUTPUT=") || strings.HasPrefix(entry, "AGENT_SPEAKER_OUTPUT=") {
			continue
		}
		filtered = append(filtered, entry)
	}
	return append(filtered, "HOME="+home)
}

func runCLI(t *testing.T, bin, home string, args ...string) (string, string, int) {
	t.Helper()
	return runCLIEnv(t, bin, home, nil, args...)
}

func runCLIEnv(t *testing.T, bin, home string, extra []string, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = append(commandEnv(home), extra...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return stdout.String(), stderr.String(), 0
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("run %v: %v", args, err)
	}
	return stdout.String(), stderr.String(), exitErr.ExitCode()
}

func mustCLI(t *testing.T, bin, home string, args ...string) string {
	t.Helper()
	stdout, stderr, code := runCLI(t, bin, home, args...)
	if code != 0 {
		t.Fatalf("exit %d for %v\nstdout: %s\nstderr: %s", code, args, stdout, stderr)
	}
	return stdout
}

type identityDTO struct {
	Npub string `json:"npub"`
}

func createIdentity(t *testing.T, bin, home, nickname string) identityDTO {
	t.Helper()
	stdout := mustCLI(t, bin, home, "identity", "create", "--nickname", nickname, "--json")
	var env struct {
		OK   bool        `json:"ok"`
		Data identityDTO `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &env); err != nil || !env.OK || env.Data.Npub == "" {
		t.Fatalf("identity %s: %v\n%s", nickname, err, stdout)
	}
	return env.Data
}

type inviteDTO struct {
	InviteID    string `json:"invite_id"`
	InviteeNpub string `json:"invitee_npub"`
	GroupID     string `json:"group_id"`
}

func listInvites(t *testing.T, bin, home, nickname, groupID string) []inviteDTO {
	t.Helper()
	stdout := mustCLI(t, bin, home, "groupchat", "invite", "list", "--group", groupID, "--identity", nickname, "--json")
	var env struct {
		OK   bool `json:"ok"`
		Data struct {
			Invites []inviteDTO `json:"invites"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &env); err != nil || !env.OK {
		t.Fatalf("invite list: %v\n%s", err, stdout)
	}
	return env.Data.Invites
}

func hasInvitee(invites []inviteDTO, npub string) bool {
	for _, invite := range invites {
		if invite.InviteeNpub == npub {
			return true
		}
	}
	return false
}

func inviteFor(t *testing.T, invites []inviteDTO, npub, groupID string) string {
	t.Helper()
	for _, invite := range invites {
		if invite.InviteeNpub == npub && invite.GroupID == groupID && invite.InviteID != "" {
			return invite.InviteID
		}
	}
	t.Fatalf("missing invite for %s in %#v", npub, invites)
	return ""
}

func oneLine(t *testing.T, stdout string) string {
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

func queryInt(t *testing.T, home, query string, args ...any) int {
	t.Helper()
	db := openMessages(t, home)
	defer db.Close()
	var n int
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

func queryState(t *testing.T, home, npub, groupID string) string {
	t.Helper()
	db := openMessages(t, home)
	defer db.Close()
	var state string
	err := db.QueryRow(`SELECT state FROM groupchat_groups WHERE local_npub = ? AND group_id = ?`, npub, groupID).Scan(&state)
	if err != nil {
		t.Fatalf("state %s %s: %v", npub, groupID, err)
	}
	return state
}

func openMessages(t *testing.T, home string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(home, ".hyphae", "messages.db"))
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func dropIdentity(t *testing.T, home, nickname string) {
	t.Helper()
	path := filepath.Join(home, ".hyphae", "keystore.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	identities, _ := doc["identities"].(map[string]any)
	delete(identities, nickname)
	if doc["default_identity"] == nickname {
		doc["default_identity"] = ""
		for name := range identities {
			doc["default_identity"] = name
			break
		}
	}
	encoded, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(encoded, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

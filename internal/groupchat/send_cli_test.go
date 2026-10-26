package groupchat_test

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGroupSendRetryStatusCLI(t *testing.T) {
	bin := buildHyphae(t)
	home := t.TempDir()
	alice := createIdentity(t, bin, home, "alice")
	bob := createIdentity(t, bin, home, "bob")
	carol := createIdentity(t, bin, home, "carol")
	groupID := activateGroup(t, bin, home, "room", alice.Npub, bob, carol)

	logical := opaqueID(t)
	sent := sendJSON(t, bin, home, nil, groupID, logical, "hello group", 10)
	if sent.Queued != 2 || sent.Failed != 0 || len(sent.Recipients) != 2 {
		t.Fatalf("send report queued=%d failed=%d recipients=%d", sent.Queued, sent.Failed, len(sent.Recipients))
	}
	for _, recipient := range sent.Recipients {
		if recipient.State != "queued" || recipient.EventID == "" || recipient.QueueID == "" || recipient.RelayAcks != 0 {
			t.Fatalf("send left %#v", recipient)
		}
	}
	if queryInt(t, home, `SELECT COUNT(*) FROM messages`) != 0 {
		t.Fatal("send wrote a DM row")
	}
	if queryInt(t, home, `SELECT COUNT(*) FROM groupchat_messages WHERE group_id=?`, groupID) != 1 {
		t.Fatal("send skipped local group history")
	}
	outbox := readOutbox(t, home)
	if !strings.Contains(outbox, "group_pending") || strings.Contains(outbox, "wss://") {
		t.Fatalf("outbox after send: %s", outbox)
	}

	history := historyJSON(t, bin, home, groupID)
	if len(history) != 1 || history[0].Plaintext != "hello group" {
		t.Fatalf("history %#v", history)
	}
	for _, flag := range []string{"--before-id", "--after-id"} {
		edge := mustCLI(t, bin, home, "groupchat", "history", "--json", "--group-id", groupID, "--identity", "alice", flag, history[0].ID)
		if !strings.Contains(edge, `"messages":null`) && !strings.Contains(edge, `"messages":[]`) {
			t.Fatalf("history %s the only message must be empty, got %s", flag, edge)
		}
	}
	if _, badErr, badCode := runCLI(t, bin, home, "groupchat", "history", "--json", "--group-id", groupID, "--identity", "alice", "--before-id", "nope"); badCode != 1 || !strings.Contains(badErr, "user_error") {
		t.Fatalf("unknown cursor code %d stderr %s", badCode, badErr)
	}
	if queryInt(t, home, `SELECT COUNT(*) FROM messages`) != 0 {
		t.Fatal("history wrote a DM row")
	}
	if queryInt(t, home, `SELECT COUNT(*) FROM groupchat_messages WHERE group_id=?`, groupID) != 1 {
		t.Fatal("history wrote a second group row")
	}

	status := statusJSON(t, bin, home, groupID, logical)
	if status.Queued != 2 || status.Failed != 0 || status.Partial || len(status.Bookkeeping) != 2 {
		t.Fatalf("status %#v", status)
	}
	beforeFanout := fanoutSnapshot(t, home, logical)
	beforeOutbox := readOutbox(t, home)
	again := statusJSON(t, bin, home, groupID, logical)
	if again.Queued != status.Queued || again.Failed != status.Failed || fanoutSnapshot(t, home, logical) != beforeFanout || readOutbox(t, home) != beforeOutbox {
		t.Fatal("status wrote fanout or outbox state")
	}

	failedLogical := opaqueID(t)
	stdout, stderr, code := runCLIEnv(t, bin, home, []string{"HYPHAE_FAIL_GROUP_RECIPIENT=" + bob.Npub},
		"groupchat", "send", "--group-id", groupID, "--logical-id", failedLogical, "--body", "half", "--identity", "alice", "--max-retries", "1")
	if code == 0 || !strings.Contains(stderr, "partial fanout queued 1 failed 1") {
		t.Fatalf("partial send code %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	// the JSON error carries the per-recipient outcome so a caller need not call status
	jsonLogical := opaqueID(t)
	_, jsonErr, jsonCode := runCLIEnv(t, bin, home, []string{"HYPHAE_FAIL_GROUP_RECIPIENT=" + bob.Npub},
		"--json", "groupchat", "send", "--group-id", groupID, "--logical-id", jsonLogical, "--body", "half json", "--identity", "alice", "--max-retries", "1")
	var failure struct {
		OK    bool       `json:"ok"`
		Error string     `json:"error"`
		Data  sendReport `json:"data"`
	}
	if err := json.Unmarshal([]byte(jsonErr), &failure); err != nil || jsonCode != 4 || failure.OK || failure.Error != "other_error" ||
		failure.Data.LogicalID != jsonLogical || failure.Data.Queued != 1 || failure.Data.Failed != 1 || len(failure.Data.Recipients) != 2 {
		t.Fatalf("partial send JSON error code %d err %v\n%s", jsonCode, err, jsonErr)
	}
	if queryInt(t, home, `SELECT COUNT(*) FROM groupchat_fanout WHERE send_key=? AND state='failed'`, failedLogical) != 1 ||
		queryInt(t, home, `SELECT COUNT(*) FROM groupchat_fanout WHERE send_key=? AND state='queued'`, failedLogical) != 1 {
		t.Fatal("partial send did not leave one failed and one queued row")
	}
	if queryInt(t, home, `SELECT COUNT(*) FROM messages`) != 0 {
		t.Fatal("partial send wrote a DM row")
	}
	failedEvent, failedQueue := fanoutPair(t, home, failedLogical, bob.Npub)
	keptEvent, keptQueue := fanoutPair(t, home, failedLogical, carol.Npub)
	if failedEvent == "" || failedQueue == "" || keptEvent == "" || keptQueue == "" {
		t.Fatal("partial send dropped an event id or queue id")
	}

	partialText := mustCLI(t, bin, home, "groupchat", "status", "--group-id", groupID, "--logical-id", failedLogical, "--identity", "alice")
	if !strings.Contains(partialText, "partial") || !strings.Contains(partialText, "queued 1") || !strings.Contains(partialText, "failed 1") {
		t.Fatalf("status text %q", partialText)
	}
	partial := statusJSON(t, bin, home, groupID, failedLogical)
	if !partial.Partial || partial.Queued != 1 || partial.Failed != 1 || len(partial.Bookkeeping) != 2 || partial.LogicalID != failedLogical {
		t.Fatalf("partial status %#v", partial)
	}
	frozen := fanoutSnapshot(t, home, failedLogical)
	statusJSON(t, bin, home, groupID, failedLogical)
	if fanoutSnapshot(t, home, failedLogical) != frozen {
		t.Fatal("status changed a partial fanout row")
	}

	retried := retryJSON(t, bin, home, groupID, failedLogical, bob.Npub)
	bobAfter := recipientBy(t, retried.Recipients, bob.Npub)
	carolAfter := recipientBy(t, retried.Recipients, carol.Npub)
	if bobAfter.EventID != failedEvent || bobAfter.QueueID == failedQueue || bobAfter.State != "queued" {
		t.Fatalf("retry bob event %s->%s queue %s->%s state %s", failedEvent, bobAfter.EventID, failedQueue, bobAfter.QueueID, bobAfter.State)
	}
	if carolAfter.EventID != keptEvent || carolAfter.QueueID != keptQueue {
		t.Fatalf("retry moved carol event %s queue %s", carolAfter.EventID, carolAfter.QueueID)
	}
	if queryInt(t, home, `SELECT COUNT(*) FROM messages`) != 0 {
		t.Fatal("retry wrote a DM row")
	}
	if queryInt(t, home, `SELECT COUNT(*) FROM groupchat_messages WHERE group_id=? AND logical_id=?`, groupID, failedLogical) != 1 {
		t.Fatal("retry changed group history")
	}
}

func activateGroup(t *testing.T, bin, home, name, alice string, bob, carol identityDTO) string {
	t.Helper()
	groupID := oneLine(t, mustCLI(t, bin, home, "groupchat", "create", "--name", name, "--identity", "alice", "--invitee", bob.Npub, "--invitee", carol.Npub))
	invites := listInvites(t, bin, home, "alice", groupID)
	for _, member := range []identityDTO{bob, carol} {
		runCLI(t, bin, home, "groupchat", "accept", "--identity", nicknameOf(t, member, bob, carol), "--invite", inviteFor(t, invites, member.Npub, groupID))
	}
	if queryState(t, home, alice, groupID) != "active" || queryState(t, home, bob.Npub, groupID) != "active" || queryState(t, home, carol.Npub, groupID) != "active" {
		t.Fatal("public accept left the group inactive")
	}
	return groupID
}

func nicknameOf(t *testing.T, member, bob, carol identityDTO) string {
	t.Helper()
	switch member.Npub {
	case bob.Npub:
		return "bob"
	case carol.Npub:
		return "carol"
	default:
		t.Fatalf("nickname for %s", member.Npub)
		return ""
	}
}

func opaqueID(t *testing.T) string {
	t.Helper()
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(raw[:])
}

type sendReport struct {
	Queued     int    `json:"queued"`
	Failed     int    `json:"failed"`
	LogicalID  string `json:"logical_id"`
	Recipients []struct {
		RecipientNpub string `json:"recipient_npub"`
		EventID       string `json:"event_id"`
		QueueID       string `json:"queue_id"`
		State         string `json:"state"`
		RelayAcks     int    `json:"relay_acks"`
	} `json:"recipients"`
}

type statusReport struct {
	LogicalID   string `json:"logical_id"`
	Queued      int    `json:"queued"`
	Failed      int    `json:"failed"`
	Partial     bool   `json:"partial"`
	Bookkeeping []struct {
		RecipientNpub string `json:"recipient_npub"`
		Bookkeeping   string `json:"bookkeeping"`
	} `json:"bookkeeping"`
}

type retryReport struct {
	Recipients []struct {
		RecipientNpub string `json:"recipient_npub"`
		EventID       string `json:"event_id"`
		QueueID       string `json:"queue_id"`
		State         string `json:"state"`
	} `json:"recipients"`
}

type historyMessage struct {
	ID        string `json:"id"`
	Plaintext string `json:"plaintext"`
}

func sendJSON(t *testing.T, bin, home string, extra []string, groupID, logical, body string, maxRetries int) sendReport {
	t.Helper()
	stdout, stderr, code := runCLIEnv(t, bin, home, extra, "groupchat", "send", "--json", "--group-id", groupID, "--logical-id", logical, "--body", body, "--identity", "alice", "--max-retries", itoa(maxRetries))
	if code != 0 {
		t.Fatalf("send exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	return decodeData[sendReport](t, stdout)
}

func statusJSON(t *testing.T, bin, home, groupID, logical string) statusReport {
	t.Helper()
	stdout := mustCLI(t, bin, home, "groupchat", "status", "--json", "--group-id", groupID, "--logical-id", logical, "--identity", "alice")
	report := decodeData[statusReport](t, stdout)
	for _, book := range report.Bookkeeping {
		if book.RecipientNpub == "" || book.Bookkeeping == "" {
			t.Fatalf("status bookkeeping %#v", report.Bookkeeping)
		}
	}
	return report
}

func retryJSON(t *testing.T, bin, home, groupID, logical, recipient string) retryReport {
	t.Helper()
	stdout := mustCLI(t, bin, home, "groupchat", "retry", "--json", "--group-id", groupID, "--logical-id", logical, "--recipient", recipient, "--identity", "alice")
	return decodeData[retryReport](t, stdout)
}

func historyJSON(t *testing.T, bin, home, groupID string) []historyMessage {
	t.Helper()
	stdout := mustCLI(t, bin, home, "groupchat", "history", "--json", "--group-id", groupID, "--identity", "alice")
	var env struct {
		OK   bool `json:"ok"`
		Data struct {
			Messages []historyMessage `json:"messages"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &env); err != nil || !env.OK {
		t.Fatalf("history json: %v\n%s", err, stdout)
	}
	return env.Data.Messages
}

func decodeData[T any](t *testing.T, stdout string) T {
	t.Helper()
	var env struct {
		OK   bool `json:"ok"`
		Data T    `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &env); err != nil || !env.OK {
		t.Fatalf("json: %v\n%s", err, stdout)
	}
	return env.Data
}

func recipientBy(t *testing.T, recipients []struct {
	RecipientNpub string `json:"recipient_npub"`
	EventID       string `json:"event_id"`
	QueueID       string `json:"queue_id"`
	State         string `json:"state"`
}, npub string) struct {
	RecipientNpub string `json:"recipient_npub"`
	EventID       string `json:"event_id"`
	QueueID       string `json:"queue_id"`
	State         string `json:"state"`
} {
	t.Helper()
	for _, recipient := range recipients {
		if recipient.RecipientNpub == npub {
			return recipient
		}
	}
	t.Fatalf("missing %s", npub)
	return recipients[0]
}

func fanoutPair(t *testing.T, home, logical, npub string) (string, string) {
	t.Helper()
	db := openMessages(t, home)
	defer db.Close()
	var eventID, queueID string
	if err := db.QueryRow(`SELECT event_id, queue_id FROM groupchat_fanout WHERE send_key=? AND recipient_npub=?`, logical, npub).Scan(&eventID, &queueID); err != nil {
		t.Fatal(err)
	}
	return eventID, queueID
}

func fanoutSnapshot(t *testing.T, home, logical string) string {
	t.Helper()
	db := openMessages(t, home)
	defer db.Close()
	rows, err := db.Query(`SELECT recipient_npub, event_id, queue_id, state, COALESCE(retry_count, -1) FROM groupchat_fanout WHERE send_key=? ORDER BY recipient_npub`, logical)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var b strings.Builder
	for rows.Next() {
		var npub, eventID, queueID, state string
		var retry int
		if err := rows.Scan(&npub, &eventID, &queueID, &state, &retry); err != nil {
			t.Fatal(err)
		}
		b.WriteString(npub + " " + eventID + " " + queueID + " " + state + "\n")
	}
	return b.String()
}

func readOutbox(t *testing.T, home string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, ".hyphae", "outbox.json"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits [12]byte
	i := len(digits)
	for n > 0 {
		i--
		digits[i] = byte('0' + n%10)
		n /= 10
	}
	return string(digits[i:])
}

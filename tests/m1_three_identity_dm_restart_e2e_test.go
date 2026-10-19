//go:build integration

package integration_test

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

type directedDM struct {
	from, to int
	body     string
	eventID  string
	sig      [64]byte
}

// TestM1ThreeIdentityDMRestartExactlyOnce sends one encrypted DM in each
// direction between Alice, Bob, and Carol. Restarting the local relay and
// each receiving daemon leaves one history row per event and does not repeat
// the receive effect.
func TestM1ThreeIdentityDMRestartExactlyOnce(t *testing.T) {
	root := projectRoot(t)
	temp := t.TempDir()
	cliBin := filepath.Join(temp, "hyphae")
	relayBin := filepath.Join(temp, "hyphae-relay")
	buildBinary(t, root, cliBin, "./cmd/hyphae")
	buildBinary(t, root, relayBin, "./cmd/hyphae-relay")

	homes := []string{
		filepath.Join(temp, "alice-home"),
		filepath.Join(temp, "bob-home"),
		filepath.Join(temp, "carol-home"),
	}
	names := []string{"alice", "bob", "carol"}
	for _, home := range homes {
		requireDir(t, home)
	}
	npubs := make([]string, len(names))
	for i, name := range names {
		npubs[i] = createIdentity(t, cliBin, homes[i], name)
	}
	for i := range names {
		for j, other := range names {
			if i == j {
				continue
			}
			runCLI(t, cliBin, homes[i], "contact", "add", "--nickname", other, "--npub", npubs[j])
		}
	}

	port := freeLoopbackPort(t)
	dataDir := filepath.Join(temp, "relay-data")
	relay := startRelay(t, relayBin, dataDir, port)
	relayURL := fmt.Sprintf("ws://127.0.0.1:%d", port)
	for _, home := range homes {
		runCLI(t, cliBin, home, "relay", "set", "--relay", relayURL)
	}

	messages := []directedDM{
		{from: 0, to: 1, body: "m1 alice to bob exactly once"},
		{from: 1, to: 0, body: "m1 bob to alice exactly once"},
		{from: 0, to: 2, body: "m1 alice to carol exactly once"},
		{from: 2, to: 0, body: "m1 carol to alice exactly once"},
		{from: 1, to: 2, body: "m1 bob to carol exactly once"},
		{from: 2, to: 1, body: "m1 carol to bob exactly once"},
	}
	seenIDs := make(map[string]struct{}, len(messages))
	for i := range messages {
		msg := &messages[i]
		msg.eventID = sendEncryptedMessage(t, cliBin, homes[msg.from], names[msg.from], names[msg.to], msg.body, relayURL)
		if _, duplicate := seenIDs[msg.eventID]; duplicate {
			t.Fatalf("CLI reused event ID %s", msg.eventID)
		}
		seenIDs[msg.eventID] = struct{}{}
		stored := queryRelayEvent(t, relayURL, msg.eventID)
		if !stored.VerifySignature() || strings.Contains(stored.Content, msg.body) {
			t.Fatalf("relay did not keep a signed ciphertext for event %s", msg.eventID)
		}
		msg.sig = stored.Sig
		assertHistoryRow(t, homes[msg.from], msg.eventID, npubs[msg.from], npubs[msg.to], msg.body, false)
		if got := countHistoryRows(t, homes[msg.from], msg.eventID); got != 1 {
			t.Fatalf("sender history for event %s has %d rows before receive", msg.eventID, got)
		}
	}
	for _, msg := range messages {
		if got := countHistoryRows(t, homes[msg.to], msg.eventID); got != 0 {
			t.Fatalf("recipient history for event %s has %d rows before the daemon scan", msg.eventID, got)
		}
		other := uninvolvedParty(msg.from, msg.to)
		if got := countHistoryRows(t, homes[other], msg.eventID); got != 0 {
			t.Fatalf("uninvolved home has %d rows for event %s", got, msg.eventID)
		}
	}
	if len(seenIDs) != len(messages) {
		t.Fatalf("recorded %d event ids, expected %d", len(seenIDs), len(messages))
	}
	relayIDs := queryRelayEventIDs(t, relayURL)
	if len(relayIDs) != len(messages) {
		t.Fatalf("relay has %d events, expected %d", len(relayIDs), len(messages))
	}
	for eventID := range seenIDs {
		if _, ok := relayIDs[eventID]; !ok {
			t.Fatalf("relay is missing event %s", eventID)
		}
	}

	relay.stop()
	relay = startRelay(t, relayBin, dataDir, port)
	assertRelayKeptSignatures(t, relayURL, messages)

	dbs := make([]*sql.DB, len(homes))
	for i, home := range homes {
		db, err := sql.Open("sqlite", filepath.Join(home, ".hyphae", "messages.db"))
		if err != nil {
			t.Fatalf("open isolated message database: %v", err)
		}
		dbs[i] = db
		t.Cleanup(func() { _ = db.Close() })
	}

	expectedIncoming := incomingByRecipient(messages, npubs)
	first := make([]*daemonCLIProcess, len(names))
	firstRows := make([]map[string]inboxHistoryRow, len(names))
	for i, name := range names {
		first[i] = startDaemonCLI(t, cliBin, homes[i], name, relayURL)
	}
	for i, npub := range npubs {
		firstRows[i] = waitForBacklogRows(t, dbs[i], npub, expectedIncoming[i], 45*time.Second, 15*time.Second)
		waitForOutputCount(t, first[i].stdout, "📨 New message from", len(expectedIncoming[i]), 5*time.Second)
		assertBacklogEffects(t, first[i].stdout.String(), len(expectedIncoming[i]))
	}
	for _, daemon := range first {
		daemon.stopAndWait(t, 3*time.Second)
	}

	relay.stop()
	relay = startRelay(t, relayBin, dataDir, port)
	assertRelayKeptSignatures(t, relayURL, messages)

	second := make([]*daemonCLIProcess, len(names))
	for i, name := range names {
		second[i] = startDaemonCLI(t, cliBin, homes[i], name, relayURL)
	}
	for i := range names {
		waitForOutputSubstring(t, second[i].stdout, "Watching... (no new messages)", 30*time.Second)
		if effects := strings.Count(second[i].stdout.String(), "📨 New message from"); effects != 0 {
			t.Fatalf("restarted %s daemon repeated %d receive effects", names[i], effects)
		}
	}
	for _, daemon := range second {
		daemon.stopAndWait(t, 3*time.Second)
	}
	assertRelayKeptSignatures(t, relayURL, messages)

	for i, npub := range npubs {
		rows := readInboxHistory(t, dbs[i], npub)
		assertHistoryStable(t, firstRows[i], rows, expectedIncoming[i])
		if got := countHomeHistory(t, homes[i]); got != 4 {
			t.Fatalf("%s history has %d rows, expected 2 outgoing and 2 incoming", names[i], got)
		}
	}
	for _, msg := range messages {
		assertHistoryRow(t, homes[msg.from], msg.eventID, npubs[msg.from], npubs[msg.to], msg.body, false)
		assertHistoryRow(t, homes[msg.to], msg.eventID, npubs[msg.from], npubs[msg.to], msg.body, true)
		if got := countHistoryRows(t, homes[msg.from], msg.eventID); got != 1 {
			t.Fatalf("sender history for event %s has %d rows after rescan", msg.eventID, got)
		}
		if got := countHistoryRows(t, homes[msg.to], msg.eventID); got != 1 {
			t.Fatalf("recipient history for event %s has %d rows after rescan", msg.eventID, got)
		}
		other := uninvolvedParty(msg.from, msg.to)
		if got := countHistoryRows(t, homes[other], msg.eventID); got != 0 {
			t.Fatalf("uninvolved home has %d rows for event %s after rescan", got, msg.eventID)
		}
	}
}

func uninvolvedParty(from, to int) int {
	for i := 0; i < 3; i++ {
		if i != from && i != to {
			return i
		}
	}
	return -1
}

func incomingByRecipient(messages []directedDM, npubs []string) []map[string]string {
	expected := make([]map[string]string, len(npubs))
	for i := range npubs {
		expected[i] = make(map[string]string)
	}
	for _, msg := range messages {
		expected[msg.to][msg.eventID] = msg.body
	}
	return expected
}

func assertRelayKeptSignatures(t *testing.T, relayURL string, messages []directedDM) {
	t.Helper()
	ids := queryRelayEventIDs(t, relayURL)
	if len(ids) != len(messages) {
		t.Fatalf("restarted relay has %d events, expected %d", len(ids), len(messages))
	}
	for _, msg := range messages {
		if _, ok := ids[msg.eventID]; !ok {
			t.Fatalf("restarted relay is missing event %s", msg.eventID)
		}
		stored := queryRelayEvent(t, relayURL, msg.eventID)
		if !stored.VerifySignature() || stored.Sig != msg.sig || strings.Contains(stored.Content, msg.body) {
			t.Fatalf("restarted relay changed the signed ciphertext for event %s", msg.eventID)
		}
	}
}

func countHomeHistory(t *testing.T, home string) int {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(home, ".hyphae", "messages.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM messages").Scan(&count); err != nil {
		t.Fatalf("count history rows: %v", err)
	}
	return count
}

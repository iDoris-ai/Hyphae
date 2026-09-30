//go:build integration

package integration_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"github.com/gorilla/websocket"
	_ "modernc.org/sqlite"
)

func TestOfflineCLIOutboxRetryAndDaemonSignal(t *testing.T) {
	root := projectRoot(t)
	temp := t.TempDir()
	cliBin := filepath.Join(temp, "hyphae")
	relayBin := filepath.Join(temp, "hyphae-relay")
	buildBinary(t, root, cliBin, "./cmd/hyphae")
	buildBinary(t, root, relayBin, "./cmd/hyphae-relay")

	aliceHome := filepath.Join(temp, "alice-home")
	bobHome := filepath.Join(temp, "bob-home")
	requireDir(t, aliceHome)
	requireDir(t, bobHome)
	aliceNPub := createIdentity(t, cliBin, aliceHome, "alice")
	bobNPub := createIdentity(t, cliBin, bobHome, "bob")
	runCLI(t, cliBin, aliceHome, "contact", "add", "--nickname", "bob", "--npub", bobNPub)
	runCLI(t, cliBin, bobHome, "contact", "add", "--nickname", "alice", "--npub", aliceNPub)

	port := freeLoopbackPort(t)
	dataDir := filepath.Join(temp, "relay-data")
	relay := startRelay(t, relayBin, dataDir, port)
	relayURL := fmt.Sprintf("ws://127.0.0.1:%d", port)
	runCLI(t, cliBin, aliceHome, "relay", "set", "--relay", relayURL)
	runCLI(t, cliBin, bobHome, "relay", "set", "--relay", relayURL)

	relay.stop()
	const plaintext = "queued while the relay is offline"
	sendOutput := runCLI(t, cliBin, aliceHome,
		"agent", "msg", "--from", "alice", "--to", "bob", "--content", plaintext, "--json")
	var sendResult struct {
		OK   bool `json:"ok"`
		Data struct {
			EventID        string `json:"event_id"`
			Encrypted      bool   `json:"encrypted"`
			PublishedTo    int    `json:"published_to"`
			RelayCount     int    `json:"relay_count"`
			QueuedForRetry bool   `json:"queued_for_retry"`
			HistoryStored  bool   `json:"history_stored"`
		} `json:"data"`
	}
	if err := json.Unmarshal(sendOutput, &sendResult); err != nil {
		t.Fatalf("decode offline send result: %v", err)
	}
	if !sendResult.OK || !sendResult.Data.Encrypted || sendResult.Data.PublishedTo != 0 || sendResult.Data.RelayCount != 1 || !sendResult.Data.QueuedForRetry || !sendResult.Data.HistoryStored || sendResult.Data.EventID == "" {
		t.Fatalf("offline send did not report stored encrypted outbox entry: %s", safeJSON(sendOutput))
	}

	var persisted struct {
		Entries []struct {
			ID            string   `json:"id"`
			QueueID       string   `json:"queue_id"`
			EventJSON     string   `json:"event_json"`
			RecipientNpub string   `json:"recipient_npub"`
			Relays        []string `json:"relays"`
			Status        string   `json:"status"`
		} `json:"entries"`
	}
	outboxPath := filepath.Join(aliceHome, ".hyphae", "outbox.json")
	outboxBytes, err := os.ReadFile(outboxPath)
	if err != nil {
		t.Fatalf("read persisted outbox: %v", err)
	}
	if err := json.Unmarshal(outboxBytes, &persisted); err != nil {
		t.Fatalf("decode persisted outbox: %v", err)
	}
	if len(persisted.Entries) != 1 {
		t.Fatalf("expected one persisted outbox entry, got %d", len(persisted.Entries))
	}
	queued := persisted.Entries[0]
	if queued.ID != sendResult.Data.EventID || queued.QueueID == "" || queued.Status != "pending" || queued.RecipientNpub != bobNPub || len(queued.Relays) != 1 || queued.Relays[0] != relayURL {
		t.Fatalf("persisted outbox metadata did not match the CLI result: %s", safeJSON(outboxBytes))
	}
	var queuedEvent nostr.Event
	if err := json.Unmarshal([]byte(queued.EventJSON), &queuedEvent); err != nil {
		t.Fatalf("decode queued event: %v", err)
	}
	if queuedEvent.ID.Hex() != sendResult.Data.EventID || !queuedEvent.VerifySignature() || strings.Contains(queued.EventJSON, plaintext) || strings.Contains(queuedEvent.Content, plaintext) {
		t.Fatal("outbox did not preserve the signed encrypted event without plaintext")
	}
	if !hasNIP44Tag(queuedEvent.Tags) {
		t.Fatal("queued event is missing NIP-44 metadata")
	}
	assertHistoryRow(t, aliceHome, sendResult.Data.EventID, aliceNPub, bobNPub, plaintext, false)
	if countHistoryRows(t, aliceHome, sendResult.Data.EventID) != 1 {
		t.Fatal("Alice should have exactly one plaintext history row before retry")
	}

	relay = startRelay(t, relayBin, dataDir, port)
	retryOutput := runCLI(t, cliBin, aliceHome, "storage", "outbox", "retry", "--id", sendResult.Data.EventID, "--json")
	var retryResult struct {
		OK   bool `json:"ok"`
		Data struct {
			EventID           string `json:"event_id"`
			Attempted         bool   `json:"attempted"`
			Sent              bool   `json:"sent"`
			Queued            bool   `json:"queued"`
			HistoryStored     bool   `json:"history_stored"`
			QueueStateUnknown bool   `json:"queue_state_unknown"`
		} `json:"data"`
	}
	if err := json.Unmarshal(retryOutput, &retryResult); err != nil {
		t.Fatalf("decode retry result: %v", err)
	}
	if !retryResult.OK || retryResult.Data.EventID != sendResult.Data.EventID || !retryResult.Data.Attempted || !retryResult.Data.Sent || retryResult.Data.Queued || !retryResult.Data.HistoryStored || retryResult.Data.QueueStateUnknown {
		t.Fatalf("outbox retry did not report relay ACK and local cleanup: %s", safeJSON(retryOutput))
	}
	outboxBytes, err = os.ReadFile(outboxPath)
	if err != nil {
		t.Fatalf("read outbox after retry: %v", err)
	}
	if err := json.Unmarshal(outboxBytes, &persisted); err != nil {
		t.Fatalf("decode outbox after retry: %v", err)
	}
	if len(persisted.Entries) != 0 {
		t.Fatalf("successful retry left %d outbox entries", len(persisted.Entries))
	}
	storedEvent := queryRelayEvent(t, relayURL, sendResult.Data.EventID)
	if !storedEvent.VerifySignature() || storedEvent.ID.Hex() != queuedEvent.ID.Hex() || storedEvent.Sig != queuedEvent.Sig {
		t.Fatal("retry did not publish the same signed event stored in the outbox")
	}

	for attempt := 0; attempt < 2; attempt++ {
		inboxOutput := runCLI(t, cliBin, bobHome, "agent", "inbox", "--as", "bob", "--json")
		var inbox struct {
			OK   bool `json:"ok"`
			Data []struct {
				Content   string `json:"content"`
				Encrypted bool   `json:"encrypted"`
				Decrypted bool   `json:"decrypted"`
				EventID   string `json:"event_id"`
			} `json:"data"`
		}
		if err := json.Unmarshal(inboxOutput, &inbox); err != nil {
			t.Fatalf("decode Bob inbox: %v", err)
		}
		if !inbox.OK || len(inbox.Data) != 1 || inbox.Data[0].Content != plaintext || !inbox.Data[0].Encrypted || !inbox.Data[0].Decrypted || inbox.Data[0].EventID != sendResult.Data.EventID {
			t.Fatalf("Bob inbox did not decrypt the queued message: %s", safeJSON(inboxOutput))
		}
		if got := countHistoryRows(t, bobHome, sendResult.Data.EventID); got != 1 {
			t.Fatalf("Bob inbox query %d left %d history rows for one event", attempt+1, got)
		}
	}
	assertHistoryRow(t, aliceHome, sendResult.Data.EventID, aliceNPub, bobNPub, plaintext, false)
	assertHistoryRow(t, bobHome, sendResult.Data.EventID, aliceNPub, bobNPub, plaintext, true)
	if got := countHistoryRows(t, aliceHome, sendResult.Data.EventID); got != 1 {
		t.Fatalf("Alice history plaintext was not preserved exactly once: %d rows", got)
	}

	relay.stop()
	stallURL, waitForSubscription := startStalledWebSocket(t)
	runCLI(t, cliBin, aliceHome, "relay", "set", "--relay", stallURL)
	daemon := exec.Command(cliBin, "daemon", "--identity", "alice", "--relay", stallURL,
		"--retry-interval", "3600", "--watch-interval", "3600", "--notify=false")
	daemon.Env = isolatedEnv(aliceHome)
	daemon.Stdout, daemon.Stderr = io.Discard, io.Discard
	if err := daemon.Start(); err != nil {
		t.Fatalf("start daemon CLI: %v", err)
	}
	daemonDone := make(chan error, 1)
	go func() { daemonDone <- daemon.Wait() }()
	select {
	case <-waitForSubscription:
	case <-time.After(5 * time.Second):
		_ = daemon.Process.Kill()
		<-daemonDone
		t.Fatal("daemon CLI did not subscribe to stalled WebSocket")
	}
	startedShutdown := time.Now()
	if err := daemon.Process.Signal(syscall.SIGTERM); err != nil {
		_ = daemon.Process.Kill()
		<-daemonDone
		t.Fatalf("signal daemon CLI: %v", err)
	}
	select {
	case err := <-daemonDone:
		if err != nil {
			t.Fatalf("daemon CLI exited with error after SIGTERM: %v", err)
		}
		if elapsed := time.Since(startedShutdown); elapsed >= 2*time.Second {
			t.Fatalf("daemon CLI shutdown took %v, want under 2s", elapsed)
		}
	case <-time.After(2 * time.Second):
		_ = daemon.Process.Kill()
		<-daemonDone
		t.Fatal("daemon CLI did not stop within 2s after SIGTERM")
	}
}

func hasNIP44Tag(tags nostr.Tags) bool {
	count := 0
	for _, tag := range tags {
		if len(tag) >= 2 && tag[0] == "enc" && tag[1] == "nip44" {
			count++
		}
	}
	return count == 1
}

func startStalledWebSocket(t *testing.T) (string, <-chan struct{}) {
	t.Helper()
	requested := make(chan struct{}, 1)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
		_, _, err = conn.ReadMessage()
		if err != nil {
			return
		}
		select {
		case requested <- struct{}{}:
		default:
		}
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	t.Cleanup(server.Close)
	return "ws" + strings.TrimPrefix(server.URL, "http"), requested
}

func countHistoryRows(t *testing.T, home, eventID string) int {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(home, ".hyphae", "messages.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM messages WHERE id = ?", eventID).Scan(&count); err != nil {
		t.Fatalf("count history rows for event %s: %v", eventID, err)
	}
	return count
}

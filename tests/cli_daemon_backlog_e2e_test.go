//go:build integration

package integration_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"github.com/gorilla/websocket"
	_ "modernc.org/sqlite"
)

const offlineBacklogSize = 125

type inboxHistoryRow struct {
	Plaintext string
	Incoming  bool
	Encrypted bool
}

func TestDaemonImportsAndDeduplicatesOfflineBacklog(t *testing.T) {
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
	startRelay(t, relayBin, dataDir, port)
	relayURL := fmt.Sprintf("ws://127.0.0.1:%d", port)
	runCLI(t, cliBin, aliceHome, "relay", "set", "--relay", relayURL)
	runCLI(t, cliBin, bobHome, "relay", "set", "--relay", relayURL)

	expected := make(map[string]string, offlineBacklogSize)
	for i := 0; i < offlineBacklogSize; i++ {
		body := fmt.Sprintf("offline backlog message %03d", i)
		eventID := sendEncryptedMessage(t, cliBin, aliceHome, "alice", "bob", body, relayURL)
		if _, duplicate := expected[eventID]; duplicate {
			t.Fatalf("CLI reused event ID %s", eventID)
		}
		expected[eventID] = body
	}
	relayIDs := queryRelayEventIDs(t, relayURL)
	if len(relayIDs) != offlineBacklogSize {
		t.Fatalf("relay has %d backlog events, expected %d", len(relayIDs), offlineBacklogSize)
	}
	for eventID := range expected {
		if _, ok := relayIDs[eventID]; !ok {
			t.Fatalf("relay is missing backlog event %s", eventID)
		}
	}

	bobDB, err := sql.Open("sqlite", filepath.Join(bobHome, ".hyphae", "messages.db"))
	if err != nil {
		t.Fatalf("open Bob's isolated message database: %v", err)
	}
	defer bobDB.Close()

	firstDaemon := startDaemonCLI(t, cliBin, bobHome, "bob", relayURL)
	firstRows := waitForBacklogRows(t, bobDB, bobNPub, expected, 45*time.Second, 12*time.Second)
	waitForOutputCount(t, firstDaemon.stdout, "📨 New message from", offlineBacklogSize, 5*time.Second)
	assertHistoryStable(t, expectedRows(expected), firstRows, expected)
	assertBacklogEffects(t, firstDaemon.stdout.String(), offlineBacklogSize)
	firstDaemon.stopAndWait(t, 3*time.Second)

	secondDaemon := startDaemonCLI(t, cliBin, bobHome, "bob", relayURL)
	waitForOutputSubstring(t, secondDaemon.stdout, "Watching... (no new messages)", 20*time.Second)
	if effects := strings.Count(secondDaemon.stdout.String(), "📨 New message from"); effects != 0 {
		t.Fatalf("restart repeated %d new-message effects after the initial historical scan", effects)
	}
	secondDaemon.stopAndWait(t, 3*time.Second)
	secondRows := readInboxHistory(t, bobDB, bobNPub)
	assertHistoryStable(t, firstRows, secondRows, expected)
}

func waitForBacklogRows(t *testing.T, db *sql.DB, recipient string, expected map[string]string, totalTimeout, idleTimeout time.Duration) map[string]inboxHistoryRow {
	t.Helper()
	deadline := time.Now().Add(totalTimeout)
	lastProgress := time.Now()
	lastCount := -1
	for time.Now().Before(deadline) {
		rows, err := readInboxHistoryWithError(db, recipient)
		if err == nil {
			if len(rows) > lastCount {
				lastCount = len(rows)
				lastProgress = time.Now()
			}
			if len(rows) > len(expected) {
				t.Fatalf("daemon stored %d inbox rows, expected %d", len(rows), len(expected))
			}
			if len(rows) == len(expected) {
				assertHistoryStable(t, expectedRows(expected), rows, expected)
				return rows
			}
		}
		if time.Since(lastProgress) > idleTimeout {
			t.Fatalf("daemon stopped making progress at %d/%d inbox rows", max(lastCount, 0), len(expected))
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out with %d/%d inbox rows", max(lastCount, 0), len(expected))
	return nil
}

func queryRelayEventIDs(t *testing.T, relayURL string) map[string]struct{} {
	t.Helper()
	dialCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, _, err := websocket.DefaultDialer.DialContext(dialCtx, relayURL, nil)
	if err != nil {
		t.Fatalf("connect to relay containing backlog events: %v", err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_ = conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if err := conn.WriteJSON([]any{"REQ", "backlog-e2e", map[string]any{"kinds": []int{30078}, "limit": 200}}); err != nil {
		t.Fatalf("query relay backlog: %v", err)
	}
	eventIDs := make(map[string]struct{}, offlineBacklogSize)
	for {
		var response []json.RawMessage
		if err := conn.ReadJSON(&response); err != nil {
			t.Fatalf("read relay backlog query: %v", err)
		}
		if len(response) > 0 && string(response[0]) == `"EOSE"` {
			return eventIDs
		}
		if len(response) == 3 && string(response[0]) == `"EVENT"` {
			var event nostr.Event
			if err := json.Unmarshal(response[2], &event); err != nil {
				t.Fatalf("decode relay backlog event: %v", err)
			}
			if !event.VerifySignature() {
				t.Fatalf("relay returned invalid signature for event %s", event.ID.Hex())
			}
			eventIDs[event.ID.Hex()] = struct{}{}
		}
	}
}

func readInboxHistory(t *testing.T, db *sql.DB, recipient string) map[string]inboxHistoryRow {
	t.Helper()
	rows, err := readInboxHistoryWithError(db, recipient)
	if err != nil {
		t.Fatalf("read Bob's inbox history: %v", err)
	}
	return rows
}

func readInboxHistoryWithError(db *sql.DB, recipient string) (map[string]inboxHistoryRow, error) {
	rows, err := db.Query(`SELECT id, plaintext, is_incoming, is_encrypted FROM messages WHERE recipient_npub = ? AND is_incoming = 1`, recipient)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[string]inboxHistoryRow)
	for rows.Next() {
		var id string
		var row inboxHistoryRow
		if err := rows.Scan(&id, &row.Plaintext, &row.Incoming, &row.Encrypted); err != nil {
			return nil, err
		}
		result[id] = row
	}
	return result, rows.Err()
}

func expectedRows(expected map[string]string) map[string]inboxHistoryRow {
	result := make(map[string]inboxHistoryRow, len(expected))
	for id, body := range expected {
		result[id] = inboxHistoryRow{Plaintext: body, Incoming: true, Encrypted: true}
	}
	return result
}

func assertHistoryStable(t *testing.T, before, after map[string]inboxHistoryRow, expected map[string]string) {
	t.Helper()
	if len(after) != len(expected) {
		t.Fatalf("inbox history has %d rows, expected exactly %d", len(after), len(expected))
	}
	for eventID, body := range expected {
		row, ok := after[eventID]
		if !ok {
			t.Fatalf("inbox is missing event %s", eventID)
		}
		if row.Plaintext != body || !row.Incoming || !row.Encrypted {
			t.Fatalf("history mismatch for event %s (incoming=%t encrypted=%t)", eventID, row.Incoming, row.Encrypted)
		}
		if before != nil && before[eventID] != row {
			t.Fatalf("history row for event %s changed after daemon restart", eventID)
		}
	}
}

func assertBacklogEffects(t *testing.T, output string, want int) {
	t.Helper()
	if got := strings.Count(output, "📨 New message from"); got != want {
		t.Fatalf("daemon reported %d new-message effects, expected %d", got, want)
	}
}

func waitForOutputCount(t *testing.T, output *boundedOutput, marker string, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Count(output.String(), marker) >= want {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("daemon output did not reach %d occurrences of %q", want, marker)
}

func waitForOutputSubstring(t *testing.T, output *boundedOutput, marker string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Contains(output.String(), marker) {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("daemon output did not include scan-complete marker %q", marker)
}

type boundedOutput struct {
	mu   sync.Mutex
	data []byte
	max  int
}

func (b *boundedOutput) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	write := len(data)
	remaining := b.max - len(b.data)
	if remaining > 0 {
		if write > remaining {
			write = remaining
		}
		b.data = append(b.data, data[:write]...)
	}
	return len(data), nil
}

func (b *boundedOutput) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.data)
}

type daemonCLIProcess struct {
	cmd       *exec.Cmd
	stdout    *boundedOutput
	stderr    *boundedOutput
	done      chan error
	stopOnce  sync.Once
	stopError error
}

func startDaemonCLI(t *testing.T, binary, home, identity, relayURL string) *daemonCLIProcess {
	t.Helper()
	cmd := exec.Command(binary, "daemon", "--identity", identity, "--relay", relayURL,
		"--watch-interval", "3600", "--retry-interval", "3600", "--notify=false", "--auto-reply=false")
	cmd.Env = isolatedEnv(home)
	process := &daemonCLIProcess{
		cmd: cmd, stdout: &boundedOutput{max: 2 << 20}, stderr: &boundedOutput{max: 1 << 20},
		done: make(chan error, 1),
	}
	cmd.Stdout, cmd.Stderr = process.stdout, process.stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start actual daemon CLI: %v", err)
	}
	go func() { process.done <- cmd.Wait() }()
	t.Cleanup(func() {
		if err := process.stop(3 * time.Second); err != nil {
			t.Errorf("stop daemon CLI during cleanup: %v", err)
		}
	})
	return process
}

func (p *daemonCLIProcess) stopAndWait(t *testing.T, timeout time.Duration) {
	t.Helper()
	if err := p.stop(timeout); err != nil {
		t.Fatalf("daemon CLI did not stop cleanly: %v; stderr: %s", err, p.stderr.String())
	}
}

func (p *daemonCLIProcess) stop(timeout time.Duration) error {
	p.stopOnce.Do(func() {
		waited := false
		if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil {
			select {
			case waitErr := <-p.done:
				p.stopError = waitErr
				waited = true
			default:
				p.stopError = err
			}
		}
		if !waited {
			select {
			case err := <-p.done:
				p.stopError = err
			case <-time.After(timeout):
				_ = p.cmd.Process.Kill()
				select {
				case <-p.done:
					p.stopError = fmt.Errorf("daemon exceeded shutdown timeout %s", timeout)
				case <-time.After(3 * time.Second):
					p.stopError = fmt.Errorf("daemon did not exit after kill")
				}
			}
		}
	})
	return p.stopError
}

//go:build integration

// Run this end-to-end check with: go test -tags integration ./tests -count=1
package integration_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"github.com/gorilla/websocket"
	_ "modernc.org/sqlite"
)

// TestEncryptedCLIRelayFlow exercises real CLI and relay processes without
// network access, shared HOME state, or files in the repository worktree.
func TestEncryptedCLIRelayFlow(t *testing.T) {
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

	const aliceMessage = "private relay integration message"
	firstEventID := sendEncryptedMessage(t, cliBin, aliceHome, "alice", "bob", aliceMessage, relayURL)

	// Restart the actual relay process, keeping only its on-disk Bolt store.
	relay.stop()
	relay = startRelay(t, relayBin, dataDir, port)
	storedEvent := queryRelayEvent(t, relayURL, firstEventID)
	if !storedEvent.VerifySignature() {
		t.Fatal("relay returned an event with an invalid signature")
	}
	if strings.Contains(storedEvent.Content, aliceMessage) {
		t.Fatal("relay event content unexpectedly contains plaintext")
	}

	inboxOutput := runCLI(t, cliBin, bobHome,
		"agent", "inbox", "--as", "bob", "--relay", relayURL, "--json")
	var inbox struct {
		OK   bool `json:"ok"`
		Data []struct {
			Content   string `json:"content"`
			Encrypted bool   `json:"encrypted"`
			Decrypted bool   `json:"decrypted"`
		} `json:"data"`
	}
	if err := json.Unmarshal(inboxOutput, &inbox); err != nil {
		t.Fatalf("decode inbox result: %v", err)
	}
	if !inbox.OK || len(inbox.Data) != 1 || inbox.Data[0].Content != aliceMessage || !inbox.Data[0].Encrypted || !inbox.Data[0].Decrypted {
		t.Fatalf("inbox did not decrypt the published message: %s", safeJSON(inboxOutput))
	}

	historyOutput := runCLI(t, cliBin, bobHome, "history", "conversation", "--with", "alice", "--limit", "10")
	if !strings.Contains(string(historyOutput), aliceMessage) {
		t.Fatalf("history conversation did not show message text: %q", safeText(historyOutput))
	}
	assertHistoryRow(t, aliceHome, firstEventID, aliceNPub, bobNPub, aliceMessage, false)
	assertHistoryRow(t, bobHome, firstEventID, aliceNPub, bobNPub, aliceMessage, true)

	// Only begin the reverse direction after the first inbox count and history
	// row have been checked, so the two message flows stay independently clear.
	const bobMessage = "reply from bob to alice"
	secondEventID := sendEncryptedMessage(t, cliBin, bobHome, "bob", "alice", bobMessage, relayURL)
	if secondEventID == firstEventID {
		t.Fatal("reverse-direction message reused the first event ID")
	}
	secondEvent := queryRelayEvent(t, relayURL, secondEventID)
	if !secondEvent.VerifySignature() {
		t.Fatal("relay returned an invalid reverse-direction event signature")
	}
	if strings.Contains(secondEvent.Content, bobMessage) {
		t.Fatal("reverse-direction relay event content unexpectedly contains plaintext")
	}

	aliceInboxOutput := runCLI(t, cliBin, aliceHome,
		"agent", "inbox", "--as", "alice", "--relay", relayURL, "--json")
	var aliceInbox struct {
		OK   bool `json:"ok"`
		Data []struct {
			Content   string `json:"content"`
			Encrypted bool   `json:"encrypted"`
			Decrypted bool   `json:"decrypted"`
		} `json:"data"`
	}
	if err := json.Unmarshal(aliceInboxOutput, &aliceInbox); err != nil {
		t.Fatalf("decode Alice inbox result: %v", err)
	}
	if !aliceInbox.OK || len(aliceInbox.Data) != 1 || aliceInbox.Data[0].Content != bobMessage || !aliceInbox.Data[0].Encrypted || !aliceInbox.Data[0].Decrypted {
		t.Fatalf("Alice inbox did not decrypt Bob's reply: %s", safeJSON(aliceInboxOutput))
	}

	aliceHistory := runCLI(t, cliBin, aliceHome, "history", "conversation", "--with", "bob", "--limit", "10")
	bobHistory := runCLI(t, cliBin, bobHome, "history", "conversation", "--with", "alice", "--limit", "10")
	for name, output := range map[string][]byte{"Alice": aliceHistory, "Bob": bobHistory} {
		if !strings.Contains(string(output), aliceMessage) || !strings.Contains(string(output), bobMessage) {
			t.Fatalf("%s history did not show both message bodies: %q", name, safeText(output))
		}
	}
	assertHistoryRow(t, bobHome, secondEventID, bobNPub, aliceNPub, bobMessage, false)
	assertHistoryRow(t, aliceHome, secondEventID, bobNPub, aliceNPub, bobMessage, true)
}

func sendEncryptedMessage(t *testing.T, cliBin, home, from, to, plaintext, relayURL string) string {
	t.Helper()
	output := runCLI(t, cliBin, home,
		"agent", "msg", "--from", from, "--to", to, "--content", plaintext,
		"--relay", relayURL, "--json")
	var result struct {
		OK   bool `json:"ok"`
		Data struct {
			EventID     string `json:"event_id"`
			Encrypted   bool   `json:"encrypted"`
			PublishedTo int    `json:"published_to"`
			RelayCount  int    `json:"relay_count"`
		} `json:"data"`
	}
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatalf("decode send result: %v", err)
	}
	if !result.OK || !result.Data.Encrypted || result.Data.PublishedTo != 1 || result.Data.RelayCount != 1 || result.Data.EventID == "" {
		t.Fatalf("send result did not report one encrypted publish: %s", safeJSON(output))
	}
	return result.Data.EventID
}

func projectRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate integration test source")
	}
	return filepath.Dir(filepath.Dir(file))
}

func buildBinary(t *testing.T, root, output, packagePath string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "build", "-o", output, packagePath)
	cmd.Dir = root
	var stderr strings.Builder
	cmd.Stderr = &stderr
	cmd.Stdout = io.Discard
	if err := cmd.Run(); err != nil {
		t.Fatalf("build %s failed: %v (%s)", packagePath, err, stderr.String())
	}
}

func requireDir(t *testing.T, path string) {
	t.Helper()
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
}

func createIdentity(t *testing.T, cliBin, home, nickname string) string {
	t.Helper()
	runCLI(t, cliBin, home, "identity", "create", "--nickname", nickname, "--default")
	output := runCLI(t, cliBin, home, "identity", "list", "--json")
	var result struct {
		OK   bool `json:"ok"`
		Data []struct {
			Nickname string `json:"nickname"`
			Npub     string `json:"npub"`
		} `json:"data"`
	}
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatalf("decode identity list: %v", err)
	}
	if !result.OK || len(result.Data) != 1 || result.Data[0].Nickname != nickname || !strings.HasPrefix(result.Data[0].Npub, "npub1") {
		t.Fatalf("identity list did not return %s public identity: %s", nickname, safeJSON(output))
	}
	if strings.Contains(string(output), "nsec") {
		t.Fatalf("identity JSON exposed a secret-key field: %s", safeJSON(output))
	}
	return result.Data[0].Npub
}

func runCLI(t *testing.T, binary, home string, args ...string) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Env = isolatedEnv(home)
	output, err := cmd.Output()
	if err != nil {
		// Do not include raw command output in failures: it could accidentally
		// echo sensitive values if a command's diagnostics change later.
		t.Fatalf("CLI command %q failed: %v", args[0], err)
	}
	return output
}

func isolatedEnv(home string) []string {
	env := make([]string, 0, len(os.Environ())+1)
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "HOME=") || strings.HasPrefix(entry, "HYPHAE_OUTPUT=") || strings.HasPrefix(entry, "AGENT_SPEAKER_OUTPUT=") {
			continue
		}
		env = append(env, entry)
	}
	return append(env, "HOME="+home)
}

func freeLoopbackPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}

type relayProcess struct {
	cmd      *exec.Cmd
	done     chan struct{}
	waitErr  error
	stopOnce sync.Once
}

func startRelay(t *testing.T, binary, dataDir string, port int) *relayProcess {
	t.Helper()
	cmd := exec.Command(binary,
		"--listen", "127.0.0.1", "--port", strconv.Itoa(port), "--data-dir", dataDir)
	cmd.Env = isolatedEnv(filepath.Dir(dataDir))
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatalf("start test relay: %v", err)
	}
	process := &relayProcess{cmd: cmd, done: make(chan struct{})}
	go func() {
		process.waitErr = cmd.Wait()
		close(process.done)
	}()
	t.Cleanup(process.stop)

	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-process.done:
			t.Fatalf("test relay exited before becoming ready: %v", process.waitErr)
		default:
		}
		dialCtx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		conn, _, err := websocket.DefaultDialer.DialContext(dialCtx, fmt.Sprintf("ws://127.0.0.1:%d", port), nil)
		cancel()
		if err == nil {
			_ = conn.Close()
			return process
		}
		time.Sleep(50 * time.Millisecond)
	}
	process.stop()
	t.Fatal("test relay did not become ready before timeout")
	return nil
}

func (p *relayProcess) stop() {
	p.stopOnce.Do(func() {
		if p.cmd.Process != nil {
			_ = p.cmd.Process.Signal(os.Interrupt)
		}
		select {
		case <-p.done:
		case <-time.After(5 * time.Second):
			if p.cmd.Process != nil {
				_ = p.cmd.Process.Kill()
			}
			<-p.done
		}
	})
}

func queryRelayEvent(t *testing.T, relayURL, eventID string) nostr.Event {
	t.Helper()
	dialCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, _, err := websocket.DefaultDialer.DialContext(dialCtx, relayURL, nil)
	if err != nil {
		t.Fatalf("connect to restarted relay: %v", err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_ = conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if err := conn.WriteJSON([]any{"REQ", "hyphae-e2e", map[string]any{"kinds": []int{30078}}}); err != nil {
		t.Fatalf("query restarted relay: %v", err)
	}
	for {
		var response []json.RawMessage
		if err := conn.ReadJSON(&response); err != nil {
			t.Fatalf("read restarted relay query: %v", err)
		}
		if len(response) > 0 && string(response[0]) == `"EOSE"` {
			t.Fatalf("event %s was not found after relay restart", eventID)
		}
		if len(response) == 3 && string(response[0]) == `"EVENT"` {
			var event nostr.Event
			if err := json.Unmarshal(response[2], &event); err != nil {
				t.Fatalf("decode relay event: %v", err)
			}
			if event.ID.Hex() == eventID {
				return event
			}
		}
	}
}

func assertHistoryRow(t *testing.T, home, eventID, sender, recipient, plaintext string, incoming bool) {
	t.Helper()
	dbPath := filepath.Join(home, ".hyphae", "messages.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var storedID, storedSender, storedRecipient, storedPlaintext string
	var storedIncoming, encrypted bool
	err = db.QueryRow(`
		SELECT id, sender_npub, recipient_npub, plaintext, is_incoming, is_encrypted
		FROM messages WHERE id = ?
	`, eventID).Scan(&storedID, &storedSender, &storedRecipient, &storedPlaintext, &storedIncoming, &encrypted)
	if err != nil {
		t.Fatalf("read history row for event %s: %v", eventID, err)
	}
	if storedID != eventID || storedSender != sender || storedRecipient != recipient || storedPlaintext != plaintext || storedIncoming != incoming || !encrypted {
		t.Fatalf("history row mismatch for event %s (incoming=%t)", eventID, incoming)
	}
}

func safeJSON(output []byte) string {
	var value any
	if json.Unmarshal(output, &value) == nil {
		encoded, _ := json.Marshal(value)
		return string(encoded)
	}
	return "<non-JSON output omitted>"
}

func safeText(output []byte) string {
	text := string(output)
	if len(text) > 200 {
		return text[:200] + "..."
	}
	return text
}

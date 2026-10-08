package messaging

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
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
	"github.com/iDoris-ai/hyphae/pkg/types"
	_ "modernc.org/sqlite"
)

func TestOutboxRetryJSONValidatesBeforeDiskAccess(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "missing id", args: []string{"--json"}},
		{name: "zero timeout", args: []string{"--id", "missing", "--timeout", "0", "--json"}},
		{name: "negative timeout", args: []string{"--id", "missing", "--timeout", "-1", "--json"}},
		{name: "duration overflow", args: []string{"--id", "missing", "--timeout", "9223372037", "--json"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			args := append([]string{"storage", "outbox", "retry"}, tc.args...)
			result := runOutboxCLI(t, home, nil, args...)
			if result.code != 1 || result.stdout != "" {
				t.Fatalf("invalid retry exit=%d stdout=%q stderr=%q", result.code, result.stdout, result.stderr)
			}
			var response map[string]any
			if err := json.Unmarshal([]byte(result.stderr), &response); err != nil {
				t.Fatalf("expected one JSON error on stderr: %v (%q)", err, result.stderr)
			}
			if response["ok"] != false || response["error"] != "user_error" {
				t.Fatalf("unexpected error result: %#v", response)
			}
			if _, err := os.Stat(filepath.Join(home, ".hyphae")); !os.IsNotExist(err) {
				t.Fatalf("invalid arguments touched the keystore directory: stat err=%v", err)
			}
		})
	}
}

func TestOutboxRetryJSONWithLocalRelay(t *testing.T) {
	temp := t.TempDir()
	relayBin := filepath.Join(temp, "hyphae-relay")
	buildCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	build := exec.CommandContext(buildCtx, "go", "build", "-o", relayBin, "./cmd/hyphae-relay")
	build.Dir = projectRootFromTest(t)
	// Keep the Go build in the parent environment so it can use the configured
	// module cache. The relay process and CLI below still run with isolated HOME.
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build relay: %v: %s", err, output)
	}

	port := freeOutboxTestPort(t)
	relayURL := fmt.Sprintf("ws://127.0.0.1:%d", port)
	relay := startOutboxTestRelay(t, relayBin, filepath.Join(temp, "relay-data"), port)
	defer relay.stop()

	// A recorded relay must work even if the user's fallback config is corrupt.
	home := t.TempDir()
	writeOutboxRelayConfig(t, home, []byte("{"))
	event, entry := signedOutboxRetryFixture(t, "local retry plaintext", []string{relayURL})
	writeOutboxFixture(t, home, &types.Outbox{Entries: []types.OutboxEntry{entry}})
	result := runOutboxCLI(t, home, nil, "storage", "outbox", "retry", "--id", event.ID.Hex(), "--timeout", "2", "--json")
	data := requireRetrySuccess(t, result, event.ID.Hex())
	if data["sent"] != true || data["queued"] != false || data["history_stored"] != true || data["marked_failed"] != false {
		t.Fatalf("unexpected acknowledged retry result: %#v", data)
	}
	assertRetryQueueEmpty(t, home)
	assertRetryHistory(t, home, event.ID.Hex(), "local retry plaintext")
	stored := queryOutboxRelayEvent(t, relayURL, event.ID.Hex())
	if !stored.VerifySignature() || stored.ID.Hex() != event.ID.Hex() {
		t.Fatal("relay did not retain the original signed event ID")
	}

	// An entry with no recorded relays resolves the user's configured relay.
	configHome := t.TempDir()
	writeOutboxRelayConfig(t, configHome, []byte(fmt.Sprintf(`{"version":1,"relays":[%q]}`, relayURL)))
	fallbackEvent, fallbackEntry := signedOutboxRetryFixture(t, "configured relay plaintext", nil)
	writeOutboxFixture(t, configHome, &types.Outbox{Entries: []types.OutboxEntry{fallbackEntry}})
	fallback := runOutboxCLI(t, configHome, nil, "storage", "outbox", "retry", "--id", fallbackEvent.ID.Hex(), "--timeout", "2", "--json")
	fallbackData := requireRetrySuccess(t, fallback, fallbackEvent.ID.Hex())
	if fallbackData["sent"] != true || fallbackData["history_stored"] != true {
		t.Fatalf("configured relay was not used: %#v", fallbackData)
	}
	assertRetryQueueEmpty(t, configHome)
	assertRetryHistory(t, configHome, fallbackEvent.ID.Hex(), "configured relay plaintext")

	// Relay ACK can succeed while local history persistence fails. The error
	// must retain the known send/queue state without exposing event content.
	partialHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(partialHome, ".hyphae"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(partialHome, ".hyphae", "messages.db"), 0700); err != nil {
		t.Fatal(err)
	}
	partialEvent, partialEntry := signedOutboxRetryFixture(t, "private partial outcome", []string{relayURL})
	writeOutboxFixture(t, partialHome, &types.Outbox{Entries: []types.OutboxEntry{partialEntry}})
	partial := runOutboxCLI(t, partialHome, nil, "storage", "outbox", "retry", "--id", partialEvent.ID.Hex(), "--timeout", "2", "--json")
	if partial.code != 4 || partial.stdout != "" {
		t.Fatalf("partial retry exit=%d stdout=%q stderr=%q", partial.code, partial.stdout, partial.stderr)
	}
	var partialResponse map[string]any
	if err := json.Unmarshal([]byte(partial.stderr), &partialResponse); err != nil {
		t.Fatalf("partial retry was not one JSON error: %v", err)
	}
	partialData := partialResponse["data"].(map[string]any)
	if partialResponse["error"] != "other_error" || partialData["event_id"] != partialEvent.ID.Hex() || partialData["sent"] != true || partialData["queued"] != true || partialData["history_stored"] != false {
		t.Fatalf("partial outcome lost known state: %#v", partialResponse)
	}
	if strings.Contains(partial.stderr, "private partial outcome") || strings.Contains(partial.stdout, "private partial outcome") {
		t.Fatal("retry JSON exposed event content")
	}
}

func TestOutboxRetryJSONUnreachableRelayRemainsQueued(t *testing.T) {
	home := t.TempDir()
	port := freeOutboxTestPort(t)
	event, entry := signedOutboxRetryFixture(t, "offline relay plaintext", []string{fmt.Sprintf("ws://127.0.0.1:%d", port)})
	writeOutboxFixture(t, home, &types.Outbox{Entries: []types.OutboxEntry{entry}})
	result := runOutboxCLI(t, home, nil, "storage", "outbox", "retry", "--id", event.ID.Hex(), "--timeout", "1", "--json")
	data := requireRetrySuccess(t, result, event.ID.Hex())
	if data["attempted"] != true || data["sent"] != false || data["queued"] != true || data["marked_failed"] != false {
		t.Fatalf("unreachable relay did not remain queued: %#v", data)
	}
	encoded, err := os.ReadFile(filepath.Join(home, ".hyphae", "outbox.json"))
	if err != nil {
		t.Fatal(err)
	}
	var persisted types.Outbox
	if err := json.Unmarshal(encoded, &persisted); err != nil {
		t.Fatal(err)
	}
	if len(persisted.Entries) != 1 || persisted.Entries[0].ID != event.ID.Hex() || persisted.Entries[0].RetryCount != 1 {
		t.Fatalf("failed retry did not preserve and update queue entry: %#v", persisted.Entries)
	}
}

func signedOutboxRetryFixture(t *testing.T, content string, relays []string) (nostr.Event, types.OutboxEntry) {
	t.Helper()
	secret := nostr.Generate()
	recipient := nostr.Generate().Public()
	event := nostr.Event{CreatedAt: nostr.Now(), Kind: AgentKind, PubKey: secret.Public(), Content: content, Tags: nostr.Tags{
		{"p", hex.EncodeToString(recipient[:])}, {"c", AgentTag}, {"v", AgentVersion},
	}}
	if err := event.Sign(secret); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	return event, types.OutboxEntry{
		ID: event.ID.Hex(), EventJSON: string(encoded), RecipientNpub: "npub1retryrecipient",
		Relays: relays, RetryCount: 0, MaxRetries: 10, CreatedAt: int64(event.CreatedAt), Status: "pending",
	}
}

// signedGroupOutboxRetryFixture builds a realistic signed group-route outbox
// entry: same event shape as signedOutboxRetryFixture (AgentKind, and the
// required "c"/"v" tags every real agent message carries), but queued with
// Route=group and Status=group_pending the way EnqueueGroupOutboxEntry
// actually writes one.
func signedGroupOutboxRetryFixture(t *testing.T, content string, relays []string) (nostr.Event, types.OutboxEntry) {
	t.Helper()
	secret := nostr.Generate()
	recipient := nostr.Generate().Public()
	event := nostr.Event{CreatedAt: nostr.Now(), Kind: AgentKind, PubKey: secret.Public(), Content: content, Tags: nostr.Tags{
		{"p", hex.EncodeToString(recipient[:])}, {"c", AgentTag}, {"v", AgentVersion},
	}}
	if err := event.Sign(secret); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	return event, types.OutboxEntry{
		ID: event.ID.Hex(), Route: OutboxRouteGroup, EventJSON: string(encoded), RecipientNpub: "npub1groupretry",
		Relays: relays, RetryCount: 0, MaxRetries: 10, CreatedAt: int64(event.CreatedAt), Status: OutboxStatusGroupPending,
	}
}

// TestOutboxRetryCLIRejectsGroupEntryBeforeTouchingRelayOrHistory is the
// regression test for the S5a review Blocker B escalation: `storage outbox
// retry --id` must reject a group-route entry before it ever reaches a
// relay or local history, proven end to end through the real compiled CLI
// binary against a real running relay (not just the in-process Action call)
// -- so there is no gap between what the unit test exercises and what a
// user actually running the binary would hit.
func TestOutboxRetryCLIRejectsGroupEntryBeforeTouchingRelayOrHistory(t *testing.T) {
	temp := t.TempDir()
	relayBin := filepath.Join(temp, "hyphae-relay")
	buildCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	build := exec.CommandContext(buildCtx, "go", "build", "-o", relayBin, "./cmd/hyphae-relay")
	build.Dir = projectRootFromTest(t)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build relay: %v: %s", err, output)
	}
	port := freeOutboxTestPort(t)
	relayURL := fmt.Sprintf("ws://127.0.0.1:%d", port)
	relay := startOutboxTestRelay(t, relayBin, filepath.Join(temp, "relay-data"), port)
	defer relay.stop()

	home := t.TempDir()
	event, entry := signedGroupOutboxRetryFixture(t, "group plaintext must stay private", []string{relayURL})
	before := writeOutboxFixture(t, home, &types.Outbox{Entries: []types.OutboxEntry{entry}})

	result := runOutboxCLI(t, home, nil, "storage", "outbox", "retry", "--id", event.ID.Hex(), "--timeout", "2", "--json")
	if result.code != 1 || result.stdout != "" {
		t.Fatalf("group retry exit=%d stdout=%q stderr=%q", result.code, result.stdout, result.stderr)
	}
	response := decodeOutboxResult(t, result.stderr)
	if response["ok"] != false || response["error"] != "user_error" {
		t.Fatalf("unexpected group retry error shape: %#v", response)
	}

	// Queue entry retained byte-for-byte: no QueueID backfill, no retry
	// count bump, no status change, no removal.
	after, err := os.ReadFile(filepath.Join(home, ".hyphae", "outbox.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("rejected group retry mutated the queue file:\nbefore=%s\nafter=%s", before, after)
	}

	// No DM history row was written for the group event.
	if _, err := os.Stat(filepath.Join(home, ".hyphae", "messages.db")); !os.IsNotExist(err) {
		t.Fatalf("rejected group retry created a history database: stat err=%v", err)
	}

	assertRelayNeverReceivedEvent(t, relayURL, event.ID.Hex())
}

// assertRelayNeverReceivedEvent queries the relay for the given event ID and
// fails the test if the relay returns it -- i.e. it fails only if a publish
// actually reached the relay, which is the strongest available proof that
// AttemptSend's publish step was never invoked.
func assertRelayNeverReceivedEvent(t *testing.T, relayURL, eventID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, relayURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	_ = conn.SetWriteDeadline(time.Now().Add(1 * time.Second))
	if err := conn.WriteJSON([]any{"REQ", "retry-rejection-check", map[string]any{"ids": []string{eventID}}}); err != nil {
		t.Fatal(err)
	}
	for {
		var response []json.RawMessage
		if err := conn.ReadJSON(&response); err != nil {
			t.Fatal(err)
		}
		if len(response) > 0 && string(response[0]) == `"EOSE"` {
			return
		}
		if len(response) == 3 && string(response[0]) == `"EVENT"` {
			var event nostr.Event
			if err := json.Unmarshal(response[2], &event); err != nil {
				t.Fatal(err)
			}
			if event.ID.Hex() == eventID {
				t.Fatalf("relay received the rejected group entry's event %s -- AttemptSend published it", eventID)
			}
		}
	}
}

func requireRetrySuccess(t *testing.T, result outboxCLIResult, eventID string) map[string]any {
	t.Helper()
	if result.code != 0 || result.stderr != "" {
		t.Fatalf("retry exit=%d stdout=%q stderr=%q", result.code, result.stdout, result.stderr)
	}
	response := decodeOutboxResult(t, result.stdout)
	data, ok := response["data"].(map[string]any)
	if response["ok"] != true || !ok || data["event_id"] != eventID {
		t.Fatalf("unexpected retry success: %#v", response)
	}
	want := []string{"event_id", "attempted", "sent", "queued", "marked_failed", "history_stored", "superseded", "queue_state_unknown"}
	if len(data) != len(want) {
		t.Fatalf("unexpected retry fields: %#v", data)
	}
	for _, key := range want {
		if _, exists := data[key]; !exists {
			t.Errorf("missing retry field %q: %#v", key, data)
		}
	}
	return data
}

func assertRetryQueueEmpty(t *testing.T, home string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(home, ".hyphae", "outbox.json"))
	if err != nil {
		t.Fatal(err)
	}
	var ob types.Outbox
	if err := json.Unmarshal(b, &ob); err != nil {
		t.Fatal(err)
	}
	if len(ob.Entries) != 0 {
		t.Fatalf("ACKed entry remains queued: %#v", ob.Entries)
	}
}

func assertRetryHistory(t *testing.T, home, eventID, plaintext string) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(home, ".hyphae", "messages.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var storedID, storedPlaintext string
	if err := db.QueryRow("SELECT id, plaintext FROM messages WHERE id = ?", eventID).Scan(&storedID, &storedPlaintext); err != nil {
		t.Fatal(err)
	}
	if storedID != eventID || storedPlaintext != plaintext {
		t.Fatalf("history mismatch for event %s", eventID)
	}
}

func writeOutboxRelayConfig(t *testing.T, home string, contents []byte) {
	t.Helper()
	dir := filepath.Join(home, ".hyphae")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "relays.json"), contents, 0600); err != nil {
		t.Fatal(err)
	}
}

func freeOutboxTestPort(t *testing.T) int {
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

type outboxRelayProcess struct {
	cmd  *exec.Cmd
	done chan struct{}
	once sync.Once
}

func startOutboxTestRelay(t *testing.T, binary, dataDir string, port int) *outboxRelayProcess {
	t.Helper()
	cmd := exec.Command(binary, "--listen", "127.0.0.1", "--port", strconv.Itoa(port), "--data-dir", dataDir)
	cmd.Env = isolatedOutboxCLIEnv(os.Environ(), filepath.Dir(dataDir), nil)
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	process := &outboxRelayProcess{cmd: cmd, done: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		close(process.done)
	}()
	t.Cleanup(process.stop)
	deadline := time.Now().Add(8 * time.Second)
	url := fmt.Sprintf("ws://127.0.0.1:%d", port)
	for time.Now().Before(deadline) {
		select {
		case <-process.done:
			t.Fatal("test relay exited before becoming ready")
		default:
		}
		ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
		conn, _, err := websocket.DefaultDialer.DialContext(ctx, url, nil)
		cancel()
		if err == nil {
			_ = conn.Close()
			return process
		}
		time.Sleep(50 * time.Millisecond)
	}
	process.stop()
	t.Fatal("test relay did not become ready")
	return nil
}

func (p *outboxRelayProcess) stop() {
	p.once.Do(func() {
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

func queryOutboxRelayEvent(t *testing.T, relayURL, eventID string) nostr.Event {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, relayURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	_ = conn.SetWriteDeadline(time.Now().Add(1 * time.Second))
	if err := conn.WriteJSON([]any{"REQ", "retry-check", map[string]any{"ids": []string{eventID}}}); err != nil {
		t.Fatal(err)
	}
	for {
		var response []json.RawMessage
		if err := conn.ReadJSON(&response); err != nil {
			t.Fatal(err)
		}
		if len(response) > 0 && string(response[0]) == `"EOSE"` {
			t.Fatalf("relay did not retain event %s", eventID)
		}
		if len(response) == 3 && string(response[0]) == `"EVENT"` {
			var event nostr.Event
			if err := json.Unmarshal(response[2], &event); err != nil {
				t.Fatal(err)
			}
			if event.ID.Hex() == eventID {
				return event
			}
		}
	}
}

func projectRootFromTest(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate test source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
}

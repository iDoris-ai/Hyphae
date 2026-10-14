package daemon

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"github.com/gorilla/websocket"
	"github.com/iDoris-ai/hyphae/internal/groupchat"
	"github.com/iDoris-ai/hyphae/internal/identity"
	"github.com/iDoris-ai/hyphae/internal/messaging"
	"github.com/iDoris-ai/hyphae/internal/relayquery"
	"github.com/iDoris-ai/hyphae/internal/storage"
	"github.com/iDoris-ai/hyphae/pkg/types"
	"github.com/urfave/cli/v3"
	_ "modernc.org/sqlite"
)

const cloudStartupPassword = "cloud-startup-test-secret"

// Cloud TEST-FIRST startup acceptance against real DaemonCmd.Action.
// Expected RED on this baseline wherever group init is not yet wired into
// daemon startup (owner policy: group migrate failure refuses before any
// relay dial / outbox worker). Do not patch production to make these green.

func TestCloudHealthyOpenDBNewStoreControl(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	messaging.ResetStoreForTest()
	t.Cleanup(messaging.ResetStoreForTest)

	owned, err := storage.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer owned.Close()
	if _, err := groupchat.NewStore(owned); err != nil {
		t.Fatalf("healthy group schema control must pass: %v", err)
	}
}

func TestCloudDaemonRefusesBrokenGroupSchemaBeforeRelayDial(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	messaging.ResetStoreForTest()
	t.Cleanup(messaging.ResetStoreForTest)

	cloudCreateEncryptedIdentity(t, "alice")
	if err := cloudPlantBrokenFanoutSchema(t); err != nil {
		t.Fatal(err)
	}
	outboxPath, beforeBytes, beforeInfo := cloudWriteOutboxMarker(t)

	conns := &atomic.Int32{}
	relayURL := cloudStartCountingRelay(t, conns)

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	err := cloudRunDaemonAction(ctx, t, []string{
		"hyphae", "daemon",
		"--identity", "alice",
		"--relay", relayURL,
		"--retry-interval", "3600",
		"--watch-interval", "3600",
		"--notify=false",
		"--password-stdin",
	}, cloudStartupPassword+"\n")

	// Owner policy: refuse group init/migration failure BEFORE any relay dial.
	if err == nil {
		t.Fatalf("EXPECTED-RED policy gap: DaemonCmd.Action returned nil with broken group schema; connections=%d (want refuse before dial)", conns.Load())
	}
	if !cloudLooksLikeGroupInitRefusal(err) {
		t.Fatalf("EXPECTED-RED policy gap: daemon error must be group-init refusal before dial; got err=%v connections=%d", err, conns.Load())
	}
	if got := conns.Load(); got != 0 {
		t.Fatalf("fail-closed startup must not dial relay; connections=%d", got)
	}
	cloudRequireOutboxUnchanged(t, outboxPath, beforeBytes, beforeInfo)
}

func TestCloudDaemonRefusesCorruptMessagesDBBeforeRelayDial(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	messaging.ResetStoreForTest()
	t.Cleanup(messaging.ResetStoreForTest)

	cloudCreateEncryptedIdentity(t, "alice")
	keystore := filepath.Join(home, ".hyphae")
	if err := os.MkdirAll(keystore, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(keystore, "messages.db"), []byte("not a sqlite database"), 0o600); err != nil {
		t.Fatal(err)
	}
	outboxPath, beforeBytes, beforeInfo := cloudWriteOutboxMarker(t)

	conns := &atomic.Int32{}
	relayURL := cloudStartCountingRelay(t, conns)

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	err := cloudRunDaemonAction(ctx, t, []string{
		"hyphae", "daemon",
		"--identity", "alice",
		"--relay", relayURL,
		"--retry-interval", "3600",
		"--watch-interval", "3600",
		"--notify=false",
		"--password-stdin",
	}, cloudStartupPassword+"\n")

	if err == nil {
		t.Fatalf("EXPECTED-RED policy gap: corrupt messages.db must fail daemon startup before dial; connections=%d", conns.Load())
	}
	if !cloudLooksLikeStorageStartupRefusal(err) {
		t.Fatalf("EXPECTED-RED policy gap: corrupt messages.db must fail closed at startup; got err=%v connections=%d", err, conns.Load())
	}
	if got := conns.Load(); got != 0 {
		t.Fatalf("fail-closed startup must not dial relay; connections=%d err=%v", got, err)
	}
	cloudRequireOutboxUnchanged(t, outboxPath, beforeBytes, beforeInfo)
}

// TestCloudHealthyStartupInjectedStorageFailureAfterStart keeps the old
// runtime scan/storage-error coverage model: healthy startup path + injected
// processing/storage failure AFTER start (hook seam), not by weakening
// startup to share the corrupt-DB case. Companion evidence also lives in
// TestDaemonJSONLocalRelayStorageFailure.
func TestCloudHealthyStartupInjectedStorageFailureAfterStart(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	messaging.ResetStoreForTest()
	t.Cleanup(messaging.ResetStoreForTest)
	if err := messaging.InitStorage(); err != nil {
		t.Fatal(err)
	}
	if _, err := groupchat.NewStore(storage.DB); err != nil {
		t.Fatalf("healthy group schema required for this control: %v", err)
	}

	ks := &types.KeyStore{Identities: make(map[string]*types.Identity), Contacts: make(map[string]*types.Contact)}
	myIdentity, err := identity.CreateIdentity(ks, "alice")
	if err != nil {
		t.Fatal(err)
	}
	mySK, err := identity.GetSecretKey(ks, "alice")
	if err != nil {
		t.Fatal(err)
	}
	compressed, err := messaging.CompressText("post-start-storage-failure")
	if err != nil {
		t.Fatal(err)
	}
	event := signedIncomingEvent(t, nostr.Generate(), mySK.Public(), compressed, nostr.Tags{{"z", messaging.CompressTag}})

	hooks := incomingReceiveHooks{store: func(*nostr.Event, string, string, bool) (bool, error) {
		return false, errors.New("injected storage failure after healthy start")
	}}
	walk := func(ctx context.Context, url string, filter nostr.Filter, callback func(nostr.Event) error) (relayquery.Stats, error) {
		if err := callback(*event); err != nil {
			return relayquery.Stats{}, err
		}
		return relayquery.Stats{Pages: 1, Fetched: 1, FinishedHint: true}, nil
	}
	result, scanErr := watchOneRelayWithHooksResult(context.Background(), "wss://relay.invalid", nostr.Filter{}, ks, mySK, newSeenSet(), false, false, myIdentity, nil, hooks, walk)
	if scanErr == nil {
		t.Fatal("injected storage failure must surface as processing failure")
	}
	if result.ProcessingFailures != 1 {
		t.Fatalf("ProcessingFailures=%d want 1", result.ProcessingFailures)
	}
	if result.NewMessages != 0 {
		t.Fatalf("NewMessages=%d want 0", result.NewMessages)
	}
	if !strings.Contains(scanErr.Error(), "failed processing") {
		t.Fatalf("scanErr=%v", scanErr)
	}
}

func cloudLooksLikeGroupInitRefusal(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	if cloudIsTimeoutOrCancel(err, msg) {
		return false
	}
	return strings.Contains(msg, "group") || strings.Contains(msg, "fanout") || strings.Contains(msg, "invalid counters") || strings.Contains(msg, "migrate")
}

func cloudLooksLikeStorageStartupRefusal(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	if cloudIsTimeoutOrCancel(err, msg) {
		return false
	}
	return strings.Contains(msg, "database") || strings.Contains(msg, "sqlite") || strings.Contains(msg, "storage") || strings.Contains(msg, "migrate") || strings.Contains(msg, "ping") || strings.Contains(msg, "file is not a database")
}

func cloudIsTimeoutOrCancel(err error, msg string) bool {
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) ||
		strings.Contains(msg, "deadline") || strings.Contains(msg, "canceled") || strings.Contains(msg, "cancelled")
}

func cloudRunDaemonAction(ctx context.Context, t *testing.T, args []string, stdin string) error {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	originalStdin := os.Stdin
	os.Stdin = r
	defer func() {
		os.Stdin = originalStdin
		_ = r.Close()
	}()
	go func() {
		_, _ = io.WriteString(w, stdin)
		_ = w.Close()
	}()

	app := &cli.Command{Name: "hyphae", Commands: []*cli.Command{DaemonCmd}}
	return app.Run(ctx, args)
}

func cloudCreateEncryptedIdentity(t *testing.T, nick string) {
	t.Helper()
	ks := &types.KeyStore{Identities: make(map[string]*types.Identity), Contacts: make(map[string]*types.Contact)}
	if _, err := identity.CreateIdentityWithPassword(ks, nick, cloudStartupPassword); err != nil {
		t.Fatal(err)
	}
	ks.MasterKey = nil
	if err := identity.SaveKeyStore(ks); err != nil {
		t.Fatal(err)
	}
}

func cloudWriteOutboxMarker(t *testing.T) (string, []byte, os.FileInfo) {
	t.Helper()
	path, err := messaging.GetOutboxPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"entries":[{"id":"cloud-startup-marker","event_json":"{}","status":"sent","retry_count":0,"max_retries":10,"created_at":1,"recipient_npub":"npub1cloud"}]}`)
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return path, append([]byte(nil), payload...), info
}

func cloudRequireOutboxUnchanged(t *testing.T, path string, before []byte, beforeInfo os.FileInfo) {
	t.Helper()
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("outbox bytes mutated on fail-closed path\nbefore=%s\nafter=%s", before, after)
	}
	if !info.ModTime().Equal(beforeInfo.ModTime()) {
		t.Fatalf("outbox mtime mutated on fail-closed path before=%s after=%s", beforeInfo.ModTime(), info.ModTime())
	}
}

func cloudStartCountingRelay(t *testing.T, conns *atomic.Int32) string {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conns.Add(1)
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	t.Cleanup(server.Close)
	return "ws" + strings.TrimPrefix(server.URL, "http")
}

func cloudPlantBrokenFanoutSchema(t *testing.T) error {
	t.Helper()
	db, err := storage.OpenDB()
	if err != nil {
		return err
	}
	path, err := storage.GetDBPath()
	if err != nil {
		_ = db.Close()
		return err
	}
	if err := db.Close(); err != nil {
		return err
	}
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer raw.Close()
	_, err = raw.Exec(`CREATE TABLE groupchat_fanout (
 local_npub TEXT NOT NULL, group_id TEXT NOT NULL, envelope_type TEXT NOT NULL, send_key TEXT NOT NULL,
 recipient_npub TEXT NOT NULL, event_id TEXT NOT NULL, event_json TEXT NOT NULL, queue_id TEXT NOT NULL DEFAULT '',
 state TEXT NOT NULL, issue TEXT NOT NULL DEFAULT '', relay_acks INTEGER NOT NULL DEFAULT 0, relay_count INTEGER NOT NULL DEFAULT 0,
 attempts INTEGER NOT NULL DEFAULT 0, max_retries INTEGER NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL,
 last_attempt_at INTEGER NOT NULL DEFAULT 0, accepted_at INTEGER NOT NULL DEFAULT 0,
 PRIMARY KEY(local_npub,group_id,envelope_type,send_key,recipient_npub), UNIQUE(local_npub,event_id))`)
	if err != nil {
		return err
	}
	_, err = raw.Exec(`INSERT INTO groupchat_fanout(local_npub,group_id,envelope_type,send_key,recipient_npub,event_id,event_json,queue_id,state,attempts,max_retries,created_at,updated_at)
 VALUES('n','g','message','m','r','eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee','{}','q','queued',1,0,1,1)`)
	return err
}

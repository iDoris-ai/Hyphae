package tui

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/iDoris-ai/hyphae/internal/groupchat"
	"github.com/iDoris-ai/hyphae/internal/identity"
	"github.com/iDoris-ai/hyphae/internal/messaging"
	"github.com/iDoris-ai/hyphae/internal/storage"
	"github.com/iDoris-ai/hyphae/pkg/types"
	_ "modernc.org/sqlite"
)

// Cloud TEST-FIRST startup acceptance against real NewChatModel.
// Expected RED on this baseline where group init is not wired into TUI
// construction (owner policy: group migrate failure refuses before watcher /
// outbox worker / alt-screen). Do not patch production to make these green.

func TestCloudHealthyNewChatModelControl(t *testing.T) {
	messaging.ResetStoreForTest()
	t.Cleanup(messaging.ResetStoreForTest)
	home := t.TempDir()
	t.Setenv("HOME", home)

	cloudSeedIdentityAndContact(t, "alice", "bob")
	model, err := NewChatModel("bob", "ws://127.0.0.1:1")
	if err != nil {
		t.Fatalf("healthy control: NewChatModel must succeed: %v", err)
	}
	defer func() { _ = model.Close() }()

	owned, err := storage.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer owned.Close()
	if _, err := groupchat.NewStore(owned); err != nil {
		t.Fatalf("healthy group schema control must pass: %v", err)
	}
}

func TestCloudNewChatModelRefusesBrokenGroupSchemaBeforeWatcher(t *testing.T) {
	messaging.ResetStoreForTest()
	t.Cleanup(func() {
		messaging.ResetStoreForTest()
		_ = storage.CloseDB()
		storage.DB = nil
	})
	home := t.TempDir()
	t.Setenv("HOME", home)

	cloudSeedIdentityAndContact(t, "alice", "bob")
	if err := cloudPlantBrokenFanoutSchema(t); err != nil {
		t.Fatal(err)
	}

	conns := &atomic.Int32{}
	relayURL := cloudStartCountingRelay(t, conns)

	model, err := NewChatModel("bob", relayURL)
	if err == nil {
		// If construction wrongly succeeds, prove no worker/dial yet and still fail the policy.
		defer func() { _ = model.Close() }()
		if model.outboxStarted || model.inboxStarted {
			t.Fatalf("workers must not start during NewChatModel construction")
		}
		t.Fatalf("EXPECTED-RED policy gap: NewChatModel must refuse broken group schema before watcher/outbox; got model ok connections=%d", conns.Load())
	}
	if !cloudLooksLikeGroupInitRefusal(err) {
		t.Fatalf("EXPECTED-RED policy gap: NewChatModel error must be group-init refusal; got %v", err)
	}
	if got := conns.Load(); got != 0 {
		t.Fatalf("fail-closed NewChatModel must not dial relay; connections=%d", got)
	}
	time.Sleep(50 * time.Millisecond)
	if got := conns.Load(); got != 0 {
		t.Fatalf("no deferred dial after refused NewChatModel; connections=%d", got)
	}
}

func TestCloudNewChatModelRefusesCorruptMessagesDB(t *testing.T) {
	messaging.ResetStoreForTest()
	t.Cleanup(func() {
		messaging.ResetStoreForTest()
		_ = storage.CloseDB()
		storage.DB = nil
	})
	home := t.TempDir()
	t.Setenv("HOME", home)

	cloudSeedIdentityAndContact(t, "alice", "bob")
	path := filepath.Join(home, ".hyphae", "messages.db")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not a sqlite database"), 0o600); err != nil {
		t.Fatal(err)
	}

	conns := &atomic.Int32{}
	relayURL := cloudStartCountingRelay(t, conns)
	_, err := NewChatModel("bob", relayURL)
	if err == nil {
		t.Fatal("corrupt messages.db must make NewChatModel fail")
	}
	if got := conns.Load(); got != 0 {
		t.Fatalf("corrupt DB must not dial relay; connections=%d", got)
	}
}

func cloudLooksLikeGroupInitRefusal(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "group") || strings.Contains(msg, "fanout") || strings.Contains(msg, "invalid counters") || strings.Contains(msg, "migrate")
}

func cloudSeedIdentityAndContact(t *testing.T, selfNick, contactNick string) {
	t.Helper()
	ks := &types.KeyStore{Identities: make(map[string]*types.Identity), Contacts: make(map[string]*types.Contact)}
	if _, err := identity.CreateIdentity(ks, selfNick); err != nil {
		t.Fatal(err)
	}
	contact, err := identity.CreateIdentity(ks, contactNick)
	if err != nil {
		t.Fatal(err)
	}
	if err := identity.AddContact(ks, contactNick, contact.Npub); err != nil {
		t.Fatal(err)
	}
	if err := identity.SaveKeyStore(ks); err != nil {
		t.Fatal(err)
	}
}

func cloudStartCountingRelay(t *testing.T, conns *atomic.Int32) string {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conns.Add(1)
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		for {
			if _, _, err := c.ReadMessage(); err != nil {
				return
			}
		}
	}))
	t.Cleanup(server.Close)
	return "ws" + strings.TrimPrefix(server.URL, "http")
}

func cloudPlantBrokenFanoutSchema(t *testing.T) error {
	t.Helper()
	// Ensure DM schema exists, then plant legacy fanout with invalid counters.
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

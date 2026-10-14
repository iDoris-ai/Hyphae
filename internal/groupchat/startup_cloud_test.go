package groupchat

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/iDoris-ai/hyphae/internal/storage"
	_ "modernc.org/sqlite"
)

// Cloud TEST-FIRST startup acceptance fixtures: prove owned OpenDB + inert
// NewStore behave as documented, and that a real broken fanout schema/counters
// case fails NewStore while leaving DM messages readable. Daemon/TUI wiring of
// this gate is covered in sibling Cloud tests (expected RED on this baseline).

func TestCloudHealthyOpenDBNewStoreAndOwnedHandleCloses(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	sentinel, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer sentinel.Close()
	previous := storage.DB
	storage.DB = sentinel
	t.Cleanup(func() { storage.DB = previous })

	owned, err := storage.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	if storage.DB != sentinel {
		t.Fatal("OpenDB must not replace the package singleton")
	}
	store, err := NewStore(owned)
	if err != nil {
		t.Fatalf("healthy group schema control: NewStore must succeed: %v", err)
	}
	if store == nil {
		t.Fatal("NewStore returned nil store")
	}
	if err := owned.Close(); err != nil {
		t.Fatal(err)
	}
	if storage.DB != sentinel {
		t.Fatal("closing owned handle must not change the singleton")
	}
	if err := sentinel.Ping(); err != nil {
		t.Fatalf("unrelated global pool must remain usable: %v", err)
	}
}

func TestCloudBrokenFanoutCountersMakeNewStoreFailWhileMessagesReadable(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	if err := cloudPlantBrokenFanoutSchema(t); err != nil {
		t.Fatal(err)
	}

	owned, err := storage.OpenDB()
	if err != nil {
		t.Fatalf("OpenDB must still open DM schema: %v", err)
	}
	defer owned.Close()

	_, err = NewStore(owned)
	if err == nil {
		t.Fatal("broken fanout counters must make NewStore fail")
	}
	if !strings.Contains(err.Error(), "invalid counters") && !strings.Contains(err.Error(), "groupchat") {
		t.Fatalf("unexpected NewStore error: %v", err)
	}

	var n int
	if err := owned.QueryRow(`SELECT COUNT(*) FROM messages`).Scan(&n); err != nil {
		t.Fatalf("DM messages table must remain readable: %v", err)
	}

	// Owner handle close must not disturb an independent OpenDB pool.
	other, err := storage.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if _, err := other.Exec(`INSERT INTO messages (id, sender_npub, recipient_npub, created_at, received_at) VALUES ('cloud-dm', 's', 'r', 1, 1)`); err != nil {
		t.Fatalf("independent pool must remain usable after NewStore failure: %v", err)
	}
}

// cloudPlantBrokenFanoutSchema writes a legacy groupchat_fanout table with
// invalid counters into the owned messages.db after a healthy OpenDB migrate.
// CREATE TABLE IF NOT EXISTS will not replace it; migrateFanoutAccounting then
// fails closed on invalid counters.
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
	if err != nil {
		return err
	}
	return nil
}

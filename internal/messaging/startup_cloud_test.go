package messaging

import (
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/iDoris-ai/hyphae/internal/identity"
	"github.com/iDoris-ai/hyphae/internal/storage"
	"github.com/iDoris-ai/hyphae/pkg/types"
	_ "modernc.org/sqlite"
)

// Cloud TEST-FIRST: broken group fanout schema must not make DM history /
// DM CLI unreadable. Isolation case keeps messages table healthy.

func TestCloudBrokenGroupSchemaLeavesDMHistoryAndCLIHealthy(t *testing.T) {
	ResetStoreForTest()
	t.Cleanup(ResetStoreForTest)

	home := t.TempDir()
	t.Setenv("HOME", home)

	ks := &types.KeyStore{Identities: map[string]*types.Identity{}, Contacts: map[string]*types.Contact{}}
	alice, err := identity.CreateIdentity(ks, "alice")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := identity.CreateIdentity(ks, "bob")
	if err != nil {
		t.Fatal(err)
	}
	if err := identity.SaveKeyStore(ks); err != nil {
		t.Fatal(err)
	}

	db, err := storage.InitDB()
	if err != nil {
		t.Fatal(err)
	}
	store := storage.NewMessageStore(db)
	msg := &types.StoredMessage{
		ID: "cloud-dm-1", SenderNpub: alice.Npub, RecipientNpub: bob.Npub,
		Content: "ciphertext", Plaintext: "hello-cloud-dm", CreatedAt: 10, ReceivedAt: 11,
		IsEncrypted: true, IsIncoming: false,
	}
	if err := store.StoreMessage(msg); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	storage.DB = nil

	if err := cloudPlantBrokenFanoutSchema(t); err != nil {
		t.Fatal(err)
	}

	// In-process DM history read through InitStorage / GetStore.
	ResetStoreForTest()
	got, err := GetStore()
	if err != nil {
		t.Fatalf("DM store must open despite broken group fanout: %v", err)
	}
	history, err := got.GetConversation(alice.Npub, bob.Npub, 10)
	if err != nil {
		t.Fatalf("DM conversation read failed: %v", err)
	}
	if len(history) != 1 || history[0].Plaintext != "hello-cloud-dm" {
		t.Fatalf("unexpected DM history: %#v", history)
	}

	// DM CLI history conversation must remain usable (reuses package outboxCLI).
	result := runHistoryConversationCLI(t, home, nil, "history", "conversation", "--with", "bob", "--json")
	if result.code != 0 {
		t.Fatalf("DM history CLI exit=%d stdout=%q stderr=%q", result.code, result.stdout, result.stderr)
	}
	var response struct {
		OK   bool                  `json:"ok"`
		Data []types.StoredMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(result.stdout), &response); err != nil {
		t.Fatalf("invalid JSON: %v: %s", err, result.stdout)
	}
	if !response.OK || len(response.Data) != 1 || response.Data[0].ID != "cloud-dm-1" {
		t.Fatalf("unexpected CLI history: %#v", response)
	}
}

func cloudPlantBrokenFanoutSchema(t *testing.T) error {
	t.Helper()
	path, err := storage.GetDBPath()
	if err != nil {
		return err
	}
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer raw.Close()
	_, err = raw.Exec(`CREATE TABLE IF NOT EXISTS groupchat_fanout (
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

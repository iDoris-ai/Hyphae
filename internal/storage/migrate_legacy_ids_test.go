package storage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/iDoris-ai/hyphae/pkg/types"
)

// Legacy messages with no id of their own get one synthesised from CreatedAt,
// which has one-second resolution. Two messages sent in the same second
// therefore produced the same id, and the migration silently kept only one.
//
// That was already data loss before this change; the upsert made it visible in
// a new way (rather than one row overwriting the other wholesale, the two
// merged into a single row with mixed provenance whose direction could be
// wrong). Including the index in the id removes the collision itself, so both
// messages survive and neither problem can occur.
func TestMigrationKeepsBothMessagesFromTheSameSecond(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dir := filepath.Join(home, ".hyphae")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatalf("creating keystore dir: %v", err)
	}

	// Two distinct messages, same second, neither carrying an id — one sent,
	// one received, so a merged row would also have a falsified direction.
	legacy := types.MessageStore{Messages: []types.StoredMessage{
		{SenderNpub: "npub1me", RecipientNpub: "npub1bob", Plaintext: "I sent this to Bob", CreatedAt: 1700000000, IsIncoming: false},
		{SenderNpub: "npub1bob", RecipientNpub: "npub1me", Plaintext: "Bob replied", CreatedAt: 1700000000, IsIncoming: true},
	}}
	blob, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("marshalling legacy store: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "messages.json"), blob, 0600); err != nil {
		t.Fatalf("writing legacy store: %v", err)
	}

	db, err := InitDB()
	if err != nil {
		t.Fatalf("opening db: %v", err)
	}
	defer db.Close()
	if err := MigrateFromJSON(db); err != nil {
		t.Fatalf("migrating: %v", err)
	}

	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM messages").Scan(&n); err != nil {
		t.Fatalf("counting: %v", err)
	}
	if n != 2 {
		t.Fatalf("both same-second legacy messages should survive migration, got %d row(s)", n)
	}

	// And the one that was only ever sent must not read back as received.
	var isIncoming bool
	if err := db.QueryRow(
		"SELECT is_incoming FROM messages WHERE plaintext = ?", "I sent this to Bob",
	).Scan(&isIncoming); err != nil {
		t.Fatalf("reading the sent message back: %v", err)
	}
	if isIncoming {
		t.Fatal("a message the user sent was migrated as incoming")
	}
}

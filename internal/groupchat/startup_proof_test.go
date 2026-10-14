package groupchat

import (
	"database/sql"
	"testing"

	"fiatjaf.com/nostr"
	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/internal/storage"
	_ "modernc.org/sqlite"
)

// Independent Linux proof (test-only). Unindexed group tables
// (groupchat_groups, groupchat_members) are created with
// CREATE TABLE IF NOT EXISTS and have no CREATE INDEX that would trip on a
// view or a wrong-shape table. These fixtures check real physical table
// health instead of assuming init failed.

var proofBrokenGroupFixtures = []struct {
	name string
	ddl  string
}{
	{"GroupsView", `CREATE VIEW groupchat_groups AS SELECT 'n' AS local_npub, 'g' AS group_id`},
	{"MembersView", `CREATE VIEW groupchat_members AS SELECT 'n' AS local_npub, 'g' AS group_id, 'x' AS npub`},
	{"GroupsWrongShapeTable", `CREATE TABLE groupchat_groups (local_npub TEXT, group_id TEXT)`},
}

func proofPlantDDL(t *testing.T, ddl string) {
	t.Helper()
	db, err := storage.OpenDB() // healthy DM schema only, no group migrate
	if err != nil {
		t.Fatal(err)
	}
	path, err := storage.GetDBPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.Exec(ddl); err != nil {
		t.Fatalf("plant fixture: %v", err)
	}
}

// TestProofNewStoreRejectsNonPhysicalUnindexedGroupTables: NewStore is the
// daemon/TUI startup gate on the candidate. If it returns success on a file
// whose group tables are not real, correctly-shaped physical tables, startup
// reports healthy while every later group write is impossible.
func TestProofNewStoreRejectsNonPhysicalUnindexedGroupTables(t *testing.T) {
	for _, fx := range proofBrokenGroupFixtures {
		t.Run(fx.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			proofPlantDDL(t, fx.ddl)
			db, err := storage.OpenDB()
			if err != nil {
				t.Fatalf("OpenDB (DM) must still open: %v", err)
			}
			defer db.Close()
			store, err := NewStore(db)
			if err != nil {
				t.Logf("NewStore refused as required: %v", err)
				return
			}
			// Demonstrate the false positive is unsafe: the gate said healthy
			// but the group schema cannot accept a group write.
			_, writeErr := store.CreateGroup("proof", proofNpub(), []string{proofNpub()})
			t.Fatalf("UNSAFE HEALTHY FALSE POSITIVE: NewStore returned nil on %s; subsequent CreateGroup err=%v", fx.name, writeErr)
		})
	}
}

func proofNpub() string { return common.EncodeNpub(nostr.Generate().Public()) }

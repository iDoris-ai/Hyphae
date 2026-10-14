package groupchat

import (
	"database/sql"
	"fmt"
	"strings"
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
//
// Carried from ab1544d (FAIL on 042d279) onto frozen 47d8daf. Existing
// groupinit safeCandidate does NOT prove V7; this is schema-gate evidence only.

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

// TestProofNewStoreRejectsNonPhysicalUnindexedGroupTables: original 3 negatives
// from ab1544d. On 042d279 these FAIL (false healthy); on 47d8daf they must PASS
// (NewStore refuses).
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
			_, writeErr := store.CreateGroup("proof", proofNpub(), []string{proofNpub()})
			t.Fatalf("UNSAFE HEALTHY FALSE POSITIVE: NewStore returned nil on %s; subsequent CreateGroup err=%v", fx.name, writeErr)
		})
	}
}

func proofNpub() string { return common.EncodeNpub(nostr.Generate().Public()) }

// Full column DDL helpers for NoPK / WrongPK / MissingCol matrix rows.
func proofColsGroups() string {
	return `local_npub TEXT NOT NULL, group_id TEXT NOT NULL, name TEXT NOT NULL, creator_npub TEXT NOT NULL,
		roster_json TEXT NOT NULL, roster_hash TEXT NOT NULL, state TEXT NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL`
}
func proofColsMembers() string {
	return `local_npub TEXT NOT NULL, group_id TEXT NOT NULL, npub TEXT NOT NULL, invite_id TEXT,
		state TEXT NOT NULL, is_creator INTEGER NOT NULL DEFAULT 0`
}
func proofColsInvites() string {
	return `local_npub TEXT NOT NULL, invite_id TEXT NOT NULL, group_id TEXT NOT NULL, creator_npub TEXT NOT NULL,
		invitee_npub TEXT NOT NULL, name TEXT NOT NULL, roster_json TEXT NOT NULL, roster_hash TEXT NOT NULL, state TEXT NOT NULL`
}
func proofColsMessages() string {
	return `local_npub TEXT NOT NULL, group_id TEXT NOT NULL, logical_id TEXT NOT NULL, sender_npub TEXT NOT NULL,
		body TEXT NOT NULL, created_at INTEGER NOT NULL`
}
func proofColsFanout() string {
	return `local_npub TEXT NOT NULL, group_id TEXT NOT NULL, envelope_type TEXT NOT NULL, send_key TEXT NOT NULL,
		recipient_npub TEXT NOT NULL, event_id TEXT NOT NULL, event_json TEXT NOT NULL, state TEXT NOT NULL, max_retries INTEGER NOT NULL`
}

// TestProofExpandedViewWrongShapeNoPKMatrix: 36 expanded negatives covering
// VIEW / wrong-shape / no-PK / wrong-PK / missing-col / mixed-case for all
// five required groupchat_* tables. Independent of candidate's own fixtures.
func TestProofExpandedViewWrongShapeNoPKMatrix(t *testing.T) {
	type fx struct {
		name string
		ddl  string
	}
	var cases []fx

	tables := []struct {
		lower, mixed, cols, pk, wrongPK, dropCol string
	}{
		{"groupchat_groups", "GroupChat_Groups", proofColsGroups(),
			"PRIMARY KEY(local_npub, group_id)", "PRIMARY KEY(group_id)", "name"},
		{"groupchat_members", "GroupChat_Members", proofColsMembers(),
			"PRIMARY KEY(local_npub, group_id, npub)", "PRIMARY KEY(group_id, npub)", "invite_id"},
		{"groupchat_invites", "GroupChat_Invites", proofColsInvites(),
			"PRIMARY KEY(local_npub, invite_id)", "PRIMARY KEY(invite_id)", "state"},
		{"groupchat_messages", "GroupChat_Messages", proofColsMessages(),
			"PRIMARY KEY(local_npub, group_id, logical_id)", "PRIMARY KEY(group_id, logical_id)", "body"},
		{"groupchat_fanout", "GroupChat_Fanout", proofColsFanout(),
			"PRIMARY KEY(local_npub, group_id, envelope_type, send_key, recipient_npub)",
			"PRIMARY KEY(group_id, envelope_type, send_key, recipient_npub)", "event_json"},
	}

	for _, tb := range tables {
		short := strings.TrimPrefix(tb.lower, "groupchat_")
		cases = append(cases,
			fx{short + "/ViewLower", fmt.Sprintf(`CREATE VIEW %s AS SELECT 1 AS dummy`, tb.lower)},
			fx{short + "/ViewMixed", fmt.Sprintf(`CREATE VIEW %s AS SELECT 1 AS dummy`, tb.mixed)},
			fx{short + "/WrongShapeLower", fmt.Sprintf(`CREATE TABLE %s (dummy INTEGER)`, tb.lower)},
			fx{short + "/WrongShapeMixed", fmt.Sprintf(`CREATE TABLE %s (dummy INTEGER)`, tb.mixed)},
			fx{short + "/NoPK", fmt.Sprintf(`CREATE TABLE %s (%s)`, tb.lower, tb.cols)},
			fx{short + "/WrongPK", fmt.Sprintf(`CREATE TABLE %s (%s, %s)`, tb.lower, tb.cols, tb.wrongPK)},
			fx{short + "/MissingCol", fmt.Sprintf(`CREATE TABLE %s (%s, %s)`, tb.lower,
				proofDropColumn(tb.cols, tb.dropCol), tb.pk)},
		)
	}
	// 5 tables × 7 variants = 35. Add one more for invite_id-specific mixed missing.
	cases = append(cases, fx{"members/MissingInviteIDMixed",
		`CREATE TABLE GroupChat_Members (
			local_npub TEXT NOT NULL, group_id TEXT NOT NULL, npub TEXT NOT NULL,
			state TEXT NOT NULL, is_creator INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY(local_npub, group_id, npub)
		)`})

	if len(cases) != 36 {
		t.Fatalf("expected 36 expanded cases, got %d", len(cases))
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			proofPlantDDL(t, c.ddl)
			db, err := storage.OpenDB()
			if err != nil {
				t.Fatalf("OpenDB (DM) must still open: %v", err)
			}
			defer db.Close()
			_, err = NewStore(db)
			if err == nil {
				t.Fatalf("NewStore must refuse fixture %s", c.name)
			}
			t.Logf("refused: %v", err)
		})
	}
}

func proofDropColumn(colsDDL, drop string) string {
	parts := strings.Split(colsDDL, ",")
	var kept []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		fields := strings.Fields(p)
		if len(fields) > 0 && strings.EqualFold(fields[0], drop) {
			continue
		}
		kept = append(kept, p)
	}
	return strings.Join(kept, ", ")
}

// TestProofNullableInviteIDLowerAndMixedCaseAccepted: healthy members tables
// with nullable TEXT invite_id (authoritative DDL) must pass NewStore whether
// the table name is lower or mixed case. Missing invite_id must still refuse.
func TestProofNullableInviteIDLowerAndMixedCaseAccepted(t *testing.T) {
	accept := []struct {
		name string
		ddl  string
	}{
		{"LowerNullableInviteID", `CREATE TABLE groupchat_members (
			local_npub TEXT NOT NULL, group_id TEXT NOT NULL, npub TEXT NOT NULL,
			invite_id TEXT, state TEXT NOT NULL, is_creator INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY(local_npub, group_id, npub)
		)`},
		{"MixedNullableInviteID", `CREATE TABLE GroupChat_Members (
			local_npub TEXT NOT NULL, group_id TEXT NOT NULL, npub TEXT NOT NULL,
			invite_id TEXT, state TEXT NOT NULL, is_creator INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY(local_npub, group_id, npub)
		)`},
	}
	for _, fx := range accept {
		t.Run(fx.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			proofPlantDDL(t, fx.ddl)
			db, err := storage.OpenDB()
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			store, err := NewStore(db)
			if err != nil {
				t.Fatalf("nullable TEXT invite_id must be accepted: %v", err)
			}
			alice, bob := proofNpub(), proofNpub()
			draft, err := store.CreateGroup("invite-id-ok", alice, []string{bob})
			if err != nil {
				t.Fatalf("CreateGroup on accepted members shape must succeed: %v", err)
			}
			if draft.Group.Name != "invite-id-ok" {
				t.Fatalf("unexpected group name %q", draft.Group.Name)
			}
		})
	}

	refuse := []struct {
		name string
		ddl  string
	}{
		{"LowerMissingInviteID", `CREATE TABLE groupchat_members (
			local_npub TEXT NOT NULL, group_id TEXT NOT NULL, npub TEXT NOT NULL,
			state TEXT NOT NULL, is_creator INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY(local_npub, group_id, npub)
		)`},
		{"MixedMissingInviteID", `CREATE TABLE GroupChat_Members (
			local_npub TEXT NOT NULL, group_id TEXT NOT NULL, npub TEXT NOT NULL,
			state TEXT NOT NULL, is_creator INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY(local_npub, group_id, npub)
		)`},
	}
	for _, fx := range refuse {
		t.Run(fx.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			proofPlantDDL(t, fx.ddl)
			db, err := storage.OpenDB()
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			_, err = NewStore(db)
			if err == nil {
				t.Fatal("missing invite_id must refuse NewStore (baseline-127 red closed on 47d8daf)")
			}
			if !strings.Contains(err.Error(), "invite_id") {
				t.Fatalf("refusal must mention invite_id, got %v", err)
			}
		})
	}
}

// TestProofHealthyCreateGroupLegacyFanoutAndReopen: positive controls —
// healthy CreateGroup, supported legacy fanout migration, second NewStore.
func TestProofHealthyCreateGroupLegacyFanoutAndReopen(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	db, err := storage.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Plant supported legacy fanout (pre-rev7) before first NewStore.
	path, err := storage.GetDBPath()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = raw.Exec(`CREATE TABLE groupchat_fanout (
		local_npub TEXT NOT NULL, group_id TEXT NOT NULL, envelope_type TEXT NOT NULL,
		send_key TEXT NOT NULL, recipient_npub TEXT NOT NULL, event_id TEXT NOT NULL,
		event_json TEXT NOT NULL, queue_id TEXT NOT NULL DEFAULT '', state TEXT NOT NULL,
		issue TEXT NOT NULL DEFAULT '', relay_acks INTEGER NOT NULL DEFAULT 0,
		relay_count INTEGER NOT NULL DEFAULT 0, attempts INTEGER NOT NULL DEFAULT 0,
		max_retries INTEGER NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL,
		last_attempt_at INTEGER NOT NULL DEFAULT 0, accepted_at INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (local_npub, group_id, envelope_type, send_key, recipient_npub),
		UNIQUE (local_npub, event_id)
	)`)
	if err != nil {
		raw.Close()
		t.Fatal(err)
	}
	_, err = raw.Exec(`INSERT INTO groupchat_fanout (
		local_npub, group_id, envelope_type, send_key, recipient_npub, event_id, event_json, state, attempts, max_retries, created_at, updated_at
	) VALUES ('npub1alice', 'g1', 'message', 'k1', 'npub1bob', 'e1', '{}', 'prepared', 1, 3, 1000, 1000)`)
	raw.Close()
	if err != nil {
		t.Fatal(err)
	}

	store, err := NewStore(db)
	if err != nil {
		t.Fatalf("legacy fanout + fresh other tables must pass NewStore: %v", err)
	}
	var origin string
	if err := db.QueryRow(`SELECT accounting_origin FROM groupchat_fanout LIMIT 1`).Scan(&origin); err != nil {
		t.Fatalf("legacy migration must add accounting_origin: %v", err)
	}
	if origin != "legacy" {
		t.Fatalf("accounting_origin=%q want legacy", origin)
	}

	alice, bob := proofNpub(), proofNpub()
	draft, err := store.CreateGroup("healthy-proof", alice, []string{bob})
	if err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	if draft.Group.Name != "healthy-proof" {
		t.Fatalf("name=%q", draft.Group.Name)
	}

	_, err = NewStore(db)
	if err != nil {
		t.Fatalf("second NewStore on migrated DB must succeed: %v", err)
	}
}

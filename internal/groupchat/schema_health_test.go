package groupchat

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "groupchat.db")
	db, err := sql.Open("sqlite", dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestNewStoreRejectsViewOccupyingGroupsTable(t *testing.T) {
	db := openTestDB(t)
	_, err := db.Exec(`CREATE VIEW groupchat_groups AS SELECT 1 AS dummy`)
	require.NoError(t, err)

	_, err = NewStore(db)
	require.Error(t, err, "a VIEW occupying groupchat_groups must fail NewStore, not silently succeed")
	require.Contains(t, err.Error(), "groupchat_groups")
}

func TestNewStoreRejectsViewOccupyingMembersTable(t *testing.T) {
	db := openTestDB(t)
	_, err := db.Exec(`CREATE VIEW groupchat_members AS SELECT 1 AS dummy`)
	require.NoError(t, err)

	_, err = NewStore(db)
	require.Error(t, err, "a VIEW occupying groupchat_members must fail NewStore, not silently succeed")
	require.Contains(t, err.Error(), "groupchat_members")
}

func TestNewStoreRejectsWrongShapeGroupsTable(t *testing.T) {
	db := openTestDB(t)
	_, err := db.Exec(`CREATE TABLE groupchat_groups (dummy INTEGER)`)
	require.NoError(t, err)

	_, err = NewStore(db)
	require.Error(t, err, "a wrong-shape TABLE occupying groupchat_groups must fail NewStore")
	require.Contains(t, err.Error(), "groupchat_groups")
}

func TestNewStoreRejectsWrongShapeMembersTable(t *testing.T) {
	db := openTestDB(t)
	_, err := db.Exec(`CREATE TABLE groupchat_members (dummy INTEGER)`)
	require.NoError(t, err)

	_, err = NewStore(db)
	require.Error(t, err, "a wrong-shape TABLE occupying groupchat_members must fail NewStore")
	require.Contains(t, err.Error(), "groupchat_members")
}

func TestNewStoreRejectsCaseInsensitiveViewOnGroupsTable(t *testing.T) {
	db := openTestDB(t)
	_, err := db.Exec(`CREATE VIEW GroupChat_Groups AS SELECT 1 AS dummy`)
	require.NoError(t, err)

	_, err = NewStore(db)
	require.Error(t, err, "SQLite names are case-insensitive, so a differently-cased VIEW must still be caught")
}

func TestNewStoreRejectsWrongPrimaryKeyOnMembersTable(t *testing.T) {
	db := openTestDB(t)
	// Right columns, but local_npub is not part of the primary key.
	_, err := db.Exec(`CREATE TABLE groupchat_members (
		local_npub TEXT NOT NULL,
		group_id TEXT NOT NULL,
		npub TEXT NOT NULL,
		invite_id TEXT,
		state TEXT NOT NULL,
		is_creator INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (group_id, npub)
	)`)
	require.NoError(t, err)

	_, err = NewStore(db)
	require.Error(t, err, "a members table missing local_npub from its primary key must fail NewStore")
	require.Contains(t, err.Error(), "primary key")
}

func TestNewStoreRejectsMembersTableMissingInviteID(t *testing.T) {
	db := openTestDB(t)
	// Baseline-127 red fixture: full members shape except operational invite_id.
	_, err := db.Exec(`CREATE TABLE groupchat_members (
		local_npub TEXT NOT NULL,
		group_id TEXT NOT NULL,
		npub TEXT NOT NULL,
		state TEXT NOT NULL,
		is_creator INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY(local_npub, group_id, npub)
	)`)
	require.NoError(t, err)

	_, err = NewStore(db)
	require.Error(t, err, "a members table missing operational invite_id must fail NewStore")
	require.Contains(t, err.Error(), "groupchat_members")
	require.Contains(t, err.Error(), "missing required column(s) invite_id")

	// Semantic negative: raw CreateGroup fails with missing column on this broken table.
	alice := newMember(t)
	bob := newMember(t)
	brokenStore := &Store{db: db}
	_, err = brokenStore.CreateGroup("test-group", alice.npub, []string{bob.npub})
	require.Error(t, err, "CreateGroup on members table missing invite_id must fail")
	require.Contains(t, err.Error(), "no column named invite_id")

	// Healthy CreateGroup control: fresh store succeeds.
	healthyDB := openTestDB(t)
	healthyStore, err := NewStore(healthyDB)
	require.NoError(t, err)
	draft, err := healthyStore.CreateGroup("healthy-group", alice.npub, []string{bob.npub})
	require.NoError(t, err, "CreateGroup on healthy store must succeed")
	require.Equal(t, "healthy-group", draft.Group.Name)
}

func TestNewStoreRejectsCaseInsensitiveMembersTableMissingInviteID(t *testing.T) {
	db := openTestDB(t)
	_, err := db.Exec(`CREATE TABLE GroupChat_Members (
		local_npub TEXT NOT NULL,
		group_id TEXT NOT NULL,
		npub TEXT NOT NULL,
		state TEXT NOT NULL,
		is_creator INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY(local_npub, group_id, npub)
	)`)
	require.NoError(t, err)

	_, err = NewStore(db)
	require.Error(t, err, "mixed-case GroupChat_Members missing invite_id must fail NewStore")
	require.Contains(t, err.Error(), "missing required column(s) invite_id")
}

func TestNewStoreAcceptsHealthyFreshSchema(t *testing.T) {
	db := openTestDB(t)
	store, err := NewStore(db)
	require.NoError(t, err, "a fresh database must pass schema-health validation")
	require.NotNil(t, store)

	// A second NewStore against the already-migrated DB must also stay healthy.
	_, err = NewStore(db)
	require.NoError(t, err)
}

func TestNewStoreAcceptsSupportedLegacyFanoutBeforeAccountingMigration(t *testing.T) {
	db := openTestDB(t)
	_, err := db.Exec(`CREATE TABLE groupchat_fanout (
		local_npub      TEXT    NOT NULL,
		group_id        TEXT    NOT NULL,
		envelope_type   TEXT    NOT NULL,
		send_key        TEXT    NOT NULL,
		recipient_npub  TEXT    NOT NULL,
		event_id        TEXT    NOT NULL,
		event_json      TEXT    NOT NULL,
		queue_id        TEXT    NOT NULL DEFAULT '',
		state           TEXT    NOT NULL,
		issue           TEXT    NOT NULL DEFAULT '',
		relay_acks      INTEGER NOT NULL DEFAULT 0,
		relay_count     INTEGER NOT NULL DEFAULT 0,
		attempts        INTEGER NOT NULL DEFAULT 0,
		max_retries     INTEGER NOT NULL,
		created_at      INTEGER NOT NULL,
		updated_at      INTEGER NOT NULL,
		last_attempt_at INTEGER NOT NULL DEFAULT 0,
		accepted_at     INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (local_npub, group_id, envelope_type, send_key, recipient_npub),
		UNIQUE (local_npub, event_id)
	)`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO groupchat_fanout (
		local_npub, group_id, envelope_type, send_key, recipient_npub, event_id, event_json, state, attempts, max_retries, created_at, updated_at
	) VALUES ('npub1alice', 'g1', 'message', 'k1', 'npub1bob', 'e1', '{}', 'prepared', 1, 3, 1000, 1000)`)
	require.NoError(t, err)

	store, err := NewStore(db)
	require.NoError(t, err, "supported legacy fanout rows without rev7 accounting columns must not be rejected")
	require.NotNil(t, store)

	var origin string
	require.NoError(t, db.QueryRow(`SELECT accounting_origin FROM groupchat_fanout LIMIT 1`).Scan(&origin))
	require.Equal(t, "legacy", origin, "backfilled legacy rows must be tagged accordingly")
}

func TestValidateTableShapeReportsMissingColumns(t *testing.T) {
	db := openTestDB(t)
	_, err := db.Exec(`CREATE TABLE groupchat_groups (local_npub TEXT, group_id TEXT, PRIMARY KEY (local_npub, group_id))`)
	require.NoError(t, err)

	err = validateTableShape(db, "groupchat_groups", requiredGroupchatShapes[0].shape)
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "name") || strings.Contains(err.Error(), "state"))
}

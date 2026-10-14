package daemon

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/internal/groupchat"
	"github.com/iDoris-ai/hyphae/internal/identity"
	"github.com/iDoris-ai/hyphae/internal/storage"
	"github.com/iDoris-ai/hyphae/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestHomeAndIdentity(t *testing.T) (string, *types.Identity) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	ks := &types.KeyStore{Identities: make(map[string]*types.Identity), Contacts: make(map[string]*types.Contact)}
	id, err := identity.CreateIdentityWithPassword(ks, "alice", "startup-secret-123")
	require.NoError(t, err)
	require.NoError(t, identity.SaveKeyStore(ks))
	return home, id
}

func setupInitialOutbox(t *testing.T, home string) (int64, time.Time) {
	t.Helper()
	outboxPath := filepath.Join(home, ".hyphae", "outbox.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(outboxPath), 0700))
	content := []byte(`{"entries":[{"id":"0000000000000000000000000000000000000000000000000000000000000001","to":"npub1test","content":"hello","status":"pending","created_at":1000,"updated_at":1000,"relays":["ws://127.0.0.1:1"]}]}`)
	require.NoError(t, os.WriteFile(outboxPath, content, 0600))
	fi, err := os.Stat(outboxPath)
	require.NoError(t, err)
	return fi.Size(), fi.ModTime()
}

func setupInvalidLegacyFanoutDB(t *testing.T, home string) {
	t.Helper()
	dbPath := filepath.Join(home, ".hyphae", "messages.db")
	require.NoError(t, os.MkdirAll(filepath.Dir(dbPath), 0700))
	db, err := sql.Open("sqlite", dbPath)
	require.NoError(t, err)
	defer db.Close()

	// Create base messages table
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS messages (
			id TEXT PRIMARY KEY,
			sender_npub TEXT NOT NULL,
			recipient_npub TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			received_at INTEGER NOT NULL,
			plaintext TEXT,
			is_outgoing INTEGER DEFAULT 0,
			is_encrypted INTEGER DEFAULT 1
		);
		CREATE TABLE IF NOT EXISTS contacts (
			npub TEXT PRIMARY KEY,
			nickname TEXT NOT NULL,
			created_at INTEGER NOT NULL
		);
	`)
	require.NoError(t, err)

	// Create legacy groupchat_fanout without accounting_origin and invalid attempts counter
	_, err = db.Exec(`
		CREATE TABLE groupchat_fanout (
			local_npub TEXT NOT NULL,
			group_id TEXT NOT NULL,
			envelope_type TEXT NOT NULL,
			send_key TEXT NOT NULL,
			recipient_npub TEXT NOT NULL,
			event_id TEXT NOT NULL,
			event_json TEXT NOT NULL,
			queue_id TEXT NOT NULL DEFAULT '',
			state TEXT NOT NULL,
			issue TEXT NOT NULL DEFAULT '',
			relay_acks INTEGER NOT NULL DEFAULT 0,
			relay_count INTEGER NOT NULL DEFAULT 0,
			attempts INTEGER NOT NULL DEFAULT 0,
			max_retries INTEGER NOT NULL,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			last_attempt_at INTEGER NOT NULL DEFAULT 0,
			accepted_at INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (local_npub, group_id, envelope_type, send_key, recipient_npub),
			UNIQUE (local_npub, event_id)
		);
		INSERT INTO groupchat_fanout (
			local_npub, group_id, envelope_type, send_key, recipient_npub, event_id, event_json, state, attempts, max_retries, created_at, updated_at
		) VALUES (
			'npub1alice', 'g1', 'message', 'k1', 'npub1bob', 'e1', '{}', 'prepared', -1, 3, 1000, 1000
		);
	`)
	require.NoError(t, err)
}

func setupConflictingViewDB(t *testing.T, home string) {
	t.Helper()
	dbPath := filepath.Join(home, ".hyphae", "messages.db")
	require.NoError(t, os.MkdirAll(filepath.Dir(dbPath), 0700))
	db, err := sql.Open("sqlite", dbPath)
	require.NoError(t, err)
	defer db.Close()

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS messages (
			id TEXT PRIMARY KEY,
			sender_npub TEXT NOT NULL,
			recipient_npub TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			received_at INTEGER NOT NULL,
			plaintext TEXT,
			is_outgoing INTEGER DEFAULT 0,
			is_encrypted INTEGER DEFAULT 1
		);
		CREATE TABLE IF NOT EXISTS contacts (
			npub TEXT PRIMARY KEY,
			nickname TEXT NOT NULL,
			created_at INTEGER NOT NULL
		);
		CREATE VIEW groupchat_invites AS SELECT 1 AS dummy;
	`)
	require.NoError(t, err)
}

func setupConflictingTableDB(t *testing.T, home string) {
	t.Helper()
	dbPath := filepath.Join(home, ".hyphae", "messages.db")
	require.NoError(t, os.MkdirAll(filepath.Dir(dbPath), 0700))
	db, err := sql.Open("sqlite", dbPath)
	require.NoError(t, err)
	defer db.Close()

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS messages (
			id TEXT PRIMARY KEY,
			sender_npub TEXT NOT NULL,
			recipient_npub TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			received_at INTEGER NOT NULL,
			plaintext TEXT,
			is_outgoing INTEGER DEFAULT 0,
			is_encrypted INTEGER DEFAULT 1
		);
		CREATE TABLE IF NOT EXISTS contacts (
			npub TEXT PRIMARY KEY,
			nickname TEXT NOT NULL,
			created_at INTEGER NOT NULL
		);
		CREATE TABLE groupchat_invites (dummy INT);
	`)
	require.NoError(t, err)
}

func setupConflictingGroupsViewDB(t *testing.T, home string) {
	t.Helper()
	dbPath := filepath.Join(home, ".hyphae", "messages.db")
	require.NoError(t, os.MkdirAll(filepath.Dir(dbPath), 0700))
	db, err := sql.Open("sqlite", dbPath)
	require.NoError(t, err)
	defer db.Close()

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS messages (
			id TEXT PRIMARY KEY,
			sender_npub TEXT NOT NULL,
			recipient_npub TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			received_at INTEGER NOT NULL,
			plaintext TEXT,
			is_outgoing INTEGER DEFAULT 0,
			is_encrypted INTEGER DEFAULT 1
		);
		CREATE TABLE IF NOT EXISTS contacts (
			npub TEXT PRIMARY KEY,
			nickname TEXT NOT NULL,
			created_at INTEGER NOT NULL
		);
		CREATE VIEW groupchat_groups AS SELECT 1 AS dummy;
	`)
	require.NoError(t, err)
}

func setupConflictingMembersTableDB(t *testing.T, home string) {
	t.Helper()
	dbPath := filepath.Join(home, ".hyphae", "messages.db")
	require.NoError(t, os.MkdirAll(filepath.Dir(dbPath), 0700))
	db, err := sql.Open("sqlite", dbPath)
	require.NoError(t, err)
	defer db.Close()

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS messages (
			id TEXT PRIMARY KEY,
			sender_npub TEXT NOT NULL,
			recipient_npub TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			received_at INTEGER NOT NULL,
			plaintext TEXT,
			is_outgoing INTEGER DEFAULT 0,
			is_encrypted INTEGER DEFAULT 1
		);
		CREATE TABLE IF NOT EXISTS contacts (
			npub TEXT PRIMARY KEY,
			nickname TEXT NOT NULL,
			created_at INTEGER NOT NULL
		);
		CREATE TABLE groupchat_members (dummy INT);
	`)
	require.NoError(t, err)
}

func TestDaemonFailClosedOnInvalidLegacyFanoutCounters(t *testing.T) {
	home, _ := setupTestHomeAndIdentity(t)
	setupInvalidLegacyFanoutDB(t, home)
	outboxSize, outboxModTime := setupInitialOutbox(t, home)

	var relayConnections atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		relayConnections.Add(1)
	}))
	defer server.Close()
	relayURL := "ws" + strings.TrimPrefix(server.URL, "http")

	// Set unrelated sentinel global DB to prove daemon does not alter or close it
	sentinel, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	defer sentinel.Close()
	prevDB := storage.DB
	storage.DB = sentinel
	t.Cleanup(func() { storage.DB = prevDB })

	// Run daemon via CLI subprocess with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	args := []string{"--json", "daemon", "--identity", "alice", "--relay", relayURL, "--password-stdin"}
	command := exec.CommandContext(ctx, daemonLockCLI, args...)
	command.Stdin = strings.NewReader("startup-secret-123\n")
	command.Env = withoutDaemonOutputEnv(os.Environ())
	command.Env = append(command.Env, "HOME="+home)

	output, err := command.CombinedOutput()
	require.Error(t, err, "daemon must exit nonzero on invalid legacy fanout counters")
	assert.Zero(t, relayConnections.Load(), "relay dial count must be 0 before startup migration error")

	// Verify outbox file was not mutated
	outboxPath := filepath.Join(home, ".hyphae", "outbox.json")
	fi, statErr := os.Stat(outboxPath)
	require.NoError(t, statErr)
	assert.Equal(t, outboxSize, fi.Size(), "outbox bytes must remain unchanged")
	assert.True(t, fi.ModTime().Equal(outboxModTime), "outbox mtime must remain unchanged")

	// Verify daemon home lock was released
	lock, lockErr := acquireDaemonHomeLock()
	require.NoError(t, lockErr, "daemon home lock must be released after fail-closed exit")
	require.NoError(t, lock.Close())

	// The parent process's own sentinel is never touched by a child process,
	// so asserting on it here would be vacuous; TestOwnedStartupNeverTouchesGlobalStorageDB
	// below is the real in-process proof for that property.

	// Verify structured error output
	var result common.Result
	require.NoError(t, json.Unmarshal(output, &result), "stderr must be a valid JSON error envelope: %s", string(output))
	assert.False(t, result.OK)
	assert.Equal(t, common.ErrCodeOther, result.Error)
	assert.Contains(t, result.Message, "group storage init failed")
}

func TestDaemonFailClosedOnConflictingView(t *testing.T) {
	home, _ := setupTestHomeAndIdentity(t)
	setupConflictingViewDB(t, home)
	outboxSize, outboxModTime := setupInitialOutbox(t, home)

	var relayConnections atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		relayConnections.Add(1)
	}))
	defer server.Close()
	relayURL := "ws" + strings.TrimPrefix(server.URL, "http")

	sentinel, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	defer sentinel.Close()
	prevDB := storage.DB
	storage.DB = sentinel
	t.Cleanup(func() { storage.DB = prevDB })

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	args := []string{"--json", "daemon", "--identity", "alice", "--relay", relayURL, "--password-stdin"}
	command := exec.CommandContext(ctx, daemonLockCLI, args...)
	command.Stdin = strings.NewReader("startup-secret-123\n")
	command.Env = withoutDaemonOutputEnv(os.Environ())
	command.Env = append(command.Env, "HOME="+home)

	output, err := command.CombinedOutput()
	require.Error(t, err, "daemon must exit nonzero on conflicting view")
	assert.Zero(t, relayConnections.Load(), "relay dial count must be 0")

	// Verify outbox untouched
	outboxPath := filepath.Join(home, ".hyphae", "outbox.json")
	fi, statErr := os.Stat(outboxPath)
	require.NoError(t, statErr)
	assert.Equal(t, outboxSize, fi.Size(), "outbox bytes must remain unchanged")
	assert.True(t, fi.ModTime().Equal(outboxModTime), "outbox mtime must remain unchanged")

	// Verify lock released
	lock, lockErr := acquireDaemonHomeLock()
	require.NoError(t, lockErr, "daemon home lock must be released")
	require.NoError(t, lock.Close())

	// See TestOwnedStartupNeverTouchesGlobalStorageDB for the in-process proof;
	// a subprocess can never affect this parent's sentinel, so asserting on
	// it here would be vacuous.

	var result common.Result
	require.NoError(t, json.Unmarshal(output, &result))
	assert.False(t, result.OK)
	assert.Equal(t, common.ErrCodeOther, result.Error)
	assert.Contains(t, result.Message, "group storage init failed")
}

func TestDaemonFailClosedOnConflictingTable(t *testing.T) {
	home, _ := setupTestHomeAndIdentity(t)
	setupConflictingTableDB(t, home)
	outboxSize, outboxModTime := setupInitialOutbox(t, home)

	var relayConnections atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		relayConnections.Add(1)
	}))
	defer server.Close()
	relayURL := "ws" + strings.TrimPrefix(server.URL, "http")

	sentinel, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	defer sentinel.Close()
	prevDB := storage.DB
	storage.DB = sentinel
	t.Cleanup(func() { storage.DB = prevDB })

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	args := []string{"--json", "daemon", "--identity", "alice", "--relay", relayURL, "--password-stdin"}
	command := exec.CommandContext(ctx, daemonLockCLI, args...)
	command.Stdin = strings.NewReader("startup-secret-123\n")
	command.Env = withoutDaemonOutputEnv(os.Environ())
	command.Env = append(command.Env, "HOME="+home)

	output, err := command.CombinedOutput()
	require.Error(t, err, "daemon must exit nonzero on conflicting table")
	assert.Zero(t, relayConnections.Load(), "relay dial count must be 0")

	// Verify outbox untouched
	outboxPath := filepath.Join(home, ".hyphae", "outbox.json")
	fi, statErr := os.Stat(outboxPath)
	require.NoError(t, statErr)
	assert.Equal(t, outboxSize, fi.Size(), "outbox bytes must remain unchanged")
	assert.True(t, fi.ModTime().Equal(outboxModTime), "outbox mtime must remain unchanged")

	// Verify lock released
	lock, lockErr := acquireDaemonHomeLock()
	require.NoError(t, lockErr, "daemon home lock must be released")
	require.NoError(t, lock.Close())

	// See TestOwnedStartupNeverTouchesGlobalStorageDB for the in-process proof;
	// a subprocess can never affect this parent's sentinel, so asserting on
	// it here would be vacuous.

	var result common.Result
	require.NoError(t, json.Unmarshal(output, &result))
	assert.False(t, result.OK)
	assert.Equal(t, common.ErrCodeOther, result.Error)
	assert.Contains(t, result.Message, "group storage init failed")
}

// TestOwnedStartupNeverTouchesGlobalStorageDB is the in-process proof the
// three subprocess tests above cannot give: it exercises the exact two
// calls the daemon's owned startup seam makes (storage.OpenDB then
// groupchat.NewStore) against a schema-broken file, in this same process,
// and checks the package-level storage.DB global immediately afterward.
func TestOwnedStartupNeverTouchesGlobalStorageDB(t *testing.T) {
	home, _ := setupTestHomeAndIdentity(t)
	setupConflictingViewDB(t, home)
	t.Setenv("HOME", home)

	sentinel, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	defer sentinel.Close()
	prevDB := storage.DB
	storage.DB = sentinel
	t.Cleanup(func() { storage.DB = prevDB })

	owned, err := storage.OpenDB()
	require.NoError(t, err, "storage.OpenDB must open the owned handle without touching the global")
	defer owned.Close()
	_, err = groupchat.NewStore(owned)
	require.Error(t, err, "conflicting view must still fail the owned handle's schema health check")

	assert.Same(t, sentinel, storage.DB, "the owned startup seam must never reassign the global storage.DB")
	require.NoError(t, sentinel.Ping(), "the global sentinel must remain usable after the owned handle's init error")
}

func TestSameBrokenFileDMCLIBaselineHealthy(t *testing.T) {
	home, id := setupTestHomeAndIdentity(t)
	setupInvalidLegacyFanoutDB(t, home)

	// In the same broken file, DM storage without NewStore opens and functions normally
	db, err := storage.OpenDB()
	require.NoError(t, err, "standard storage.OpenDB must open without calling groupchat.NewStore")
	defer db.Close()

	// Write and query DM history
	_, err = db.Exec(`INSERT INTO messages (id, sender_npub, recipient_npub, created_at, received_at, plaintext)
		VALUES ('dm-msg-1', ?, 'npub1recipient', 12345, 12345, 'dm content')`, id.Npub)
	require.NoError(t, err)

	var count int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM messages WHERE id = 'dm-msg-1'`).Scan(&count))
	assert.Equal(t, 1, count, "DM messages table must accept writes")
}

func TestHealthyGroupDDLCreatesExpectedIndexes(t *testing.T) {
	_, _ = setupTestHomeAndIdentity(t)

	// Start daemon with clean DB and let it start up
	db, err := storage.OpenDB()
	require.NoError(t, err)
	defer db.Close()

	// Run groupchat migration as daemon does
	_, err = groupchat.NewStore(db)
	require.NoError(t, err)

	// Verify the expected group indexes are created
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='index'`)
	require.NoError(t, err)
	defer rows.Close()

	indexes := make(map[string]bool)
	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name))
		indexes[name] = true
	}
	assert.True(t, indexes["idx_groupchat_invites_state"], "idx_groupchat_invites_state index must exist")
	assert.True(t, indexes["idx_groupchat_messages_order"], "idx_groupchat_messages_order index must exist")
	assert.True(t, indexes["idx_groupchat_fanout_pending"], "idx_groupchat_fanout_pending index must exist")
}

// TestDaemonFailClosedOnConflictingGroupsView is the real gap this change
// closes: groupchat_groups has no CREATE INDEX statement of its own, so
// unlike groupchat_invites a VIEW occupying its name was never caught by
// accident. Before the fix this hits daemon.go's "group storage init
// failed" seam the same way the other fail-closed tests do; before the
// schema-health validator existed, NewStore returned success here and the
// daemon went on to dial relays.
func TestDaemonFailClosedOnConflictingGroupsView(t *testing.T) {
	home, _ := setupTestHomeAndIdentity(t)
	setupConflictingGroupsViewDB(t, home)
	outboxSize, outboxModTime := setupInitialOutbox(t, home)

	var relayConnections atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		relayConnections.Add(1)
	}))
	defer server.Close()
	relayURL := "ws" + strings.TrimPrefix(server.URL, "http")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	args := []string{"--json", "daemon", "--identity", "alice", "--relay", relayURL, "--password-stdin"}
	command := exec.CommandContext(ctx, daemonLockCLI, args...)
	command.Stdin = strings.NewReader("startup-secret-123\n")
	command.Env = withoutDaemonOutputEnv(os.Environ())
	command.Env = append(command.Env, "HOME="+home)

	output, err := command.CombinedOutput()
	require.NoError(t, ctx.Err(), "fail-closed startup must refuse well before the 15s bound")
	require.Error(t, err, "daemon must exit nonzero on a VIEW occupying groupchat_groups")
	assert.Zero(t, relayConnections.Load(), "relay dial count must be 0 before any group schema is touched")

	outboxPath := filepath.Join(home, ".hyphae", "outbox.json")
	fi, statErr := os.Stat(outboxPath)
	require.NoError(t, statErr)
	assert.Equal(t, outboxSize, fi.Size(), "outbox bytes must remain unchanged")
	assert.True(t, fi.ModTime().Equal(outboxModTime), "outbox mtime must remain unchanged")

	lock, lockErr := acquireDaemonHomeLock()
	require.NoError(t, lockErr, "daemon home lock must be released after fail-closed exit")
	require.NoError(t, lock.Close())

	var result common.Result
	require.NoError(t, json.Unmarshal(output, &result), "stderr must be a valid JSON error envelope: %s", string(output))
	assert.False(t, result.OK)
	assert.Equal(t, common.ErrCodeOther, result.Error)
	assert.Contains(t, result.Message, "group storage init failed")
}

// TestDaemonFailClosedOnConflictingMembersTable covers the same gap as
// TestDaemonFailClosedOnConflictingGroupsView but for a wrong-shape TABLE
// (rather than a VIEW) occupying groupchat_members.
func TestDaemonFailClosedOnConflictingMembersTable(t *testing.T) {
	home, _ := setupTestHomeAndIdentity(t)
	setupConflictingMembersTableDB(t, home)
	outboxSize, outboxModTime := setupInitialOutbox(t, home)

	var relayConnections atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		relayConnections.Add(1)
	}))
	defer server.Close()
	relayURL := "ws" + strings.TrimPrefix(server.URL, "http")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	args := []string{"--json", "daemon", "--identity", "alice", "--relay", relayURL, "--password-stdin"}
	command := exec.CommandContext(ctx, daemonLockCLI, args...)
	command.Stdin = strings.NewReader("startup-secret-123\n")
	command.Env = withoutDaemonOutputEnv(os.Environ())
	command.Env = append(command.Env, "HOME="+home)

	output, err := command.CombinedOutput()
	require.NoError(t, ctx.Err(), "fail-closed startup must refuse well before the 15s bound")
	require.Error(t, err, "daemon must exit nonzero on a wrong-shape TABLE occupying groupchat_members")
	assert.Zero(t, relayConnections.Load(), "relay dial count must be 0 before any group schema is touched")

	outboxPath := filepath.Join(home, ".hyphae", "outbox.json")
	fi, statErr := os.Stat(outboxPath)
	require.NoError(t, statErr)
	assert.Equal(t, outboxSize, fi.Size(), "outbox bytes must remain unchanged")
	assert.True(t, fi.ModTime().Equal(outboxModTime), "outbox mtime must remain unchanged")

	lock, lockErr := acquireDaemonHomeLock()
	require.NoError(t, lockErr, "daemon home lock must be released after fail-closed exit")
	require.NoError(t, lock.Close())

	var result common.Result
	require.NoError(t, json.Unmarshal(output, &result), "stderr must be a valid JSON error envelope: %s", string(output))
	assert.False(t, result.OK)
	assert.Equal(t, common.ErrCodeOther, result.Error)
	assert.Contains(t, result.Message, "group storage init failed")
}

// TestDMCLIHealthyDespiteBrokenGroupOnlySchema proves the fail-closed check
// is scoped to the groupchat_* seam: a broken groupchat_groups view must not
// stop the unrelated DM-history CLI path from reading/writing the healthy
// messages table in the same database file.
func TestDMCLIHealthyDespiteBrokenGroupOnlySchema(t *testing.T) {
	home, id := setupTestHomeAndIdentity(t)
	setupConflictingGroupsViewDB(t, home)

	db, err := storage.OpenDB()
	require.NoError(t, err, "standard storage.OpenDB must open without calling groupchat.NewStore")
	defer db.Close()

	_, err = db.Exec(`INSERT INTO messages (id, sender_npub, recipient_npub, created_at, received_at, plaintext)
		VALUES ('dm-msg-2', ?, 'npub1recipient', 12345, 12345, 'dm content')`, id.Npub)
	require.NoError(t, err)

	var count int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM messages WHERE id = 'dm-msg-2'`).Scan(&count))
	assert.Equal(t, 1, count, "DM messages table must accept writes despite a broken group-only schema")

	_, err = groupchat.NewStore(db)
	require.Error(t, err, "group storage must still refuse to initialize on the same broken file")
}

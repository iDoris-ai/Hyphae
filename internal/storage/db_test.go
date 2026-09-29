package storage

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetDBPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path, err := GetDBPath()
	require.NoError(t, err)
	assert.NotEmpty(t, path)
	assert.Contains(t, path, "messages.db")
}

func TestInitDBPragmasApplyToEveryConnection(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home with ?hash#percent%")
	require.NoError(t, os.MkdirAll(home, 0700))
	t.Setenv("HOME", home)

	db, err := InitDB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	dbPath := filepath.Join(home, ".hyphae", "messages.db")
	info, err := os.Stat(dbPath)
	require.NoError(t, err, "InitDB must create the database at the expected path")
	require.True(t, info.Mode().IsRegular(), "database path must be a regular file")
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(0)

	ctx := context.Background()
	first, err := db.Conn(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = first.Close() })
	second, err := db.Conn(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = second.Close() })
	assertPragmas(t, first, 5000, 1, 1)
	assertPragmas(t, second, 5000, 1, 1)

	// With no idle connections allowed, closing first discards its physical
	// connection. Opening another while second remains held forces a new one.
	require.NoError(t, first.Close())
	third, err := db.Conn(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = third.Close() })
	assertPragmas(t, third, 5000, 1, 1)

	_, err = third.ExecContext(ctx, `CREATE TABLE pragma_parent (id INTEGER PRIMARY KEY)`)
	require.NoError(t, err)
	_, err = third.ExecContext(ctx, `CREATE TABLE pragma_child (parent_id INTEGER REFERENCES pragma_parent(id))`)
	require.NoError(t, err)
	_, err = third.ExecContext(ctx, `INSERT INTO pragma_parent (id) VALUES (99)`)
	require.NoError(t, err)
	_, err = third.ExecContext(ctx, `INSERT INTO pragma_child (parent_id) VALUES (99)`)
	require.NoError(t, err, "a child row with an existing parent must be accepted")
	_, err = third.ExecContext(ctx, `INSERT INTO pragma_child (parent_id) VALUES (100)`)
	require.Error(t, err)
	require.Contains(strings.ToLower(err.Error()), "foreign key constraint failed",
		"the failed insert must be rejected by SQLite's foreign-key constraint")

	require.NoError(t, third.Close())
	require.NoError(t, second.Close())
}

func TestConnectionLocalExecPragmasOnlyAffectOneHeldConnection(t *testing.T) {
	home := filepath.Join(t.TempDir(), "isolated home")
	require.NoError(t, os.MkdirAll(home, 0700))
	t.Setenv("HOME", home)
	dbPath := filepath.Join(home, "legacy-control.db")

	db, err := sql.Open("sqlite", dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(1)
	for _, statement := range []string{
		"PRAGMA busy_timeout = 5000",
		"PRAGMA foreign_keys = ON",
		"PRAGMA synchronous = NORMAL",
	} {
		_, err := db.Exec(statement)
		require.NoError(t, err)
	}

	ctx := context.Background()
	configured, err := db.Conn(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = configured.Close() })
	untouched, err := db.Conn(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = untouched.Close() })
	assertPragmas(t, configured, 5000, 1, 1)
	assertPragmas(t, untouched, 0, 0, 2)
	require.NoError(t, configured.Close())
	require.NoError(t, untouched.Close())
}

func assertPragmas(t *testing.T, conn *sql.Conn, busyTimeout, foreignKeys, synchronous int) {
	t.Helper()
	var gotBusy, gotForeignKeys, gotSynchronous int
	for _, check := range []struct {
		query string
		dest  *int
	}{
		{"PRAGMA busy_timeout", &gotBusy},
		{"PRAGMA foreign_keys", &gotForeignKeys},
		{"PRAGMA synchronous", &gotSynchronous},
	} {
		require.NoError(t, conn.QueryRowContext(context.Background(), check.query).Scan(check.dest))
	}
	assert.Equal(t, busyTimeout, gotBusy, "busy_timeout")
	assert.Equal(t, foreignKeys, gotForeignKeys, "foreign_keys")
	assert.Equal(t, synchronous, gotSynchronous, "synchronous")
}

func TestInitDB(t *testing.T) {
	// Use temp directory for testing
	tempDir := t.TempDir()
	t.Setenv("HOME", tempDir)

	db, err := InitDB()
	require.NoError(t, err)
	defer db.Close()

	// Verify connection
	err = db.Ping()
	assert.NoError(t, err)

	// Verify table exists
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='messages'").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	// Verify indexes exist
	rows, err := db.Query("SELECT name FROM sqlite_master WHERE type='index' AND tbl_name='messages'")
	require.NoError(t, err)
	defer rows.Close()

	indexes := []string{}
	for rows.Next() {
		var name string
		err := rows.Scan(&name)
		require.NoError(t, err)
		indexes = append(indexes, name)
	}

	assert.GreaterOrEqual(t, len(indexes), 4, "Expected at least 4 indexes")
}

func TestMigrateFromJSON(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("HOME", tempDir)

	// Create keystore dir
	keystoreDir := filepath.Join(tempDir, ".hyphae")
	err := os.MkdirAll(keystoreDir, 0700)
	require.NoError(t, err)

	// Create a test JSON file
	testJSON := `{
		"messages": [
			{
				"id": "test1",
				"sender_npub": "npub1abc",
				"recipient_npub": "npub1def",
				"content": "Hello",
				"created_at": 1234567890,
				"is_encrypted": false,
				"is_incoming": true
			},
			{
				"id": "test2",
				"sender_npub": "npub1def",
				"recipient_npub": "npub1abc",
				"content": "World",
				"created_at": 1234567891,
				"is_encrypted": true,
				"is_incoming": false
			}
		]
	}`
	jsonPath := filepath.Join(keystoreDir, "messages.json")
	err = os.WriteFile(jsonPath, []byte(testJSON), 0600)
	require.NoError(t, err)

	// Init DB and run migration
	db, err := InitDB()
	require.NoError(t, err)
	defer db.Close()

	err = MigrateFromJSON(db)
	require.NoError(t, err)

	// Verify backup was created
	backupPath := jsonPath + ".backup"
	_, err = os.Stat(backupPath)
	assert.NoError(t, err, "Backup file should exist")

	// Verify messages were actually migrated into SQLite
	store := NewMessageStore(db)
	msg1, err := store.GetMessage("test1")
	require.NoError(t, err)
	require.NotNil(t, msg1)
	assert.Equal(t, "npub1abc", msg1.SenderNpub)
	assert.Equal(t, "npub1def", msg1.RecipientNpub)
	assert.Equal(t, "Hello", msg1.Content)
	assert.True(t, msg1.IsIncoming)
	assert.False(t, msg1.IsEncrypted)

	msg2, err := store.GetMessage("test2")
	require.NoError(t, err)
	require.NotNil(t, msg2)
	assert.Equal(t, "World", msg2.Content)
	assert.True(t, msg2.IsEncrypted)
	assert.False(t, msg2.IsIncoming)

	// Verify stats reflect migrated messages
	stats, err := store.GetStats("npub1abc")
	require.NoError(t, err)
	assert.Equal(t, 2, stats["total"])
	assert.Equal(t, 1, stats["incoming"])
	assert.Equal(t, 1, stats["outgoing"])
	assert.Equal(t, 1, stats["encrypted"])
}

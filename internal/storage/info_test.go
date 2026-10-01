package storage

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInspectStorageInfoMissingDoesNotCreateHomeOrDatabaseDirectory(t *testing.T) {
	home := filepath.Join(t.TempDir(), "not-created-home")
	t.Setenv("HOME", home)

	got, err := inspectStorageInfo(context.Background())
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, ".hyphae", "messages.db"), got.Path)
	assert.False(t, got.Exists)
	assert.Zero(t, got.SizeBytes)
	assert.Empty(t, got.Mode)
	assert.Zero(t, got.MessageCount)
	assert.NotNil(t, got.Tables)
	assert.Empty(t, got.Tables)
	assert.NoDirExists(t, home, "inspecting an absent database must not create its home")
}

func TestInspectStorageInfoReadOnlyAndEncodedPath(t *testing.T) {
	home := filepath.Join(t.TempDir(), "space ?hash#percent% home")
	t.Setenv("HOME", home)
	dbPath := filepath.Join(home, ".hyphae", "messages.db")
	require.NoError(t, os.MkdirAll(filepath.Dir(dbPath), 0700))
	db, err := sql.Open("sqlite", sqliteDSN(dbPath))
	require.NoError(t, err)
	_, err = db.Exec(`CREATE TABLE messages (id TEXT); CREATE TABLE zebra (id INTEGER); CREATE TABLE alpha (id INTEGER); INSERT INTO messages VALUES ('one'), ('two')`)
	require.NoError(t, err)
	require.NoError(t, db.Close())
	before, err := os.ReadFile(dbPath)
	require.NoError(t, err)

	got, err := inspectStorageInfo(context.Background())
	require.NoError(t, err)
	assert.Equal(t, dbPath, got.Path)
	assert.True(t, got.Exists)
	assert.EqualValues(t, len(before), got.SizeBytes)
	assert.NotEmpty(t, got.Mode)
	assert.EqualValues(t, 2, got.MessageCount)
	assert.Equal(t, []string{"alpha", "messages", "zebra"}, got.Tables)
	after, err := os.ReadFile(dbPath)
	require.NoError(t, err)
	assert.Equal(t, before, after, "read-only inspection must not rewrite the database file")
}

func TestInspectStorageInfoRejectsUnsupportedSchemaWithoutMigration(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dbPath := filepath.Join(home, ".hyphae", "messages.db")
	require.NoError(t, os.MkdirAll(filepath.Dir(dbPath), 0700))
	db, err := sql.Open("sqlite", dbPath)
	require.NoError(t, err)
	_, err = db.Exec("CREATE TABLE unrelated (value TEXT)")
	require.NoError(t, err)
	require.NoError(t, db.Close())

	_, err = inspectStorageInfo(context.Background())
	require.Error(t, err, "a missing messages table must be an error")
	check, err := sql.Open("sqlite", dbPath)
	require.NoError(t, err)
	defer check.Close()
	var messagesTables int
	require.NoError(t, check.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='messages'").Scan(&messagesTables))
	assert.Zero(t, messagesTables, "inspection must not run migrations")
}

func TestInspectStorageInfoRejectsNonRegularPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dbPath := filepath.Join(home, ".hyphae", "messages.db")
	require.NoError(t, os.MkdirAll(dbPath, 0700))
	_, err := inspectStorageInfo(context.Background())
	require.EqualError(t, err, "database path is not a regular file")
	entries, err := os.ReadDir(dbPath)
	require.NoError(t, err)
	assert.Empty(t, entries)
}

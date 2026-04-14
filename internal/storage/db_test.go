package storage

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetDBPath(t *testing.T) {
	path, err := GetDBPath()
	require.NoError(t, err)
	assert.NotEmpty(t, path)
	assert.Contains(t, path, "messages.db")
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
	keystoreDir := filepath.Join(tempDir, ".agent-speaker")
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

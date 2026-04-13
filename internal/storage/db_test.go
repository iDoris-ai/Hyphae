package storage

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetDBPath(t *testing.T) {
	path := GetDBPath()
	assert.NotEmpty(t, path)
	assert.Contains(t, path, "messages.db")
}

func TestInitDB(t *testing.T) {
	// Use temp directory for testing
	tempDir := t.TempDir()
	os.Setenv("HOME", tempDir)
	defer os.Unsetenv("HOME")

	db, err := InitDB()
	require.NoError(t, err)
	defer db.Close()
	defer os.RemoveAll(tempDir)

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
	os.Setenv("HOME", tempDir)
	defer os.Unsetenv("HOME")

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
				"is_encrypted": false
			}
		]
	}`
	jsonPath := filepath.Join(keystoreDir, "messages.json")
	err = os.WriteFile(jsonPath, []byte(testJSON), 0600)
	require.NoError(t, err)

	// Run migration
	err = MigrateFromJSON()
	require.NoError(t, err)

	// Verify backup was created
	backupPath := jsonPath + ".backup"
	_, err = os.Stat(backupPath)
	assert.NoError(t, err, "Backup file should exist")
}

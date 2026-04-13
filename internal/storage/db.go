package storage

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jason/agent-speaker/internal/identity"
	_ "modernc.org/sqlite"
)

// DB is the global database instance
var DB *sql.DB

// GetDBPath returns the path to the SQLite database
func GetDBPath() string {
	path, _ := identity.EnsureKeyStore()
	return filepath.Join(path, "messages.db")
}

// InitDB initializes the SQLite database
func InitDB() (*sql.DB, error) {
	dbPath := GetDBPath()

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Test connection
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	// Enable foreign keys
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		return nil, fmt.Errorf("failed to enable foreign keys: %w", err)
	}

	// Run migrations
	if err := migrate(db); err != nil {
		return nil, fmt.Errorf("failed to migrate database: %w", err)
	}

	DB = db
	return db, nil
}

// CloseDB closes the database connection
func CloseDB() error {
	if DB != nil {
		return DB.Close()
	}
	return nil
}

// migrate runs database migrations
func migrate(db *sql.DB) error {
	// Create messages table
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS messages (
			id TEXT PRIMARY KEY,
			event_id TEXT UNIQUE,
			sender_npub TEXT NOT NULL,
			recipient_npub TEXT NOT NULL,
			content TEXT,
			plaintext TEXT,
			created_at INTEGER NOT NULL,
			received_at INTEGER NOT NULL,
			is_encrypted BOOLEAN DEFAULT 0,
			is_incoming BOOLEAN DEFAULT 0,
			relay TEXT,
			kind INTEGER DEFAULT 30078
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create messages table: %w", err)
	}

	// Create indexes
	indexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_messages_sender ON messages(sender_npub)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_recipient ON messages(recipient_npub)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_created ON messages(created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_conversation ON messages(sender_npub, recipient_npub, created_at DESC)`,
	}

	for _, idx := range indexes {
		if _, err := db.Exec(idx); err != nil {
			return fmt.Errorf("failed to create index: %w", err)
		}
	}

	return nil
}

// MigrateFromJSON migrates existing JSON data to SQLite
func MigrateFromJSON() error {
	jsonPath := filepath.Join(identity.GetKeyStorePath(), "messages.json")

	// Check if JSON file exists
	if _, err := os.Stat(jsonPath); os.IsNotExist(err) {
		return nil // No migration needed
	}

	// Read JSON file
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		return fmt.Errorf("failed to read JSON file: %w", err)
	}

	// If empty or invalid, skip
	if len(data) < 10 {
		return nil
	}

	// TODO: Parse JSON and insert into SQLite
	// For now, just rename the file as backup
	backupPath := jsonPath + ".backup"
	if err := os.Rename(jsonPath, backupPath); err != nil {
		return fmt.Errorf("failed to backup JSON file: %w", err)
	}

	return nil
}

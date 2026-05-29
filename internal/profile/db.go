package profile

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/AuraAIHQ/agent-speaker/internal/storage"
	"github.com/AuraAIHQ/agent-speaker/pkg/types"
)

// DB wraps profile database operations
type DB struct {
	db *sql.DB
}

// NewDB creates a new profile DB instance
func NewDB() (*DB, error) {
	db, err := storage.InitDB()
	if err != nil {
		return nil, err
	}

	p := &DB{db: db}
	if err := p.migrate(); err != nil {
		return nil, err
	}

	return p, nil
}

// NewDBWithPath creates a new profile DB instance for testing
func NewDBWithPath(dbPath string) (*DB, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		return nil, fmt.Errorf("failed to enable foreign keys: %w", err)
	}

	p := &DB{db: db}
	if err := p.migrate(); err != nil {
		return nil, err
	}

	return p, nil
}

// migrate creates profile tables
func (p *DB) migrate() error {
	_, err := p.db.Exec(`
		CREATE TABLE IF NOT EXISTS agent_profiles (
			npub TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			description TEXT,
			availability TEXT,
			profile_json TEXT NOT NULL,
			updated_at INTEGER NOT NULL,
			discovered_at INTEGER NOT NULL
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create agent_profiles table: %w", err)
	}

	indexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_profiles_name ON agent_profiles(name)`,
		`CREATE INDEX IF NOT EXISTS idx_profiles_availability ON agent_profiles(availability)`,
		`CREATE INDEX IF NOT EXISTS idx_profiles_updated ON agent_profiles(updated_at DESC)`,
	}

	for _, idx := range indexes {
		if _, err := p.db.Exec(idx); err != nil {
			return fmt.Errorf("failed to create index: %w", err)
		}
	}

	return nil
}

// StoreProfile stores or updates an agent profile
func (p *DB) StoreProfile(npub string, profile *types.AgentProfile) error {
	jsonData, err := json.Marshal(profile)
	if err != nil {
		return fmt.Errorf("failed to marshal profile: %w", err)
	}

	_, err = p.db.Exec(`
		INSERT OR REPLACE INTO agent_profiles (npub, name, description, availability, profile_json, updated_at, discovered_at)
		VALUES (?, ?, ?, ?, ?, ?, COALESCE((SELECT discovered_at FROM agent_profiles WHERE npub = ?), ?))
	`, npub, profile.Name, profile.Description, profile.Availability, string(jsonData), profile.UpdatedAt, npub, profile.UpdatedAt)

	if err != nil {
		return fmt.Errorf("failed to store profile: %w", err)
	}

	return nil
}

// GetProfile retrieves a profile by npub
func (p *DB) GetProfile(npub string) (*types.AgentProfile, error) {
	var jsonData string
	err := p.db.QueryRow(
		"SELECT profile_json FROM agent_profiles WHERE npub = ?",
		npub,
	).Scan(&jsonData)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get profile: %w", err)
	}

	profile, err := types.AgentProfileFromJSON([]byte(jsonData))
	if err != nil {
		return nil, err
	}

	return profile, nil
}

// ListProfiles lists all stored profiles
func (p *DB) ListProfiles() ([]*types.StoredProfile, error) {
	rows, err := p.db.Query(`
		SELECT npub, name, description, availability, profile_json, updated_at, discovered_at
		FROM agent_profiles
		ORDER BY name
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to list profiles: %w", err)
	}
	defer rows.Close()

	var profiles []*types.StoredProfile
	for rows.Next() {
		var sp types.StoredProfile
		var jsonData string
		if err := rows.Scan(&sp.Npub, &sp.Name, &sp.Description, &sp.Availability, &jsonData, &sp.UpdatedAt, &sp.DiscoveredAt); err != nil {
			continue
		}
		profile, err := types.AgentProfileFromJSON([]byte(jsonData))
		if err != nil {
			continue
		}
		sp.Profile = profile
		profiles = append(profiles, &sp)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return profiles, nil
}

// SearchProfiles searches profiles by name or capability
func (p *DB) SearchProfiles(query string) ([]*types.StoredProfile, error) {
	pattern := "%" + query + "%"
	rows, err := p.db.Query(`
		SELECT npub, name, description, availability, profile_json, updated_at, discovered_at
		FROM agent_profiles
		WHERE name LIKE ? OR description LIKE ? OR profile_json LIKE ?
		ORDER BY name
	`, pattern, pattern, pattern)
	if err != nil {
		return nil, fmt.Errorf("failed to search profiles: %w", err)
	}
	defer rows.Close()

	var profiles []*types.StoredProfile
	for rows.Next() {
		var sp types.StoredProfile
		var jsonData string
		if err := rows.Scan(&sp.Npub, &sp.Name, &sp.Description, &sp.Availability, &jsonData, &sp.UpdatedAt, &sp.DiscoveredAt); err != nil {
			continue
		}
		profile, err := types.AgentProfileFromJSON([]byte(jsonData))
		if err != nil {
			continue
		}
		sp.Profile = profile
		profiles = append(profiles, &sp)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return profiles, nil
}

// DeleteProfile deletes a profile by npub
func (p *DB) DeleteProfile(npub string) error {
	_, err := p.db.Exec("DELETE FROM agent_profiles WHERE npub = ?", npub)
	return err
}

// Close closes the database connection
func (p *DB) Close() error {
	return p.db.Close()
}

package group

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/AuraAIHQ/agent-speaker/internal/storage"
	"github.com/AuraAIHQ/agent-speaker/pkg/types"
)

// DB wraps group database operations
type DB struct {
	db *sql.DB
}

// NewDB creates a new group DB instance
func NewDB() (*DB, error) {
	// Ensure main DB is initialized
	db, err := storage.InitDB()
	if err != nil {
		return nil, err
	}

	g := &DB{db: db}
	if err := g.migrate(); err != nil {
		return nil, err
	}

	return g, nil
}

// migrate creates group tables
func (g *DB) migrate() error {
	// Groups table
	_, err := g.db.Exec(`
		CREATE TABLE IF NOT EXISTS groups (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			description TEXT,
			creator TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create groups table: %w", err)
	}

	// Group members table
	_, err = g.db.Exec(`
		CREATE TABLE IF NOT EXISTS group_members (
			group_id TEXT NOT NULL,
			npub TEXT NOT NULL,
			nickname TEXT,
			joined_at INTEGER NOT NULL,
			is_admin BOOLEAN DEFAULT 0,
			PRIMARY KEY (group_id, npub),
			FOREIGN KEY (group_id) REFERENCES groups(id) ON DELETE CASCADE
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create group_members table: %w", err)
	}

	// Group messages table
	_, err = g.db.Exec(`
		CREATE TABLE IF NOT EXISTS group_messages (
			id TEXT PRIMARY KEY,
			event_id TEXT UNIQUE,
			group_id TEXT NOT NULL,
			sender TEXT NOT NULL,
			content TEXT,
			plaintext TEXT,
			created_at INTEGER NOT NULL,
			is_encrypted BOOLEAN DEFAULT 0,
			FOREIGN KEY (group_id) REFERENCES groups(id) ON DELETE CASCADE
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create group_messages table: %w", err)
	}

	// Indexes
	indexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_group_members_group ON group_members(group_id)`,
		`CREATE INDEX IF NOT EXISTS idx_group_members_npub ON group_members(npub)`,
		`CREATE INDEX IF NOT EXISTS idx_group_messages_group ON group_messages(group_id)`,
		`CREATE INDEX IF NOT EXISTS idx_group_messages_sender ON group_messages(sender)`,
		`CREATE INDEX IF NOT EXISTS idx_group_messages_created ON group_messages(created_at DESC)`,
	}

	for _, idx := range indexes {
		if _, err := g.db.Exec(idx); err != nil {
			return fmt.Errorf("failed to create index: %w", err)
		}
	}

	return nil
}

// CreateGroup creates a new group
func (g *DB) CreateGroup(name, description, creator string, members []string) (*types.Group, error) {
	id := generateGroupID(name, creator)
	now := time.Now().Unix()

	// Insert group
	_, err := g.db.Exec(
		"INSERT INTO groups (id, name, description, creator, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)",
		id, name, description, creator, now, now,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create group: %w", err)
	}

	// Add members
	for _, member := range members {
		_, err := g.db.Exec(
			"INSERT INTO group_members (group_id, npub, joined_at, is_admin) VALUES (?, ?, ?, ?)",
			id, member, now, member == creator,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to add member: %w", err)
		}
	}

	return g.GetGroup(id)
}

// GetGroup retrieves a group by ID
func (g *DB) GetGroup(id string) (*types.Group, error) {
	var group types.Group
	err := g.db.QueryRow(
		"SELECT id, name, description, creator, created_at, updated_at FROM groups WHERE id = ?",
		id,
	).Scan(&group.ID, &group.Name, &group.Description, &group.Creator, &group.CreatedAt, &group.UpdatedAt)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	// Load members
	members, err := g.GetGroupMembers(id)
	if err != nil {
		return nil, err
	}
	group.Members = members

	return &group, nil
}

// GetGroupMembers gets all members of a group
func (g *DB) GetGroupMembers(groupID string) ([]string, error) {
	rows, err := g.db.Query(
		"SELECT npub FROM group_members WHERE group_id = ? ORDER BY joined_at",
		groupID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var members []string
	for rows.Next() {
		var npub string
		if err := rows.Scan(&npub); err != nil {
			continue
		}
		members = append(members, npub)
	}

	return members, nil
}

// GetGroupsForUser gets all groups a user is member of
func (g *DB) GetGroupsForUser(npub string) ([]*types.Group, error) {
	rows, err := g.db.Query(`
		SELECT g.id, g.name, g.description, g.creator, g.created_at, g.updated_at
		FROM groups g
		JOIN group_members gm ON g.id = gm.group_id
		WHERE gm.npub = ?
		ORDER BY g.updated_at DESC
	`, npub)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var groups []*types.Group
	for rows.Next() {
		var group types.Group
		if err := rows.Scan(&group.ID, &group.Name, &group.Description, &group.Creator, &group.CreatedAt, &group.UpdatedAt); err != nil {
			continue
		}
		// Load members
		members, err := g.GetGroupMembers(group.ID)
		if err != nil {
			continue
		}
		group.Members = members
		groups = append(groups, &group)
	}

	return groups, nil
}

// AddMember adds a member to a group
func (g *DB) AddMember(groupID, npub string) error {
	_, err := g.db.Exec(
		"INSERT OR IGNORE INTO group_members (group_id, npub, joined_at) VALUES (?, ?, ?)",
		groupID, npub, time.Now().Unix(),
	)
	if err != nil {
		return err
	}

	// Update group updated_at
	_, err = g.db.Exec("UPDATE groups SET updated_at = ? WHERE id = ?", time.Now().Unix(), groupID)
	return err
}

// RemoveMember removes a member from a group
func (g *DB) RemoveMember(groupID, npub string) error {
	_, err := g.db.Exec(
		"DELETE FROM group_members WHERE group_id = ? AND npub = ?",
		groupID, npub,
	)
	if err != nil {
		return err
	}

	// Update group updated_at
	_, err = g.db.Exec("UPDATE groups SET updated_at = ? WHERE id = ?", time.Now().Unix(), groupID)
	return err
}

// IsMember checks if a user is a member of a group
func (g *DB) IsMember(groupID, npub string) (bool, error) {
	var count int
	err := g.db.QueryRow(
		"SELECT COUNT(*) FROM group_members WHERE group_id = ? AND npub = ?",
		groupID, npub,
	).Scan(&count)
	return count > 0, err
}

// DeleteGroup deletes a group and all related data
func (g *DB) DeleteGroup(groupID string) error {
	_, err := g.db.Exec("DELETE FROM groups WHERE id = ?", groupID)
	return err
}

// StoreGroupMessage stores a group message
func (g *DB) StoreGroupMessage(msg *types.GroupMessage) error {
	_, err := g.db.Exec(
		"INSERT OR REPLACE INTO group_messages (id, event_id, group_id, sender, content, plaintext, created_at, is_encrypted) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		msg.ID, msg.EventID, msg.GroupID, msg.Sender, msg.Content, msg.Plaintext, msg.CreatedAt, msg.IsEncrypted,
	)
	return err
}

// GetGroupMessages retrieves messages for a group
func (g *DB) GetGroupMessages(groupID string, limit int) ([]*types.GroupMessage, error) {
	rows, err := g.db.Query(`
		SELECT id, event_id, group_id, sender, content, plaintext, created_at, is_encrypted
		FROM group_messages
		WHERE group_id = ?
		ORDER BY created_at DESC
		LIMIT ?
	`, groupID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []*types.GroupMessage
	for rows.Next() {
		var msg types.GroupMessage
		if err := rows.Scan(&msg.ID, &msg.EventID, &msg.GroupID, &msg.Sender, &msg.Content, &msg.Plaintext, &msg.CreatedAt, &msg.IsEncrypted); err != nil {
			continue
		}
		messages = append(messages, &msg)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return messages, nil
}

// generateGroupID generates a unique group ID
func generateGroupID(name, creator string) string {
	return fmt.Sprintf("group_%s_%d", creator[:8], time.Now().UnixNano())
}

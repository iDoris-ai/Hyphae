package storage

import (
	"database/sql"
	"fmt"
	"time"

	"fiatjaf.com/nostr"
	"github.com/jason/agent-speaker/internal/common"
	"github.com/jason/agent-speaker/pkg/types"
)

// MessageStore provides database operations for messages
type MessageStore struct {
	db *sql.DB
}

// NewMessageStore creates a new message store
func NewMessageStore(db *sql.DB) *MessageStore {
	return &MessageStore{db: db}
}

// StoreMessage stores a message in the database
func (s *MessageStore) StoreMessage(msg *types.StoredMessage) error {
	query := `
		INSERT OR REPLACE INTO messages (
			id, event_id, sender_npub, recipient_npub, content, plaintext,
			created_at, received_at, is_encrypted, is_incoming, relay, kind
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	_, err := s.db.Exec(query,
		msg.ID,
		msg.ID, // event_id same as id for now
		msg.SenderNpub,
		msg.RecipientNpub,
		msg.Content,
		msg.Plaintext,
		msg.CreatedAt,
		time.Now().Unix(), // received_at
		msg.IsEncrypted,
		msg.IsIncoming,
		msg.Relay,
		30078, // AgentKind
	)

	if err != nil {
		return fmt.Errorf("failed to store message: %w", err)
	}

	return nil
}

// GetMessage retrieves a message by ID
func (s *MessageStore) GetMessage(id string) (*types.StoredMessage, error) {
	query := `
		SELECT id, sender_npub, recipient_npub, content, plaintext,
		       created_at, received_at, is_encrypted, is_incoming, relay
		FROM messages WHERE id = ?
	`

	var msg types.StoredMessage
	err := s.db.QueryRow(query, id).Scan(
		&msg.ID,
		&msg.SenderNpub,
		&msg.RecipientNpub,
		&msg.Content,
		&msg.Plaintext,
		&msg.CreatedAt,
		&msg.ReceivedAt,
		&msg.IsEncrypted,
		&msg.IsIncoming,
		&msg.Relay,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get message: %w", err)
	}

	return &msg, nil
}

// GetConversation retrieves messages between two users
func (s *MessageStore) GetConversation(user1Npub, user2Npub string, limit int) ([]types.StoredMessage, error) {
	query := `
		SELECT id, sender_npub, recipient_npub, content, plaintext,
		       created_at, received_at, is_encrypted, is_incoming, relay
		FROM messages
		WHERE (sender_npub = ? AND recipient_npub = ?)
		   OR (sender_npub = ? AND recipient_npub = ?)
		ORDER BY created_at DESC
		LIMIT ?
	`

	rows, err := s.db.Query(query, user1Npub, user2Npub, user2Npub, user1Npub, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query conversation: %w", err)
	}
	defer rows.Close()

	return s.scanMessages(rows)
}

// GetInbox retrieves messages for a user
func (s *MessageStore) GetInbox(userNpub string, limit int) ([]types.StoredMessage, error) {
	query := `
		SELECT id, sender_npub, recipient_npub, content, plaintext,
		       created_at, received_at, is_encrypted, is_incoming, relay
		FROM messages
		WHERE recipient_npub = ?
		ORDER BY created_at DESC
		LIMIT ?
	`

	rows, err := s.db.Query(query, userNpub, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query inbox: %w", err)
	}
	defer rows.Close()

	return s.scanMessages(rows)
}

// GetSent retrieves messages sent by a user
func (s *MessageStore) GetSent(userNpub string, limit int) ([]types.StoredMessage, error) {
	query := `
		SELECT id, sender_npub, recipient_npub, content, plaintext,
		       created_at, received_at, is_encrypted, is_incoming, relay
		FROM messages
		WHERE sender_npub = ?
		ORDER BY created_at DESC
		LIMIT ?
	`

	rows, err := s.db.Query(query, userNpub, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query sent messages: %w", err)
	}
	defer rows.Close()

	return s.scanMessages(rows)
}

// SearchMessages searches messages by content
func (s *MessageStore) SearchMessages(userNpub, query string, limit int) ([]types.StoredMessage, error) {
	searchQuery := `%` + query + `%`
	sqlQuery := `
		SELECT id, sender_npub, recipient_npub, content, plaintext,
		       created_at, received_at, is_encrypted, is_incoming, relay
		FROM messages
		WHERE (sender_npub = ? OR recipient_npub = ?)
		  AND (plaintext LIKE ? OR content LIKE ?)
		ORDER BY created_at DESC
		LIMIT ?
	`

	rows, err := s.db.Query(sqlQuery, userNpub, userNpub, searchQuery, searchQuery, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to search messages: %w", err)
	}
	defer rows.Close()

	return s.scanMessages(rows)
}

// GetStats returns message statistics for a user
func (s *MessageStore) GetStats(userNpub string) (map[string]int, error) {
	stats := make(map[string]int)

	// Total messages
	var total int
	err := s.db.QueryRow(
		"SELECT COUNT(*) FROM messages WHERE sender_npub = ? OR recipient_npub = ?",
		userNpub, userNpub,
	).Scan(&total)
	if err != nil {
		return nil, err
	}
	stats["total"] = total

	// Incoming
	var incoming int
	err = s.db.QueryRow(
		"SELECT COUNT(*) FROM messages WHERE recipient_npub = ?",
		userNpub,
	).Scan(&incoming)
	if err != nil {
		return nil, err
	}
	stats["incoming"] = incoming

	// Outgoing
	var outgoing int
	err = s.db.QueryRow(
		"SELECT COUNT(*) FROM messages WHERE sender_npub = ?",
		userNpub,
	).Scan(&outgoing)
	if err != nil {
		return nil, err
	}
	stats["outgoing"] = outgoing

	// Encrypted
	var encrypted int
	err = s.db.QueryRow(
		"SELECT COUNT(*) FROM messages WHERE (sender_npub = ? OR recipient_npub = ?) AND is_encrypted = 1",
		userNpub, userNpub,
	).Scan(&encrypted)
	if err != nil {
		return nil, err
	}
	stats["encrypted"] = encrypted

	return stats, nil
}

// DeleteMessage deletes a message by ID
func (s *MessageStore) DeleteMessage(id string) error {
	_, err := s.db.Exec("DELETE FROM messages WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("failed to delete message: %w", err)
	}
	return nil
}

// StoreOutgoingMessage stores a sent message from a nostr event
func (s *MessageStore) StoreOutgoingMessage(event *nostr.Event, recipientNpub, plaintext string, isEncrypted bool) error {
	msg := &types.StoredMessage{
		ID:            string(event.ID[:]),
		SenderNpub:    common.EncodeNpub(event.PubKey),
		RecipientNpub: recipientNpub,
		Content:       event.Content,
		Plaintext:     plaintext,
		CreatedAt:     int64(event.CreatedAt),
		ReceivedAt:    time.Now().Unix(),
		IsEncrypted:   isEncrypted,
		IsIncoming:    false,
	}
	return s.StoreMessage(msg)
}

// StoreIncomingMessage stores a received message from a nostr event
func (s *MessageStore) StoreIncomingMessage(event *nostr.Event, plaintext string, isEncrypted bool) error {
	// Get recipient from p tag
	recipientNpub := ""
	for _, tag := range event.Tags {
		if len(tag) >= 2 && tag[0] == "p" {
			pk, _ := common.ParsePublicKey(tag[1])
			recipientNpub = common.EncodeNpub(pk)
			break
		}
	}

	msg := &types.StoredMessage{
		ID:            string(event.ID[:]),
		SenderNpub:    common.EncodeNpub(event.PubKey),
		RecipientNpub: recipientNpub,
		Content:       event.Content,
		Plaintext:     plaintext,
		CreatedAt:     int64(event.CreatedAt),
		ReceivedAt:    time.Now().Unix(),
		IsEncrypted:   isEncrypted,
		IsIncoming:    true,
	}
	return s.StoreMessage(msg)
}

// scanMessages scans message rows
func (s *MessageStore) scanMessages(rows *sql.Rows) ([]types.StoredMessage, error) {
	var messages []types.StoredMessage

	for rows.Next() {
		var msg types.StoredMessage
		err := rows.Scan(
			&msg.ID,
			&msg.SenderNpub,
			&msg.RecipientNpub,
			&msg.Content,
			&msg.Plaintext,
			&msg.CreatedAt,
			&msg.ReceivedAt,
			&msg.IsEncrypted,
			&msg.IsIncoming,
			&msg.Relay,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan message: %w", err)
		}
		messages = append(messages, msg)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error: %w", err)
	}

	return messages, nil
}

package storage

import (
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"fiatjaf.com/nostr"
	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/pkg/types"
)

type sqlExecutor interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

// MessageStore provides database operations for messages
type MessageStore struct {
	db sqlExecutor
}

// NewMessageStore creates a new message store
func NewMessageStore(db sqlExecutor) *MessageStore {
	return &MessageStore{db: db}
}

// StoreMessage stores a message in the database
func (s *MessageStore) StoreMessage(msg *types.StoredMessage) error {
	// UPSERT, not `INSERT OR REPLACE`. Two separate reasons, and each one alone
	// would justify the change:
	//
	// 1. `is_incoming` must be MONOTONIC. `agent msg` publishes to the relay
	//    before it stores the message locally, so when the daemon's own
	//    subscription receives the echo inside that window it writes the row with
	//    is_incoming=1 — and the sender's later write then set it back to 0,
	//    while the daemon's `seen` set already held the id and never reprocessed
	//    it. The arrival was erased and could not be re-observed, which is what
	//    breaks a self-addressed liveness canary: it can never confirm its own
	//    message arrived. How often the window is actually hit is environment
	//    dependent and has not been pinned down -- see the test file.
	//
	// 2. `INSERT OR REPLACE` is DELETE-then-INSERT, so a later write that carries
	//    no plaintext blanked the decrypted text a previous write had stored.
	//    That is not specific to the race above; any second write did it.
	//
	// is_incoming is written with a CASE rather than the shorter
	// `messages.is_incoming | excluded.is_incoming`. Both are monotonic for
	// clean 0/1 input, but `|` ABSORBS a bad value permanently -- NULL|1 is
	// NULL, -1|1 is -1 -- and every read then fails scanning into a Go bool,
	// whereas `INSERT OR REPLACE` used to overwrite such a value back to a
	// clean 0/1. Nothing can write a non-0/1 value today (the column is only
	// ever bound from a Go bool, and defaults to 0), so this costs nothing; it
	// just refuses to trade away the old behaviour's self-healing.
	//
	// Only content, plaintext and relay are guarded against being blanked.
	// sender_npub, recipient_npub, created_at, received_at, is_encrypted and
	// kind are deliberately last-write-wins, exactly as before this change --
	// every production caller fills them from the nostr event, so a zero there
	// would mean the caller is already wrong.
	query := `
		INSERT INTO messages (
			id, event_id, sender_npub, recipient_npub, content, plaintext,
			created_at, received_at, is_encrypted, is_incoming, relay, kind
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			event_id       = excluded.event_id,
			sender_npub    = excluded.sender_npub,
			recipient_npub = excluded.recipient_npub,
			content        = CASE WHEN excluded.content   != '' THEN excluded.content   ELSE messages.content   END,
			plaintext      = CASE WHEN excluded.plaintext != '' THEN excluded.plaintext ELSE messages.plaintext END,
			created_at     = excluded.created_at,
			received_at    = excluded.received_at,
			is_encrypted   = excluded.is_encrypted,
			is_incoming    = CASE WHEN messages.is_incoming = 1 OR excluded.is_incoming = 1 THEN 1 ELSE 0 END,
			relay          = CASE WHEN excluded.relay     != '' THEN excluded.relay     ELSE messages.relay     END,
			kind           = excluded.kind
	`

	receivedAt := msg.ReceivedAt
	if receivedAt == 0 {
		receivedAt = time.Now().Unix()
	}

	_, err := s.db.Exec(query,
		msg.ID,
		msg.ID, // event_id same as id for now
		msg.SenderNpub,
		msg.RecipientNpub,
		msg.Content,
		msg.Plaintext,
		msg.CreatedAt,
		receivedAt,
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

// GetReceivedCount returns the total received message count for a user
func (s *MessageStore) GetReceivedCount(userNpub string) (int, error) {
	var count int
	err := s.db.QueryRow(
		"SELECT COUNT(*) FROM messages WHERE recipient_npub = ?",
		userNpub,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count received messages: %w", err)
	}
	return count, nil
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

// SearchMessages searches messages by content (case-insensitive)
func (s *MessageStore) SearchMessages(userNpub, query string, limit int) ([]types.StoredMessage, error) {
	// Application-level lowercasing for better Unicode support than SQLite's LOWER()
	searchQuery := "%" + strings.ToLower(query) + "%"
	sqlQuery := `
		SELECT id, sender_npub, recipient_npub, content, plaintext,
		       created_at, received_at, is_encrypted, is_incoming, relay
		FROM messages
		WHERE (sender_npub = ? OR recipient_npub = ?)
		  AND (LOWER(plaintext) LIKE ? OR LOWER(content) LIKE ?)
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

	query := `
		SELECT
			COUNT(*) AS total,
			SUM(CASE WHEN recipient_npub = ? THEN 1 ELSE 0 END) AS incoming,
			SUM(CASE WHEN sender_npub = ? THEN 1 ELSE 0 END) AS outgoing,
			SUM(CASE WHEN is_encrypted = 1 THEN 1 ELSE 0 END) AS encrypted
		FROM messages
		WHERE sender_npub = ? OR recipient_npub = ?
	`

	var total, incoming, outgoing, encrypted int
	err := s.db.QueryRow(query, userNpub, userNpub, userNpub, userNpub).Scan(
		&total, &incoming, &outgoing, &encrypted,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get stats: %w", err)
	}

	stats["total"] = total
	stats["incoming"] = incoming
	stats["outgoing"] = outgoing
	stats["encrypted"] = encrypted

	return stats, nil
}

// GetRecentIncomingEventIDs returns up to `limit` recent Nostr event IDs that
// have been stored as incoming for the given recipient npub, newest first.
// Used by the daemon to prime its in-memory dedup set on startup so that a
// restart does not re-process events the relay still echoes back.
func (s *MessageStore) GetRecentIncomingEventIDs(npub string, limit int) ([]string, error) {
	query := `
		SELECT event_id FROM messages
		WHERE recipient_npub = ? AND is_incoming = 1
		ORDER BY created_at DESC
		LIMIT ?
	`
	rows, err := s.db.Query(query, npub, limit)
	if err != nil {
		return nil, fmt.Errorf("query recent event ids: %w", err)
	}
	defer rows.Close()

	ids := make([]string, 0, limit)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan event id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows: %w", err)
	}
	return ids, nil
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
		ID:            hex.EncodeToString(event.ID[:]),
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
		ID:            hex.EncodeToString(event.ID[:]),
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

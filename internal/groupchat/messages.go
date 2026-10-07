package groupchat

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/iDoris-ai/hyphae/pkg/types"
)

// ReceiveMessage validates active group membership from the verified event
// author/recipient and atomically stores a logical message once. The envelope
// cannot alter the roster. Reuse of a logical ID for different content fails.
func (s *Store) ReceiveMessage(v VerifiedIncoming) (bool, error) {
	e, err := envelopeFrom(v, EnvelopeMessage)
	if err != nil {
		return false, err
	}
	return s.storeMessage(v.recipientNpub, e.GroupID, e.LogicalID, v.senderNpub,
		e.Body, v.createdAt, v.eventID, false)
}

// StoreLocalMessage stores one sender-side history row before any fanout.
// Fanout event IDs are per recipient and therefore are not stored as one
// canonical event ID on the logical local message.
func (s *Store) StoreLocalMessage(localNpub, groupID, logicalID, body string, createdAt int64) (types.GroupMessage, error) {
	localNpub, err := canonicalNpub(localNpub)
	if err != nil {
		return types.GroupMessage{}, err
	}
	if !validOpaqueID(logicalID) {
		return types.GroupMessage{}, errors.New("invalid logical message ID")
	}
	if err := validateBody(body); err != nil {
		return types.GroupMessage{}, err
	}
	if createdAt <= 0 {
		createdAt = time.Now().Unix()
	}
	if _, err := s.activeRoster(localNpub, groupID, localNpub); err != nil {
		return types.GroupMessage{}, err
	}
	_, err = s.storeMessage(localNpub, groupID, logicalID, localNpub, body, createdAt, "", true)
	if err != nil {
		return types.GroupMessage{}, err
	}
	return types.GroupMessage{ID: logicalID, GroupID: groupID, Sender: localNpub,
		Plaintext: body, CreatedAt: createdAt, IsEncrypted: true}, nil
}

// GroupMessages returns chronological local history. A left/cancelled/pending
// group is not displayable through the core API; its retained database rows
// remain available for explicit future export tooling.
func (s *Store) GroupMessages(localNpub, groupID string, limit int) ([]types.GroupMessage, error) {
	localNpub, err := canonicalNpub(localNpub)
	if err != nil {
		return nil, err
	}
	group, err := loadGroup(s.db, localNpub, groupID)
	if err != nil {
		return nil, err
	}
	if group.State == StateLeft {
		return nil, ErrGroupLeft
	}
	if group.State != StateActive {
		return nil, ErrGroupNotActive
	}
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(`SELECT logical_id, COALESCE(event_id, ''), group_id, sender_npub, body, created_at
		FROM groupchat_messages WHERE local_npub = ? AND group_id = ?
		ORDER BY created_at DESC, logical_id DESC LIMIT ?`, localNpub, groupID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var messages []types.GroupMessage
	for rows.Next() {
		var message types.GroupMessage
		if err := rows.Scan(&message.ID, &message.EventID, &message.GroupID, &message.Sender, &message.Plaintext, &message.CreatedAt); err != nil {
			return nil, err
		}
		message.IsEncrypted = true
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sortGroupMessages(messages)
	if len(messages) > limit {
		messages = messages[len(messages)-limit:]
	}
	return messages, nil
}

func (s *Store) storeMessage(localNpub, groupID, logicalID, senderNpub, body string, createdAt int64, eventID string, outgoing bool) (bool, error) {
	if !validOpaqueID(logicalID) {
		return false, errors.New("invalid logical message ID")
	}
	if err := validateBody(body); err != nil {
		return false, err
	}
	if createdAt <= 0 {
		createdAt = time.Now().Unix()
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	group, err := loadGroup(tx, localNpub, groupID)
	if err != nil {
		return false, err
	}
	if group.State == StateLeft {
		return false, ErrGroupLeft
	}
	if group.State != StateActive {
		return false, ErrGroupNotActive
	}
	if outgoing && senderNpub != localNpub {
		return false, ErrIdentityMismatch
	}
	if _, err := activeRosterTx(tx, localNpub, groupID, senderNpub); err != nil {
		return false, err
	}
	if _, err := activeRosterTx(tx, localNpub, groupID, localNpub); err != nil {
		return false, err
	}

	var oldSender, oldBody, oldEventID string
	err = tx.QueryRow(`SELECT sender_npub, body, event_id FROM groupchat_messages
		WHERE local_npub = ? AND group_id = ? AND logical_id = ?`, localNpub, groupID, logicalID).
		Scan(&oldSender, &oldBody, &oldEventID)
	if err == nil {
		if oldSender != senderNpub || oldBody != body {
			return false, ErrLogicalIDConflict
		}
		if eventID != "" && oldEventID == "" {
			if _, err := tx.Exec(`UPDATE groupchat_messages SET event_id = ? WHERE local_npub = ? AND group_id = ? AND logical_id = ?`,
				eventID, localNpub, groupID, logicalID); err != nil {
				return false, err
			}
		}
		if err := tx.Commit(); err != nil {
			return false, err
		}
		return false, nil
	}
	if err != sql.ErrNoRows {
		return false, err
	}
	if _, err := tx.Exec(`INSERT INTO groupchat_messages
		(local_npub, group_id, logical_id, sender_npub, body, created_at, event_id)
		VALUES (?, ?, ?, ?, ?, ?, NULLIF(?, ''))`, localNpub, groupID, logicalID,
		senderNpub, body, createdAt, eventID); err != nil {
		return false, fmt.Errorf("store group message: %w", err)
	}
	if _, err := tx.Exec(`UPDATE groupchat_groups SET updated_at = ? WHERE local_npub = ? AND group_id = ?`,
		time.Now().Unix(), localNpub, groupID); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) activeRoster(localNpub, groupID, member string) ([]string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	group, err := loadGroup(tx, localNpub, groupID)
	if err != nil {
		return nil, err
	}
	if group.State == StateLeft {
		return nil, ErrGroupLeft
	}
	if group.State != StateActive {
		return nil, ErrGroupNotActive
	}
	if _, err := activeRosterTx(tx, localNpub, groupID, member); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return group.Roster, nil
}

func activeRosterTx(tx *sql.Tx, localNpub, groupID, member string) (bool, error) {
	var state string
	err := tx.QueryRow(`SELECT state FROM groupchat_members WHERE local_npub = ? AND group_id = ? AND npub = ?`,
		localNpub, groupID, member).Scan(&state)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, ErrProtocolMismatch
		}
		return false, err
	}
	if state != string(InviteActive) {
		return false, ErrGroupNotActive
	}
	return true, nil
}

func validateBody(body string) error {
	if !validProtocolText(body) || strings.TrimSpace(body) == "" || utf8.RuneCountInString(body) > MaxBodyRunes {
		return fmt.Errorf("group message body must contain 1 to %d valid text runes", MaxBodyRunes)
	}
	return nil
}

package groupchat

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"fiatjaf.com/nostr"
	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/internal/messaging"
)

// PrepareControlFanout freezes one signed invite and writes it through the raw
// group outbox path. A repeated call returns the same event id.
func (s *Store) PrepareControlFanout(senderSK nostr.SecretKey, e Envelope, createdAt nostr.Timestamp, maxRetries int) (FanoutReport, error) {
	if senderSK == (nostr.SecretKey{}) || maxRetries <= 0 || createdAt < 0 {
		return FanoutReport{}, errors.New("invalid control fanout parameters")
	}
	if e.Type != EnvelopeInvite {
		return FanoutReport{}, ErrProtocolMismatch
	}
	plaintext, err := Encode(e)
	if err != nil {
		return FanoutReport{}, err
	}
	local := common.EncodeNpub(senderSK.Public())
	if local != e.CreatorNpub {
		return FanoutReport{}, ErrProtocolMismatch
	}
	recipient, err := canonicalNpub(e.InviteeNpub)
	if err != nil || recipient != e.InviteeNpub || recipient == local {
		return FanoutReport{}, ErrProtocolMismatch
	}
	key := FanoutKey{LocalNpub: local, GroupID: e.GroupID, EnvelopeType: e.Type, SendKey: e.InviteID}
	if err = s.freezeControlInvite(senderSK, key, e, recipient, plaintext, createdAt, maxRetries); err != nil {
		return FanoutReport{}, err
	}
	now := time.Now().Unix()
	if now < 0 {
		now = 0
	}
	return s.enqueuePreparedRaw(key, now)
}

func (s *Store) freezeControlInvite(senderSK nostr.SecretKey, key FanoutKey, e Envelope, recipient, plaintext string, createdAt nostr.Timestamp, maxRetries int) error {
	tx, err := s.beginImmediate()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var groupID, creator, invitee, hash, state string
	err = tx.QueryRow(`SELECT group_id, creator_npub, invitee_npub, roster_hash, state FROM groupchat_invites WHERE local_npub = ? AND invite_id = ?`,
		key.LocalNpub, e.InviteID).Scan(&groupID, &creator, &invitee, &hash, &state)
	if err == sql.ErrNoRows {
		return ErrInviteNotFound
	}
	if err != nil {
		return err
	}
	if groupID != e.GroupID || creator != e.CreatorNpub || invitee != recipient || hash != e.RosterHash || state != string(InvitePending) {
		return ErrProtocolMismatch
	}
	var storedRecipient, eventID, raw string
	var storedAt int64
	var budget int
	err = tx.QueryRow(`SELECT recipient_npub, event_id, event_json, created_at, max_retries FROM groupchat_fanout WHERE local_npub = ? AND group_id = ? AND envelope_type = ? AND send_key = ?`,
		key.LocalNpub, key.GroupID, key.EnvelopeType, key.SendKey).Scan(&storedRecipient, &eventID, &raw, &storedAt, &budget)
	if err == nil {
		if storedRecipient != recipient || budget != maxRetries {
			return ErrProtocolMismatch
		}
		var event nostr.Event
		if err = json.Unmarshal([]byte(raw), &event); err != nil {
			return err
		}
		if event.ID.Hex() != eventID {
			return ErrProtocolMismatch
		}
		pk, parseErr := common.ParsePublicKey(recipient)
		if parseErr != nil {
			return parseErr
		}
		if err = validatePreparedEvent(&event, senderSK, pk, plaintext, nostr.Timestamp(storedAt)); err != nil {
			return err
		}
		return tx.Commit()
	}
	if err != sql.ErrNoRows {
		return err
	}
	if createdAt == 0 {
		createdAt = nostr.Timestamp(time.Now().Unix())
	}
	pk, err := common.ParsePublicKey(recipient)
	if err != nil {
		return err
	}
	event, err := messaging.BuildAgentMessageEvent(senderSK, pk, plaintext, createdAt)
	if err != nil {
		return fmt.Errorf("build control event: %w", err)
	}
	if err = validatePreparedEvent(event, senderSK, pk, plaintext, createdAt); err != nil {
		return err
	}
	encoded, err := json.Marshal(event)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO groupchat_fanout (local_npub,group_id,envelope_type,send_key,recipient_npub,event_id,event_json,state,max_retries,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		key.LocalNpub, key.GroupID, key.EnvelopeType, key.SendKey, recipient, event.ID.Hex(), string(encoded), RecipientPrepared, maxRetries, int64(createdAt), int64(createdAt))
	if err != nil {
		return fmt.Errorf("store prepared control fanout: %w", err)
	}
	return tx.Commit()
}

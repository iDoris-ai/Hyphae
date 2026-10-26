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

// PrepareControlFanout freezes one signed control envelope (invite, accept or
// activate) and writes it through the raw group outbox path. The sender is the
// creator for invite and activate, the invitee for accept. A repeated call
// returns the same event id.
func (s *Store) PrepareControlFanout(senderSK nostr.SecretKey, e Envelope, createdAt nostr.Timestamp, maxRetries int) (FanoutReport, error) {
	if senderSK == (nostr.SecretKey{}) || maxRetries <= 0 || createdAt < 0 {
		return FanoutReport{}, errors.New("invalid control fanout parameters")
	}
	local := common.EncodeNpub(senderSK.Public())
	sender, recipient, ok := controlParties(e)
	if !ok || local != sender {
		return FanoutReport{}, ErrProtocolMismatch
	}
	plaintext, err := Encode(e)
	if err != nil {
		return FanoutReport{}, err
	}
	canonical, err := canonicalNpub(recipient)
	if err != nil || canonical != recipient || recipient == local {
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

// controlParties names who signs and who receives a control envelope.
func controlParties(e Envelope) (sender, recipient string, ok bool) {
	switch e.Type {
	case EnvelopeInvite, EnvelopeActivate:
		return e.CreatorNpub, e.InviteeNpub, true
	case EnvelopeAccept:
		return e.InviteeNpub, e.CreatorNpub, true
	}
	return "", "", false
}

// controlStateAllowed reports whether the local invite row may still send e.
func controlStateAllowed(t EnvelopeType, state string) bool {
	switch t {
	case EnvelopeInvite:
		return state == string(InvitePending)
	case EnvelopeAccept, EnvelopeActivate:
		return state == string(InviteAccepted) || state == string(InviteActive)
	}
	return false
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
	if groupID != e.GroupID || creator != e.CreatorNpub || invitee != e.InviteeNpub || hash != e.RosterHash || !controlStateAllowed(e.Type, state) {
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

// QueueActivations durably queues each creator activation for the relay and
// records it as queued. An invite already activated by an in-process delivery
// (empty activation event id) is skipped. Replays re-queue the same frozen
// event, so a retry after a crash does not duplicate anything.
func (s *Store) QueueActivations(creatorSK nostr.SecretKey, activations []Envelope) error {
	creator := common.EncodeNpub(creatorSK.Public())
	for _, activation := range activations {
		var state, activationEvent string
		err := s.db.QueryRow(`SELECT state, activation_event_id FROM groupchat_invites WHERE local_npub = ? AND invite_id = ?`,
			creator, activation.InviteID).Scan(&state, &activationEvent)
		if err == sql.ErrNoRows {
			return ErrInviteNotFound
		}
		if err != nil {
			return err
		}
		if state == string(InviteActive) && activationEvent == "" {
			continue
		}
		report, err := s.PrepareControlFanout(creatorSK, activation, 0, controlFanoutRetries)
		if err != nil {
			return fmt.Errorf("queue activation for %s: %w", activation.InviteeNpub, err)
		}
		if len(report.Recipients) != 1 {
			return fmt.Errorf("queue activation for %s: %d recipients", activation.InviteeNpub, len(report.Recipients))
		}
		if err := s.MarkActivationQueued(creator, activation.GroupID, activation.InviteID, report.Recipients[0].EventID, true); err != nil {
			return err
		}
	}
	return nil
}

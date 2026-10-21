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
	return s.enqueueControlPrepared(key)
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

func (s *Store) enqueueControlPrepared(key FanoutKey) (FanoutReport, error) {
	now := time.Now().Unix()
	if now < 0 {
		now = 0
	}
	local, err := canonicalNpub(key.LocalNpub)
	if err != nil {
		return FanoutReport{}, err
	}
	key.LocalNpub = local
	err = messaging.WithGroupOutboxLock(func(path string) error {
		tx, err := s.beginImmediate()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		rows, err := tx.Query(`SELECT recipient_npub, event_id, event_json, max_retries,
			accounting_origin = 'native' AND (attempts <> 0 OR COALESCE(retry_count, 0) <> 0)
			FROM groupchat_fanout
			WHERE local_npub = ? AND group_id = ? AND envelope_type = ? AND send_key = ? AND state = ?
			ORDER BY recipient_npub`,
			local, key.GroupID, key.EnvelopeType, key.SendKey, RecipientPrepared)
		if err != nil {
			return err
		}
		type preparedTarget struct {
			recipient, eventID, eventJSON string
			maxRetries                    int
			seeded                        bool
		}
		var targets []preparedTarget
		for rows.Next() {
			var item preparedTarget
			if err = rows.Scan(&item.recipient, &item.eventID, &item.eventJSON, &item.maxRetries, &item.seeded); err != nil {
				rows.Close()
				return err
			}
			targets = append(targets, item)
		}
		if err = rows.Err(); err != nil {
			rows.Close()
			return err
		}
		if err = rows.Close(); err != nil {
			return err
		}
		queues := make([]string, len(targets))
		for i, item := range targets {
			rec := messaging.GroupEnqueueRecord{
				EventJSON: item.eventJSON, RecipientNpub: item.recipient, MaxRetries: int64(item.maxRetries), CreatedAt: now,
			}
			queueID, existed, checkErr := messaging.CheckGroupEnqueueLocked(path, rec)
			if checkErr != nil {
				return fmt.Errorf("O1 precheck for %s: %w", item.recipient, checkErr)
			}
			if item.seeded && !existed {
				return fmt.Errorf("%w: %s", errPreparedCountersWithoutEpoch, item.recipient)
			}
			if existed {
				queues[i] = queueID
			}
		}
		for i, item := range targets {
			rec := messaging.GroupEnqueueRecord{
				EventJSON: item.eventJSON, RecipientNpub: item.recipient, MaxRetries: int64(item.maxRetries), CreatedAt: now,
			}
			queueID, existed, commitErr := messaging.CommitGroupEnqueueLocked(path, rec)
			if commitErr != nil {
				return fmt.Errorf("O1 for %s: %w", item.recipient, commitErr)
			}
			if queues[i] != "" && (!existed || queueID != queues[i]) {
				return fmt.Errorf("O1 adoption queue_id mismatch for %s: expected %s, got %s (existed=%v)", item.recipient, queues[i], queueID, existed)
			}
			entry, _, readErr := messaging.ReadGroupEntryLocked(path, item.eventID)
			if readErr != nil {
				return fmt.Errorf("read O1 entry for %s: %w", item.recipient, readErr)
			}
			if entry == nil || entry.QueueID != queueID {
				return fmt.Errorf("O1 entry missing or queue_id mismatch for %s", item.recipient)
			}
			changed, transErr := transitionFanoutTx(tx, key, item.recipient, RecipientPrepared, RecipientQueued, map[string]any{"queue_id": queueID})
			if transErr != nil {
				return transErr
			}
			if !changed {
				return fmt.Errorf("T2 did not apply for %s", item.recipient)
			}
			if _, err = tx.Exec(`UPDATE groupchat_fanout SET retry_count = ?, retry_queue_id = queue_id
				WHERE local_npub = ? AND group_id = ? AND envelope_type = ? AND send_key = ? AND recipient_npub = ? AND state = ? AND queue_id = ?`,
				int64(entry.Entry.RetryCount), local, key.GroupID, key.EnvelopeType, key.SendKey, item.recipient, RecipientQueued, queueID); err != nil {
				return err
			}
		}
		if len(targets) > 0 {
			if err = confirmOutboxDurable(path); err != nil {
				return fmt.Errorf("%w: O1 durability unconfirmed before T2: %w", errFanoutEnqueuePending, err)
			}
		}
		return tx.Commit()
	})
	if err != nil {
		return FanoutReport{}, err
	}
	return s.LoadFanoutReport(key)
}

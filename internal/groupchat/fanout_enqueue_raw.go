package groupchat

import (
	"database/sql"
	"fmt"

	"github.com/iDoris-ai/hyphae/internal/messaging"
)

// enqueuePreparedRaw is O1 + T2 for every prepared recipient of key under
// the rev7 lock order (outbox lock → BEGIN IMMEDIATE → O1 raw append → T2 CAS
// → commit). It is the raw implementation behind EnqueuePrepared (wiring
// plan W-3c).
//
// O1 is idempotent (an active pending entry for the same frozen event and
// recipient is reused), so a crash between O1 and T2 is healed by re-running
// this function. T2 also initialises the queue's budget snapshot
// (retry_count 0, retry_queue_id) for the new epoch.
func (s *Store) enqueuePreparedRaw(key FanoutKey, now int64) (FanoutReport, error) {
	local, err := canonicalNpub(key.LocalNpub)
	if err != nil {
		return FanoutReport{}, err
	}
	key.LocalNpub = local
	if now < 0 {
		return FanoutReport{}, fmt.Errorf("enqueue time must be non-negative")
	}
	err = messaging.WithGroupOutboxLock(func(path string) error {
		tx, err := s.beginImmediate()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		rows, err := tx.Query(`SELECT recipient_npub, event_id, event_json, max_retries FROM groupchat_fanout
            WHERE local_npub = ? AND group_id = ? AND envelope_type = ? AND send_key = ? AND state = ? ORDER BY recipient_npub`,
			local, key.GroupID, key.EnvelopeType, key.SendKey, RecipientPrepared)
		if err != nil {
			return err
		}
		var targets []fanoutQueueTarget
		for rows.Next() {
			var r fanoutQueueTarget
			if err := rows.Scan(&r.recipient, &r.eventID, &r.eventJSON, &r.maxRetries); err != nil {
				rows.Close()
				return err
			}
			targets = append(targets, r)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		for _, target := range targets {
			queueID, _, err := messaging.CommitGroupEnqueueLocked(path, messaging.GroupEnqueueRecord{
				EventJSON: target.eventJSON, RecipientNpub: target.recipient, MaxRetries: int64(target.maxRetries), CreatedAt: now,
			})
			if err != nil {
				return fmt.Errorf("O1 for %s: %w", target.recipient, err)
			}
			changed, err := transitionFanoutTx(tx, key, target.recipient, RecipientPrepared, RecipientQueued, map[string]any{"queue_id": queueID})
			if err != nil {
				return err
			}
			if !changed {
				return fmt.Errorf("T2 did not apply for %s", target.recipient)
			}
			if _, err := tx.Exec(`UPDATE groupchat_fanout SET retry_count = COALESCE(retry_count, 0), retry_queue_id = queue_id
                WHERE local_npub = ? AND group_id = ? AND envelope_type = ? AND send_key = ? AND recipient_npub = ? AND state = ? AND queue_id = ?`,
				local, key.GroupID, key.EnvelopeType, key.SendKey, target.recipient, RecipientQueued, queueID); err != nil {
				return err
			}
			if err := onFanoutQueuedTx(tx, key, fanoutQueuedRow{RecipientNpub: target.recipient, EventID: target.eventID, QueueID: queueID}); err != nil {
				return err
			}
		}
		return tx.Commit()
	})
	if err != nil {
		return FanoutReport{}, err
	}
	return s.LoadFanoutReport(key)
}

// requeueFailedRaw is D6 + T2' for every matching failed recipient of key
// under the rev7 lock order. It authorizes recovery for each failed
// recipient via authorizeFanoutRecovery, replacing the typed queueTargets
// requeue path (wiring plan W-3c).
func (s *Store) requeueFailedRaw(key FanoutKey, recipient string, now int64) (FanoutReport, error) {
	local, err := canonicalNpub(key.LocalNpub)
	if err != nil {
		return FanoutReport{}, err
	}
	key.LocalNpub = local
	if now < 0 {
		return FanoutReport{}, fmt.Errorf("requeue time must be non-negative")
	}
	if recipient != "" {
		if c, err := canonicalNpub(recipient); err == nil {
			recipient = c
		}
	}

	query := `SELECT recipient_npub, event_id, event_json, state, max_retries FROM groupchat_fanout
		WHERE local_npub = ? AND group_id = ? AND envelope_type = ? AND send_key = ?`
	args := []any{local, key.GroupID, key.EnvelopeType, key.SendKey}
	if recipient != "" {
		query += " AND recipient_npub = ?"
		args = append(args, recipient)
	}
	rows, err := s.db.Query(query+" ORDER BY recipient_npub", args...)
	if err != nil {
		return FanoutReport{}, err
	}
	defer rows.Close()

	var targets []fanoutQueueTarget
	matched := 0
	for rows.Next() {
		matched++
		var r fanoutQueueTarget
		var state RecipientDeliveryState
		if err := rows.Scan(&r.recipient, &r.eventID, &r.eventJSON, &state, &r.maxRetries); err != nil {
			return FanoutReport{}, err
		}
		if state != RecipientFailed {
			if recipient != "" {
				return FanoutReport{}, fmt.Errorf("cannot requeue recipient in %s state: only failed recipients can be requeued", state)
			}
			continue
		}
		targets = append(targets, r)
	}
	if err := rows.Err(); err != nil {
		return FanoutReport{}, err
	}
	if matched == 0 {
		return FanoutReport{}, sql.ErrNoRows
	}
	if len(targets) == 0 {
		return FanoutReport{}, fmt.Errorf("no failed recipients to requeue")
	}
	for _, target := range targets {
		if _, err := s.authorizeFanoutRecovery(key, target.recipient, now); err != nil {
			return FanoutReport{}, err
		}
	}
	return s.LoadFanoutReport(key)
}

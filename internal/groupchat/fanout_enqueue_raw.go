package groupchat

import (
	"errors"
	"fmt"

	"github.com/iDoris-ai/hyphae/internal/messaging"
)

// preparedRowIter is the database/sql rows contract used while collecting
// prepared recipients. Tests wrap it to inject a Next failure after the first
// real row (Close alone does not surface iteration errors).
type preparedRowIter interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
	Close() error
}

// openPreparedFanoutRows opens the prepared-recipient query. Production uses
// *sql.Rows directly; tests may wrap the iterator.
var openPreparedFanoutRows = func(tx *immediateTx, query string, args ...any) (preparedRowIter, error) {
	return tx.Query(query, args...)
}

// errPreparedCountersWithoutEpoch: a native prepared row already carries
// attempts or retry_count but no outbox entry exists to adopt them from.
var errPreparedCountersWithoutEpoch = errors.New("native prepared row has counters but no outbox entry to adopt")

// enqueuePreparedRaw is O1 + T2 for every prepared recipient of key under
// the rev7 lock order (outbox lock → BEGIN IMMEDIATE → O1 raw append → T2 CAS
// → commit). It is the raw alternative to EnqueuePrepared (wiring plan
// W-3b) and is inert until that switch is reviewed: existing callers still
// use the typed queueTargets path.
//
// O1 is idempotent (an active pending entry for the same frozen event and
// recipient is reused), so a crash between O1 and T2 is healed by re-running
// this function. T2 persists the adopted (or fresh) outbox entry's
// retry_count into the queue budget snapshot with retry_queue_id.
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
		rows, err := openPreparedFanoutRows(tx, `SELECT recipient_npub, event_id, event_json, max_retries,
            accounting_origin = 'native' AND (attempts <> 0 OR COALESCE(retry_count, 0) <> 0) FROM groupchat_fanout
            WHERE local_npub = ? AND group_id = ? AND envelope_type = ? AND send_key = ? AND state = ? ORDER BY recipient_npub`,
			local, key.GroupID, key.EnvelopeType, key.SendKey, RecipientPrepared)
		if err != nil {
			return err
		}
		var targets, seeded []fanoutQueueTarget
		for rows.Next() {
			var r fanoutQueueTarget
			var nativeCounters bool
			if err := rows.Scan(&r.recipient, &r.eventID, &r.eventJSON, &r.maxRetries, &nativeCounters); err != nil {
				rows.Close()
				return err
			}
			if nativeCounters {
				seeded = append(seeded, r)
			}
			targets = append(targets, r)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
		// A native row with nonzero counters is not fresh: without an outbox
		// epoch to adopt (O1 still decides compatibility) refuse before any O1.
		for _, target := range seeded {
			entry, _, err := messaging.ReadGroupEntryLocked(path, target.eventID)
			if err != nil {
				return err
			}
			if entry == nil {
				return fmt.Errorf("%w: %s", errPreparedCountersWithoutEpoch, target.recipient)
			}
		}
		for _, target := range targets {
			queueID, _, err := messaging.CommitGroupEnqueueLocked(path, messaging.GroupEnqueueRecord{
				EventJSON: target.eventJSON, RecipientNpub: target.recipient, MaxRetries: int64(target.maxRetries), CreatedAt: now,
			})
			if err != nil {
				return fmt.Errorf("O1 for %s: %w", target.recipient, err)
			}
			entry, _, err := messaging.ReadGroupEntryLocked(path, target.eventID)
			if err != nil {
				return fmt.Errorf("read O1 entry for %s: %w", target.recipient, err)
			}
			if entry == nil || entry.QueueID != queueID {
				return fmt.Errorf("O1 entry missing or queue_id mismatch for %s", target.recipient)
			}
			adoptedRetry := int64(entry.Entry.RetryCount)
			changed, err := transitionFanoutTx(tx, key, target.recipient, RecipientPrepared, RecipientQueued, map[string]any{"queue_id": queueID})
			if err != nil {
				return err
			}
			if !changed {
				return fmt.Errorf("T2 did not apply for %s", target.recipient)
			}
			if _, err := tx.Exec(`UPDATE groupchat_fanout SET retry_count = ?, retry_queue_id = queue_id
                WHERE local_npub = ? AND group_id = ? AND envelope_type = ? AND send_key = ? AND recipient_npub = ? AND state = ? AND queue_id = ?`,
				adoptedRetry, local, key.GroupID, key.EnvelopeType, key.SendKey, target.recipient, RecipientQueued, queueID); err != nil {
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

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

var errFanoutEnqueuePending = errors.New("fanout enqueue durability pending")

// enqueuePreparedRaw is O1 + T2 for every prepared recipient of key under
// the rev7 lock order (outbox lock → BEGIN IMMEDIATE → O1 raw append → T2 CAS
// → commit). It is the raw alternative to EnqueuePrepared (wiring plan
// W-3b) and is inert until that switch is reviewed: existing callers still
// use the typed queueTargets path.
//
// Refusals follow semantics (a): any deterministic O1 refusal holds the
// whole key with zero outbox writes and zero SQLite writes. The durability
// gate before T2 commit confirms outbox directory fsync; failures roll back
// leaving rows prepared to heal on rerun.
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
		var targets []fanoutQueueTarget
		var seeded []bool
		for rows.Next() {
			var r fanoutQueueTarget
			var nativeCounters bool
			if err := rows.Scan(&r.recipient, &r.eventID, &r.eventJSON, &r.maxRetries, &nativeCounters); err != nil {
				rows.Close()
				return err
			}
			targets = append(targets, r)
			seeded = append(seeded, nativeCounters)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
		precheckedQueues := make([]string, len(targets))
		for i, target := range targets {
			rec := messaging.GroupEnqueueRecord{
				EventJSON: target.eventJSON, RecipientNpub: target.recipient, MaxRetries: int64(target.maxRetries), CreatedAt: now,
			}
			q, existed, err := messaging.CheckGroupEnqueueLocked(path, rec)
			if err != nil {
				return fmt.Errorf("O1 precheck for %s: %w", target.recipient, err)
			}
			if seeded[i] && !existed {
				return fmt.Errorf("%w: %s", errPreparedCountersWithoutEpoch, target.recipient)
			}
			if existed {
				precheckedQueues[i] = q
			}
		}
		for i, target := range targets {
			rec := messaging.GroupEnqueueRecord{
				EventJSON: target.eventJSON, RecipientNpub: target.recipient, MaxRetries: int64(target.maxRetries), CreatedAt: now,
			}
			queueID, existed, err := messaging.CommitGroupEnqueueLocked(path, rec)
			if err != nil {
				return fmt.Errorf("O1 for %s: %w", target.recipient, err)
			}
			if precheckedQueues[i] != "" && (!existed || queueID != precheckedQueues[i]) {
				return fmt.Errorf("O1 adoption queue_id mismatch for %s: expected %s, got %s (existed=%v)", target.recipient, precheckedQueues[i], queueID, existed)
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
		if len(targets) > 0 {
			if err := confirmOutboxDurable(path); err != nil {
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

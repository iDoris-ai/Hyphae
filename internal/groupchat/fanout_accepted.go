package groupchat

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/iDoris-ai/hyphae/internal/messaging"
)

// ErrFanoutCleanupPending: T3 is committed (the relay acceptance is durable)
// but the precise O2 did not run; only diagnostics, never a retry of T3.
var ErrFanoutCleanupPending = errors.New("fanout relay acceptance is durable; outbox cleanup pending")

// commitFanoutAccepted is the F3 success boundary for the current started
// token (rev7 §3.3 "成功"): outbox lock → BEGIN IMMEDIATE → read-only token
// check and D14 → T3 (terminal relay_accepted, relay_acks=1,
// accepted_at=max(now,last_attempt_at), issue cleared, result watermark) →
// commit → precise O2 under the same outbox lock. A stale token writes
// nothing. Inert: no production caller.
func (s *Store) commitFanoutAccepted(token fanoutAttemptToken, now int64) error {
	if now < 0 {
		return fmt.Errorf("fanout acceptance time must be non-negative")
	}
	return messaging.WithGroupOutboxLock(func(path string) error {
		tx, err := s.beginImmediate()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		row, err := loadFanoutFailureRow(tx, token.key, token.recipient)
		if err != nil {
			return err
		}
		if row.token != token || RecipientDeliveryState(row.state) != RecipientQueued || row.phase != "started" || row.lastResultGeneration >= token.generation {
			return errFanoutStaleAttempt
		}
		entry, receipt, err := messaging.ReadGroupEntryLocked(path, token.eventID)
		if err != nil {
			return err
		}
		if entry != nil && entry.QueueID != token.queueID {
			return ErrFanoutQueueConflict
		}
		if receipt != "" && receipt != row.receipt {
			return fmt.Errorf("%w: unconsumed failure record for the accepted attempt", ErrFanoutBookkeepingConflict)
		}
		acceptedAt := max(now, row.lastAttemptAt, 1)
		t := token
		updated, err := tx.Exec(`UPDATE groupchat_fanout SET state=?,attempt_phase='accepted',relay_acks=1,accepted_at=?,issue='',
            last_result_generation=?,row_revision=row_revision+1,updated_at=MAX(updated_at,?)
            WHERE local_npub=? AND group_id=? AND envelope_type=? AND send_key=? AND recipient_npub=? AND event_id=? AND queue_id=? AND attempt_generation=? AND attempt_phase='started' AND state=? AND row_revision=?`,
			RecipientRelayAccepted, acceptedAt, t.generation, time.Now().Unix(),
			t.key.LocalNpub, t.key.GroupID, t.key.EnvelopeType, t.key.SendKey, t.recipient, t.eventID, t.queueID, t.generation, RecipientQueued, row.revision)
		if err != nil {
			return fmt.Errorf("T3: %w", err)
		}
		if n, err := updated.RowsAffected(); err != nil || n != 1 {
			return fmt.Errorf("T3 CAS affected %d rows (%v)", n, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("T3 commit: %w", err)
		}
		return s.cleanupAcceptedLocked(path, row)
	})
}

// cleanupAcceptedLocked is the precise O2 for an accepted row, run under the
// outbox lock. O2 problems are reported as ErrFanoutCleanupPending; T3 stays.
func (s *Store) cleanupAcceptedLocked(path string, row fanoutFailureRow) error {
	_, lastDigest, _ := strings.Cut(row.receipt, ":")
	err := messaging.CommitGroupCleanupLocked(path, messaging.GroupCleanupRecord{
		EventID: row.token.eventID, QueueID: row.token.queueID, FrozenEventJSON: row.eventJSON, MaxRetries: row.maxRetries,
		LastResultDigest: lastDigest, LastRecoveryDigest: row.recoveryDigest,
	})
	if err != nil {
		return fmt.Errorf("%w: %v", ErrFanoutCleanupPending, err)
	}
	return nil
}

// cleanupAcceptedFanout retries the precise O2 for a row that is already
// relay_accepted (ErrFanoutCleanupPending follow-up). A row in any other
// state is left alone.
func (s *Store) cleanupAcceptedFanout(key FanoutKey, recipient string) error {
	return messaging.WithGroupOutboxLock(func(path string) error {
		tx, err := s.beginImmediate()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		row, err := loadFanoutFailureRow(tx, key, recipient)
		if err != nil {
			return err
		}
		if err := tx.Rollback(); err != nil {
			return err
		}
		if RecipientDeliveryState(row.state) != RecipientRelayAccepted {
			return fmt.Errorf("fanout row is %s, not relay_accepted; nothing to clean up", row.state)
		}
		return s.cleanupAcceptedLocked(path, row)
	})
}

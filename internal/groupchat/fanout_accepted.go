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

// ErrFanoutAcceptedEvidenceCorrupt: a row claims relay_accepted but its
// durable ACK evidence (persisted QueueID, relay_acks, accepted_at) does not
// satisfy the rev7 §3.5 O2 conditions. Cleanup fails closed: both stores are
// held unchanged and nothing is repaired. This takes priority over
// ErrFanoutCleanupPending.
var ErrFanoutAcceptedEvidenceCorrupt = errors.New("fanout relay_accepted row has corrupt or missing durable ACK evidence")

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
// state is left alone. The persisted QueueID and ACK fields are loaded in the
// same SQLite snapshot as the row and must carry durable T3 evidence before
// O2 is authorized (rev7 §3.5): queue_id non-empty, relay_acks=1,
// accepted_at>0. attempt_phase is deliberately not required to be
// "accepted": legacy relay_accepted rows (state-CAS acceptance, F1
// migration) carry the same ACK evidence under other phases.
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
		var relayAcks, acceptedAt int64
		if err := tx.QueryRow(`SELECT relay_acks,accepted_at FROM groupchat_fanout
            WHERE local_npub=? AND group_id=? AND envelope_type=? AND send_key=? AND recipient_npub=? AND event_id=? AND queue_id=?`,
			row.token.key.LocalNpub, key.GroupID, key.EnvelopeType, key.SendKey, recipient, row.token.eventID, row.token.queueID).Scan(&relayAcks, &acceptedAt); err != nil {
			return fmt.Errorf("%w: reload ACK fields: %v", ErrFanoutAcceptedEvidenceCorrupt, err)
		}
		if err := tx.Rollback(); err != nil {
			return err
		}
		if RecipientDeliveryState(row.state) != RecipientRelayAccepted {
			return fmt.Errorf("fanout row is %s, not relay_accepted; nothing to clean up", row.state)
		}
		if err := validateAcceptedEvidence(row.token.queueID, relayAcks, acceptedAt); err != nil {
			return err
		}
		return s.cleanupAcceptedLocked(path, row)
	})
}

// validateAcceptedEvidence checks exactly the rev7 §3.5 O2 ACK conditions for
// a relay_accepted row; it never repairs a failing row.
func validateAcceptedEvidence(queueID string, relayAcks, acceptedAt int64) error {
	switch {
	case queueID == "":
		return fmt.Errorf("%w: persisted queue_id is empty", ErrFanoutAcceptedEvidenceCorrupt)
	case relayAcks != 1:
		return fmt.Errorf("%w: relay_acks=%d, want 1", ErrFanoutAcceptedEvidenceCorrupt, relayAcks)
	case acceptedAt <= 0:
		return fmt.Errorf("%w: accepted_at=%d, want >0", ErrFanoutAcceptedEvidenceCorrupt, acceptedAt)
	}
	return nil
}

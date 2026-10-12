package groupchat

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/iDoris-ai/hyphae/internal/messaging"
)

// ErrFanoutRecoveryPending: D6 is durable in the outbox but T2' is not (or
// the D6 commit is uncertain). The new queue entry carries an unconsumed
// recovery_witness; no reservation or publish may proceed until the barrier
// replays T2' (rev7 §3.4).
var ErrFanoutRecoveryPending = errors.New("fanout recovery bookkeeping is pending T2'")

// authorizeFanoutRecovery is D6 + T2' for one explicitly named failed
// recipient (rev7 §3.4, schema supplement §4): outbox lock → BEGIN
// IMMEDIATE → the row must be failed with no unconsumed record → witness
// (reason missing / exhausted_pending / exhausted_failed from the locked
// outbox observation) → D6 JSON commit → T2' CAS (queued, new queue_id,
// retry 0, issue cleared, phase idle, recovery id/digest) with the
// activation hook in the same transaction → commit. Returns the new QueueID.
func (s *Store) authorizeFanoutRecovery(key FanoutKey, recipient string, authorizedAt int64) (newQueueID string, err error) {
	if authorizedAt < 0 {
		return "", fmt.Errorf("fanout recovery authorization time must be non-negative")
	}
	err = messaging.WithGroupOutboxLock(func(path string) error {
		tx, err := s.beginImmediate()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		row, err := loadFanoutFailureRow(tx, key, recipient)
		if err != nil {
			return err
		}
		if RecipientDeliveryState(row.state) != RecipientFailed {
			return fmt.Errorf("fanout recovery requires a failed row; recipient is %s", row.state)
		}
		entry, receipt, err := messaging.ReadGroupEntryLocked(path, row.token.eventID)
		if err != nil {
			return err
		}
		if receipt != "" && receipt != row.receipt {
			return fmt.Errorf("%w: unconsumed failure record on the failed row", ErrFanoutBookkeepingConflict)
		}
		if entry != nil && entry.QueueID != row.token.queueID {
			// A previous D6 landed without its T2': replay it instead of
			// authorizing a second epoch, and report that epoch's queue.
			if entry.RecoveryWitness == nil {
				return ErrFanoutQueueConflict
			}
			if err := replayFanoutRecoveryTx(tx, &row, entry); err != nil {
				return err
			}
			if err := tx.Commit(); err != nil {
				return err
			}
			newQueueID = entry.QueueID
			return nil
		}
		witness, err := row.recoveryWitness(entry, authorizedAt)
		if err != nil {
			return err
		}
		// missing: no old entry exists, so there are no records to attest and
		// the frozen event comes from the row; exhausted: the old entry's
		// consumed records are attested by the row's receipt/digest.
		rec := messaging.GroupRecoveryRecord{Witness: witness}
		if witness.Reason == "missing" {
			rec.FrozenEventJSON = row.eventJSON
		} else {
			_, rec.LastResultDigest, _ = strings.Cut(row.receipt, ":")
			rec.LastRecoveryDigest = row.recoveryDigest
		}
		digest, err := commitGroupRecoveryLocked(path, rec)
		if messaging.IsOutboxCommitUncertain(err) {
			if digest, err = confirmFanoutRecoveryCommit(path, row, witness); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		// D6 is durable from here; a failure leaves the witness unconsumed
		// for the barrier, never a second D6.
		fanoutCrashGate("d6")
		if err := projectFanoutRecoveryTx(tx, row, witness, digest); err != nil {
			return fmt.Errorf("%w: %v", ErrFanoutRecoveryPending, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("%w: T2' commit: %v", ErrFanoutRecoveryPending, err)
		}
		newQueueID = witness.NewQueueID
		return nil
	})
	return newQueueID, err
}

// recoveryWitness derives the D6 witness from the failed row and the locked
// outbox observation of its old entry (nil = observed absent).
func (r *fanoutFailureRow) recoveryWitness(old *messaging.RawGroupEntry, authorizedAt int64) (messaging.RawRecoveryWitness, error) {
	recoveryID, err := NewOpaqueID()
	if err != nil {
		return messaging.RawRecoveryWitness{}, err
	}
	newQueueID, err := NewOpaqueID()
	if err != nil {
		return messaging.RawRecoveryWitness{}, err
	}
	t := r.token
	w := messaging.RawRecoveryWitness{
		Version: 1, RecoveryID: recoveryID,
		FanoutKey: messaging.OutboxFanoutKey{
			LocalNpub: t.key.LocalNpub, RecipientNpub: t.recipient,
			GroupID: t.key.GroupID, SendKey: t.key.SendKey, EnvelopeType: string(t.key.EnvelopeType),
		},
		EventID: t.eventID, EventJSONDigest: messaging.OutboxEventDigest(r.eventJSON), RecipientNpub: t.recipient,
		MaxRetries: r.maxRetries, ExpectedRowRevision: r.revision, ExpectedAttemptGeneration: t.generation,
		OldQueueID: t.queueID, NewQueueID: newQueueID, AuthorizedAt: authorizedAt,
	}
	switch {
	case old == nil:
		w.Reason, w.ObservedAbsent = "missing", true
	case old.Entry.Status == messaging.OutboxStatusGroupFailed:
		w.Reason = "exhausted_failed"
	case old.Entry.Status == messaging.OutboxStatusGroupPending && int64(old.Entry.RetryCount) >= r.maxRetries:
		w.Reason = "exhausted_pending"
	default:
		return w, fmt.Errorf("%w: failed row but outbox entry is active pending %d/%d", ErrFanoutBookkeepingConflict, old.Entry.RetryCount, r.maxRetries)
	}
	if old != nil {
		w.OldEntryDigest, w.OldStatus, w.OldRetryCount = messaging.OutboxEntryDigest(old), old.Entry.Status, int64(old.Entry.RetryCount)
	}
	return w, nil
}

// projectFanoutRecoveryTx is T2': failed → queued under the new epoch,
// preserving attempts, generation, last_attempt_at, relay_count and the
// failure watermark/receipt, with the queued activation hook in the same
// transaction.
func projectFanoutRecoveryTx(tx queryExecer, row fanoutFailureRow, w messaging.RawRecoveryWitness, digest string) error {
	t := row.token
	updated, err := tx.Exec(`UPDATE groupchat_fanout SET state=?,queue_id=?,retry_count=0,retry_queue_id=?,issue='',attempt_phase='idle',
        last_recovery_id=?,last_recovery_digest=?,row_revision=row_revision+1,updated_at=MAX(updated_at,?)
        WHERE local_npub=? AND group_id=? AND envelope_type=? AND send_key=? AND recipient_npub=? AND event_id=? AND queue_id=? AND state=? AND row_revision=? AND attempt_generation=?`,
		RecipientQueued, w.NewQueueID, w.NewQueueID, w.RecoveryID, digest, time.Now().Unix(),
		t.key.LocalNpub, t.key.GroupID, t.key.EnvelopeType, t.key.SendKey, t.recipient, t.eventID, w.OldQueueID, RecipientFailed, w.ExpectedRowRevision, w.ExpectedAttemptGeneration)
	if err != nil {
		return fmt.Errorf("T2': %w", err)
	}
	if n, err := updated.RowsAffected(); err != nil || n != 1 {
		return fmt.Errorf("T2' CAS affected %d rows (%v)", n, err)
	}
	return onFanoutQueuedTx(tx, t.key, fanoutQueuedRow{RecipientNpub: t.recipient, EventID: t.eventID, QueueID: w.NewQueueID})
}

// confirmFanoutRecoveryCommit resolves a commit-uncertain D6 write while the
// locks are held and returns the witness digest to project.
func confirmFanoutRecoveryCommit(path string, row fanoutFailureRow, w messaging.RawRecoveryWitness) (string, error) {
	if err := messaging.ConfirmOutboxDurable(path); err != nil {
		return "", fmt.Errorf("%w: outbox directory durability unconfirmed: %v", ErrFanoutRecoveryPending, err)
	}
	entry, _, err := messaging.ReadGroupEntryLocked(path, row.token.eventID)
	if err != nil {
		return "", fmt.Errorf("%w: reread after uncertain commit: %v", ErrFanoutRecoveryPending, err)
	}
	switch {
	case entry != nil && entry.QueueID == w.NewQueueID && entry.RecoveryWitness != nil && entry.RecoveryWitness.RecoveryID == w.RecoveryID:
		return messaging.OutboxWitnessDigest(entry), nil
	case (entry == nil && w.Reason == "missing") || (entry != nil && entry.QueueID == w.OldQueueID):
		return "", fmt.Errorf("%w: old snapshot survived; D6 has no durable evidence", ErrFanoutRecoveryPending)
	default:
		return "", fmt.Errorf("%w: outbox after uncertain D6 commit is neither the old nor the new epoch", ErrFanoutBookkeepingConflict)
	}
}

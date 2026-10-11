package groupchat

import (
	"fmt"

	"github.com/iDoris-ai/hyphae/internal/messaging"
)

// recoverFanoutRecovery is the recovery half of the barrier for a D6 that
// was committed to the outbox but whose T2' never landed (rev7 §3.4): under
// outbox lock + BEGIN IMMEDIATE it replays T2' exactly once for a witness
// that is an exact replay of the failed row's pre-image; a witness already
// recorded as last_recovery_id/digest is a no-op. replayed reports whether
// T2' was written. Inert: no production caller.
func (s *Store) recoverFanoutRecovery(key FanoutKey, recipient string) (replayed bool, err error) {
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
		entry, _, err := messaging.ReadGroupEntryLocked(path, row.token.eventID)
		if err != nil || entry == nil || entry.QueueID == row.token.queueID || entry.RecoveryWitness == nil {
			return err
		}
		if err := replayFanoutRecoveryTx(tx, &row, entry); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		replayed = true
		return nil
	})
	return replayed, err
}

// replayFanoutRecoveryTx projects an unconsumed D6 witness into the failed
// row when the witness and its new entry are an exact replay of the row's
// pre-image; otherwise it returns ErrFanoutBookkeepingConflict and writes
// nothing. row is updated to the post-image on success.
func replayFanoutRecoveryTx(tx queryExecer, row *fanoutFailureRow, entry *messaging.RawGroupEntry) error {
	w := entry.RecoveryWitness
	if w == nil || !matchesRecoveryWitness(entry, row) {
		return fmt.Errorf("%w: outbox epoch %q is not an exact D6 replay for the failed row", ErrFanoutBookkeepingConflict, entry.QueueID)
	}
	digest := messaging.OutboxWitnessDigest(entry)
	if err := projectFanoutRecoveryTx(tx, *row, *w, digest); err != nil {
		return err
	}
	row.revision++
	row.state, row.phase, row.token.queueID = string(RecipientQueued), "idle", w.NewQueueID
	row.recoveryDigest = digest
	row.retryCount.Int64, row.retryCount.Valid = 0, true
	return nil
}

// matchesRecoveryWitness checks the witness and new entry against the failed
// row: key, event and frozen digest, recipient, max_retries, expected
// revision and generation, old queue, and the §4 new-epoch shape.
func matchesRecoveryWitness(entry *messaging.RawGroupEntry, row *fanoutFailureRow) bool {
	w, t := entry.RecoveryWitness, row.token
	key := messaging.OutboxFanoutKey{
		LocalNpub: t.key.LocalNpub, RecipientNpub: t.recipient,
		GroupID: t.key.GroupID, SendKey: t.key.SendKey, EnvelopeType: string(t.key.EnvelopeType),
	}
	return RecipientDeliveryState(row.state) == RecipientFailed &&
		w.FanoutKey == key && w.EventID == t.eventID && w.EventJSONDigest == messaging.OutboxEventDigest(row.eventJSON) &&
		w.RecipientNpub == t.recipient && w.MaxRetries == row.maxRetries &&
		w.ExpectedRowRevision == row.revision && w.ExpectedAttemptGeneration == t.generation &&
		w.OldQueueID == t.queueID && w.NewQueueID == entry.QueueID &&
		entry.Entry.EventJSON == row.eventJSON && int64(entry.Entry.MaxRetries) == row.maxRetries &&
		entry.Entry.Status == messaging.OutboxStatusGroupPending && entry.Entry.RetryCount == 0 && entry.Entry.LastAttempt == 0 &&
		entry.Entry.CreatedAt == w.AuthorizedAt && entry.FailureResult == nil
}

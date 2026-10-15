package groupchat

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/iDoris-ai/hyphae/internal/messaging"
)

// ErrFanoutQueueConflict is D14: the queued row's persisted queue_id is not
// the unique outbox entry's queue_id. Both stores are held unchanged; no
// reservation, publish or deletion happens (rev7 §3.5, schema supplement §5).
var ErrFanoutQueueConflict = errors.New("fanout queue conflict: outbox queue_id differs from the persisted queue_id")

var ErrFanoutNotActive = errors.New("fanout row is not an active pending queue entry")
var ErrFanoutRetryExhausted = errors.New("fanout retry budget exhausted")
var confirmOutboxDurable = messaging.ConfirmOutboxDurable

// fanoutBarrierTx is the recovery barrier that every R and S must pass
// (rev7 §3.3): under the outbox lock and the caller's SQLite transaction it
// replays an unconsumed O3 record (or holds on conflict), then checks D14
// and that the unique outbox entry is the row's active pending queue entry.
// The returned row reflects any replay.
func (s *Store) fanoutBarrierTx(tx queryExecer, path string, key FanoutKey, recipient string) (row fanoutFailureRow, replayed bool, err error) {
	row, err = loadFanoutFailureRow(tx, key, recipient)
	if err != nil {
		return row, false, err
	}
	if row.token.queueID == "" {
		return row, false, fmt.Errorf("fanout row has no queue_id; nothing to reserve")
	}
	entry, receipt, err := messaging.ReadGroupEntryLocked(path, row.token.eventID)
	if err != nil {
		return row, false, err
	}
	if entry == nil {
		return row, false, fmt.Errorf("%s: outbox has no entry for the queued event", messaging.AgentMessageIssueQueueMissing)
	}
	if entry.QueueID != row.token.queueID {
		// Only a failed row with an exact D6 witness may adopt a new epoch.
		if RecipientDeliveryState(row.state) != RecipientFailed || entry.RecoveryWitness == nil {
			return row, false, ErrFanoutQueueConflict
		}
		if err := confirmOutboxDurable(path); err != nil {
			return row, false, fmt.Errorf("%w: outbox directory durability unconfirmed before replay: %v", ErrFanoutRecoveryPending, err)
		}
		if err := replayFanoutRecoveryTx(tx, &row, entry); err != nil {
			return row, false, err
		}
		replayed = true
	}
	if entry.Entry.EventJSON != row.eventJSON || int64(entry.Entry.MaxRetries) != row.maxRetries {
		return row, replayed, ErrFanoutQueueConflict
	}
	if w := messaging.OutboxWitnessDigest(entry); w != "" && w != row.recoveryDigest {
		return row, replayed, fmt.Errorf("%w: unconsumed recovery_witness on the queue entry", ErrFanoutRecoveryPending)
	}
	if receipt != "" && receipt != row.receipt {
		if err := confirmOutboxDurable(path); err != nil {
			return row, false, fmt.Errorf("%w: outbox directory durability unconfirmed before replay: %v", ErrFanoutBookkeepingPending, err)
		}
		if err := replayFanoutFailureTx(tx, &row, entry, receipt); err != nil {
			return row, false, err
		}
		replayed = true
	}
	if RecipientDeliveryState(row.state) != RecipientQueued || entry.Entry.Status != messaging.OutboxStatusGroupPending ||
		!row.retryCount.Valid || int64(entry.Entry.RetryCount) != row.retryCount.Int64 || row.retryCount.Int64 >= row.maxRetries ||
		entry.Entry.RetryCount >= entry.Entry.MaxRetries {
		baseErr := ErrFanoutNotActive
		if (RecipientDeliveryState(row.state) == RecipientFailed || entry.Entry.Status == messaging.OutboxStatusGroupFailed) &&
			((row.retryCount.Valid && row.retryCount.Int64 >= row.maxRetries) || entry.Entry.RetryCount >= entry.Entry.MaxRetries) {
			baseErr = ErrFanoutRetryExhausted
		}
		return row, replayed, fmt.Errorf("%w: fanout row is not an active pending queue entry (state %s, outbox %s %d/%d)", baseErr, row.state, entry.Entry.Status, entry.Entry.RetryCount, row.maxRetries)
	}
	return row, replayed, nil
}

// commitReplayOnRefusal keeps a barrier replay durable when the operation
// that triggered it is refused afterwards.
func commitReplayOnRefusal(tx *immediateTx, replayed bool, err error) error {
	if replayed {
		if commitErr := tx.Commit(); commitErr != nil {
			return fmt.Errorf("%w: %w (barrier replay commit: %v)", ErrFanoutBookkeepingPending, err, commitErr)
		}
	}
	return err
}

// reserveFanoutAttempt is R behind the barrier: it consumes one generation
// for the row's current queue entry. Inert: no production caller.
func (s *Store) reserveFanoutAttempt(key FanoutKey, recipient string) (token fanoutAttemptToken, err error) {
	err = messaging.WithGroupOutboxLock(func(path string) error {
		tx, err := s.beginImmediate()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if _, replayed, err := s.fanoutBarrierTx(tx, path, key, recipient); err != nil {
			return commitReplayOnRefusal(tx, replayed, err)
		}
		if token, err = reserveFanoutAttemptTx(tx, key, recipient); err != nil {
			return err
		}
		return tx.Commit()
	})
	if err != nil {
		return fanoutAttemptToken{}, err
	}
	return token, nil
}

// startFanoutAttempt is S behind the barrier: it records the publish start
// for a reserved token. The caller performs P only after it returns nil.
func (s *Store) startFanoutAttempt(token fanoutAttemptToken, targets int, now int64) error {
	return messaging.WithGroupOutboxLock(func(path string) error {
		tx, err := s.beginImmediate()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		row, replayed, err := s.fanoutBarrierTx(tx, path, token.key, token.recipient)
		if err != nil {
			return commitReplayOnRefusal(tx, replayed, err)
		}
		if row.token != token || row.phase != "reserved" {
			return commitReplayOnRefusal(tx, replayed, errFanoutStaleAttempt)
		}
		if err := startFanoutAttemptTx(tx, token, targets, now); err != nil {
			return err
		}
		return tx.Commit()
	})
}

func loadFanoutFailureRow(tx queryExecer, key FanoutKey, recipient string) (fanoutFailureRow, error) {
	local, err := canonicalNpub(key.LocalNpub)
	if err != nil {
		return fanoutFailureRow{}, err
	}
	key.LocalNpub = local
	row := fanoutFailureRow{token: fanoutAttemptToken{key: key, recipient: recipient}}
	err = row.scan(tx.QueryRow(`SELECT `+fanoutFailureColumns+` FROM groupchat_fanout
        WHERE local_npub=? AND group_id=? AND envelope_type=? AND send_key=? AND recipient_npub=?`,
		key.LocalNpub, key.GroupID, key.EnvelopeType, key.SendKey, recipient))
	if errors.Is(err, sql.ErrNoRows) {
		return row, fmt.Errorf("fanout row not found")
	}
	return row, err
}

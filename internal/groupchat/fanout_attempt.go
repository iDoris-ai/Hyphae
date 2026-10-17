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

// ErrFanoutRetryExhausted is the barrier refusal after an exhausting O3 has
// been projected. A replay performed by this call is committed by the caller.
var ErrFanoutRetryExhausted = errors.New("fanout retry exhausted")

// confirmOutboxDurable is replaced in tests to prove an unconfirmed rename
// does not replay T4.
var confirmOutboxDurable = messaging.ConfirmOutboxDurable

// fanoutBarrierTx is the recovery barrier that every R and S must pass
// (rev7 §3.3): under the outbox lock and the caller's SQLite transaction it
// replays an unconsumed O3 record (or holds on conflict), then checks D14
// and that the unique outbox entry is the row's active pending queue entry.
// The returned row reflects any replay. replayed is true only after T4 was
// written in this transaction; the caller must commit that replay before
// returning a later refusal.
func (s *Store) fanoutBarrierTx(tx queryExecer, path string, key FanoutKey, recipient string) (fanoutFailureRow, bool, error) {
	row, err := loadFanoutFailureRow(tx, key, recipient)
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
	if entry.QueueID != row.token.queueID || entry.Entry.EventJSON != row.eventJSON || int64(entry.Entry.MaxRetries) != row.maxRetries {
		return row, false, ErrFanoutQueueConflict
	}
	replayed := false
	if receipt != "" && receipt != row.receipt {
		if err := confirmOutboxDurable(path); err != nil {
			return row, false, fmt.Errorf("%w: outbox directory durability unconfirmed: %v", ErrFanoutBookkeepingPending, err)
		}
		if err := replayFanoutFailureTx(tx, &row, entry, receipt); err != nil {
			return row, false, err
		}
		replayed = true
	}
	exhausted := !row.retryCount.Valid || row.retryCount.Int64 >= row.maxRetries || int64(entry.Entry.RetryCount) >= int64(entry.Entry.MaxRetries) || RecipientDeliveryState(row.state) == RecipientFailed
	active := RecipientDeliveryState(row.state) == RecipientQueued && entry.Entry.Status == messaging.OutboxStatusGroupPending &&
		row.retryCount.Valid && int64(entry.Entry.RetryCount) == row.retryCount.Int64 && row.retryCount.Int64 < row.maxRetries && int64(entry.Entry.RetryCount) < int64(entry.Entry.MaxRetries)
	if !active {
		if exhausted {
			return row, replayed, ErrFanoutRetryExhausted
		}
		return row, replayed, fmt.Errorf("fanout row is not an active pending queue entry (state %s, outbox %s %d/%d)", row.state, entry.Entry.Status, entry.Entry.RetryCount, row.maxRetries)
	}
	return row, replayed, nil
}

func commitReplayedBarrier(tx interface{ Commit() error }, replayed bool, err error) error {
	if err == nil || !replayed {
		return err
	}
	if cerr := tx.Commit(); cerr != nil {
		return fmt.Errorf("%w: replay commit: %v", ErrFanoutBookkeepingPending, cerr)
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
			return commitReplayedBarrier(tx, replayed, err)
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
			return commitReplayedBarrier(tx, replayed, err)
		}
		if row.token != token || row.phase != "reserved" {
			return errFanoutStaleAttempt
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

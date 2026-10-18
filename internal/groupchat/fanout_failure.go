package groupchat

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/iDoris-ai/hyphae/internal/messaging"
)

var (
	// errFanoutStaleAttempt: the token is no longer the row's current started
	// attempt. Nothing was written to either store.
	errFanoutStaleAttempt = errors.New("stale fanout attempt")
	// ErrFanoutBookkeepingPending: O3 may be durable but T4 is not, or the O3
	// commit is uncertain. No new reservation or publish may proceed until
	// the recovery barrier replays or resolves it (rev7 §3.3).
	ErrFanoutBookkeepingPending = errors.New("fanout failure bookkeeping is pending recovery")
	// ErrFanoutBookkeepingConflict: the outbox carries a failure_result that
	// is neither the row's consumed receipt nor an exact replayable O3 for its
	// started attempt. Both stores are held unchanged (rev7 §3.3/§3.5).
	ErrFanoutBookkeepingConflict = errors.New("fanout failure bookkeeping conflict")
)

// Raw-writer seams: tests wrap them to return commit-uncertain errors with
// or without a durable write, exercising confirmFanout*Commit end to end.
var (
	commitGroupFailureLocked  = messaging.CommitGroupFailureLocked
	commitGroupRecoveryLocked = messaging.CommitGroupRecoveryLocked
)

// fanoutCrashGate is called between a durable JSON commit (O3, D6) and its
// SQLite projection (T4, T2') while both locks are held. Production is a
// no-op; crash tests replace it to stop the process at that exact boundary.
var fanoutCrashGate = func(stage string) {}

// fanoutFailureRow is the locked SQLite pre-image that O3 and T4 rely on.
type fanoutFailureRow struct {
	token                                                     fanoutAttemptToken
	state, phase, eventJSON, receipt, recoveryDigest          string
	revision, attempts, lastAttemptAt, relayCount, maxRetries int64
	lastResultGeneration                                      int64
	retryCount                                                sql.NullInt64
}

const fanoutFailureColumns = `event_id,queue_id,attempt_generation,state,attempt_phase,event_json,last_failure_receipt,last_recovery_digest,
    row_revision,attempts,last_attempt_at,relay_count,max_retries,last_result_generation,retry_count`

func (r *fanoutFailureRow) scan(row *sql.Row) error {
	return row.Scan(&r.token.eventID, &r.token.queueID, &r.token.generation, &r.state, &r.phase, &r.eventJSON, &r.receipt, &r.recoveryDigest,
		&r.revision, &r.attempts, &r.lastAttemptAt, &r.relayCount, &r.maxRetries, &r.lastResultGeneration, &r.retryCount)
}

// commitGroupFailureLocked is O3; tests override it to simulate a write that
// lands commit-uncertain, so commitFanoutFailure's confirm+reread resolution
// is exercised without touching the real writer (outbox_raw_write.go).
var commitGroupFailureLocked = messaging.CommitGroupFailureLocked

// commitFanoutFailure is the F3 failure boundary for the current started
// token (rev7 §3.3, schema supplement §3): outbox lock → SQLite BEGIN
// IMMEDIATE → read-only token check → O3 JSON commit → T4 CAS → commit. Inert:
// no production caller until F3 wiring is reviewed.
func (s *Store) commitFanoutFailure(token fanoutAttemptToken) error {
	return messaging.WithGroupOutboxLock(func(path string) error {
		tx, err := s.beginImmediate()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		row := fanoutFailureRow{token: token}
		err = row.scan(tx.QueryRow(`SELECT `+fanoutFailureColumns+` FROM groupchat_fanout
            WHERE local_npub=? AND group_id=? AND envelope_type=? AND send_key=? AND recipient_npub=? AND event_id=? AND queue_id=? AND attempt_generation=?`,
			token.key.LocalNpub, token.key.GroupID, token.key.EnvelopeType, token.key.SendKey, token.recipient, token.eventID, token.queueID, token.generation))
		if errors.Is(err, sql.ErrNoRows) {
			return errFanoutStaleAttempt
		}
		if err != nil {
			return err
		}
		if RecipientDeliveryState(row.state) != RecipientQueued || row.phase != "started" || row.lastResultGeneration >= token.generation {
			return errFanoutStaleAttempt
		}
		if !row.retryCount.Valid || row.retryCount.Int64 >= row.maxRetries {
			return fmt.Errorf("fanout retry budget is unknown or exhausted for a started attempt; holding")
		}
		result := row.failureResult()
		_, lastDigest, _ := strings.Cut(row.receipt, ":")
		receipt, err := commitGroupFailureLocked(path, messaging.GroupFailureRecord{
			Result: result, LastResultDigest: lastDigest, LastRecoveryDigest: row.recoveryDigest,
			FrozenEventJSON: row.eventJSON, MaxRetries: row.maxRetries,
		})
		if messaging.IsOutboxCommitUncertain(err) {
			// Never write a second O3: confirm the directory and reread. The
			// record present and exact → continue to T4; the old snapshot →
			// no durable evidence of this P outcome; anything else → hold.
			if receipt, err = confirmFanoutFailureCommit(path, row, result); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		// From here the O3 record is durable: any failure leaves it unconsumed
		// for the recovery barrier, never a second O3.
		fanoutCrashGate("o3")
		if err := projectFanoutFailureTx(tx, row, result, receipt); err != nil {
			return fmt.Errorf("%w: %v", ErrFanoutBookkeepingPending, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("%w: T4 commit: %v", ErrFanoutBookkeepingPending, err)
		}
		return nil
	})
}

// recoverFanoutFailure is the failure half of the recovery barrier (rev7
// §3.3 "崩溃 O3 后 T4 前"): under the same locks it replays T4 exactly once for
// an outbox failure_result that was committed by O3 but never projected. A
// result whose receipt is already the row's last_failure_receipt is a no-op;
// anything else that is not an exact replay of the started attempt's
// pre-image returns ErrFanoutBookkeepingConflict. replayed reports whether T4
// was written.
func (s *Store) recoverFanoutFailure(key FanoutKey, recipient string) (replayed bool, err error) {
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
		if row.token.queueID == "" {
			return nil // prepared rows have no queue entry and no O3 record
		}
		entry, receipt, err := messaging.ReadGroupEntryLocked(path, row.token.eventID)
		if err != nil || entry == nil || receipt == "" || receipt == row.receipt {
			return err
		}
		if entry.QueueID != row.token.queueID {
			return ErrFanoutQueueConflict
		}
		if err := confirmOutboxDurable(path); err != nil {
			return fmt.Errorf("%w: outbox directory durability unconfirmed before replay: %v", ErrFanoutBookkeepingPending, err)
		}
		if err := replayFanoutFailureTx(tx, &row, entry, receipt); err != nil {
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

// replayFanoutFailureTx projects an unconsumed outbox failure_result into the
// row when it is an exact replay of the row's started attempt (token, O3
// pre-image and projected entry fields); otherwise it returns
// ErrFanoutBookkeepingConflict and writes nothing. row is updated to the
// post-image on success.
func replayFanoutFailureTx(tx queryExecer, row *fanoutFailureRow, entry *messaging.RawGroupEntry, receipt string) error {
	want := row.failureResult()
	if RecipientDeliveryState(row.state) != RecipientQueued || row.phase != "started" || row.lastResultGeneration >= row.token.generation ||
		!row.retryCount.Valid || !matchesFailureResult(entry, want, row) {
		return ErrFanoutBookkeepingConflict
	}
	if err := projectFanoutFailureTx(tx, *row, want, receipt); err != nil {
		return err
	}
	row.revision++
	row.phase, row.receipt, row.lastResultGeneration = "failed", receipt, row.token.generation
	row.retryCount = sql.NullInt64{Int64: want.RetryCount, Valid: true}
	if want.Status == messaging.OutboxStatusGroupFailed {
		row.state = string(RecipientFailed)
	}
	return nil
}

// matchesFailureResult reports whether the entry's failure_result and its
// projected fields equal the O3 record derived from the row's pre-image.
func matchesFailureResult(entry *messaging.RawGroupEntry, want messaging.RawFailureResult, row *fanoutFailureRow) bool {
	stored := entry.FailureResult
	return stored != nil && entry.Entry.EventJSON == row.eventJSON && int64(entry.Entry.MaxRetries) == row.maxRetries &&
		int64(entry.Entry.RetryCount) == want.RetryCount && entry.Entry.Status == want.Status && entry.Entry.LastAttempt == want.LastAttemptAt &&
		stored.Token == want.Token && stored.ExpectedRowRevision == want.ExpectedRowRevision && stored.Attempts == want.Attempts &&
		stored.LastAttemptAt == want.LastAttemptAt && stored.RelayCount == want.RelayCount && stored.PriorRetryCount == want.PriorRetryCount &&
		stored.RetryCount == want.RetryCount && stored.Status == want.Status && stored.Issue == want.Issue
}

// failureResult derives the O3 record for the row's started attempt; status
// and issue follow the P4 budget mapping against the frozen max_retries.
func (r *fanoutFailureRow) failureResult() messaging.RawFailureResult {
	t := r.token
	result := messaging.RawFailureResult{
		Version: 1,
		Token: messaging.OutboxAttemptToken{
			FanoutKey: messaging.OutboxFanoutKey{
				LocalNpub: t.key.LocalNpub, RecipientNpub: t.recipient,
				GroupID: t.key.GroupID, SendKey: t.key.SendKey, EnvelopeType: string(t.key.EnvelopeType),
			},
			EventID: t.eventID, QueueID: t.queueID, AttemptGeneration: t.generation,
		},
		ExpectedRowRevision: r.revision, Attempts: r.attempts, LastAttemptAt: r.lastAttemptAt, RelayCount: r.relayCount,
		PriorRetryCount: r.retryCount.Int64, RetryCount: r.retryCount.Int64 + 1,
		Status: messaging.OutboxStatusGroupPending, Issue: string(messaging.AgentMessageIssueSendFailed),
	}
	if result.RetryCount >= r.maxRetries {
		result.Status, result.Issue = messaging.OutboxStatusGroupFailed, string(messaging.AgentMessageIssueRetryExhausted)
	}
	return result
}

// projectFanoutFailureTx is T4: a CAS on the started token and its O3
// pre-image revision that projects the O3 result into the row.
func projectFanoutFailureTx(tx queryExecer, row fanoutFailureRow, result messaging.RawFailureResult, receipt string) error {
	state := RecipientQueued
	if result.Status == messaging.OutboxStatusGroupFailed {
		state = RecipientFailed
	}
	t := row.token
	updated, err := tx.Exec(`UPDATE groupchat_fanout SET retry_count=?,retry_queue_id=queue_id,issue=?,attempt_phase='failed',state=?,
        last_result_generation=?,last_failure_receipt=?,row_revision=row_revision+1,updated_at=MAX(updated_at,?)
        WHERE local_npub=? AND group_id=? AND envelope_type=? AND send_key=? AND recipient_npub=? AND event_id=? AND queue_id=? AND attempt_generation=? AND attempt_phase='started' AND state=? AND row_revision=?`,
		result.RetryCount, result.Issue, state, t.generation, receipt, time.Now().Unix(),
		t.key.LocalNpub, t.key.GroupID, t.key.EnvelopeType, t.key.SendKey, t.recipient, t.eventID, t.queueID, t.generation, RecipientQueued, row.revision)
	if err != nil {
		return fmt.Errorf("T4: %w", err)
	}
	if n, err := updated.RowsAffected(); err != nil || n != 1 {
		return fmt.Errorf("T4 CAS affected %d rows (%v)", n, err)
	}
	return nil
}

// confirmFanoutFailureCommit resolves a commit-uncertain O3 write while the
// locks are still held and returns the receipt to project.
func confirmFanoutFailureCommit(path string, row fanoutFailureRow, want messaging.RawFailureResult) (string, error) {
	if err := confirmOutboxDurable(path); err != nil {
		return "", fmt.Errorf("%w: outbox directory durability unconfirmed: %v", ErrFanoutBookkeepingPending, err)
	}
	entry, receipt, err := messaging.ReadGroupEntryLocked(path, row.token.eventID)
	if err != nil {
		return "", fmt.Errorf("%w: reread after uncertain commit: %v", ErrFanoutBookkeepingPending, err)
	}
	if entry == nil || entry.QueueID != row.token.queueID {
		return "", fmt.Errorf("%w: outbox entry missing or replaced after uncertain commit", ErrFanoutBookkeepingConflict)
	}
	if receipt == "" || receipt == row.receipt {
		return "", fmt.Errorf("%w: old snapshot survived; this publish outcome has no durable evidence", ErrFanoutBookkeepingPending)
	}
	if !matchesFailureResult(entry, want, &row) {
		return "", fmt.Errorf("%w: outbox record after uncertain commit is not this attempt's result", ErrFanoutBookkeepingConflict)
	}
	return receipt, nil
}

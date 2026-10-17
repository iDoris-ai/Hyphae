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
)

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
		var row struct {
			state, phase, eventJSON, receipt, recoveryDigest          string
			revision, attempts, lastAttemptAt, relayCount, maxRetries int64
			lastResultGeneration                                      int64
			retryCount                                                sql.NullInt64
		}
		err = tx.QueryRow(`SELECT state,attempt_phase,event_json,last_failure_receipt,last_recovery_digest,row_revision,attempts,last_attempt_at,relay_count,max_retries,last_result_generation,retry_count
            FROM groupchat_fanout WHERE local_npub=? AND group_id=? AND envelope_type=? AND send_key=? AND recipient_npub=? AND event_id=? AND queue_id=? AND attempt_generation=?`,
			token.key.LocalNpub, token.key.GroupID, token.key.EnvelopeType, token.key.SendKey, token.recipient, token.eventID, token.queueID, token.generation).
			Scan(&row.state, &row.phase, &row.eventJSON, &row.receipt, &row.recoveryDigest, &row.revision, &row.attempts, &row.lastAttemptAt, &row.relayCount, &row.maxRetries, &row.lastResultGeneration, &row.retryCount)
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
		result := messaging.RawFailureResult{
			Version: 1,
			Token: messaging.OutboxAttemptToken{
				FanoutKey: messaging.OutboxFanoutKey{
					LocalNpub: token.key.LocalNpub, RecipientNpub: token.recipient,
					GroupID: token.key.GroupID, SendKey: token.key.SendKey, EnvelopeType: string(token.key.EnvelopeType),
				},
				EventID: token.eventID, QueueID: token.queueID, AttemptGeneration: token.generation,
			},
			ExpectedRowRevision: row.revision, Attempts: row.attempts, LastAttemptAt: row.lastAttemptAt, RelayCount: row.relayCount,
			PriorRetryCount: row.retryCount.Int64, RetryCount: row.retryCount.Int64 + 1,
			Status: messaging.OutboxStatusGroupPending, Issue: string(messaging.AgentMessageIssueSendFailed),
		}
		state := RecipientQueued
		if result.RetryCount >= row.maxRetries {
			result.Status, result.Issue, state = messaging.OutboxStatusGroupFailed, string(messaging.AgentMessageIssueRetryExhausted), RecipientFailed
		}
		_, lastDigest, _ := strings.Cut(row.receipt, ":")
		receipt, err := messaging.CommitGroupFailureLocked(path, messaging.GroupFailureRecord{
			Result: result, LastResultDigest: lastDigest, LastRecoveryDigest: row.recoveryDigest,
			FrozenEventJSON: row.eventJSON, MaxRetries: row.maxRetries,
		})
		if messaging.IsOutboxCommitUncertain(err) {
			return fmt.Errorf("%w: %v", ErrFanoutBookkeepingPending, err)
		}
		if err != nil {
			return err
		}
		// From here the O3 record is durable: any failure leaves it unconsumed
		// for the recovery barrier, never a second O3.
		updated, err := tx.Exec(`UPDATE groupchat_fanout SET retry_count=?,retry_queue_id=queue_id,issue=?,attempt_phase='failed',state=?,
            last_result_generation=?,last_failure_receipt=?,row_revision=row_revision+1,updated_at=MAX(updated_at,?)
            WHERE local_npub=? AND group_id=? AND envelope_type=? AND send_key=? AND recipient_npub=? AND event_id=? AND queue_id=? AND attempt_generation=? AND attempt_phase='started' AND state=? AND row_revision=?`,
			result.RetryCount, result.Issue, state, token.generation, receipt, time.Now().Unix(),
			token.key.LocalNpub, token.key.GroupID, token.key.EnvelopeType, token.key.SendKey, token.recipient, token.eventID, token.queueID, token.generation, RecipientQueued, row.revision)
		if err != nil {
			return fmt.Errorf("%w: T4: %v", ErrFanoutBookkeepingPending, err)
		}
		if n, err := updated.RowsAffected(); err != nil || n != 1 {
			return fmt.Errorf("%w: T4 CAS affected %d rows (%v)", ErrFanoutBookkeepingPending, n, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("%w: T4 commit: %v", ErrFanoutBookkeepingPending, err)
		}
		return nil
	})
}

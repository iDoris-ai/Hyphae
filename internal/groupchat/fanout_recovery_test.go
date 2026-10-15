package groupchat

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/iDoris-ai/hyphae/internal/messaging"
	"github.com/stretchr/testify/require"
)

type recoveryRow struct {
	State, Phase, Issue, QueueID, RetryQueueID, RecoveryID, RecoveryDigest, Receipt string
	RetryCount, Revision, Generation, Attempts, Watermark                           int64
}

func (f failureFixture) recovery(t *testing.T) recoveryRow {
	t.Helper()
	var r recoveryRow
	require.NoError(t, f.member.db.QueryRow(`SELECT state,attempt_phase,issue,queue_id,retry_queue_id,last_recovery_id,last_recovery_digest,last_failure_receipt,
        retry_count,row_revision,attempt_generation,attempts,last_result_generation FROM groupchat_fanout WHERE event_id=?`, f.eventID).
		Scan(&r.State, &r.Phase, &r.Issue, &r.QueueID, &r.RetryQueueID, &r.RecoveryID, &r.RecoveryDigest, &r.Receipt,
			&r.RetryCount, &r.Revision, &r.Generation, &r.Attempts, &r.Watermark))
	return r
}

// exhaust drives the fixture to a failed row with an exhausted outbox entry.
func (f failureFixture) exhaust(t *testing.T) {
	t.Helper()
	for range 3 {
		require.NoError(t, f.member.store.commitFanoutFailure(f.startAttempt(t, 1)))
	}
	require.Equal(t, string(RecipientFailed), f.row(t).State)
}

func (f failureFixture) entryByQueue(t *testing.T, queueID string) *messaging.RawGroupEntry {
	t.Helper()
	entry := f.outboxEntry(t)
	require.Equal(t, queueID, entry.QueueID)
	return entry
}

func TestAuthorizeFanoutRecoveryExhaustedFailed(t *testing.T) {
	f := newFailureFixture(t)
	f.exhaust(t)
	before := f.recovery(t)
	old := f.outboxEntry(t)
	authorizedAt := time.Now().Unix()

	newQueue, err := f.member.store.authorizeFanoutRecovery(f.key, f.recipient, authorizedAt)
	require.NoError(t, err)
	require.Len(t, newQueue, 32)
	require.NotEqual(t, f.queueID, newQueue)

	entry := f.entryByQueue(t, newQueue)
	w := entry.RecoveryWitness
	require.NotNil(t, w)
	require.Nil(t, entry.FailureResult, "old result is not copied into the new epoch")
	require.Equal(t, "exhausted_failed", w.Reason)
	require.Equal(t, messaging.OutboxEntryDigest(old), w.OldEntryDigest)
	require.Equal(t, messaging.OutboxStatusGroupFailed, w.OldStatus)
	require.EqualValues(t, 3, w.OldRetryCount)
	require.Equal(t, f.queueID, w.OldQueueID)
	require.Equal(t, before.Revision, w.ExpectedRowRevision)
	require.Equal(t, before.Generation, w.ExpectedAttemptGeneration)
	require.Equal(t, authorizedAt, w.AuthorizedAt)
	require.Equal(t, authorizedAt, entry.Entry.CreatedAt)
	require.Equal(t, 0, entry.Entry.RetryCount)
	require.Equal(t, messaging.OutboxStatusGroupPending, entry.Entry.Status)
	require.Equal(t, "[]", string(entry.Fields["relays"]))

	after := f.recovery(t)
	require.Equal(t, string(RecipientQueued), after.State)
	require.Equal(t, "idle", after.Phase)
	require.Equal(t, "", after.Issue)
	require.Equal(t, newQueue, after.QueueID)
	require.Equal(t, newQueue, after.RetryQueueID)
	require.EqualValues(t, 0, after.RetryCount)
	require.Equal(t, w.RecoveryID, after.RecoveryID)
	require.Equal(t, messaging.OutboxWitnessDigest(entry), after.RecoveryDigest)
	require.Equal(t, before.Revision+1, after.Revision)
	require.Equal(t, before.Generation, after.Generation, "generation is not reset")
	require.Equal(t, before.Attempts, after.Attempts, "attempts are preserved")
	require.Equal(t, before.Watermark, after.Watermark)
	require.Equal(t, before.Receipt, after.Receipt, "consumed failure receipt is preserved")

	// The new epoch is immediately reservable and the next failure counts from 0.
	token, err := f.member.store.reserveFanoutAttempt(f.key, f.recipient)
	require.NoError(t, err)
	require.Equal(t, newQueue, token.queueID)
	require.Equal(t, before.Generation+1, token.generation)
	require.NoError(t, f.member.store.startFanoutAttempt(token, 1, time.Now().Unix()))
	require.NoError(t, f.member.store.commitFanoutFailure(token))
	require.EqualValues(t, 1, f.recovery(t).RetryCount)
	require.NotNil(t, f.entryByQueue(t, newQueue).RecoveryWitness, "consumed witness coexists with the new result")
}

func TestAuthorizeFanoutRecoveryMissing(t *testing.T) {
	f := newFailureFixture(t)
	f.exhaust(t)
	require.NoError(t, os.WriteFile(f.outboxPath, []byte(`{"entries":[]}`), 0600))
	newQueue, err := f.member.store.authorizeFanoutRecovery(f.key, f.recipient, 1_800_000_000)
	require.NoError(t, err)
	entry := f.entryByQueue(t, newQueue)
	require.Equal(t, "missing", entry.RecoveryWitness.Reason)
	require.True(t, entry.RecoveryWitness.ObservedAbsent)
	require.Equal(t, f.queueID, entry.RecoveryWitness.OldQueueID)
	require.Equal(t, f.recovery(t).QueueID, newQueue)
	require.Equal(t, messaging.OutboxEventDigest(entry.Entry.EventJSON), entry.RecoveryWitness.EventJSONDigest)
}

func TestAuthorizeFanoutRecoveryExhaustedPending(t *testing.T) {
	f := newFailureFixture(t)
	f.exhaust(t)
	// Legacy shape: the row is failed but the entry still says group_pending at budget without a modern failure_result.
	rawEditEntryField(t, f.outboxPath, func(fields map[string]json.RawMessage) {
		fields["status"] = json.RawMessage(`"group_pending"`)
		delete(fields, "failure_result")
	})
	newQueue, err := f.member.store.authorizeFanoutRecovery(f.key, f.recipient, time.Now().Unix())
	require.NoError(t, err)
	w := f.entryByQueue(t, newQueue).RecoveryWitness
	require.Equal(t, "exhausted_pending", w.Reason)
	require.Equal(t, messaging.OutboxStatusGroupPending, w.OldStatus)
}

func TestAuthorizeFanoutRecoveryRefusals(t *testing.T) {
	t.Run("row not failed", func(t *testing.T) {
		f := newFailureFixture(t)
		unchanged := f.snapshot(t)
		_, err := f.member.store.authorizeFanoutRecovery(f.key, f.recipient, 1)
		require.Error(t, err)
		unchanged()
	})
	for _, state := range []RecipientDeliveryState{RecipientQueued, RecipientRelayAccepted} {
		t.Run("non-failed empty outbox "+string(state), func(t *testing.T) {
			f := newFailureFixture(t)
			_, err := f.member.db.Exec(`UPDATE groupchat_fanout SET state=? WHERE event_id=?`, state, f.eventID)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(f.outboxPath, []byte(`{"entries":[]}`), 0600))
			unchanged := f.snapshot(t)
			_, err = f.member.store.authorizeFanoutRecovery(f.key, f.recipient, 1)
			require.Error(t, err)
			unchanged()
		})
	}
	t.Run("unconsumed O3 on the failed row", func(t *testing.T) {
		f := newFailureFixture(t)
		for range 2 {
			require.NoError(t, f.member.store.commitFanoutFailure(f.startAttempt(t, 1)))
		}
		f.failO3BeforeT4(t, f.startAttempt(t, 1))
		_, err := f.member.db.Exec(`UPDATE groupchat_fanout SET state='failed'`)
		require.NoError(t, err)
		unchanged := f.snapshot(t)
		_, err = f.member.store.authorizeFanoutRecovery(f.key, f.recipient, 1)
		require.ErrorIs(t, err, ErrFanoutBookkeepingConflict)
		unchanged()
	})
	t.Run("active pending entry under a failed row", func(t *testing.T) {
		f := newFailureFixture(t)
		_, err := f.member.db.Exec(`UPDATE groupchat_fanout SET state='failed'`)
		require.NoError(t, err)
		unchanged := f.snapshot(t)
		_, err = f.member.store.authorizeFanoutRecovery(f.key, f.recipient, 1)
		require.ErrorIs(t, err, ErrFanoutBookkeepingConflict)
		unchanged()
	})
	t.Run("negative time", func(t *testing.T) {
		f := newFailureFixture(t)
		_, err := f.member.store.authorizeFanoutRecovery(f.key, f.recipient, -1)
		require.Error(t, err)
	})
}

func TestAuthorizeFanoutRecoveryT2PrimeFailureLeavesUnconsumedWitness(t *testing.T) {
	f := newFailureFixture(t)
	f.exhaust(t)
	rowBefore := fanoutRowSnapshot(t, f.member, f.eventID)
	_, err := f.member.db.Exec(`CREATE TRIGGER inject_t2_failure BEFORE UPDATE OF last_recovery_id ON groupchat_fanout
        BEGIN SELECT RAISE(ABORT, 'injected T2 prime failure'); END`)
	require.NoError(t, err)
	_, err = f.member.store.authorizeFanoutRecovery(f.key, f.recipient, time.Now().Unix())
	require.ErrorIs(t, err, ErrFanoutRecoveryPending)
	require.Equal(t, rowBefore, fanoutRowSnapshot(t, f.member, f.eventID), "T2' rolled back")
	entry := f.outboxEntry(t)
	require.NotNil(t, entry.RecoveryWitness, "D6 stays durable")
	require.NotEqual(t, f.queueID, entry.QueueID)
	_, err = f.member.db.Exec(`DROP TRIGGER inject_t2_failure`)
	require.NoError(t, err)

	// The barrier replay of this witness is covered in fanout_recovery_replay_test.go.
}

func TestFanoutBarrierHoldsUnconsumedWitness(t *testing.T) {
	f := newFailureFixture(t)
	f.exhaust(t)
	_, err := f.member.store.authorizeFanoutRecovery(f.key, f.recipient, time.Now().Unix())
	require.NoError(t, err)
	// A witness whose digest is not the row's consumed digest blocks R.
	_, err = f.member.db.Exec(`UPDATE groupchat_fanout SET last_recovery_digest='stale'`)
	require.NoError(t, err)
	unchanged := f.snapshot(t)
	_, err = f.member.store.reserveFanoutAttempt(f.key, f.recipient)
	require.ErrorIs(t, err, ErrFanoutRecoveryPending)
	unchanged()
}

func TestConfirmFanoutRecoveryCommit(t *testing.T) {
	f := newFailureFixture(t)
	f.exhaust(t)
	tx, err := f.member.store.beginImmediate()
	require.NoError(t, err)
	row, err := loadFanoutFailureRow(tx, f.key, f.recipient)
	require.NoError(t, err)
	require.NoError(t, tx.Rollback())
	old := f.outboxEntry(t)
	w, err := row.recoveryWitness(old, 1_800_000_000)
	require.NoError(t, err)

	// Old snapshot survived → pending, never a second D6.
	_, err = confirmFanoutRecoveryCommit(f.outboxPath, row, w)
	require.ErrorIs(t, err, ErrFanoutRecoveryPending)

	// New epoch present with the same recovery_id → its digest.
	_, lastDigest, _ := strings.Cut(row.receipt, ":")
	digest, err := messaging.CommitGroupRecoveryLocked(f.outboxPath, messaging.GroupRecoveryRecord{Witness: w, LastResultDigest: lastDigest})
	require.NoError(t, err)
	got, err := confirmFanoutRecoveryCommit(f.outboxPath, row, w)
	require.NoError(t, err)
	require.Equal(t, digest, got)

	// A different recovery_id under the new queue → conflict.
	w.RecoveryID = "00000000000000000000000000000000"
	_, err = confirmFanoutRecoveryCommit(f.outboxPath, row, w)
	require.ErrorIs(t, err, ErrFanoutBookkeepingConflict)
}

package groupchat

import (
	"os"
	"testing"
	"time"

	"github.com/iDoris-ai/hyphae/internal/messaging"
	"github.com/stretchr/testify/require"
)

func TestReserveAndStartFanoutAttemptBehindBarrier(t *testing.T) {
	f := newFailureFixture(t)
	token, err := f.member.store.reserveFanoutAttempt(f.key, f.recipient)
	require.NoError(t, err)
	require.EqualValues(t, 1, token.generation)
	require.Equal(t, f.queueID, token.queueID)
	require.Equal(t, "reserved", f.row(t).Phase)

	// S must see the reserved token; a second S for the same token is stale.
	require.NoError(t, f.member.store.startFanoutAttempt(token, 2, time.Now().Unix()))
	require.Equal(t, "started", f.row(t).Phase)
	unchanged := f.snapshot(t)
	require.ErrorIs(t, f.member.store.startFanoutAttempt(token, 2, time.Now().Unix()), errFanoutStaleAttempt)
	unchanged()

	// A failed P round-trips through O3/T4 and the next R succeeds.
	require.NoError(t, f.member.store.commitFanoutFailure(token))
	next, err := f.member.store.reserveFanoutAttempt(f.key, f.recipient)
	require.NoError(t, err)
	require.EqualValues(t, 2, next.generation)
}

func TestReserveFanoutAttemptReplaysPendingO3First(t *testing.T) {
	f := newFailureFixture(t)
	token := f.startAttempt(t, 1)
	f.failO3BeforeT4(t, token)
	require.EqualValues(t, 0, f.row(t).RetryCount, "T4 was aborted")

	next, err := f.member.store.reserveFanoutAttempt(f.key, f.recipient)
	require.NoError(t, err)
	require.EqualValues(t, 2, next.generation)
	row := f.row(t)
	require.EqualValues(t, 1, row.RetryCount, "barrier replayed T4 before reserving")
	require.Equal(t, token.generation, row.LastResultGeneration)
	entry := f.outboxEntry(t)
	require.Equal(t, messaging.FailureReceipt(entry.FailureResult.ResultID, sha256Hex(entry.Fields["failure_result"])), row.Receipt)
}

func TestReserveFanoutAttemptHoldsOnConflictAndExhaustion(t *testing.T) {
	t.Run("pending O3 conflict", func(t *testing.T) {
		f := newFailureFixture(t)
		f.failO3BeforeT4(t, f.startAttempt(t, 1))
		_, err := f.member.db.Exec(`UPDATE groupchat_fanout SET attempts=attempts+1`)
		require.NoError(t, err)
		unchanged := f.snapshot(t)
		_, err = f.member.store.reserveFanoutAttempt(f.key, f.recipient)
		require.ErrorIs(t, err, ErrFanoutBookkeepingConflict)
		unchanged()
	})
	t.Run("exhausted", func(t *testing.T) {
		f := newFailureFixture(t)
		for range 3 {
			require.NoError(t, f.member.store.commitFanoutFailure(f.startAttempt(t, 1)))
		}
		require.Equal(t, string(RecipientFailed), f.row(t).State)
		unchanged := f.snapshot(t)
		_, err := f.member.store.reserveFanoutAttempt(f.key, f.recipient)
		require.Error(t, err)
		unchanged()
	})
	t.Run("missing row", func(t *testing.T) {
		f := newFailureFixture(t)
		_, err := f.member.store.reserveFanoutAttempt(f.key, "npub1nobody")
		require.Error(t, err)
	})
}

func TestFanoutBarrierD14QueueConflict(t *testing.T) {
	f := newFailureFixture(t)
	_, err := f.member.db.Exec(`UPDATE groupchat_fanout SET queue_id='ffffffffffffffffffffffffffffffff'`)
	require.NoError(t, err)
	unchanged := f.snapshot(t)
	_, err = f.member.store.reserveFanoutAttempt(f.key, f.recipient)
	require.ErrorIs(t, err, ErrFanoutQueueConflict)
	replayed, err := f.member.store.recoverFanoutFailure(f.key, f.recipient)
	require.False(t, replayed)
	require.NoError(t, err, "no failure record: recovery has nothing to replay")
	unchanged()

	// The entry vanished (relays:null legacy or cleared): queue_missing, no write.
	require.NoError(t, os.WriteFile(f.outboxPath, []byte(`{"entries":[]}`), 0600))
	_, err = f.member.store.reserveFanoutAttempt(f.key, f.recipient)
	require.ErrorContains(t, err, string(messaging.AgentMessageIssueQueueMissing))
}

func TestConfirmFanoutFailureCommit(t *testing.T) {
	f := newFailureFixture(t)
	token := f.startAttempt(t, 1)
	tx, err := f.member.store.beginImmediate()
	require.NoError(t, err)
	row, err := loadFanoutFailureRow(tx, f.key, f.recipient)
	require.NoError(t, err)
	require.NoError(t, tx.Rollback())
	require.Equal(t, token, row.token)
	want := row.failureResult()

	// Old snapshot survived: no durable evidence → pending, never a 2nd O3.
	_, err = confirmFanoutFailureCommit(f.outboxPath, row, want)
	require.ErrorIs(t, err, ErrFanoutBookkeepingPending)

	// Record present and exact → the receipt to project.
	receipt, err := messaging.CommitGroupFailureLocked(f.outboxPath, messaging.GroupFailureRecord{Result: want, FrozenEventJSON: row.eventJSON, MaxRetries: row.maxRetries})
	require.NoError(t, err)
	got, err := confirmFanoutFailureCommit(f.outboxPath, row, want)
	require.NoError(t, err)
	require.Equal(t, receipt, got)

	// Record present but for a different pre-image → conflict.
	row.attempts++
	_, err = confirmFanoutFailureCommit(f.outboxPath, row, row.failureResult())
	require.ErrorIs(t, err, ErrFanoutBookkeepingConflict)

	// The row's persisted queue is not the entry's queue → conflict.
	row.attempts--
	row.token.queueID = "ffffffffffffffffffffffffffffffff"
	_, err = confirmFanoutFailureCommit(f.outboxPath, row, want)
	require.ErrorIs(t, err, ErrFanoutBookkeepingConflict)
}

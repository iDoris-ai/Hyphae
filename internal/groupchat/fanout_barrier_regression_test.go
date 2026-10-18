package groupchat

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// These assertions use only the original e4 API, so a baseline failure measures
// persisted behavior rather than a missing symbol introduced by the fix.
func TestBarrierRegressionExhaustingReplaySurvivesRefusal(t *testing.T) {
	f := newFailureFixture(t)
	for range 2 {
		require.NoError(t, f.member.store.commitFanoutFailure(f.startAttempt(t, 1)))
	}
	token := f.startAttempt(t, 1)
	f.failO3BeforeT4(t, token)
	_, err := f.member.store.reserveFanoutAttempt(f.key, f.recipient)
	require.Error(t, err)
	row := f.row(t)
	require.EqualValues(t, 3, row.RetryCount, "exhausting replay must persist even when R is refused")
	require.Equal(t, string(RecipientFailed), row.State)
	require.NotEmpty(t, row.Receipt)
}

func TestBarrierRegressionStaleStartKeepsReplay(t *testing.T) {
	f := newFailureFixture(t)
	token, err := f.member.store.reserveFanoutAttempt(f.key, f.recipient)
	require.NoError(t, err)
	require.NoError(t, f.member.store.startFanoutAttempt(token, 1, time.Now().Unix()))
	f.failO3BeforeT4(t, token)
	require.Error(t, f.member.store.startFanoutAttempt(token, 1, time.Now().Unix()))
	require.EqualValues(t, 1, f.row(t).RetryCount, "stale S must retain the barrier replay")
	require.NotEmpty(t, f.row(t).Receipt)
}

func TestStartFanoutAttemptHoldsBeforeUnconfirmedReplay(t *testing.T) {
	f := newFailureFixture(t)
	token := f.startAttempt(t, 1)
	f.failO3BeforeT4(t, token)
	unchanged := f.snapshot(t)
	original := confirmOutboxDurable
	t.Cleanup(func() { confirmOutboxDurable = original })
	calls := 0
	confirmOutboxDurable = func(path string) error {
		calls++
		require.Equal(t, f.outboxPath, path)
		return errors.New("injected directory sync failure")
	}
	require.ErrorIs(t, f.member.store.startFanoutAttempt(token, 1, time.Now().Unix()), ErrFanoutBookkeepingPending)
	require.Equal(t, 1, calls)
	unchanged()
	confirmOutboxDurable = original
	require.ErrorIs(t, f.member.store.startFanoutAttempt(token, 1, time.Now().Unix()), errFanoutStaleAttempt)
	require.EqualValues(t, 1, f.row(t).RetryCount)
}

func TestBarrierInactiveRefusalIsTypedAndUnchanged(t *testing.T) {
	f := newFailureFixture(t)
	token := f.startAttempt(t, 1)
	_, err := f.member.db.Exec(`UPDATE groupchat_fanout SET state='relay_accepted' WHERE event_id=?`, f.eventID)
	require.NoError(t, err)
	unchanged := f.snapshot(t)
	_, err = f.member.store.reserveFanoutAttempt(f.key, f.recipient)
	require.ErrorIs(t, err, ErrFanoutNotActive)
	require.NotErrorIs(t, err, ErrFanoutRetryExhausted)
	require.ErrorIs(t, f.member.store.startFanoutAttempt(token, 1, time.Now().Unix()), ErrFanoutNotActive)
	unchanged()
}

func TestCommitReplayFailurePreservesStaleRefusal(t *testing.T) {
	f := newFailureFixture(t)
	tx, err := f.member.store.beginImmediate()
	require.NoError(t, err)
	require.NoError(t, tx.Rollback())
	err = commitReplayOnRefusal(tx, true, errFanoutStaleAttempt)
	require.ErrorIs(t, err, ErrFanoutBookkeepingPending)
	require.ErrorIs(t, err, errFanoutStaleAttempt)
}

// B1 (PR244 clestons review): recoverFanoutFailure is the dedicated T4-replay
// entry point and must confirm outbox directory durability before projecting,
// exactly as the barrier does before its identical replay
// (fanout_attempt.go:47). Without that confirm, an injected directory-sync
// failure lets recover commit T4 while R/S would have refused, wedging the
// two stores against each other with no path to repair it.
func TestRecoverFanoutFailureHoldsBeforeUnconfirmedReplay(t *testing.T) {
	f := newFailureFixture(t)
	token := f.startAttempt(t, 1)
	f.failO3BeforeT4(t, token)
	unchanged := f.snapshot(t)

	original := confirmOutboxDurable
	t.Cleanup(func() { confirmOutboxDurable = original })
	calls := 0
	confirmOutboxDurable = func(path string) error {
		calls++
		require.Equal(t, f.outboxPath, path)
		return errors.New("injected directory sync failure")
	}
	replayed, err := f.member.store.recoverFanoutFailure(f.key, f.recipient)
	require.ErrorIs(t, err, ErrFanoutBookkeepingPending)
	require.False(t, replayed)
	require.Equal(t, 1, calls)
	unchanged()

	// Restore the hook: recover now replays and the row catches up.
	confirmOutboxDurable = original
	replayed, err = f.member.store.recoverFanoutFailure(f.key, f.recipient)
	require.NoError(t, err)
	require.True(t, replayed)
	require.EqualValues(t, 1, f.row(t).RetryCount)
}

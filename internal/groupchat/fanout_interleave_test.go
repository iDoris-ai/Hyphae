package groupchat

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// rev7 §3.3 "必测 A/B 交错": same event, same queue. A reserves and starts P;
// B reserves and starts P; A's publish outcome arrives late. A must return
// stale_attempt before any JSON write, with both durable records unchanged,
// in each of B's three outcomes.
func TestLateAttemptAIsStaleAgainstEveryBOutcome(t *testing.T) {
	outcomes := map[string]func(t *testing.T, f failureFixture, b fanoutAttemptToken){
		"B unreturned": func(*testing.T, failureFixture, fanoutAttemptToken) {},
		"B accepted (T3/O2 done)": func(t *testing.T, f failureFixture, b fanoutAttemptToken) {
			require.NoError(t, f.member.store.commitFanoutAccepted(b, time.Now().Unix()))
		},
		"B failed (O3/T4 done)": func(t *testing.T, f failureFixture, b fanoutAttemptToken) {
			require.NoError(t, f.member.store.commitFanoutFailure(b))
		},
	}
	for name, settleB := range outcomes {
		t.Run(name, func(t *testing.T) {
			f := newFailureFixture(t)
			a := f.startAttempt(t, 2)
			b := f.startAttempt(t, 2)
			require.Equal(t, a.generation+1, b.generation, "B supersedes A's unfinished generation")
			settleB(t, f, b)

			unchanged := f.snapshot(t)
			require.ErrorIs(t, f.member.store.commitFanoutFailure(a), errFanoutStaleAttempt, "late A failure")
			unchanged()
			require.ErrorIs(t, f.member.store.commitFanoutAccepted(a, time.Now().Unix()), errFanoutStaleAttempt, "late A ACK")
			unchanged()
			require.Error(t, f.member.store.startFanoutAttempt(a, 1, time.Now().Unix()), "late A start must not be accepted")
			unchanged()
		})
	}
}

// Reverse order: A committed O3 but crashed before T4. B's reservation must
// replay A's T4 first; when that exhausts the budget, B cannot reserve.
func TestReservationReplaysCrashedO3AndRefusesWhenExhausted(t *testing.T) {
	f := newFailureFixture(t)
	for range 2 {
		require.NoError(t, f.member.store.commitFanoutFailure(f.startAttempt(t, 1)))
	}
	a := f.startAttempt(t, 1)
	f.failO3BeforeT4(t, a) // third failure: O3 durable (exhausting), T4 lost
	require.Equal(t, string(RecipientQueued), f.row(t).State)

	_, err := f.member.store.reserveFanoutAttempt(f.key, f.recipient)
	require.Error(t, err, "B must not reserve on an exhausted budget")
	require.NotErrorIs(t, err, ErrFanoutBookkeepingConflict, "A's exact record is replayed, not held as a conflict")
	row := f.row(t)
	require.Equal(t, string(RecipientFailed), row.State, "A's T4 was replayed before B's reservation was judged")
	require.EqualValues(t, 3, row.RetryCount)
	require.Equal(t, a.generation, row.LastResultGeneration)
	require.Equal(t, "retry_exhausted", row.Issue)
	entry := f.outboxEntry(t)
	require.Equal(t, "group_failed", entry.Entry.Status)
}

// A finished failure cannot be flipped to ACK by another callback of the same
// token, and a token from a superseded epoch is stale after D6.
func TestFinishedResultsAreFinalPerToken(t *testing.T) {
	f := newFailureFixture(t)
	a := f.startAttempt(t, 1)
	require.NoError(t, f.member.store.commitFanoutFailure(a))
	unchanged := f.snapshot(t)
	require.ErrorIs(t, f.member.store.commitFanoutAccepted(a, time.Now().Unix()), errFanoutStaleAttempt)
	require.ErrorIs(t, f.member.store.commitFanoutFailure(a), errFanoutStaleAttempt)
	unchanged()

	f2 := newFailureFixture(t)
	f2.exhaust(t)
	stale := fanoutAttemptToken{key: f2.key, recipient: f2.recipient, eventID: f2.eventID, queueID: f2.queueID, generation: 3}
	_, err := f2.member.store.authorizeFanoutRecovery(f2.key, f2.recipient, time.Now().Unix())
	require.NoError(t, err)
	unchanged = f2.snapshot(t)
	require.ErrorIs(t, f2.member.store.commitFanoutAccepted(stale, time.Now().Unix()), errFanoutStaleAttempt, "old epoch ACK after D6")
	require.ErrorIs(t, f2.member.store.commitFanoutFailure(stale), errFanoutStaleAttempt, "old epoch failure after D6")
	unchanged()
}

package groupchat

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Pending O3 is the case where entering fanoutBarrierTx replays T4.
// Empty or divergent caller bytes must return before that entry.
func TestReserveEmptyCallerBytesDoesNotReachBarrier(t *testing.T) {
	f := newFailureFixture(t)
	f.failO3BeforeT4(t, f.startAttempt(t, 1))
	unchanged := f.snapshot(t)

	calls := 0
	observeFanoutBarrier = func() { calls++ }
	t.Cleanup(func() { observeFanoutBarrier = nil })

	_, err := f.member.store.reserveFanoutAttempt(f.key, f.recipient)
	require.ErrorIs(t, err, errFanoutPayloadMismatch)
	require.Zero(t, calls, "empty caller bytes reached the barrier")
	unchanged()

	_, err = f.member.store.reserveFanoutAttemptFrozen(f.key, f.recipient, "not-the-frozen-event")
	require.ErrorIs(t, err, errFanoutPayloadMismatch)
	require.Zero(t, calls, "mismatched caller bytes reached the barrier")
	unchanged()

	token, err := f.reserveBound()
	require.NoError(t, err)
	require.EqualValues(t, 2, token.generation)
	require.EqualValues(t, 1, f.row(t).RetryCount, "bound caller bytes still replay before reserve")
	require.Equal(t, 1, calls)
}

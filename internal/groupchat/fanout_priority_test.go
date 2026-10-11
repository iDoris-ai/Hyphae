package groupchat

import (
	"bytes"
	"errors"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// V5: rev7 §3.5 first-match order. Each scenario stacks an unconsumed O3
// record (which alone would be replayed) under an earlier-ranked condition
// and asserts the earlier class wins with zero writes to either store:
//
//	file corrupt → duplicate EventID → queue conflict (D14) → bookkeeping
//	conflict → unconsumed credential projection → normal.
func TestBarrierClassificationOrder(t *testing.T) {
	type scenario struct {
		arrange func(t *testing.T, f failureFixture)
		check   func(t *testing.T, err error)
	}
	scenarios := map[string]scenario{
		"1 file corrupt beats projection": {
			arrange: func(t *testing.T, f failureFixture) {
				data, err := os.ReadFile(f.outboxPath)
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(f.outboxPath, append(data, []byte("{}")...), 0600))
			},
			check: func(t *testing.T, err error) {
				require.Error(t, err)
				require.NotErrorIs(t, err, ErrFanoutBookkeepingConflict)
				require.NotErrorIs(t, err, ErrFanoutQueueConflict)
				require.ErrorContains(t, err, "trailing")
			},
		},
		"2 duplicate EventID beats projection": {
			arrange: func(t *testing.T, f failureFixture) {
				data, err := os.ReadFile(f.outboxPath)
				require.NoError(t, err)
				open := bytes.Index(data, []byte("{"))
				start := bytes.Index(data[open+1:], []byte("{")) + open + 1
				end := bytes.LastIndex(data, []byte("}"))
				end = bytes.LastIndex(data[:end], []byte("}")) + 1
				entry := data[start:end]
				dup := append(append(append([]byte{}, data[:end]...), []byte(",\n")...), entry...)
				dup = append(dup, data[end:]...)
				require.NoError(t, os.WriteFile(f.outboxPath, dup, 0600))
			},
			check: func(t *testing.T, err error) {
				require.Error(t, err)
				require.NotErrorIs(t, err, ErrFanoutBookkeepingConflict)
				require.ErrorContains(t, err, "duplicate")
			},
		},
		"3 queue conflict (D14) beats projection": {
			arrange: func(t *testing.T, f failureFixture) {
				_, err := f.member.db.Exec(`UPDATE groupchat_fanout SET queue_id='ffffffffffffffffffffffffffffffff'`)
				require.NoError(t, err)
			},
			check: func(t *testing.T, err error) { require.ErrorIs(t, err, ErrFanoutQueueConflict) },
		},
		"4 bookkeeping conflict beats projection": {
			arrange: func(t *testing.T, f failureFixture) {
				_, err := f.member.db.Exec(`UPDATE groupchat_fanout SET attempts=attempts+1`)
				require.NoError(t, err)
			},
			check: func(t *testing.T, err error) { require.ErrorIs(t, err, ErrFanoutBookkeepingConflict) },
		},
	}
	for name, sc := range scenarios {
		t.Run(name, func(t *testing.T) {
			f := newFailureFixture(t)
			f.failO3BeforeT4(t, f.startAttempt(t, 1))
			sc.arrange(t, f)
			unchanged := f.snapshot(t)
			_, err := f.member.store.reserveFanoutAttempt(f.key, f.recipient)
			sc.check(t, err)
			unchanged()
			replayed, rerr := f.member.store.recoverFanoutFailure(f.key, f.recipient)
			require.False(t, replayed, "explicit recovery must respect the same order")
			if errors.Is(err, ErrFanoutQueueConflict) {
				require.ErrorIs(t, rerr, ErrFanoutQueueConflict)
			} else {
				require.Error(t, rerr)
			}
			unchanged()
		})
	}

	t.Run("5 projection then normal", func(t *testing.T) {
		f := newFailureFixture(t)
		f.failO3BeforeT4(t, f.startAttempt(t, 1))
		token, err := f.member.store.reserveFanoutAttempt(f.key, f.recipient)
		require.NoError(t, err)
		require.EqualValues(t, 2, token.generation)
		require.EqualValues(t, 1, f.row(t).RetryCount, "the unconsumed record was projected before the reservation")
	})
}

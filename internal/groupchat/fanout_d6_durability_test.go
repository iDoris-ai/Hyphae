package groupchat

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestD6ReplayRequiresDirectoryDurability verifies rev7 §3.3 L315 and §3.4:
// with directory durability unconfirmed, a barrier replay, explicit recover,
// or authorizeFanoutRecovery of an unconsumed D6 witness must not project T2'
// into SQLite or alter the durable outbox. Once durability is restored,
// replay succeeds exactly once.
func TestD6ReplayRequiresDirectoryDurability(t *testing.T) {
	cases := []struct {
		name string
		op   func(f failureFixture) error
	}{
		{
			name: "barrier R",
			op: func(f failureFixture) error {
				_, err := f.member.store.reserveFanoutAttempt(f.key, f.recipient)
				return err
			},
		},
		{
			name: "recoverFanoutRecovery",
			op: func(f failureFixture) error {
				_, err := f.member.store.recoverFanoutRecovery(f.key, f.recipient)
				return err
			},
		},
		{
			name: "authorizeFanoutRecovery",
			op: func(f failureFixture) error {
				_, err := f.member.store.authorizeFanoutRecovery(f.key, f.recipient, time.Now().Unix())
				return err
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFailureFixture(t)
			newQueue := f.crashD6BeforeT2(t)

			outboxBefore, err := os.ReadFile(f.outboxPath)
			require.NoError(t, err)
			rowBefore := f.row(t)

			// Step 1: Injected dir sync failure -> fail Pending, DB/outbox unchanged
			orig := confirmOutboxDurable
			calls := 0
			confirmOutboxDurable = func(string) error {
				calls++
				return errors.New("injected directory fsync failure")
			}
			t.Cleanup(func() { confirmOutboxDurable = orig })

			err = tc.op(f)
			require.Error(t, err, "T2' must not be projected while directory durability is unconfirmed")
			require.ErrorIs(t, err, ErrFanoutRecoveryPending, "must fail with ErrFanoutRecoveryPending")
			require.GreaterOrEqual(t, calls, 1, "confirmOutboxDurable must be invoked")

			// Verify DB and outbox unchanged
			require.Equal(t, string(RecipientFailed), f.row(t).State)
			require.Equal(t, rowBefore, f.row(t), "SQLite row must be untouched on durability failure")

			outboxAfter, err := os.ReadFile(f.outboxPath)
			require.NoError(t, err)
			require.Equal(t, outboxBefore, outboxAfter, "outbox file must be untouched on durability failure")

			// Step 2: Restore hook -> exact-once replay succeeds
			confirmOutboxDurable = orig
			err = tc.op(f)
			require.NoError(t, err, "replay must succeed after durability confirmation is restored")

			rowAfter := f.row(t)
			require.Equal(t, string(RecipientQueued), rowAfter.State)
			require.Equal(t, newQueue, rowAfter.RetryQueueID)
			require.Equal(t, int64(0), rowAfter.RetryCount)
		})
	}
}

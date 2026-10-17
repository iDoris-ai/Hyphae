package groupchat

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/iDoris-ai/hyphae/internal/messaging"
	"github.com/stretchr/testify/require"
)

// TestD6ReplayRequiresDirectoryDurability verifies rev7 §3.3 L315 and §3.4:
// with directory durability unconfirmed, a barrier replay, explicit recover,
// or authorizeFanoutRecovery of an unconsumed D6 witness must not project T2'
// into SQLite or alter the durable outbox. Once durability is restored,
// replay succeeds exactly once, and repeat refusal / no-replay leaves
// DB and outbox unchanged.
func TestD6ReplayRequiresDirectoryDurability(t *testing.T) {
	cases := []struct {
		name        string
		op          func(f failureFixture, newQueue string) error
		expectPhase string
		revDelta    int64
		genDelta    int64
		repeat      func(t *testing.T, f failureFixture, newQueue string, rowAfter recoveryRow)
	}{
		{
			name: "barrier R",
			op: func(f failureFixture, newQueue string) error {
				token, err := f.member.store.reserveFanoutAttempt(f.key, f.recipient)
				if err != nil {
					return err
				}
				require.Equal(t, newQueue, token.queueID)
				return nil
			},
			expectPhase: "reserved",
			revDelta:    2,
			genDelta:    1,
			repeat: func(t *testing.T, f failureFixture, newQueue string, rowAfter recoveryRow) {
				stale := fanoutAttemptToken{key: f.key, recipient: f.recipient, queueID: "stale-queue", generation: 0}
				unchanged := f.snapshot(t)
				err := f.member.store.startFanoutAttempt(stale, 2, time.Now().Unix())
				require.ErrorIs(t, err, errFanoutStaleAttempt, "stale attempt behind barrier must be refused")
				unchanged()

				afterRepeat := f.recovery(t)
				require.Equal(t, rowAfter.Generation, afterRepeat.Generation, "generation unchanged after repeat")
				require.Equal(t, rowAfter.RecoveryID, afterRepeat.RecoveryID, "witness nonce unchanged after repeat")
				require.Equal(t, rowAfter.RecoveryDigest, afterRepeat.RecoveryDigest, "witness digest unchanged after repeat")
				require.Equal(t, rowAfter.QueueID, afterRepeat.QueueID, "queue ID unchanged after repeat")
			},
		},
		{
			name: "recoverFanoutRecovery",
			op: func(f failureFixture, newQueue string) error {
				replayed, err := f.member.store.recoverFanoutRecovery(f.key, f.recipient)
				if err != nil {
					return err
				}
				require.True(t, replayed, "first recovery replay must report replayed=true")
				return nil
			},
			expectPhase: "idle",
			revDelta:    1,
			genDelta:    0,
			repeat: func(t *testing.T, f failureFixture, newQueue string, rowAfter recoveryRow) {
				unchanged := f.snapshot(t)
				replayed, err := f.member.store.recoverFanoutRecovery(f.key, f.recipient)
				require.NoError(t, err, "repeat recoverFanoutRecovery must succeed with NoError")
				require.False(t, replayed, "consumed witness must report replayed=false")
				unchanged()

				afterRepeat := f.recovery(t)
				require.Equal(t, rowAfter.Generation, afterRepeat.Generation, "generation unchanged after repeat")
				require.Equal(t, rowAfter.RecoveryID, afterRepeat.RecoveryID, "witness nonce unchanged after repeat")
				require.Equal(t, rowAfter.RecoveryDigest, afterRepeat.RecoveryDigest, "witness digest unchanged after repeat")
				require.Equal(t, rowAfter.QueueID, afterRepeat.QueueID, "queue ID unchanged after repeat")

				unchanged3 := f.snapshot(t)
				replayed, err = f.member.store.recoverFanoutRecovery(f.key, f.recipient)
				require.NoError(t, err)
				require.False(t, replayed)
				unchanged3()

				token, err := f.member.store.reserveFanoutAttempt(f.key, f.recipient)
				require.NoError(t, err)
				require.Equal(t, newQueue, token.queueID)
			},
		},
		{
			name: "authorizeFanoutRecovery",
			op: func(f failureFixture, newQueue string) error {
				gotQueue, err := f.member.store.authorizeFanoutRecovery(f.key, f.recipient, time.Now().Unix())
				if err != nil {
					return err
				}
				require.Equal(t, newQueue, gotQueue, "authorize reports existing pending epoch queue")
				return nil
			},
			expectPhase: "idle",
			revDelta:    1,
			genDelta:    0,
			repeat: func(t *testing.T, f failureFixture, newQueue string, rowAfter recoveryRow) {
				unchanged := f.snapshot(t)
				_, err := f.member.store.authorizeFanoutRecovery(f.key, f.recipient, time.Now().Unix())
				require.Error(t, err, "repeat authorizeFanoutRecovery must be refused when row is queued")
				unchanged()

				afterRepeat := f.recovery(t)
				require.Equal(t, rowAfter.Generation, afterRepeat.Generation, "generation unchanged after repeat")
				require.Equal(t, rowAfter.RecoveryID, afterRepeat.RecoveryID, "witness nonce unchanged after repeat")
				require.Equal(t, rowAfter.RecoveryDigest, afterRepeat.RecoveryDigest, "witness digest unchanged after repeat")
				require.Equal(t, rowAfter.QueueID, afterRepeat.QueueID, "queue ID unchanged after repeat")

				unchanged3 := f.snapshot(t)
				_, err = f.member.store.authorizeFanoutRecovery(f.key, f.recipient, time.Now().Unix())
				require.Error(t, err)
				unchanged3()

				token, err := f.member.store.reserveFanoutAttempt(f.key, f.recipient)
				require.NoError(t, err)
				require.Equal(t, newQueue, token.queueID)
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
			recoveryBefore := f.recovery(t)

			// Step 1: Injected dir sync failure -> fail Pending, DB/outbox unchanged
			orig := confirmOutboxDurable
			calls := 0
			confirmOutboxDurable = func(string) error {
				calls++
				return errors.New("injected directory fsync failure")
			}
			t.Cleanup(func() { confirmOutboxDurable = orig })

			unchangedFail := f.snapshot(t)
			err = tc.op(f, newQueue)
			require.Error(t, err, "T2' must not be projected while directory durability is unconfirmed")
			require.ErrorIs(t, err, ErrFanoutRecoveryPending, "must fail with ErrFanoutRecoveryPending")
			require.GreaterOrEqual(t, calls, 1, "confirmOutboxDurable must be invoked")
			unchangedFail()

			// Verify DB and outbox unchanged
			require.Equal(t, string(RecipientFailed), f.row(t).State)
			require.Equal(t, rowBefore, f.row(t), "SQLite row must be untouched on durability failure")

			outboxAfter, err := os.ReadFile(f.outboxPath)
			require.NoError(t, err)
			require.Equal(t, outboxBefore, outboxAfter, "outbox file must be untouched on durability failure")

			// Step 2: Restore hook -> exact-once replay succeeds
			confirmOutboxDurable = orig
			err = tc.op(f, newQueue)
			require.NoError(t, err, "replay must succeed after durability confirmation is restored")

			entry := f.outboxEntry(t)
			require.NotNil(t, entry.RecoveryWitness)
			witnessDigest := messaging.OutboxWitnessDigest(entry)

			rowAfter := f.recovery(t)
			require.Equal(t, string(RecipientQueued), rowAfter.State)
			require.Equal(t, newQueue, rowAfter.QueueID)
			require.Equal(t, newQueue, rowAfter.RetryQueueID)
			require.EqualValues(t, 0, rowAfter.RetryCount)
			require.Equal(t, tc.expectPhase, rowAfter.Phase)
			require.Equal(t, entry.RecoveryWitness.RecoveryID, rowAfter.RecoveryID, "witness nonce must be recorded")
			require.Equal(t, witnessDigest, rowAfter.RecoveryDigest, "witness digest must match")
			require.NotEmpty(t, rowAfter.RecoveryDigest)
			require.Equal(t, recoveryBefore.Revision+tc.revDelta, rowAfter.Revision, "revision delta")
			require.Equal(t, recoveryBefore.Generation+tc.genDelta, rowAfter.Generation, "generation delta")
			require.Equal(t, recoveryBefore.Attempts, rowAfter.Attempts, "attempts unchanged")
			require.Equal(t, recoveryBefore.Watermark, rowAfter.Watermark, "watermark unchanged")
			require.Equal(t, recoveryBefore.Receipt, rowAfter.Receipt, "receipt unchanged")

			outboxAfterReplay, err := os.ReadFile(f.outboxPath)
			require.NoError(t, err)
			require.Equal(t, string(outboxBefore), string(outboxAfterReplay), "T2' replay must preserve D6 bytes / no duplicate D6 written")

			// Step 3: Case-appropriate repeat refusal / no-replay
			tc.repeat(t, f, newQueue, rowAfter)

			outboxAfterRepeat, err := os.ReadFile(f.outboxPath)
			require.NoError(t, err)
			require.Equal(t, string(outboxBefore), string(outboxAfterRepeat), "no new D6 written after repeat")
		})
	}
}

// S for the old epoch must refuse publishing while retaining the D6 projection
// performed by its barrier. A second pass must not reset the new epoch again.
func TestFanoutBarrierStaleStartKeepsD6Replay(t *testing.T) {
	f := newFailureFixture(t)
	old := f.startAttempt(t, 2)
	newQueue := f.crashD6BeforeT2(t)
	before := f.recovery(t)
	fileBefore, err := os.ReadFile(f.outboxPath)
	require.NoError(t, err)

	require.ErrorIs(t, f.member.store.startFanoutAttempt(old, 2, time.Now().Unix()), errFanoutStaleAttempt)
	after := f.recovery(t)
	require.Equal(t, string(RecipientQueued), after.State)
	require.Equal(t, newQueue, after.QueueID)
	require.Equal(t, newQueue, after.RetryQueueID)
	require.Equal(t, "idle", after.Phase)
	require.EqualValues(t, 0, after.RetryCount)
	require.Equal(t, before.Revision+1, after.Revision)
	require.Equal(t, before.Generation, after.Generation)
	require.Equal(t, before.Attempts, after.Attempts, "refused S must not count a publish")
	require.Equal(t, before.Watermark, after.Watermark)
	require.Equal(t, before.Receipt, after.Receipt)
	require.NotEmpty(t, after.RecoveryDigest)
	fileAfter, err := os.ReadFile(f.outboxPath)
	require.NoError(t, err)
	require.Equal(t, fileBefore, fileAfter, "T2' replay must preserve D6 bytes")

	unchanged := f.snapshot(t)
	require.ErrorIs(t, f.member.store.startFanoutAttempt(old, 2, time.Now().Unix()), errFanoutStaleAttempt)
	unchanged()
	token, err := f.member.store.reserveFanoutAttempt(f.key, f.recipient)
	require.NoError(t, err)
	require.Equal(t, newQueue, token.queueID)
}

func TestFanoutBarrierD6WitnessCannotReplaceNonFailedEpoch(t *testing.T) {
	for _, state := range []RecipientDeliveryState{RecipientQueued, RecipientRelayAccepted} {
		t.Run(string(state), func(t *testing.T) {
			f := newFailureFixture(t)
			f.crashD6BeforeT2(t)
			_, err := f.member.db.Exec(`UPDATE groupchat_fanout SET state=? WHERE event_id=?`, state, f.eventID)
			require.NoError(t, err)
			unchanged := f.snapshot(t)
			_, err = f.member.store.reserveFanoutAttempt(f.key, f.recipient)
			require.ErrorIs(t, err, ErrFanoutQueueConflict)
			unchanged()
		})
	}
}

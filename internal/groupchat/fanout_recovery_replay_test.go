package groupchat

import (
	"bytes"
	"os"
	"testing"
	"time"

	"github.com/iDoris-ai/hyphae/internal/messaging"
	"github.com/stretchr/testify/require"
)

// crashD6BeforeT2 commits D6 for the exhausted fixture while an injected
// trigger aborts T2', leaving the new epoch's witness unconsumed. Returns the
// new queue id written to the outbox.
func (f failureFixture) crashD6BeforeT2(t *testing.T) string {
	t.Helper()
	f.exhaust(t)
	_, err := f.member.db.Exec(`CREATE TRIGGER inject_t2_failure BEFORE UPDATE OF last_recovery_id ON groupchat_fanout
        BEGIN SELECT RAISE(ABORT, 'injected T2 prime failure'); END`)
	require.NoError(t, err)
	_, err = f.member.store.authorizeFanoutRecovery(f.key, f.recipient, 1_800_000_000)
	require.ErrorIs(t, err, ErrFanoutRecoveryPending)
	_, err = f.member.db.Exec(`DROP TRIGGER inject_t2_failure`)
	require.NoError(t, err)
	entry := f.outboxEntry(t)
	require.NotNil(t, entry.RecoveryWitness)
	require.Equal(t, string(RecipientFailed), f.row(t).State)
	return entry.QueueID
}

func TestRecoverFanoutRecoveryReplaysT2PrimeOnce(t *testing.T) {
	f := newFailureFixture(t)
	newQueue := f.crashD6BeforeT2(t)
	before := f.recovery(t)
	entry := f.outboxEntry(t)

	replayed, err := f.member.store.recoverFanoutRecovery(f.key, f.recipient)
	require.NoError(t, err)
	require.True(t, replayed)
	after := f.recovery(t)
	require.Equal(t, string(RecipientQueued), after.State)
	require.Equal(t, newQueue, after.QueueID)
	require.Equal(t, newQueue, after.RetryQueueID)
	require.EqualValues(t, 0, after.RetryCount)
	require.Equal(t, "idle", after.Phase)
	require.Equal(t, entry.RecoveryWitness.RecoveryID, after.RecoveryID)
	require.Equal(t, messaging.OutboxWitnessDigest(entry), after.RecoveryDigest)
	require.Equal(t, before.Revision+1, after.Revision)
	require.Equal(t, before.Generation, after.Generation)
	require.Equal(t, before.Receipt, after.Receipt)

	unchanged := f.snapshot(t)
	replayed, err = f.member.store.recoverFanoutRecovery(f.key, f.recipient)
	require.NoError(t, err)
	require.False(t, replayed, "consumed witness is a no-op")
	unchanged()

	token, err := f.reserveBound()
	require.NoError(t, err)
	require.Equal(t, newQueue, token.queueID)
}

func TestFanoutBarrierReplaysCrashedD6(t *testing.T) {
	f := newFailureFixture(t)
	newQueue := f.crashD6BeforeT2(t)
	// R behind the barrier replays T2' and reserves in the same locked pass.
	token, err := f.reserveBound()
	require.NoError(t, err)
	require.Equal(t, newQueue, token.queueID)
	row := f.recovery(t)
	require.Equal(t, newQueue, row.QueueID)
	require.Equal(t, "reserved", row.Phase)
	require.EqualValues(t, 0, row.RetryCount)
}

func TestAuthorizeFanoutRecoveryReplaysInsteadOfSecondD6(t *testing.T) {
	f := newFailureFixture(t)
	newQueue := f.crashD6BeforeT2(t)
	fileBefore, err := os.ReadFile(f.outboxPath)
	require.NoError(t, err)
	got, err := f.member.store.authorizeFanoutRecovery(f.key, f.recipient, time.Now().Unix())
	require.NoError(t, err)
	require.Equal(t, newQueue, got, "the pending epoch is reported, not a new one")
	fileAfter, err := os.ReadFile(f.outboxPath)
	require.NoError(t, err)
	require.Equal(t, string(fileBefore), string(fileAfter), "no second D6")
	require.Equal(t, string(RecipientQueued), f.row(t).State)
}

func TestRecoverFanoutRecoveryNoopCases(t *testing.T) {
	f := newFailureFixture(t)
	unchanged := f.snapshot(t)
	replayed, err := f.member.store.recoverFanoutRecovery(f.key, f.recipient)
	require.NoError(t, err)
	require.False(t, replayed, "queued row without witness")
	unchanged()

	f.exhaust(t)
	unchanged = f.snapshot(t)
	replayed, err = f.member.store.recoverFanoutRecovery(f.key, f.recipient)
	require.NoError(t, err)
	require.False(t, replayed, "failed row with its own exhausted entry")
	unchanged()
}

func TestRecoverFanoutRecoveryHoldsConflicts(t *testing.T) {
	for name, tamper := range map[string]func(t *testing.T, f failureFixture){
		"revision moved": func(t *testing.T, f failureFixture) {
			_, err := f.member.db.Exec(`UPDATE groupchat_fanout SET row_revision=row_revision+1`)
			require.NoError(t, err)
		},
		"generation moved": func(t *testing.T, f failureFixture) {
			_, err := f.member.db.Exec(`UPDATE groupchat_fanout SET attempt_generation=attempt_generation+1`)
			require.NoError(t, err)
		},
		"old queue differs": func(t *testing.T, f failureFixture) {
			_, err := f.member.db.Exec(`UPDATE groupchat_fanout SET queue_id='ffffffffffffffffffffffffffffffff'`)
			require.NoError(t, err)
		},
		"max_retries differs": func(t *testing.T, f failureFixture) {
			_, err := f.member.db.Exec(`UPDATE groupchat_fanout SET max_retries=4`)
			require.NoError(t, err)
		},
		"new entry already attempted": func(t *testing.T, f failureFixture) {
			data, err := os.ReadFile(f.outboxPath)
			require.NoError(t, err)
			edited := bytes.Replace(data, []byte(`"retry_count":0,`), []byte(`"retry_count":1,`), 1)
			require.NotEqual(t, data, edited)
			require.NoError(t, os.WriteFile(f.outboxPath, edited, 0600))
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFailureFixture(t)
			f.crashD6BeforeT2(t)
			tamper(t, f)
			unchanged := f.snapshot(t)
			replayed, err := f.member.store.recoverFanoutRecovery(f.key, f.recipient)
			require.ErrorIs(t, err, ErrFanoutBookkeepingConflict)
			require.False(t, replayed)
			_, err = f.reserveBound()
			require.ErrorIs(t, err, ErrFanoutBookkeepingConflict, "R holds on the same conflict")
			unchanged()
		})
	}
}

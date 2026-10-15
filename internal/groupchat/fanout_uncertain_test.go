package groupchat

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/iDoris-ai/hyphae/internal/messaging"
	"github.com/stretchr/testify/require"
)

// uncertainAfterWrite performs the real raw commit, then reports it as
// commit-uncertain (the directory fsync "failed" after the rename).
func uncertainAfterWrite(t *testing.T) {
	t.Helper()
	f, r := commitGroupFailureLocked, commitGroupRecoveryLocked
	commitGroupFailureLocked = func(path string, rec messaging.GroupFailureRecord) (string, error) {
		if _, err := f(path, rec); err != nil {
			return "", err
		}
		return "", messaging.MarkOutboxCommitUncertain(errors.New("injected: directory fsync failed after rename"))
	}
	commitGroupRecoveryLocked = func(path string, rec messaging.GroupRecoveryRecord) (string, error) {
		if _, err := r(path, rec); err != nil {
			return "", err
		}
		return "", messaging.MarkOutboxCommitUncertain(errors.New("injected: directory fsync failed after rename"))
	}
	t.Cleanup(func() { commitGroupFailureLocked, commitGroupRecoveryLocked = f, r })
}

// uncertainWithoutWrite reports commit-uncertain while the old snapshot is
// in fact what survived (rename never became visible).
func uncertainWithoutWrite(t *testing.T) {
	t.Helper()
	f, r := commitGroupFailureLocked, commitGroupRecoveryLocked
	commitGroupFailureLocked = func(string, messaging.GroupFailureRecord) (string, error) {
		return "", messaging.MarkOutboxCommitUncertain(errors.New("injected: lost rename"))
	}
	commitGroupRecoveryLocked = func(string, messaging.GroupRecoveryRecord) (string, error) {
		return "", messaging.MarkOutboxCommitUncertain(errors.New("injected: lost rename"))
	}
	t.Cleanup(func() { commitGroupFailureLocked, commitGroupRecoveryLocked = f, r })
}

func TestUncertainO3IsConfirmedAndProjected(t *testing.T) {
	f := newFailureFixture(t)
	token := f.startAttempt(t, 1)
	uncertainAfterWrite(t)
	require.NoError(t, f.member.store.commitFanoutFailure(token), "confirm finds the exact record and T4 proceeds")
	entry := f.outboxEntry(t)
	row := f.row(t)
	require.EqualValues(t, 1, row.RetryCount)
	require.Equal(t, "failed", row.Phase)
	require.Equal(t, messaging.FailureReceipt(entry.FailureResult.ResultID, sha256Hex(entry.Fields["failure_result"])), row.Receipt)
}

func TestUncertainO3WithoutDurableRecordIsPending(t *testing.T) {
	f := newFailureFixture(t)
	token := f.startAttempt(t, 1)
	uncertainWithoutWrite(t)
	unchanged := f.snapshot(t)
	err := f.member.store.commitFanoutFailure(token)
	require.ErrorIs(t, err, ErrFanoutBookkeepingPending)
	require.ErrorContains(t, err, "no durable evidence")
	unchanged()
	require.Equal(t, "started", f.row(t).Phase, "outcome unknown: no RetryCount guess, no second O3")
}

func TestUncertainD6IsConfirmedAndProjected(t *testing.T) {
	f := newFailureFixture(t)
	f.exhaust(t)
	uncertainAfterWrite(t)
	newQueue, err := f.member.store.authorizeFanoutRecovery(f.key, f.recipient, time.Now().Unix())
	require.NoError(t, err)
	entry := f.outboxEntry(t)
	require.Equal(t, newQueue, entry.QueueID)
	row := f.recovery(t)
	require.Equal(t, string(RecipientQueued), row.State)
	require.Equal(t, messaging.OutboxWitnessDigest(entry), row.RecoveryDigest)
}

func TestUncertainD6WithoutDurableEpochIsPending(t *testing.T) {
	f := newFailureFixture(t)
	f.exhaust(t)
	uncertainWithoutWrite(t)
	unchanged := f.snapshot(t)
	_, err := f.member.store.authorizeFanoutRecovery(f.key, f.recipient, time.Now().Unix())
	require.ErrorIs(t, err, ErrFanoutRecoveryPending)
	require.ErrorContains(t, err, "no durable evidence")
	unchanged()
	data, err := os.ReadFile(f.outboxPath)
	require.NoError(t, err)
	require.NotContains(t, string(data), "recovery_witness")
}

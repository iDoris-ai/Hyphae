package groupchat

import (
	"errors"
	"testing"

	"github.com/iDoris-ai/hyphae/internal/messaging"
	"github.com/stretchr/testify/require"
)

// F2 (PR244 clestons review): the commit-uncertain O3 branch in
// commitFanoutFailure (IsOutboxCommitUncertain -> confirmFanoutFailureCommit)
// had no test driving it, and confirmFanoutFailureCommit called
// messaging.ConfirmOutboxDurable directly instead of the confirmOutboxDurable
// hook the barrier uses. These tests drive commitFanoutFailure genuinely
// uncertain end-to-end and assert the hook is the one consulted.

// TestCommitFanoutFailureUncertainRecordPresentProjectsT4 is the
// record-present case: the real O3 write lands, is merely reported
// commit-uncertain (the post-rename directory-fsync class), and
// confirmFanoutFailureCommit's confirm+reread finds the exact record and lets
// T4 proceed.
func TestCommitFanoutFailureUncertainRecordPresentProjectsT4(t *testing.T) {
	f := newFailureFixture(t)
	token := f.startAttempt(t, 2)

	origCommit := commitGroupFailureLocked
	t.Cleanup(func() { commitGroupFailureLocked = origCommit })
	commitGroupFailureLocked = func(path string, rec messaging.GroupFailureRecord) (string, error) {
		receipt, err := origCommit(path, rec)
		if err != nil {
			return receipt, err
		}
		// The write genuinely landed; only its durability confirmation is
		// uncertain, exactly as a post-rename directory-fsync failure is.
		return "", messaging.WrapOutboxCommitUncertain(errors.New("injected post-rename directory fsync failure"))
	}
	origConfirm := confirmOutboxDurable
	t.Cleanup(func() { confirmOutboxDurable = origConfirm })
	confirmCalls := 0
	confirmOutboxDurable = func(path string) error {
		confirmCalls++
		return origConfirm(path)
	}

	require.NoError(t, f.member.store.commitFanoutFailure(token))
	require.Equal(t, 1, confirmCalls, "confirmFanoutFailureCommit must go through the confirmOutboxDurable hook")

	entry := f.outboxEntry(t)
	require.NotNil(t, entry.FailureResult)
	require.EqualValues(t, 1, entry.Entry.RetryCount)
	after := f.row(t)
	require.EqualValues(t, 1, after.RetryCount)
	require.Equal(t, "failed", after.Phase)
	require.Equal(t, messaging.FailureReceipt(entry.FailureResult.ResultID, sha256Hex(entry.Fields["failure_result"])), after.Receipt)
}

// TestCommitFanoutFailureUncertainOldSnapshotHoldsPending is the old-snapshot
// case: the O3 write never actually landed (the outbox still holds the prior
// consumed snapshot) but is reported commit-uncertain. confirmFanoutFailureCommit
// must find no durable evidence of this attempt's outcome and hold both
// stores unchanged rather than mistaking the stale snapshot for success.
func TestCommitFanoutFailureUncertainOldSnapshotHoldsPending(t *testing.T) {
	f := newFailureFixture(t)
	require.NoError(t, f.member.store.commitFanoutFailure(f.startAttempt(t, 1)))
	require.EqualValues(t, 1, f.row(t).RetryCount)

	token := f.startAttempt(t, 1)
	origCommit := commitGroupFailureLocked
	t.Cleanup(func() { commitGroupFailureLocked = origCommit })
	commitGroupFailureLocked = func(path string, rec messaging.GroupFailureRecord) (string, error) {
		// No real write happens: the outbox stays at the prior round's
		// consumed snapshot.
		return "", messaging.WrapOutboxCommitUncertain(errors.New("injected pre-rename uncertainty; write never landed"))
	}

	unchanged := f.snapshot(t)
	err := f.member.store.commitFanoutFailure(token)
	require.ErrorIs(t, err, ErrFanoutBookkeepingPending)
	require.ErrorContains(t, err, "old snapshot")
	unchanged()
}

package groupchat

import (
	"os"
	"testing"
	"time"

	"github.com/iDoris-ai/hyphae/internal/messaging"
	"github.com/stretchr/testify/require"
)

type acceptedRow struct {
	State, Phase, Issue              string
	RelayAcks, AcceptedAt, Watermark int64
}

func (f failureFixture) accepted(t *testing.T) acceptedRow {
	t.Helper()
	var r acceptedRow
	require.NoError(t, f.member.db.QueryRow(`SELECT state,attempt_phase,issue,relay_acks,accepted_at,last_result_generation FROM groupchat_fanout WHERE event_id=?`, f.eventID).
		Scan(&r.State, &r.Phase, &r.Issue, &r.RelayAcks, &r.AcceptedAt, &r.Watermark))
	return r
}

func TestCommitFanoutAcceptedT3ThenO2(t *testing.T) {
	f := newFailureFixture(t)
	// One failed round first so the entry carries a consumed failure_result.
	require.NoError(t, f.member.store.commitFanoutFailure(f.startAttempt(t, 1)))
	token := f.startAttempt(t, 2)
	before := f.row(t)
	now := time.Now().Unix()
	require.NoError(t, f.member.store.commitFanoutAccepted(token, now))

	got := f.accepted(t)
	require.Equal(t, string(RecipientRelayAccepted), got.State)
	require.Equal(t, "accepted", got.Phase)
	require.Equal(t, "", got.Issue)
	require.EqualValues(t, 1, got.RelayAcks)
	require.GreaterOrEqual(t, got.AcceptedAt, now)
	require.Equal(t, token.generation, got.Watermark)
	require.Equal(t, before.Revision+1, f.row(t).Revision)
	data, err := os.ReadFile(f.outboxPath)
	require.NoError(t, err)
	require.NotContains(t, string(data), f.eventID, "O2 removed the entry")
	evidence, err := messaging.ParseRawOutboxEvidence(data)
	require.NoError(t, err)
	require.Empty(t, evidence.Entries)

	// Late callbacks after acceptance are stale and write nothing.
	unchanged := f.snapshot(t)
	require.ErrorIs(t, f.member.store.commitFanoutAccepted(token, now), errFanoutStaleAttempt)
	require.ErrorIs(t, f.member.store.commitFanoutFailure(token), errFanoutStaleAttempt)
	unchanged()
}

func TestCommitFanoutAcceptedStaleAndConflicts(t *testing.T) {
	t.Run("superseded generation", func(t *testing.T) {
		f := newFailureFixture(t)
		old := f.startAttempt(t, 1)
		require.NoError(t, f.member.store.commitFanoutFailure(old))
		_ = f.startAttempt(t, 1)
		unchanged := f.snapshot(t)
		require.ErrorIs(t, f.member.store.commitFanoutAccepted(old, time.Now().Unix()), errFanoutStaleAttempt)
		unchanged()
	})
	t.Run("reserved not started", func(t *testing.T) {
		f := newFailureFixture(t)
		token, err := f.member.store.reserveFanoutAttempt(f.key, f.recipient)
		require.NoError(t, err)
		unchanged := f.snapshot(t)
		require.ErrorIs(t, f.member.store.commitFanoutAccepted(token, time.Now().Unix()), errFanoutStaleAttempt)
		unchanged()
	})
	t.Run("D14 queue conflict", func(t *testing.T) {
		f := newFailureFixture(t)
		token := f.startAttempt(t, 1)
		_, err := f.member.db.Exec(`UPDATE groupchat_fanout SET queue_id='ffffffffffffffffffffffffffffffff'`)
		require.NoError(t, err)
		token.queueID = "ffffffffffffffffffffffffffffffff"
		unchanged := f.snapshot(t)
		require.ErrorIs(t, f.member.store.commitFanoutAccepted(token, time.Now().Unix()), ErrFanoutQueueConflict)
		unchanged()
	})
	t.Run("unconsumed O3 for the same attempt", func(t *testing.T) {
		f := newFailureFixture(t)
		token := f.startAttempt(t, 1)
		f.failO3BeforeT4(t, token)
		unchanged := f.snapshot(t)
		require.ErrorIs(t, f.member.store.commitFanoutAccepted(token, time.Now().Unix()), ErrFanoutBookkeepingConflict)
		unchanged()
	})
}

func TestCommitFanoutAcceptedCleanupPending(t *testing.T) {
	f := newFailureFixture(t)
	token := f.startAttempt(t, 1)
	// The entry's max_retries no longer matches the frozen row: T3 commits,
	// O2 is refused and reported, and a later cleanup retry is also refused.
	_, err := f.member.db.Exec(`UPDATE groupchat_fanout SET max_retries=4`)
	require.NoError(t, err)
	outboxBefore, err := os.ReadFile(f.outboxPath)
	require.NoError(t, err)
	err = f.member.store.commitFanoutAccepted(token, time.Now().Unix())
	require.ErrorIs(t, err, ErrFanoutCleanupPending)
	require.Equal(t, string(RecipientRelayAccepted), f.accepted(t).State, "T3 is durable")
	outboxAfter, err := os.ReadFile(f.outboxPath)
	require.NoError(t, err)
	require.Equal(t, string(outboxBefore), string(outboxAfter))
	require.ErrorIs(t, f.member.store.cleanupAcceptedFanout(f.key, f.recipient), ErrFanoutCleanupPending)

	// Restoring the frozen value lets the precise O2 complete.
	_, err = f.member.db.Exec(`UPDATE groupchat_fanout SET max_retries=3`)
	require.NoError(t, err)
	require.NoError(t, f.member.store.cleanupAcceptedFanout(f.key, f.recipient))
	data, err := os.ReadFile(f.outboxPath)
	require.NoError(t, err)
	require.NotContains(t, string(data), f.eventID)
	require.NoError(t, f.member.store.cleanupAcceptedFanout(f.key, f.recipient), "repeat is a no-op")
}

func TestCleanupAcceptedFanoutRefusesNonAccepted(t *testing.T) {
	f := newFailureFixture(t)
	unchanged := f.snapshot(t)
	require.Error(t, f.member.store.cleanupAcceptedFanout(f.key, f.recipient))
	unchanged()
}

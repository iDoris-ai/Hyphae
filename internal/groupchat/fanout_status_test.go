package groupchat

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestInspectFanoutBookkeepingClasses(t *testing.T) {
	cases := map[string]struct {
		arrange func(t *testing.T, f failureFixture)
		want    FanoutBookkeeping
	}{
		"normal queued": {func(*testing.T, failureFixture) {}, BookkeepingNormal},
		"pending failure": {func(t *testing.T, f failureFixture) {
			f.failO3BeforeT4(t, f.startAttempt(t, 1))
		}, BookkeepingPendingFailure},
		"pending recovery": {func(t *testing.T, f failureFixture) {
			f.crashD6BeforeT2(t)
		}, BookkeepingPendingRecovery},
		"cleanup pending": {func(t *testing.T, f failureFixture) {
			token := f.startAttempt(t, 1)
			_, err := f.member.db.Exec(`UPDATE groupchat_fanout SET max_retries=4`)
			require.NoError(t, err)
			require.ErrorIs(t, f.member.store.commitFanoutAccepted(token, time.Now().Unix()), ErrFanoutCleanupPending)
		}, BookkeepingCleanupPending},
		"accepted and cleaned": {func(t *testing.T, f failureFixture) {
			require.NoError(t, f.member.store.commitFanoutAccepted(f.startAttempt(t, 1), time.Now().Unix()))
		}, BookkeepingNormal},
		"queue missing": {func(t *testing.T, f failureFixture) {
			require.NoError(t, os.WriteFile(f.outboxPath, []byte(`{"entries":[]}`), 0600))
		}, BookkeepingQueueMissing},
		"queue conflict": {func(t *testing.T, f failureFixture) {
			_, err := f.member.db.Exec(`UPDATE groupchat_fanout SET queue_id='ffffffffffffffffffffffffffffffff'`)
			require.NoError(t, err)
		}, BookkeepingQueueConflict},
		"bookkeeping conflict": {func(t *testing.T, f failureFixture) {
			f.failO3BeforeT4(t, f.startAttempt(t, 1))
			_, err := f.member.db.Exec(`UPDATE groupchat_fanout SET attempts=attempts+1`)
			require.NoError(t, err)
		}, BookkeepingConflict},
		"file corrupt": {func(t *testing.T, f failureFixture) {
			data, err := os.ReadFile(f.outboxPath)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(f.outboxPath, append(data, '{'), 0600))
		}, BookkeepingFileCorrupt},
		"failed with exhausted entry": {func(t *testing.T, f failureFixture) {
			f.exhaust(t)
		}, BookkeepingNormal},
		"failed, entry cleaned": {func(t *testing.T, f failureFixture) {
			f.exhaust(t)
			require.NoError(t, os.WriteFile(f.outboxPath, []byte(`{"entries":[]}`), 0600))
		}, BookkeepingNormal},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFailureFixture(t)
			c.arrange(t, f)
			unchanged := f.snapshot(t)
			got, err := f.member.store.inspectFanoutBookkeeping(f.key, f.recipient)
			require.NoError(t, err)
			require.Equal(t, c.want, got)
			unchanged()
		})
	}
}

func TestInspectFanoutBookkeepingMissingRow(t *testing.T) {
	f := newFailureFixture(t)
	_, err := f.member.store.inspectFanoutBookkeeping(f.key, "npub1nobody")
	require.Error(t, err)
	_, err = f.member.store.inspectFanoutBookkeeping(FanoutKey{LocalNpub: "bad"}, f.recipient)
	require.Error(t, err)
}

func TestInspectFanoutBookkeepingByKey(t *testing.T) {
	f, npubs := newPreparedFixture(t, 2)
	_, err := f.member.store.enqueuePreparedRaw(f.key, 1)
	require.NoError(t, err)
	unchanged := f.snapshot(t)

	reports, err := f.member.store.InspectFanoutBookkeeping(f.key)
	require.NoError(t, err)
	require.Len(t, reports, 2)
	require.True(t, reports[0].RecipientNpub < reports[1].RecipientNpub, "npub order")
	for _, r := range reports {
		require.Contains(t, npubs, r.RecipientNpub)
		require.Equal(t, BookkeepingNormal, r.Bookkeeping)
	}
	unchanged()

	// One recipient left with an unconsumed O3: only that recipient changes class.
	f.failO3BeforeT4(t, f.startAttempt(t, 1))
	unchanged = f.snapshot(t)
	reports, err = f.member.store.InspectFanoutBookkeeping(f.key)
	require.NoError(t, err)
	classes := map[string]FanoutBookkeeping{}
	for _, r := range reports {
		classes[r.RecipientNpub] = r.Bookkeeping
	}
	require.Equal(t, BookkeepingPendingFailure, classes[f.recipient])
	for _, n := range npubs {
		if n != f.recipient {
			require.Equal(t, BookkeepingNormal, classes[n])
		}
	}
	unchanged()

	// Unknown key: empty, not an error. Bad npub: error.
	empty, err := f.member.store.InspectFanoutBookkeeping(FanoutKey{LocalNpub: f.key.LocalNpub, GroupID: f.key.GroupID, EnvelopeType: f.key.EnvelopeType, SendKey: "nope"})
	require.NoError(t, err)
	require.Empty(t, empty)
	require.NotNil(t, empty)
	_, err = f.member.store.InspectFanoutBookkeeping(FanoutKey{LocalNpub: "bad"})
	require.Error(t, err)
}

package groupchat

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/iDoris-ai/hyphae/internal/messaging"
	"github.com/stretchr/testify/require"
)

func TestFanoutBarrierRejectsOutboxPayloadMismatch(t *testing.T) {
	f := newFailureFixture(t)
	_, err := f.member.db.Exec(`UPDATE groupchat_fanout SET event_json='different' WHERE event_id=?`, f.eventID)
	require.NoError(t, err)
	unchanged := f.snapshot(t)
	_, err = f.member.store.reserveFanoutAttempt(f.key, f.recipient)
	require.ErrorIs(t, err, errFanoutPayloadMismatch)
	unchanged()
}

func TestReserveAndStartFanoutAttemptBehindBarrier(t *testing.T) {
	f := newFailureFixture(t)
	token, err := f.member.store.reserveFanoutAttempt(f.key, f.recipient)
	require.NoError(t, err)
	require.EqualValues(t, 1, token.generation)
	require.Equal(t, f.queueID, token.queueID)
	require.Equal(t, "reserved", f.row(t).Phase)

	// S must see the reserved token; a second S for the same token is stale.
	require.NoError(t, f.member.store.startFanoutAttempt(token, 2, time.Now().Unix()))
	require.Equal(t, "started", f.row(t).Phase)
	unchanged := f.snapshot(t)
	require.ErrorIs(t, f.member.store.startFanoutAttempt(token, 2, time.Now().Unix()), errFanoutStaleAttempt)
	unchanged()

	// A failed P round-trips through O3/T4 and the next R succeeds.
	require.NoError(t, f.member.store.commitFanoutFailure(token))
	next, err := f.member.store.reserveFanoutAttempt(f.key, f.recipient)
	require.NoError(t, err)
	require.EqualValues(t, 2, next.generation)
}

func TestReserveFanoutAttemptReplaysPendingO3First(t *testing.T) {
	f := newFailureFixture(t)
	token := f.startAttempt(t, 1)
	f.failO3BeforeT4(t, token)
	require.EqualValues(t, 0, f.row(t).RetryCount, "T4 was aborted")

	next, err := f.member.store.reserveFanoutAttempt(f.key, f.recipient)
	require.NoError(t, err)
	require.EqualValues(t, 2, next.generation)
	row := f.row(t)
	require.EqualValues(t, 1, row.RetryCount, "barrier replayed T4 before reserving")
	require.Equal(t, token.generation, row.LastResultGeneration)
	entry := f.outboxEntry(t)
	require.Equal(t, messaging.FailureReceipt(entry.FailureResult.ResultID, sha256Hex(entry.Fields["failure_result"])), row.Receipt)
}

func TestReserveFanoutAttemptHoldsOnConflictAndExhaustion(t *testing.T) {
	t.Run("pending O3 conflict", func(t *testing.T) {
		f := newFailureFixture(t)
		f.failO3BeforeT4(t, f.startAttempt(t, 1))
		_, err := f.member.db.Exec(`UPDATE groupchat_fanout SET attempts=attempts+1`)
		require.NoError(t, err)
		unchanged := f.snapshot(t)
		_, err = f.member.store.reserveFanoutAttempt(f.key, f.recipient)
		require.ErrorIs(t, err, ErrFanoutBookkeepingConflict)
		unchanged()
	})
	t.Run("exhausted", func(t *testing.T) {
		f := newFailureFixture(t)
		for range 3 {
			require.NoError(t, f.member.store.commitFanoutFailure(f.startAttempt(t, 1)))
		}
		require.Equal(t, string(RecipientFailed), f.row(t).State)
		unchanged := f.snapshot(t)
		_, err := f.member.store.reserveFanoutAttempt(f.key, f.recipient)
		require.ErrorIs(t, err, ErrFanoutRetryExhausted)
		unchanged()
	})
	t.Run("missing row", func(t *testing.T) {
		f := newFailureFixture(t)
		_, err := f.member.store.reserveFanoutAttempt(f.key, "npub1nobody")
		require.Error(t, err)
	})
}

func TestReserveCommitsExhaustingReplay(t *testing.T) {
	f := newFailureFixture(t)
	require.NoError(t, f.member.store.commitFanoutFailure(f.startAttempt(t, 1)))
	require.NoError(t, f.member.store.commitFanoutFailure(f.startAttempt(t, 1)))
	f.failO3BeforeT4(t, f.startAttempt(t, 1))
	_, err := f.member.store.reserveFanoutAttempt(f.key, f.recipient)
	require.ErrorIs(t, err, ErrFanoutRetryExhausted)
	row := f.row(t)
	require.Equal(t, string(RecipientFailed), row.State)
	require.Equal(t, int64(3), row.RetryCount)
}

func TestReserveRefusesReplayUntilDurable(t *testing.T) {
	f := newFailureFixture(t)
	f.failO3BeforeT4(t, f.startAttempt(t, 1))
	before := f.row(t)
	orig := confirmOutboxDurable
	confirmOutboxDurable = func(string) error { return errors.New("dir fsync failed") }
	t.Cleanup(func() { confirmOutboxDurable = orig })
	_, err := f.member.store.reserveFanoutAttempt(f.key, f.recipient)
	require.ErrorIs(t, err, ErrFanoutBookkeepingPending)
	require.Equal(t, before, f.row(t))
}

func TestRecoverRefusesReplayUntilDurable(t *testing.T) {
	f := newFailureFixture(t)
	f.failO3BeforeT4(t, f.startAttempt(t, 1))
	before := f.row(t)
	orig := confirmOutboxDurable
	confirmOutboxDurable = func(string) error { return errors.New("dir fsync failed") }
	t.Cleanup(func() { confirmOutboxDurable = orig })
	replayed, err := f.member.store.recoverFanoutFailure(f.key, f.recipient)
	require.False(t, replayed)
	require.ErrorIs(t, err, ErrFanoutBookkeepingPending)
	require.Equal(t, before, f.row(t))
	require.Equal(t, int64(0), before.RetryCount)
}

func TestReserveRejectsFrozenEventJSONDrift(t *testing.T) {
	f := newFailureFixture(t)
	_, err := f.member.db.Exec(`UPDATE groupchat_fanout SET event_json=event_json||' '`)
	require.NoError(t, err)
	unchanged := f.snapshot(t)
	_, err = f.member.store.reserveFanoutAttempt(f.key, f.recipient)
	require.ErrorIs(t, err, ErrFanoutQueueConflict)
	unchanged()
}

func TestReserveRejectsFrozenIntentDrift(t *testing.T) {
	f := newFailureFixture(t)
	_, err := f.member.db.Exec(`UPDATE groupchat_fanout SET max_retries=1`)
	require.NoError(t, err)
	unchanged := f.snapshot(t)
	_, err = f.member.store.reserveFanoutAttempt(f.key, f.recipient)
	require.ErrorContains(t, err, "outbox_max=3")
	require.ErrorContains(t, err, "row_max=1")
	require.NotErrorIs(t, err, ErrFanoutQueueConflict)
	unchanged()
}

func TestFanoutBarrierD14QueueConflict(t *testing.T) {
	f := newFailureFixture(t)
	_, err := f.member.db.Exec(`UPDATE groupchat_fanout SET queue_id='ffffffffffffffffffffffffffffffff'`)
	require.NoError(t, err)
	unchanged := f.snapshot(t)
	_, err = f.member.store.reserveFanoutAttempt(f.key, f.recipient)
	require.ErrorIs(t, err, ErrFanoutQueueConflict)
	replayed, err := f.member.store.recoverFanoutFailure(f.key, f.recipient)
	require.False(t, replayed)
	require.NoError(t, err, "no failure record: recovery has nothing to replay")
	unchanged()

	// The entry vanished (relays:null legacy or cleared): queue_missing, no write.
	require.NoError(t, os.WriteFile(f.outboxPath, []byte(`{"entries":[]}`), 0600))
	_, err = f.member.store.reserveFanoutAttempt(f.key, f.recipient)
	require.ErrorContains(t, err, string(messaging.AgentMessageIssueQueueMissing))
}

func TestConfirmFanoutFailureCommit(t *testing.T) {
	f := newFailureFixture(t)
	token := f.startAttempt(t, 1)
	tx, err := f.member.store.beginImmediate()
	require.NoError(t, err)
	row, err := loadFanoutFailureRow(tx, f.key, f.recipient)
	require.NoError(t, err)
	require.NoError(t, tx.Rollback())
	require.Equal(t, token, row.token)
	want := row.failureResult()

	// Old snapshot survived: no durable evidence → pending, never a 2nd O3.
	_, err = confirmFanoutFailureCommit(f.outboxPath, row, want)
	require.ErrorIs(t, err, ErrFanoutBookkeepingPending)

	// Record present and exact → the receipt to project.
	receipt, err := messaging.CommitGroupFailureLocked(f.outboxPath, messaging.GroupFailureRecord{Result: want, FrozenEventJSON: row.eventJSON, MaxRetries: row.maxRetries})
	require.NoError(t, err)
	got, err := confirmFanoutFailureCommit(f.outboxPath, row, want)
	require.NoError(t, err)
	require.Equal(t, receipt, got)

	// Old consumed snapshot re-presented -> Pending (not Conflict)
	consumedRow := row
	consumedRow.receipt = receipt
	consumedRow.attempts++
	_, err = confirmFanoutFailureCommit(f.outboxPath, consumedRow, consumedRow.failureResult())
	require.ErrorIs(t, err, ErrFanoutBookkeepingPending)

	// Record present but for a different pre-image → conflict.
	row.attempts++
	_, err = confirmFanoutFailureCommit(f.outboxPath, row, row.failureResult())
	require.ErrorIs(t, err, ErrFanoutBookkeepingConflict)

	// The row's persisted queue is not the entry's queue → conflict.
	row.attempts--
	row.token.queueID = "ffffffffffffffffffffffffffffffff"
	_, err = confirmFanoutFailureCommit(f.outboxPath, row, want)
	require.ErrorIs(t, err, ErrFanoutBookkeepingConflict)
}

func rawEditEntryField(t *testing.T, outboxPath string, mutator func(fields map[string]json.RawMessage)) {
	t.Helper()
	data, err := os.ReadFile(outboxPath)
	require.NoError(t, err)
	var doc struct {
		Entries []json.RawMessage `json:"entries"`
	}
	require.NoError(t, json.Unmarshal(data, &doc))
	require.Len(t, doc.Entries, 1)
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(doc.Entries[0], &fields))
	mutator(fields)
	entryBytes, err := json.Marshal(fields)
	require.NoError(t, err)
	doc.Entries[0] = entryBytes
	out, err := json.Marshal(doc)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(outboxPath, out, 0600))
}

func TestReserveCommitsExhaustingReplayBeforeRefusal(t *testing.T) {
	f := newFailureFixture(t)
	// Two normal failures:
	require.NoError(t, f.member.store.commitFanoutFailure(f.startAttempt(t, 1)))
	require.NoError(t, f.member.store.commitFanoutFailure(f.startAttempt(t, 1)))
	// Third attempt starts, then fails O3 before T4 (exhausting):
	token3 := f.startAttempt(t, 1)
	f.failO3BeforeT4(t, token3)

	// Barrier must replay the exhausting failure, persist it, and refuse R.
	_, err := f.member.store.reserveFanoutAttempt(f.key, f.recipient)
	require.ErrorIs(t, err, ErrFanoutRetryExhausted)

	row := f.row(t)
	require.Equal(t, string(RecipientFailed), row.State)
	require.EqualValues(t, 3, row.RetryCount)
	require.Equal(t, token3.generation, row.LastResultGeneration)
	entry := f.outboxEntry(t)
	require.Equal(t, messaging.FailureReceipt(entry.FailureResult.ResultID, sha256Hex(entry.Fields["failure_result"])), row.Receipt)

	// A second reserve attempt fails identically with stores unchanged (no new R).
	unchanged := f.snapshot(t)
	_, err = f.member.store.reserveFanoutAttempt(f.key, f.recipient)
	require.ErrorIs(t, err, ErrFanoutRetryExhausted)
	unchanged()
}

func TestStartCommitsReplayOnStaleToken(t *testing.T) {
	f := newFailureFixture(t)
	tokenA, err := f.member.store.reserveFanoutAttempt(f.key, f.recipient)
	require.NoError(t, err)
	require.NoError(t, f.member.store.startFanoutAttempt(tokenA, 1, time.Now().Unix()))

	// Inject O3-before-T4 for tokenA:
	f.failO3BeforeT4(t, tokenA)

	// Call startFanoutAttempt(tokenA) again:
	err = f.member.store.startFanoutAttempt(tokenA, 1, time.Now().Unix())
	require.True(t, errors.Is(err, errFanoutStaleAttempt) || errors.Is(err, ErrFanoutNotActive), "expected stale or not active, got %v", err)

	// Replay must be durable (retry count +1) and attempts unchanged:
	row := f.row(t)
	require.EqualValues(t, 1, row.RetryCount)
	require.Equal(t, "failed", row.Phase)
	entry := f.outboxEntry(t)
	require.Equal(t, messaging.FailureReceipt(entry.FailureResult.ResultID, sha256Hex(entry.Fields["failure_result"])), row.Receipt)
}

func TestBarrierConfirmsO3DurabilityBeforeReplay(t *testing.T) {
	f := newFailureFixture(t)
	token := f.startAttempt(t, 1)
	f.failO3BeforeT4(t, token)

	origHook := confirmOutboxDurable
	confirmOutboxDurable = func(path string) error {
		return errors.New("simulated EIO: disk I/O error")
	}
	t.Cleanup(func() { confirmOutboxDurable = origHook })

	unchanged := f.snapshot(t)
	_, err := f.member.store.reserveFanoutAttempt(f.key, f.recipient)
	require.ErrorIs(t, err, ErrFanoutBookkeepingPending)
	unchanged()

	// Restore hook: reserve succeeds with gen 2 and replay applied.
	confirmOutboxDurable = origHook
	next, err := f.member.store.reserveFanoutAttempt(f.key, f.recipient)
	require.NoError(t, err)
	require.EqualValues(t, 2, next.generation)
	require.EqualValues(t, 1, f.row(t).RetryCount)
}

func TestBarrierRejectsFrozenIntentMismatch(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(t *testing.T, f *failureFixture)
	}{
		{
			name: "max_retries 3 to 2 in outbox entry",
			mutate: func(t *testing.T, f *failureFixture) {
				rawEditEntryField(t, f.outboxPath, func(fields map[string]json.RawMessage) {
					fields["max_retries"] = json.RawMessage("2")
				})
			},
		},
		{
			name: "event_json modified in outbox entry",
			mutate: func(t *testing.T, f *failureFixture) {
				rawEditEntryField(t, f.outboxPath, func(fields map[string]json.RawMessage) {
					var s string
					require.NoError(t, json.Unmarshal(fields["event_json"], &s))
					s += " "
					b, err := json.Marshal(s)
					require.NoError(t, err)
					fields["event_json"] = b
				})
			},
		},
		{
			name: "event_json modified in row",
			mutate: func(t *testing.T, f *failureFixture) {
				_, err := f.member.db.Exec(`UPDATE groupchat_fanout SET event_json = event_json || ' ' WHERE event_id=?`, f.eventID)
				require.NoError(t, err)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFailureFixture(t)
			token := f.startAttempt(t, 1)
			require.NoError(t, f.member.store.commitFanoutFailure(token))

			tc.mutate(t, &f)

			unchanged := f.snapshot(t)
			_, err := f.member.store.reserveFanoutAttempt(f.key, f.recipient)
			if strings.Contains(tc.name, "max_retries") {
				require.ErrorContains(t, err, "outbox_max=")
				require.ErrorContains(t, err, "row_max=")
			} else {
				require.ErrorIs(t, err, ErrFanoutQueueConflict)
			}
			err = f.member.store.startFanoutAttempt(token, 1, time.Now().Unix())
			if strings.Contains(tc.name, "max_retries") {
				require.ErrorContains(t, err, "outbox_max=")
			} else {
				require.ErrorIs(t, err, ErrFanoutQueueConflict)
			}
			unchanged()
		})
	}
}

func TestCommitReplayOnRefusalTypesCommitFailure(t *testing.T) {
	f := newFailureFixture(t)
	tx, err := f.member.store.beginImmediate()
	require.NoError(t, err)
	require.NoError(t, tx.Rollback())

	err = commitReplayOnRefusal(tx, true, ErrFanoutRetryExhausted)
	require.ErrorIs(t, err, ErrFanoutBookkeepingPending)
	require.ErrorIs(t, err, ErrFanoutRetryExhausted)

	err = commitReplayOnRefusal(tx, false, ErrFanoutRetryExhausted)
	require.Equal(t, ErrFanoutRetryExhausted, err)
}

package groupchat

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/iDoris-ai/hyphae/internal/messaging"
	"github.com/iDoris-ai/hyphae/pkg/types"
	"github.com/stretchr/testify/require"
)

func (f failureFixture) typedEntry(t *testing.T) types.OutboxEntry {
	t.Helper()
	return f.outboxEntry(t).Entry
}

// Real end-to-end failure path: AttemptSendRouted → FanoutAttemptHandler →
// R/S behind the barrier → publish to an unreachable relay → O3+T4.
func TestFanoutAttemptHandlerDrivesAttemptSendRouted(t *testing.T) {
	f := newFailureFixture(t)
	handler := NewFanoutAttemptHandler(f.member.store)
	opts := messaging.AttemptOptions{Handlers: messaging.OutboxHandlers{Attempt: handler}}
	relays := []string{"ws://127.0.0.1:1"}
	for round := int64(1); round <= 3; round++ {
		result, err := messaging.AttemptSendRouted(context.Background(), nil, f.typedEntry(t), relays, 200*time.Millisecond, opts)
		require.NoError(t, err, "round %d", round)
		require.True(t, result.Attempted)
		require.False(t, result.Sent)
		row := f.recovery(t)
		require.Equal(t, round, row.RetryCount)
		require.Equal(t, round, row.Generation)
		require.Equal(t, round, row.Attempts)
		if round < 3 {
			require.True(t, result.Queued)
			require.Equal(t, messaging.AgentMessageIssueSendFailed, result.Issue)
			require.Equal(t, string(RecipientQueued), row.State)
		} else {
			require.True(t, result.MarkedFailed)
			require.Equal(t, messaging.AgentMessageIssueRetryExhausted, result.Issue)
			require.Equal(t, string(RecipientFailed), row.State)
		}
		require.NotNil(t, f.outboxEntry(t).FailureResult)
	}
	// Exhausted: the barrier refuses before any publish; no writes.
	unchanged := f.snapshot(t)
	result, err := messaging.AttemptSendRouted(context.Background(), nil, f.typedEntry(t), relays, 200*time.Millisecond, opts)
	require.Error(t, err)
	require.Equal(t, messaging.AgentMessageIssueOutboxBookkeepingFailed, result.Issue)
	require.False(t, result.Attempted)
	unchanged()

	// Operator recovery, then the next attempt runs on the new epoch.
	newQueue, err := f.member.store.authorizeFanoutRecovery(f.key, f.recipient, time.Now().Unix())
	require.NoError(t, err)
	result, err = messaging.AttemptSendRouted(context.Background(), nil, f.typedEntry(t), relays, 200*time.Millisecond, opts)
	require.NoError(t, err)
	require.True(t, result.Queued)
	row := f.recovery(t)
	require.Equal(t, newQueue, row.QueueID)
	require.EqualValues(t, 1, row.RetryCount)
	require.EqualValues(t, 4, row.Generation)
}

func TestFanoutAttemptHandlerAcceptedAndMappings(t *testing.T) {
	f := newFailureFixture(t)
	handler := NewFanoutAttemptHandler(f.member.store)
	entry := f.typedEntry(t)
	token, err := handler.Reserve(entry)
	require.NoError(t, err)
	require.Equal(t, f.eventID, token.EventID)
	require.Equal(t, f.queueID, token.QueueID)
	require.Equal(t, f.recipient, token.Key.RecipientNpub)
	require.Equal(t, f.key.LocalNpub, token.Key.LocalNpub)
	require.EqualValues(t, 1, token.AttemptGeneration)
	require.NoError(t, handler.Start(token, 1, time.Now().Unix()))
	require.NoError(t, handler.Accepted(token, time.Now().Unix()))
	require.Equal(t, string(RecipientRelayAccepted), f.accepted(t).State)

	// Late callbacks map to the stale sentinel.
	_, err = handler.Failed(token)
	require.ErrorIs(t, err, messaging.ErrGroupAttemptStale)
	require.ErrorIs(t, err, errFanoutStaleAttempt, "store chain stays inspectable")
	require.ErrorIs(t, handler.Accepted(token, time.Now().Unix()), messaging.ErrGroupAttemptStale)

	// Recipient / row mismatches are conflicts.
	wrong := entry
	wrong.RecipientNpub = f.key.LocalNpub
	_, err = handler.Reserve(wrong)
	require.ErrorIs(t, err, messaging.ErrGroupAttemptConflict)
	unknown := entry
	unknown.ID = "0000000000000000000000000000000000000000000000000000000000000000"
	_, err = handler.Reserve(unknown)
	require.ErrorIs(t, err, messaging.ErrGroupAttemptConflict)
}

func TestFanoutAttemptHandlerRejectsStaleAndForgedSnapshotsWithoutMutation(t *testing.T) {
	t.Run("stale queue epoch", func(t *testing.T) {
		f := newFailureFixture(t)
		stale := f.typedEntry(t)
		f.exhaust(t)
		_, err := f.member.store.authorizeFanoutRecovery(f.key, f.recipient, time.Now().Unix())
		require.NoError(t, err)

		unchanged := f.snapshot(t)
		_, err = NewFanoutAttemptHandler(f.member.store).Reserve(stale)
		require.ErrorIs(t, err, messaging.ErrGroupAttemptStale)
		unchanged()
	})

	t.Run("same ID forged content never publishes", func(t *testing.T) {
		f := newFailureFixture(t)
		entry := f.typedEntry(t)
		entry.EventJSON = strings.Replace(entry.EventJSON, `"content":"`, `"content":"x`, 1)
		require.NotEqual(t, f.typedEntry(t).EventJSON, entry.EventJSON)
		unchanged := f.snapshot(t)
		result, err := messaging.AttemptSendRouted(context.Background(), nil, entry, []string{"ws://127.0.0.1:1"}, 200*time.Millisecond,
			messaging.AttemptOptions{Handlers: messaging.OutboxHandlers{Attempt: NewFanoutAttemptHandler(f.member.store)}})
		require.ErrorIs(t, err, messaging.ErrGroupAttemptStale)
		require.False(t, result.Attempted, "a forged snapshot must be refused before publish start")
		unchanged()
	})
}

func TestFanoutAttemptHandlerReservesEscapedEventMemberNames(t *testing.T) {
	f := newFailureFixture(t)
	entry := f.typedEntry(t)
	entry.EventJSON = strings.Replace(entry.EventJSON, `"id":`, `"\u0069d":`, 1)
	entry.EventJSON = strings.Replace(entry.EventJSON, `"pubkey":`, `"pub\u006bey":`, 1)
	require.NotEqual(t, f.outboxEntry(t).Entry.EventJSON, entry.EventJSON)

	outbox, err := messaging.LoadOutbox()
	require.NoError(t, err)
	for i := range outbox.Entries {
		if outbox.Entries[i].ID == entry.ID {
			outbox.Entries[i].EventJSON = entry.EventJSON
		}
	}
	require.NoError(t, messaging.SaveOutbox(outbox))
	_, err = f.member.db.Exec(`UPDATE groupchat_fanout SET event_json=? WHERE event_id=?`, entry.EventJSON, entry.ID)
	require.NoError(t, err)

	parsed := f.outboxEntry(t)
	require.Equal(t, entry.EventJSON, parsed.Entry.EventJSON)
	token, err := NewFanoutAttemptHandler(f.member.store).Reserve(entry)
	require.NoError(t, err)
	require.Equal(t, entry.ID, token.EventID)
	require.Equal(t, entry.QueueID, token.QueueID)
	require.EqualValues(t, 1, token.AttemptGeneration)
}

func TestFanoutAttemptReservationRacesLockedQueueRotation(t *testing.T) {
	f := newFailureFixture(t)
	stale := f.typedEntry(t)
	locked := make(chan struct{})
	rotationErr := make(chan error, 1)
	reserveStarted := make(chan struct{})
	type baselineState struct {
		outbox []byte
		row    []any
	}
	baseline := make(chan baselineState, 1)
	rotate := "rotated-queue-epoch"

	go func() {
		rotationErr <- messaging.WithGroupOutboxLock(func(string) error {
			outbox, err := messaging.LoadOutbox()
			if err != nil {
				return err
			}
			for i := range outbox.Entries {
				if outbox.Entries[i].ID == stale.ID {
					outbox.Entries[i].QueueID = rotate
				}
			}
			data, err := json.Marshal(outbox)
			if err != nil {
				return err
			}
			if err := os.WriteFile(f.outboxPath, data, 0o600); err != nil {
				return err
			}
			if _, err := f.member.db.Exec(`UPDATE groupchat_fanout SET queue_id=? WHERE event_id=?`, rotate, stale.ID); err != nil {
				return err
			}
			data, err = os.ReadFile(f.outboxPath)
			if err != nil {
				return err
			}
			baseline <- baselineState{outbox: data, row: fanoutRowSnapshot(t, f.member, stale.ID)}
			close(locked)
			// Hold the same lock Reserve needs until its call is launched.
			<-reserveStarted
			return nil
		})
	}()
	<-locked
	type reserveResult struct {
		result messaging.SendResult
		err    error
	}
	reserved := make(chan reserveResult, 1)
	go func() {
		close(reserveStarted)
		result, err := messaging.AttemptSendRouted(context.Background(), nil, stale, []string{"ws://127.0.0.1:1"}, 200*time.Millisecond,
			messaging.AttemptOptions{Handlers: messaging.OutboxHandlers{Attempt: NewFanoutAttemptHandler(f.member.store)}})
		reserved <- reserveResult{result: result, err: err}
	}()
	got := <-reserved
	require.NoError(t, <-rotationErr)
	require.ErrorIs(t, got.err, messaging.ErrGroupAttemptStale)
	require.False(t, got.result.Attempted, "stale snapshot must be refused before publish")
	state := <-baseline
	after, err := os.ReadFile(f.outboxPath)
	require.NoError(t, err)
	require.Equal(t, string(state.outbox), string(after), "stale reservation changed rotated outbox")
	require.Equal(t, state.row, fanoutRowSnapshot(t, f.member, stale.ID), "stale reservation changed rotated row")
}

func TestValidateFanoutAttemptSnapshotAcceptsOpaqueLegacyQueueID(t *testing.T) {
	f := newFailureFixture(t)
	entry := f.typedEntry(t)
	entry.QueueID = "legacy-queue-id"
	entry.Route = messaging.OutboxRouteGroup
	entry.Status = messaging.OutboxStatusGroupPending
	require.NoError(t, validateFanoutAttemptSnapshot(entry, entry, entry.EventJSON, entry.QueueID, f.recipient, f.key.LocalNpub))
}

func TestFanoutAttemptHandlerPendingAndQueueMappings(t *testing.T) {
	f := newFailureFixture(t)
	handler := NewFanoutAttemptHandler(f.member.store)
	f.failO3BeforeT4(t, f.startAttempt(t, 1))
	_, err := f.member.db.Exec(`UPDATE groupchat_fanout SET attempts=attempts+1`)
	require.NoError(t, err)
	_, err = handler.Reserve(f.typedEntry(t))
	require.ErrorIs(t, err, messaging.ErrGroupAttemptConflict)

	f2 := newFailureFixture(t)
	h2 := NewFanoutAttemptHandler(f2.member.store)
	_, err = f2.member.db.Exec(`UPDATE groupchat_fanout SET queue_id='ffffffffffffffffffffffffffffffff'`)
	require.NoError(t, err)
	_, err = h2.Reserve(f2.typedEntry(t))
	require.ErrorIs(t, err, messaging.ErrGroupAttemptQueueConflict)

	f3 := newFailureFixture(t)
	h3 := NewFanoutAttemptHandler(f3.member.store)
	entry := f3.typedEntry(t)
	require.NoError(t, messaging.SaveOutbox(&types.Outbox{Entries: []types.OutboxEntry{}}))
	_, err = h3.Reserve(entry)
	require.ErrorIs(t, err, messaging.ErrGroupAttemptQueueMissing)

	require.NoError(t, mapFanoutError(nil))
	require.ErrorIs(t, mapFanoutError(ErrFanoutRecoveryPending), messaging.ErrGroupAttemptPending)
	require.ErrorIs(t, mapFanoutError(ErrFanoutCleanupPending), messaging.ErrGroupAttemptCleanupPending)
	require.ErrorIs(t, mapFanoutError(errors.New("other")), messaging.ErrGroupAttemptConflict)
}

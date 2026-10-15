package groupchat

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/iDoris-ai/hyphae/internal/messaging"
	"github.com/iDoris-ai/hyphae/pkg/types"
	"github.com/stretchr/testify/require"
)

// W1a-P1: barrier refusals keep BOTH typed chains (9fd dual errors) and write nothing.
func TestW1aHandlerKeepsTypedBarrierChainsWriteFree(t *testing.T) {
	f := newFailureFixture(t)
	f.exhaust(t)
	h := NewFanoutAttemptHandler(f.member.store)
	unchanged := f.snapshot(t)
	_, err := h.Reserve(f.typedEntry(t))
	require.ErrorIs(t, err, ErrFanoutRetryExhausted)
	require.ErrorIs(t, err, messaging.ErrGroupAttemptConflict)
	unchanged()

}

// W1a-P2: a missing outbox entry is the typed sentinel on both sides and writes nothing.
func TestW1aHandlerQueueMissingTypedWriteFree(t *testing.T) {
	f := newFailureFixture(t)
	h := NewFanoutAttemptHandler(f.member.store)
	entry := f.typedEntry(t)
	require.NoError(t, messaging.SaveOutbox(&types.Outbox{Entries: []types.OutboxEntry{}}))
	unchanged := f.snapshot(t)
	_, err := h.Reserve(entry)
	require.ErrorIs(t, err, ErrFanoutQueueMissing)
	require.ErrorIs(t, err, messaging.ErrGroupAttemptQueueMissing)
	unchanged()
}

// W1a-P3: directory confirmation failure before the T4 replay maps to Pending
// with the store chain intact and leaves outbox and row untouched.
func TestW1aHandlerDirConfirmBeforeReplayIsPending(t *testing.T) {
	f := newFailureFixture(t)
	f.failO3BeforeT4(t, f.startAttempt(t, 1))
	h := NewFanoutAttemptHandler(f.member.store)
	errDir := errors.New("injected dir fsync failure")
	orig := confirmOutboxDurable
	confirmOutboxDurable = func(string) error { return errDir }
	t.Cleanup(func() { confirmOutboxDurable = orig })
	unchanged := f.snapshot(t)
	_, err := h.Reserve(f.typedEntry(t))
	require.ErrorIs(t, err, messaging.ErrGroupAttemptPending)
	require.ErrorIs(t, err, ErrFanoutBookkeepingPending)
	unchanged()
	confirmOutboxDurable = orig
	_, err = h.Reserve(f.typedEntry(t))
	require.NoError(t, err)
	require.EqualValues(t, 1, f.row(t).RetryCount)
}

// W1a-P4: a stale caller snapshot is refused before publish: Superseded, not
// attempted, outbox bytes untouched.
func TestW1aStaleSnapshotNeverPublishes(t *testing.T) {
	f := newFailureFixture(t)
	h := NewFanoutAttemptHandler(f.member.store)
	entry := f.typedEntry(t)
	entry.QueueID = "ffffffffffffffffffffffffffffffff"
	before, err := os.ReadFile(f.outboxPath)
	require.NoError(t, err)
	opts := messaging.AttemptOptions{Handlers: messaging.OutboxHandlers{Attempt: h}}
	result, err := messaging.AttemptSendRouted(context.Background(), nil, entry, []string{"ws://127.0.0.1:1"}, 100*time.Millisecond, opts)
	require.ErrorIs(t, err, messaging.ErrGroupAttemptStale)
	require.True(t, result.Superseded)
	require.False(t, result.Attempted)
	after, err := os.ReadFile(f.outboxPath)
	require.NoError(t, err)
	require.Equal(t, string(before), string(after))
}

// W1a-P5: an entry the strict 9fd parser rejects (unknown member) is refused
// before R: nothing published, outbox bytes/mtime and row untouched.
func TestW1aUnknownMemberRefusedWriteFree(t *testing.T) {
	f := newFailureFixture(t)
	entry := f.typedEntry(t)
	rawEditEntryField(t, f.outboxPath, func(fields map[string]json.RawMessage) {
		fields["x_w1a_future"] = json.RawMessage(`{"k":[1,2,"z"]}`)
	})
	unchanged := f.snapshot(t)
	h := NewFanoutAttemptHandler(f.member.store)
	opts := messaging.AttemptOptions{Handlers: messaging.OutboxHandlers{Attempt: h}}
	result, err := messaging.AttemptSendRouted(context.Background(), nil, entry, []string{"ws://127.0.0.1:1"}, 100*time.Millisecond, opts)
	require.ErrorContains(t, err, "x_w1a_future")
	require.False(t, result.Attempted)
	unchanged()
}

// W1a-P6: the handler failure path keeps the frozen event_json bytes and
// writes failure_result via the raw O3 writer.
func TestW1aFailurePathKeepsFrozenEventBytes(t *testing.T) {
	f := newFailureFixture(t)
	frozen := f.outboxEntry(t)
	h := NewFanoutAttemptHandler(f.member.store)
	opts := messaging.AttemptOptions{Handlers: messaging.OutboxHandlers{Attempt: h}}
	result, err := messaging.AttemptSendRouted(context.Background(), nil, f.typedEntry(t), []string{"ws://127.0.0.1:1"}, 100*time.Millisecond, opts)
	require.NoError(t, err)
	require.True(t, result.Queued)
	after := f.outboxEntry(t)
	require.Equal(t, string(frozen.Fields["event_json"]), string(after.Fields["event_json"]))
	require.NotNil(t, after.FailureResult)
}

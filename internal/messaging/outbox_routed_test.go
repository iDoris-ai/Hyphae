package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"github.com/iDoris-ai/hyphae/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type routedHandler struct {
	before   func(types.OutboxEntry) (bool, error)
	accepted func(types.OutboxEntry, int, int) error
	failure  func(types.OutboxEntry, bool, AgentMessageDeliveryIssue) error
}

func (h *routedHandler) BeforePublish(entry types.OutboxEntry) (bool, error) {
	return h.before(entry)
}
func (h *routedHandler) MarkRelayAccepted(entry types.OutboxEntry, acks, count int) error {
	return h.accepted(entry, acks, count)
}
func (h *routedHandler) RecordAttemptFailure(entry types.OutboxEntry, exhausted bool, issue AgentMessageDeliveryIssue) error {
	return h.failure(entry, exhausted, issue)
}

func routedFixture(t *testing.T, route, status string, retries, maxRetries int) (types.OutboxEntry, *types.Outbox) {
	t.Helper()
	setupTempOutbox(t)
	event := nostr.Event{Kind: 1, Content: "routed"}
	event.ID[0] = 99
	encoded, err := json.Marshal(event)
	require.NoError(t, err)
	entry := types.OutboxEntry{
		QueueID: "routed-queue", ID: event.ID.Hex(), Route: route, EventJSON: string(encoded),
		RecipientNpub: "npub1recipient", RetryCount: retries, MaxRetries: maxRetries,
		Status: status, CreatedAt: 42,
	}
	ob := &types.Outbox{Entries: []types.OutboxEntry{entry}}
	require.NoError(t, SaveOutbox(ob))
	return entry, ob
}

func TestAttemptSendRouted_GroupHandlerDispatchAndAck(t *testing.T) {
	entry, ob := routedFixture(t, OutboxRouteGroup, OutboxStatusGroupPending, 0, 3)
	var before, accepted, dmStore int
	var gotEntry types.OutboxEntry
	var gotAcks, gotCount int
	handler := &routedHandler{
		before: func(e types.OutboxEntry) (bool, error) { before++; gotEntry = e; return false, nil },
		accepted: func(e types.OutboxEntry, acks, count int) error {
			accepted++
			gotAcks, gotCount = acks, count
			return nil
		},
		failure: func(types.OutboxEntry, bool, AgentMessageDeliveryIssue) error {
			t.Fatal("failure callback on ACK")
			return nil
		},
	}
	result, err := attemptSendRouted(context.Background(), ob, entry, []string{"relay-a", "relay-b"}, time.Second,
		AttemptOptions{Handlers: OutboxHandlers{Group: handler}},
		func(context.Context, []string, nostr.Event, time.Duration) bool { return true },
		func(*nostr.Event, string, string, bool) error { dmStore++; return nil })
	require.NoError(t, err)
	assert.Equal(t, 1, before)
	assert.Equal(t, 1, accepted)
	assert.Zero(t, dmStore)
	assert.Equal(t, entry.QueueID, gotEntry.QueueID)
	assert.Equal(t, 1, gotAcks)
	assert.Equal(t, 2, gotCount)
	assert.True(t, result.Attempted)
	assert.True(t, result.Sent)
	assert.Empty(t, result.Issue)
	assert.Empty(t, ob.Entries)
}

func TestAttemptSendRouted_MissingHandlerFailsClosed(t *testing.T) {
	entry, ob := routedFixture(t, OutboxRouteGroup, OutboxStatusGroupPending, 0, 3)
	published, stored := 0, 0
	result, err := attemptSendRouted(context.Background(), ob, entry, nil, time.Second, AttemptOptions{},
		func(context.Context, []string, nostr.Event, time.Duration) bool { published++; return true },
		func(*nostr.Event, string, string, bool) error { stored++; return nil })
	require.ErrorIs(t, err, ErrGroupRouteHandlerMissing)
	assert.Equal(t, AgentMessageIssueRouteHandlerMissing, result.Issue)
	assert.False(t, result.Attempted)
	assert.Zero(t, published)
	assert.Zero(t, stored)
	assert.Len(t, ob.Entries, 1)
}

func TestAttemptSendWithKeyStore_GroupRouteFailsClosed(t *testing.T) {
	entry, ob := routedFixture(t, OutboxRouteGroup, OutboxStatusGroupPending, 0, 1)
	entry.Relays = []string{"://invalid-relay"}
	ob.Entries[0] = entry
	require.NoError(t, SaveOutbox(ob))

	result, err := AttemptSendWithKeyStore(context.Background(), ob, entry, nil, time.Millisecond, nil)
	require.ErrorIs(t, err, ErrGroupRouteHandlerMissing)
	assert.False(t, result.Attempted)
	assert.False(t, result.Sent)
	assert.Equal(t, AgentMessageIssueRouteHandlerMissing, result.Issue)
	latest, loadErr := LoadOutbox()
	require.NoError(t, loadErr)
	require.Equal(t, []types.OutboxEntry{entry}, latest.Entries)
}

func TestAttemptSendRouted_UnsupportedRouteFailsClosed(t *testing.T) {
	entry, ob := routedFixture(t, "unknown", OutboxStatusGroupPending, 0, 3)
	published, stored := 0, 0
	result, err := attemptSendRouted(context.Background(), ob, entry, nil, time.Second, AttemptOptions{},
		func(context.Context, []string, nostr.Event, time.Duration) bool { published++; return true },
		func(*nostr.Event, string, string, bool) error { stored++; return nil })
	require.ErrorContains(t, err, `unsupported outbox route "unknown"`)
	assert.False(t, result.Attempted)
	assert.Zero(t, published)
	assert.Zero(t, stored)
	assert.Len(t, ob.Entries, 1)
}

func TestAttemptSendRouted_FailureBookkeepingAndCancellation(t *testing.T) {
	for _, tc := range []struct {
		name       string
		cancel     bool
		wantStatus string
	}{
		{name: "exhausted", wantStatus: OutboxStatusGroupFailed},
		{name: "cancelled", cancel: true, wantStatus: OutboxStatusGroupFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entry, ob := routedFixture(t, OutboxRouteGroup, OutboxStatusGroupPending, 0, 1)
			var callback int
			var exhausted bool
			var issue AgentMessageDeliveryIssue
			handler := &routedHandler{
				before:   func(types.OutboxEntry) (bool, error) { return false, nil },
				accepted: func(types.OutboxEntry, int, int) error { t.Fatal("unexpected ACK"); return nil },
				failure: func(_ types.OutboxEntry, e bool, i AgentMessageDeliveryIssue) error {
					callback++
					exhausted, issue = e, i
					return nil
				},
			}
			ctx := context.Background()
			if tc.cancel {
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelled
			}
			result, err := attemptSendRouted(ctx, ob, entry, nil, time.Second,
				AttemptOptions{Handlers: OutboxHandlers{Group: handler}},
				func(publishCtx context.Context, _ []string, _ nostr.Event, _ time.Duration) bool {
					if tc.cancel {
						assert.ErrorIs(t, publishCtx.Err(), context.Canceled)
					}
					return false
				},
				func(*nostr.Event, string, string, bool) error { t.Fatal("group must not write DM history"); return nil })
			require.NoError(t, err)
			assert.True(t, result.Attempted)
			assert.False(t, result.Sent)
			assert.Equal(t, AgentMessageIssueSendFailed, result.Issue)
			assert.Equal(t, 1, callback)
			assert.True(t, exhausted)
			assert.Equal(t, AgentMessageIssueSendFailed, issue)
			latest, loadErr := LoadOutbox()
			require.NoError(t, loadErr)
			require.Len(t, latest.Entries, 1)
			assert.Equal(t, tc.wantStatus, latest.Entries[0].Status)
		})
	}
}

func TestAttemptSendRouted_ForwardsKeyStoreToDMPath(t *testing.T) {
	resetStore(t)
	t.Cleanup(ResetStoreForTest)
	ks, event, plaintext := realEncryptedHistoryFixture(t)
	entry, ob := queueHistoryEvent(t, event)
	result, err := attemptSendRouted(context.Background(), ob, entry, nil, time.Second,
		AttemptOptions{KeyStore: ks},
		func(context.Context, []string, nostr.Event, time.Duration) bool { return true }, StoreOutgoingMessage)
	require.NoError(t, err)
	assert.True(t, result.HistoryStored)
	stored, err := mustGetStoredMessage(t, entry.ID)
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.Equal(t, plaintext, stored.Plaintext)
	assert.Empty(t, result.Issue)
}

func TestAttemptSendRouted_HandlerErrorPreservesQueue(t *testing.T) {
	entry, ob := routedFixture(t, OutboxRouteGroup, OutboxStatusGroupPending, 0, 3)
	want := errors.New("sqlite unavailable")
	published := false
	handler := &routedHandler{
		before:   func(types.OutboxEntry) (bool, error) { return false, want },
		accepted: func(types.OutboxEntry, int, int) error { return nil },
		failure:  func(types.OutboxEntry, bool, AgentMessageDeliveryIssue) error { return nil },
	}
	result, err := attemptSendRouted(context.Background(), ob, entry, nil, time.Second,
		AttemptOptions{Handlers: OutboxHandlers{Group: handler}},
		func(context.Context, []string, nostr.Event, time.Duration) bool { published = true; return true },
		StoreOutgoingMessage)
	require.ErrorIs(t, err, want)
	assert.False(t, published)
	assert.False(t, result.Attempted)
	assert.Len(t, ob.Entries, 1)
}

func TestAttemptSendRouted_MarkRelayAcceptedErrorPreservesPendingEntry(t *testing.T) {
	entry, ob := routedFixture(t, OutboxRouteGroup, OutboxStatusGroupPending, 0, 1)
	want := errors.New("sqlite unavailable")
	var failureCalls int
	handler := &routedHandler{
		before:   func(types.OutboxEntry) (bool, error) { return false, nil },
		accepted: func(types.OutboxEntry, int, int) error { return want },
		failure: func(types.OutboxEntry, bool, AgentMessageDeliveryIssue) error {
			failureCalls++
			return nil
		},
	}
	result, err := attemptSendRouted(context.Background(), ob, entry, nil, time.Second,
		AttemptOptions{Handlers: OutboxHandlers{Group: handler}},
		func(context.Context, []string, nostr.Event, time.Duration) bool { return true },
		func(*nostr.Event, string, string, bool) error { t.Fatal("group must not write DM history"); return nil })
	require.ErrorIs(t, err, want)
	assert.ErrorContains(t, err, "mark group relay accepted")
	assert.True(t, result.Attempted)
	assert.True(t, result.Sent)
	assert.Equal(t, AgentMessageIssueOutboxBookkeepingFailed, result.Issue)
	assert.Zero(t, failureCalls, "accepted relay event must not spend retry budget")
	assert.Equal(t, []types.OutboxEntry{entry}, ob.Entries)
	latest, loadErr := LoadOutbox()
	require.NoError(t, loadErr)
	assert.Equal(t, []types.OutboxEntry{entry}, latest.Entries)
}

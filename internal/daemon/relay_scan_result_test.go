package daemon

import (
	"context"
	"errors"
	"testing"

	"fiatjaf.com/nostr"
	"github.com/iDoris-ai/hyphae/internal/identity"
	"github.com/iDoris-ai/hyphae/internal/messaging"
	"github.com/iDoris-ai/hyphae/internal/relayquery"
	"github.com/iDoris-ai/hyphae/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWatchOneRelayWithHooksResultPreservesPartialWork(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	messaging.ResetStoreForTest()
	t.Cleanup(messaging.ResetStoreForTest)
	require.NoError(t, messaging.InitStorage())
	ks := &types.KeyStore{Identities: make(map[string]*types.Identity), Contacts: make(map[string]*types.Contact)}
	myIdentity, err := identity.CreateIdentity(ks, "alice")
	require.NoError(t, err)
	mySK, err := identity.GetSecretKey(ks, "alice")
	require.NoError(t, err)
	bad := signedIncomingEvent(t, nostr.Generate(), mySK.Public(), "bad compressed payload", nostr.Tags{{"z", messaging.CompressTag}})
	compressed, err := messaging.CompressText("newly stored")
	require.NoError(t, err)
	good := signedIncomingEvent(t, nostr.Generate(), mySK.Public(), compressed, nostr.Tags{{"z", messaging.CompressTag}})
	queryErr := errors.New("relay failed after partial page")
	stats := relayquery.Stats{Pages: 2, Fetched: 2, FinishedHint: false}
	hooks := incomingReceiveHooks{store: func(event *nostr.Event, recipient, body string, encrypted bool) (bool, error) {
		return messaging.StoreIncomingMessageOnce(event, recipient, body, encrypted)
	}}
	walkCalls := 0
	walk := func(ctx context.Context, url string, filter nostr.Filter, callback func(nostr.Event) error) (relayquery.Stats, error) {
		walkCalls++
		assert.Zero(t, filter.Limit, "Walk owns pagination limits")
		for _, event := range []*nostr.Event{bad, good} {
			if err := callback(*event); err != nil {
				return stats, err
			}
		}
		return stats, queryErr
	}

	result, scanErr := watchOneRelayWithHooksResult(context.Background(), "wss://relay.invalid", nostr.Filter{Limit: 42}, ks, mySK, newSeenSet(), false, false, myIdentity, nil, hooks, walk)

	require.Equal(t, 1, walkCalls)
	assert.ErrorIs(t, scanErr, queryErr)
	assert.Contains(t, scanErr.Error(), "failed processing")
	assert.Equal(t, stats, result.Stats)
	assert.Equal(t, 1, result.NewMessages)
	assert.Equal(t, 1, result.ProcessingFailures)
	assert.True(t, result.QueryFailed)
	assert.False(t, result.Canceled)
}

func TestWatchOneRelayWithHooksResultCountsOnlyDurableFirstWrites(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ks := &types.KeyStore{Identities: make(map[string]*types.Identity), Contacts: make(map[string]*types.Contact)}
	myIdentity, err := identity.CreateIdentity(ks, "alice")
	require.NoError(t, err)
	mySK, err := identity.GetSecretKey(ks, "alice")
	require.NoError(t, err)
	compressed, err := messaging.CompressText("already stored")
	require.NoError(t, err)
	event := signedIncomingEvent(t, nostr.Generate(), mySK.Public(), compressed, nostr.Tags{{"z", messaging.CompressTag}})
	calls := 0
	hooks := incomingReceiveHooks{store: func(*nostr.Event, string, string, bool) (bool, error) {
		calls++
		return false, nil
	}}
	walk := func(ctx context.Context, url string, filter nostr.Filter, callback func(nostr.Event) error) (relayquery.Stats, error) {
		require.NoError(t, callback(*event))
		return relayquery.Stats{Pages: 1, Fetched: 1, FinishedHint: true}, nil
	}
	result, err := watchOneRelayWithHooksResult(context.Background(), "wss://relay.invalid", nostr.Filter{}, ks, mySK, newSeenSet(), false, false, myIdentity, nil, hooks, walk)
	require.NoError(t, err)
	assert.Equal(t, 1, calls)
	assert.Zero(t, result.NewMessages, "an existing durable message is not newly stored")
	assert.Zero(t, result.ProcessingFailures)
	assert.True(t, result.Stats.FinishedHint, "retain the hint as a hint only")
}

func TestWatchOneRelayScanResultWithLocalRelay(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	messaging.ResetStoreForTest()
	t.Cleanup(messaging.ResetStoreForTest)
	require.NoError(t, messaging.InitStorage())
	ks := &types.KeyStore{Identities: make(map[string]*types.Identity), Contacts: make(map[string]*types.Contact)}
	myIdentity, err := identity.CreateIdentity(ks, "alice")
	require.NoError(t, err)
	mySK, err := identity.GetSecretKey(ks, "alice")
	require.NoError(t, err)
	compressed, err := messaging.CompressText("local relay result")
	require.NoError(t, err)
	event := signedIncomingEvent(t, nostr.Generate(), mySK.Public(), compressed, nostr.Tags{{"z", messaging.CompressTag}})
	relayURL, _ := startHistoryRelay(t, []*nostr.Event{event})
	hooks := incomingReceiveHooks{store: messaging.StoreIncomingMessageOnce}

	result, err := watchOneRelayWithHooksResult(context.Background(), relayURL, nostr.Filter{}, ks, mySK, newSeenSet(), false, false, myIdentity, nil, hooks, relayquery.Walk)
	require.NoError(t, err)
	assert.Equal(t, 1, result.NewMessages)
	assert.Equal(t, 1, result.Stats.Pages)
	assert.Equal(t, 1, result.Stats.Fetched)
	assert.False(t, result.Stats.FinishedHint, "ordinary EOSE is not a finish hint")
}

func TestWatchOneRelayWithHooksResultSeparatesWalkDeadlineFromParentCancellation(t *testing.T) {
	deadlineErr := context.DeadlineExceeded
	deadlineWalk := func(context.Context, string, nostr.Filter, func(nostr.Event) error) (relayquery.Stats, error) {
		return relayquery.Stats{Pages: 1, Fetched: 3}, deadlineErr
	}
	result, err := watchOneRelayWithHooksResult(context.Background(), "wss://relay.invalid", nostr.Filter{}, nil, nostr.SecretKey{}, newSeenSet(), false, false, nil, nil, incomingReceiveHooks{}, deadlineWalk)
	assert.ErrorIs(t, err, deadlineErr)
	assert.True(t, result.QueryFailed, "an internal Walk deadline with a live parent is incomplete")
	assert.False(t, result.Canceled)
	assert.Equal(t, 3, result.Stats.Fetched)

	parent, cancel := context.WithCancel(context.Background())
	cancelWalk := func(ctx context.Context, _ string, _ nostr.Filter, _ func(nostr.Event) error) (relayquery.Stats, error) {
		cancel()
		return relayquery.Stats{Pages: 1, Fetched: 2}, ctx.Err()
	}
	result, err = watchOneRelayWithHooksResult(parent, "wss://relay.invalid", nostr.Filter{}, nil, nostr.SecretKey{}, newSeenSet(), false, false, nil, nil, incomingReceiveHooks{}, cancelWalk)
	assert.ErrorIs(t, err, context.Canceled)
	assert.False(t, result.QueryFailed)
	assert.True(t, result.Canceled)
	assert.Equal(t, 2, result.Stats.Fetched)
}

func TestWatchOneRelayWithHooksResultRejectsNilWalk(t *testing.T) {
	result, err := watchOneRelayWithHooksResult(context.Background(), "wss://relay.invalid", nostr.Filter{}, nil, nostr.SecretKey{}, newSeenSet(), false, false, nil, nil, incomingReceiveHooks{}, nil)
	require.EqualError(t, err, "relay history walk is required")
	assert.False(t, result.Visited)
	assert.False(t, result.QueryFailed, "an invalid hook is not a relay query result")
}

func TestWatchInboxWithResultsLeavesUnvisitedRelayUnknown(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ks := &types.KeyStore{Identities: make(map[string]*types.Identity), Contacts: make(map[string]*types.Contact)}
	myIdentity, err := identity.CreateIdentity(ks, "alice")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	walkCalls := 0
	walk := func(ctx context.Context, _ string, _ nostr.Filter, _ func(nostr.Event) error) (relayquery.Stats, error) {
		walkCalls++
		cancel()
		return relayquery.Stats{Pages: 1, Fetched: 4}, ctx.Err()
	}
	count, results, err := watchInboxWithResults(ctx, myIdentity, ks, newSeenSet(), []string{"wss://first.invalid", "wss://second.invalid"}, false, false, walk)
	require.ErrorIs(t, err, context.Canceled)
	assert.Zero(t, count)
	assert.Equal(t, 1, walkCalls)
	require.Len(t, results, 2)
	assert.Equal(t, 0, results[0].RelayIndex)
	assert.True(t, results[0].Visited)
	assert.True(t, results[0].Canceled)
	assert.Equal(t, 4, results[0].Stats.Fetched)
	assert.Equal(t, 1, results[1].RelayIndex)
	assert.False(t, results[1].Visited)
	assert.False(t, results[1].Canceled, "an unvisited relay has no observed result")
}

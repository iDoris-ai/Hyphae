package messaging

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"github.com/iDoris-ai/hyphae/internal/identity"
	"github.com/iDoris-ai/hyphae/internal/relay"
	"github.com/iDoris-ai/hyphae/internal/relayquery"
	"github.com/iDoris-ai/hyphae/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWatchAgentInboxRecoversHistoryAndDeduplicatesLiveOverlap(t *testing.T) {
	recipient, recipientPK := setupAgentInbox(t)
	ResetStoreForTest()
	t.Cleanup(ResetStoreForTest)
	store, err := GetStore()
	require.NoError(t, err)
	handler, closeRelay, err := relay.New(relay.Config{Address: "127.0.0.1:0", DataDir: t.TempDir()})
	require.NoError(t, err)
	server := httptest.NewServer(handler)
	t.Cleanup(func() {
		server.Close()
		closeRelay()
	})
	relayURL := "ws" + strings.TrimPrefix(server.URL, "http")

	event := makeInboxEvent(t, recipientPK, nostr.Generate(), nostr.Now(), "arrived before TUI", nil)
	publishCtx, cancelPublish := context.WithTimeout(context.Background(), 3*time.Second)
	publisher, err := nostr.RelayConnect(publishCtx, relayURL, nostr.RelayOptions{})
	require.NoError(t, err)
	require.NoError(t, publisher.Publish(publishCtx, event))
	publisher.Close()
	cancelPublish()

	watchCtx, cancelWatch := context.WithCancel(context.Background())
	updates := make(chan AgentInboxWatchUpdate, 16)
	watchDone := make(chan error, 1)
	go func() {
		watchDone <- WatchAgentInboxWithStore(watchCtx, recipient.Nickname, []string{relayURL, relayURL}, store, func(update AgentInboxWatchUpdate) {
			updates <- update
		})
	}()

	deadline := time.After(5 * time.Second)
	messageUpdates := 0
	for messageUpdates == 0 {
		select {
		case update := <-updates:
			if update.Message != nil {
				messageUpdates++
				assert.Equal(t, event.ID.Hex(), update.Message.EventID)
				assert.Equal(t, "arrived before TUI", update.Message.Content)
			}
		case <-deadline:
			t.Fatal("watcher did not recover the historical message")
		}
	}

	// The one-second overlap deliberately returns the same event through the
	// live subscription. It must not emit a second UI notification or row.
	quietUntil := time.After(350 * time.Millisecond)
waitForOverlap:
	for {
		select {
		case update := <-updates:
			if update.Message != nil {
				messageUpdates++
			}
		case <-quietUntil:
			break waitForOverlap
		}
	}
	assert.Equal(t, 1, messageUpdates)
	stored, err := mustGetStoredMessage(t, event.ID.Hex())
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.Equal(t, "arrived before TUI", stored.Plaintext)

	cancelWatch()
	select {
	case err := <-watchDone:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("watcher did not stop after context cancellation")
	}
}

func TestWatchAgentInboxReceivesLatePublishedOldCreatedEvent(t *testing.T) {
	recipient, recipientPK := setupAgentInbox(t)
	store, err := GetStore()
	require.NoError(t, err)
	handler, closeRelay, err := relay.New(relay.Config{Address: "127.0.0.1:0", DataDir: t.TempDir()})
	require.NoError(t, err)
	server := httptest.NewServer(handler)
	t.Cleanup(func() {
		server.Close()
		closeRelay()
	})
	relayURL := "ws" + strings.TrimPrefix(server.URL, "http")

	watchCtx, cancelWatch := context.WithCancel(context.Background())
	updates := make(chan AgentInboxWatchUpdate, 16)
	watchDone := make(chan error, 1)
	go func() {
		watchDone <- WatchAgentInboxWithStore(watchCtx, recipient.Nickname, []string{relayURL}, store, func(update AgentInboxWatchUpdate) {
			updates <- update
		})
	}()
	t.Cleanup(func() {
		cancelWatch()
		select {
		case <-watchDone:
		case <-time.After(3 * time.Second):
			t.Error("watcher did not stop during cleanup")
		}
	})

	connected := false
	connectDeadline := time.NewTimer(5 * time.Second)
	defer connectDeadline.Stop()
	for !connected {
		select {
		case update := <-updates:
			if update.Err != nil && !update.Connected {
				continue
			}
			connected = update.Connected && update.Err == nil
		case <-connectDeadline.C:
			t.Fatal("inbox watcher did not connect to the relay")
		}
	}

	// This event was signed before the receiver's history scan but is only
	// published after the live subscription is established, matching a durable
	// outbox retry after the relay comes back online. A CreatedAt-based overlap
	// cursor must not silently discard it.
	createdAt := nostr.Now() - 10
	event := makeInboxEvent(t, recipientPK, nostr.Generate(), createdAt, "late published offline event", nil)
	publishCtx, cancelPublish := context.WithTimeout(context.Background(), 3*time.Second)
	publisher, err := nostr.RelayConnect(publishCtx, relayURL, nostr.RelayOptions{})
	require.NoError(t, err)
	require.NoError(t, publisher.Publish(publishCtx, event))
	publisher.Close()
	cancelPublish()

	oldOverlapFilter := BuildAgentMessageFilter(recipientPK.Hex())
	oldOverlapFilter.Since = nostr.Now() - 1
	historyCtx, cancelHistory := context.WithTimeout(context.Background(), 3*time.Second)
	page, err := relayquery.Fetch(historyCtx, relayURL, oldOverlapFilter)
	cancelHistory()
	require.NoError(t, err)
	assert.Empty(t, page.Events, "the previous 1-second CreatedAt overlap would have filtered out this unpublished-then-late event")

	select {
	case update := <-updates:
		require.NoError(t, update.Err)
		require.NotNil(t, update.Message)
		assert.Equal(t, event.ID.Hex(), update.Message.EventID)
		assert.Equal(t, "late published offline event", update.Message.Content)
	case <-time.After(5 * time.Second):
		t.Fatal("watcher did not receive the late-published event with an old CreatedAt")
	}
	stored, err := mustGetStoredMessage(t, event.ID.Hex())
	require.NoError(t, err)
	require.NotNil(t, stored, "late-published event must be durable in receiver history")
}

func TestWatchAgentInboxWithStoreRejectsInvalidInputs(t *testing.T) {
	emit := func(AgentInboxWatchUpdate) {}
	cases := []struct {
		name     string
		ctx      context.Context
		nickname string
		relays   []string
		store    *storage.MessageStore
		emit     func(AgentInboxWatchUpdate)
	}{
		{name: "missing context", nickname: "alice", relays: []string{"ws://127.0.0.1:1"}, emit: emit},
		{name: "missing identity", ctx: context.Background(), relays: []string{"ws://127.0.0.1:1"}, emit: emit},
		{name: "missing callback", ctx: context.Background(), nickname: "alice", relays: []string{"ws://127.0.0.1:1"}},
		{name: "missing store", ctx: context.Background(), nickname: "alice", relays: []string{"ws://127.0.0.1:1"}, emit: emit},
		{name: "empty relays", ctx: context.Background(), nickname: "alice", emit: emit},
		{name: "only empty relay", ctx: context.Background(), nickname: "alice", relays: []string{""}, emit: emit},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := WatchAgentInboxWithStore(tc.ctx, tc.nickname, tc.relays, tc.store, tc.emit)
			require.Error(t, err)
		})
	}
}

func TestStoreIncomingWatchEventRejectsBadEventAndContinues(t *testing.T) {
	recipient, recipientPK := setupAgentInbox(t)
	ResetStoreForTest()
	t.Cleanup(ResetStoreForTest)
	store, err := GetStore()
	require.NoError(t, err)
	keyStore, err := identity.LoadKeyStore()
	require.NoError(t, err)
	recipientSK, err := identity.GetSecretKey(keyStore, recipient.Nickname)
	require.NoError(t, err)
	updates := make([]AgentInboxWatchUpdate, 0, 4)
	emit := func(update AgentInboxWatchUpdate) { updates = append(updates, update) }

	badEvents := []nostr.Event{
		makeInboxEvent(t, nostr.Generate().Public(), nostr.Generate(), nostr.Now(), "wrong recipient", nil),
		makeInboxEvent(t, recipientPK, nostr.Generate(), nostr.Now()+1, "invalid zstd", nostr.Tags{{"z", CompressTag}}),
		makeInboxEvent(t, recipientPK, nostr.Generate(), nostr.Now()+2, "invalid ciphertext", nostr.Tags{{"enc", "nip44"}}),
	}
	for _, bad := range badEvents {
		storeIncomingWatchEvent(&bad, recipient, recipientSK, recipientPK.Hex(), store, "ws://127.0.0.1:1", emit)
		stored, err := mustGetStoredMessage(t, bad.ID.Hex())
		require.NoError(t, err)
		assert.Nil(t, stored, "rejected event must never create a placeholder row")
	}

	valid := makeInboxEvent(t, recipientPK, nostr.Generate(), nostr.Now(), "later valid event", nil)
	storeIncomingWatchEvent(&valid, recipient, recipientSK, recipientPK.Hex(), store, "ws://127.0.0.1:1", emit)
	require.Len(t, updates, 4)
	for _, update := range updates[:3] {
		assert.Error(t, update.Err)
		assert.Nil(t, update.Message)
	}
	assert.Nil(t, updates[3].Err)
	require.NotNil(t, updates[3].Message)
	assert.Equal(t, "later valid event", updates[3].Message.Content)
}

func TestWatchAgentInboxWalksPastFirstPageWithSameSecondEvents(t *testing.T) {
	recipient, recipientPK := setupAgentInbox(t)
	ResetStoreForTest()
	t.Cleanup(ResetStoreForTest)
	store, err := GetStore()
	require.NoError(t, err)
	handler, closeRelay, err := relay.New(relay.Config{Address: "127.0.0.1:0", DataDir: t.TempDir()})
	require.NoError(t, err)
	server := httptest.NewServer(handler)
	t.Cleanup(func() {
		server.Close()
		closeRelay()
	})
	relayURL := "ws" + strings.TrimPrefix(server.URL, "http")
	sender := nostr.Generate()
	createdAt := nostr.Now()
	events := make([]nostr.Event, 120)
	publishCtx, cancelPublish := context.WithTimeout(context.Background(), 10*time.Second)
	publisher, err := nostr.RelayConnect(publishCtx, relayURL, nostr.RelayOptions{})
	require.NoError(t, err)
	for i := range events {
		dTag, err := FormatAgentMessageDTag(fmt.Sprintf("%016x", i+1))
		require.NoError(t, err)
		events[i] = makeInboxEvent(t, recipientPK, sender, createdAt, fmt.Sprintf("historical-%03d", i), nostr.Tags{{"d", dTag}})
		require.NoError(t, publisher.Publish(publishCtx, events[i]))
	}
	publisher.Close()
	cancelPublish()

	watchCtx, cancelWatch := context.WithCancel(context.Background())
	updates := make(chan AgentInboxWatchUpdate, 256)
	watchDone := make(chan error, 1)
	go func() {
		watchDone <- WatchAgentInboxWithStore(watchCtx, recipient.Nickname, []string{relayURL}, store, func(update AgentInboxWatchUpdate) {
			updates <- update
		})
	}()
	seen := make(map[string]bool, len(events))
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	for len(seen) < len(events) {
		select {
		case update := <-updates:
			if update.Message != nil {
				seen[update.Message.EventID] = true
			}
		case <-timer.C:
			t.Fatalf("received %d of %d historical events", len(seen), len(events))
		}
	}
	cancelWatch()
	select {
	case err := <-watchDone:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("watcher did not stop after context cancellation")
	}
	for _, event := range events {
		stored, err := mustGetStoredMessage(t, event.ID.Hex())
		require.NoError(t, err)
		require.NotNil(t, stored, "event %s should be durable", event.ID.Hex())
	}
}

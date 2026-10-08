package tui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"github.com/gorilla/websocket"
	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/internal/identity"
	"github.com/iDoris-ai/hyphae/internal/messaging"
	"github.com/iDoris-ai/hyphae/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFormatOutboxStatusDistinguishesQueueAcceptanceAndFailure(t *testing.T) {
	const eventID = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	tests := []struct {
		name   string
		update outboxDeliveryUpdate
		want   []string
		avoid  []string
	}{
		{
			name:   "queued is not delivered",
			update: outboxDeliveryUpdate{eventID: eventID, state: messaging.AgentMessageQueued},
			want:   []string{"Outbox: queued for retry", eventID[:12], "not delivered", "awaiting relay ACK"},
			avoid:  []string{"relay accepted", "recipient received"},
		},
		{
			name:   "relay acceptance is not recipient delivery",
			update: outboxDeliveryUpdate{eventID: eventID, state: messaging.AgentMessageRelayAccepted},
			want:   []string{"Outbox: relay accepted", eventID[:12], "recipient delivery/read not confirmed"},
			avoid:  []string{"recipient received"},
		},
		{
			name:   "uncertain queue never claims queued",
			update: outboxDeliveryUpdate{eventID: eventID, state: messaging.AgentMessageFailed, issue: messaging.AgentMessageIssueQueueStateUnknown},
			want:   []string{"Outbox: failed", "queue state unknown", "verify before resending"},
			avoid:  []string{"queued for retry", "relay accepted"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := formatOutboxStatus(test.update)
			for _, fragment := range test.want {
				assert.Contains(t, got, fragment)
			}
			for _, fragment := range test.avoid {
				assert.NotContains(t, got, fragment)
			}
		})
	}
}

func TestTUIOutboxQueuesAndRestartsWithSameSignedEvent(t *testing.T) {
	cleanup := setupTestEnv(t)
	defer cleanup()
	ks, err := identity.LoadKeyStore()
	require.NoError(t, err)
	_, err = identity.CreateIdentity(ks, "offline-peer")
	require.NoError(t, err)
	ks, err = identity.LoadKeyStore()
	require.NoError(t, err)
	peer, err := identity.GetIdentity(ks, "offline-peer")
	require.NoError(t, err)
	require.NoError(t, identity.AddContact(ks, peer.Nickname, peer.Npub))

	var accept atomic.Bool
	relayURL, eventIDs := startTUIOutboxRelay(t, &accept, nil)
	model, err := NewChatModel(peer.Nickname, relayURL)
	require.NoError(t, err)
	model.startOutboxWorker()()
	model.outboxRequests <- outboxSendRequest{requestID: 1, content: "offline private message"}

	queued := waitTUIOutboxUpdate(t, model.outboxUpdates, func(update outboxDeliveryUpdate) bool {
		return update.requestID == 1 && update.state == messaging.AgentMessageQueued
	})
	require.NotEmpty(t, queued.eventID)
	assert.Contains(t, formatOutboxStatus(queued), "not delivered; awaiting relay ACK")

	initialID := receiveTUIEventID(t, eventIDs)
	assert.Equal(t, queued.eventID, initialID)
	outbox, err := messaging.LoadOutbox()
	require.NoError(t, err)
	require.Equal(t, []string{initialID}, outboxEntryIDs(outbox), "relay rejection must retain the durable queue entry")
	assert.Equal(t, initialID, outbox.Entries[0].ID)
	assert.Equal(t, "pending", outbox.Entries[0].Status)
	assert.False(t, strings.Contains(outbox.Entries[0].EventJSON, "offline private message"), "only the signed encrypted event belongs in the outbox")

	require.NoError(t, model.Close(), "closing the TUI must cancel and join its retry worker")
	accept.Store(true)

	// A new TUI process reconstructs pending state from the same durable outbox.
	reopened, err := NewChatModel(peer.Nickname, relayURL)
	require.NoError(t, err)
	reopened.startOutboxWorker()()
	defer reopened.Close()

	restored := waitTUIOutboxUpdate(t, reopened.outboxUpdates, func(update outboxDeliveryUpdate) bool {
		return update.eventID == initialID && update.state == messaging.AgentMessageQueued
	})
	assert.Contains(t, formatOutboxStatus(restored), initialID[:12])
	accepted := waitTUIOutboxUpdate(t, reopened.outboxUpdates, func(update outboxDeliveryUpdate) bool {
		return update.eventID == initialID && update.state == messaging.AgentMessageRelayAccepted
	})
	assert.Contains(t, formatOutboxStatus(accepted), "recipient delivery/read not confirmed")
	assert.Equal(t, initialID, receiveTUIEventID(t, eventIDs), "retry must reuse the original signed event ID")

	outbox, err = messaging.LoadOutbox()
	require.NoError(t, err)
	assert.Empty(t, outboxEntryIDs(outbox), "ACKed entry is removed by the shared AttemptSend path")
	stored, err := reopened.store.GetMessage(initialID)
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.True(t, stored.Plaintext == "offline private message", "local history must preserve the original text")
}

func TestTUIOutboxRetriesCompetingWorkersIdempotently(t *testing.T) {
	cleanup := setupTestEnv(t)
	defer cleanup()
	ks, err := identity.LoadKeyStore()
	require.NoError(t, err)
	_, err = identity.CreateIdentity(ks, "race-peer")
	require.NoError(t, err)
	ks, err = identity.LoadKeyStore()
	require.NoError(t, err)
	peer, err := identity.GetIdentity(ks, "race-peer")
	require.NoError(t, err)
	require.NoError(t, identity.AddContact(ks, peer.Nickname, peer.Npub))

	var accept atomic.Bool
	accept.Store(false)
	var holdAck atomic.Bool
	releaseAck := make(chan struct{})
	relayURL, eventIDs := startTUIOutboxRelay(t, &accept, &holdAck, releaseAck)
	model, err := NewChatModel(peer.Nickname, relayURL)
	require.NoError(t, err)
	model.startOutboxWorker()()
	model.outboxRequests <- outboxSendRequest{requestID: 1, content: "race-safe message"}
	queued := waitTUIOutboxUpdate(t, model.outboxUpdates, func(update outboxDeliveryUpdate) bool {
		return update.requestID == 1 && update.state == messaging.AgentMessageQueued
	})
	_ = receiveTUIEventID(t, eventIDs)
	require.NoError(t, model.Close())

	accept.Store(true)
	holdAck.Store(true)
	_, err = messaging.UpdateOutbox(func(outbox *types.Outbox) error {
		for i := range outbox.Entries {
			if outbox.Entries[i].ID == queued.eventID {
				outbox.Entries[i].LastAttempt = time.Now().Add(-time.Minute).Unix()
			}
		}
		return nil
	})
	require.NoError(t, err)
	competing, err := NewChatModel(peer.Nickname, relayURL)
	require.NoError(t, err)
	defer competing.Close()
	ctx := competing.outboxCtx
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			competing.retryPendingOutbox(ctx)
		}()
	}
	first := receiveTUIEventID(t, eventIDs)
	second := receiveTUIEventID(t, eventIDs)
	assert.Equal(t, queued.eventID, first)
	assert.Equal(t, first, second, "competing retry attempts may duplicate relay observation but must reuse one signed event")
	close(releaseAck)
	wg.Wait()
	outbox, err := messaging.LoadOutbox()
	require.NoError(t, err)
	assert.Empty(t, outboxEntryIDs(outbox))
	conversation, err := competing.store.GetConversation(competing.myIdentity.Npub, competing.contactNpub, -1)
	require.NoError(t, err)
	assert.Len(t, conversation, 1, "same-event concurrent retry remains history-idempotent")
}

func TestTUIOutboxCloseCancelsAndJoinsInflightSend(t *testing.T) {
	cleanup := setupTestEnv(t)
	defer cleanup()
	ks, err := identity.LoadKeyStore()
	require.NoError(t, err)
	_, err = identity.CreateIdentity(ks, "close-peer")
	require.NoError(t, err)
	ks, err = identity.LoadKeyStore()
	require.NoError(t, err)
	peer, err := identity.GetIdentity(ks, "close-peer")
	require.NoError(t, err)
	require.NoError(t, identity.AddContact(ks, peer.Nickname, peer.Npub))

	var accept atomic.Bool
	var holdAck atomic.Bool
	holdAck.Store(true)
	relayURL, eventIDs := startTUIOutboxRelay(t, &accept, &holdAck)
	model, err := NewChatModel(peer.Nickname, relayURL)
	require.NoError(t, err)
	model.startOutboxWorker()()
	model.outboxRequests <- outboxSendRequest{requestID: 1, content: "shutdown race message"}
	eventID := receiveTUIEventID(t, eventIDs)

	outbox, err := messaging.LoadOutbox()
	require.NoError(t, err)
	require.Equal(t, []string{eventID}, outboxEntryIDs(outbox))
	assert.Equal(t, eventID, outbox.Entries[0].ID, "durable enqueue precedes the blocked publish")
	require.NoError(t, model.Close(), "Close must cancel and join the in-flight network attempt")
	select {
	case <-model.outboxDone:
	default:
		t.Fatal("Close returned before the TUI-owned outbox worker stopped")
	}
	outbox, err = messaging.LoadOutbox()
	require.NoError(t, err)
	require.Equal(t, []string{eventID}, outboxEntryIDs(outbox), "shutdown during publish must retain the queue entry")
	assert.Equal(t, eventID, outbox.Entries[0].ID)
	assert.Equal(t, "pending", outbox.Entries[0].Status)
}

func TestTUIOutboxRetriesAllCurrentIdentityContactsButNeverAnotherIdentity(t *testing.T) {
	cleanup := setupTestEnv(t)
	defer cleanup()
	ks, err := identity.LoadKeyStore()
	require.NoError(t, err)
	_, err = identity.CreateIdentity(ks, "current-contact")
	require.NoError(t, err)
	_, err = identity.CreateIdentity(ks, "other-contact")
	require.NoError(t, err)
	_, err = identity.CreateIdentity(ks, "foreign-sender")
	require.NoError(t, err)
	ks, err = identity.LoadKeyStore()
	require.NoError(t, err)
	currentContact, err := identity.GetIdentity(ks, "current-contact")
	require.NoError(t, err)
	otherContact, err := identity.GetIdentity(ks, "other-contact")
	require.NoError(t, err)
	currentSender, err := identity.GetIdentity(ks, "testuser")
	require.NoError(t, err)
	foreignSender, err := identity.GetIdentity(ks, "foreign-sender")
	require.NoError(t, err)
	require.NoError(t, identity.AddContact(ks, currentContact.Nickname, currentContact.Npub))
	require.NoError(t, identity.AddContact(ks, otherContact.Nickname, otherContact.Npub))

	var accept atomic.Bool
	relayURL, eventIDs := startTUIOutboxRelay(t, &accept, nil)
	currentID := queuePlainTestEvent(t, "testuser", currentSender.Npub, currentContact.Npub, relayURL)
	otherContactID := queuePlainTestEvent(t, "testuser", currentSender.Npub, otherContact.Npub, relayURL)
	foreignIdentityID := queuePlainTestEvent(t, "foreign-sender", foreignSender.Npub, currentContact.Npub, relayURL)
	for i := 0; i < 3; i++ {
		assert.NotEmpty(t, receiveTUIEventID(t, eventIDs), "initial rejected publish must leave each event pending")
	}
	_, err = messaging.UpdateOutbox(func(outbox *types.Outbox) error {
		for i := range outbox.Entries {
			outbox.Entries[i].LastAttempt = time.Now().Add(-time.Minute).Unix()
		}
		return nil
	})
	require.NoError(t, err)
	accept.Store(true)

	model, err := NewChatModel(currentContact.Nickname, relayURL)
	require.NoError(t, err)
	model.startOutboxWorker()()
	defer model.Close()
	queued := waitTUIOutboxUpdate(t, model.outboxUpdates, func(update outboxDeliveryUpdate) bool {
		return update.eventID == currentID && update.state == messaging.AgentMessageQueued
	})
	assert.Equal(t, currentID, queued.eventID)
	accepted := waitTUIOutboxUpdate(t, model.outboxUpdates, func(update outboxDeliveryUpdate) bool {
		return update.eventID == currentID && update.state == messaging.AgentMessageRelayAccepted
	})
	assert.Equal(t, currentID, accepted.eventID)

	seen := map[string]int{}
	for i := 0; i < 2; i++ {
		seen[receiveTUIEventID(t, eventIDs)]++
	}
	assert.Equal(t, 1, seen[currentID])
	assert.Equal(t, 1, seen[otherContactID], "a chat worker recovers other contacts for the same identity")
	assert.NotContains(t, seen, foreignIdentityID, "another identity's signed event must not be retried")

	outbox := waitTUIOutboxIDs(t, []string{foreignIdentityID})
	assert.Equal(t, []string{foreignIdentityID}, outboxEntryIDs(outbox))
	for {
		select {
		case update := <-model.outboxUpdates:
			assert.NotEqual(t, foreignIdentityID, update.eventID, "another identity ACK/state must never be shown in this chat")
		default:
			return
		}
	}
}

func queuePlainTestEvent(t *testing.T, nickname, senderNpub, recipientNpub, relayURL string) string {
	t.Helper()
	ks, err := identity.LoadKeyStore()
	require.NoError(t, err)
	secret, err := identity.GetSecretKey(ks, nickname)
	require.NoError(t, err)
	recipient, err := common.ParsePublicKey(recipientNpub)
	require.NoError(t, err)
	createdAt := nostr.Now()
	dTag, err := messaging.NewAgentMessageDTag(nickname, createdAt)
	require.NoError(t, err)
	event := &nostr.Event{
		CreatedAt: createdAt,
		Kind:      messaging.AgentKind,
		Tags: nostr.Tags{
			{"p", common.PubKeyToHex(recipient)},
			{"c", messaging.AgentTag},
			{"v", messaging.AgentVersion},
			{"d", dTag},
		},
		Content: nickname + " private body",
		PubKey:  secret.Public(),
	}
	require.NoError(t, messaging.ValidateAgentMessageEvent(event))
	validSender, err := common.ParsePublicKey(senderNpub)
	require.NoError(t, err)
	require.Equal(t, validSender, event.PubKey)
	event.Sign(secret)
	result, sendErr := messaging.SendQueuedAgentMessage(context.Background(), event, recipientNpub,
		"local history", false, []string{relayURL}, relayDialTimeout)
	require.NoError(t, sendErr)
	require.Equal(t, messaging.AgentMessageQueued, result.State)
	return result.EventID
}

func startTUIOutboxRelay(t *testing.T, accept *atomic.Bool, holdAck *atomic.Bool, releaseAck ...<-chan struct{}) (string, <-chan string) {
	t.Helper()
	eventIDs := make(chan string, 16)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var envelope []json.RawMessage
			if json.Unmarshal(data, &envelope) != nil || len(envelope) < 2 {
				continue
			}
			var command string
			if json.Unmarshal(envelope[0], &command) != nil || command != "EVENT" {
				continue
			}
			var event nostr.Event
			if json.Unmarshal(envelope[1], &event) != nil {
				continue
			}
			select {
			case eventIDs <- event.ID.Hex():
			default:
			}
			if holdAck != nil && holdAck.Load() && len(releaseAck) != 0 {
				select {
				case <-releaseAck[0]:
				case <-r.Context().Done():
					return
				}
			} else if holdAck != nil && holdAck.Load() {
				continue
			}
			response, _ := json.Marshal([]any{"OK", event.ID.Hex(), accept.Load(), "local test relay"})
			if err := conn.WriteMessage(websocket.TextMessage, response); err != nil {
				return
			}
		}
	}))
	t.Cleanup(server.Close)
	return "ws" + strings.TrimPrefix(server.URL, "http"), eventIDs
}

func waitTUIOutboxUpdate(t *testing.T, updates <-chan outboxDeliveryUpdate, matches func(outboxDeliveryUpdate) bool) outboxDeliveryUpdate {
	t.Helper()
	timer := time.NewTimer(8 * time.Second)
	defer timer.Stop()
	for {
		select {
		case update := <-updates:
			if matches(update) {
				return update
			}
		case <-timer.C:
			t.Fatal("timed out waiting for TUI outbox state")
		}
	}
}

func receiveTUIEventID(t *testing.T, eventIDs <-chan string) string {
	t.Helper()
	select {
	case id := <-eventIDs:
		return id
	case <-time.After(8 * time.Second):
		t.Fatal("timed out waiting for relay event")
		return ""
	}
}

func outboxEntryIDs(outbox *types.Outbox) []string {
	ids := make([]string, 0, len(outbox.Entries))
	for _, entry := range outbox.Entries {
		ids = append(ids, entry.ID)
	}
	return ids
}

func waitTUIOutboxIDs(t *testing.T, want []string) *types.Outbox {
	t.Helper()
	deadline := time.NewTimer(8 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		outbox, err := messaging.LoadOutbox()
		if err == nil && assert.ObjectsAreEqual(want, outboxEntryIDs(outbox)) {
			return outbox
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatal("timed out waiting for expected outbox entry IDs")
			return nil
		}
	}
}

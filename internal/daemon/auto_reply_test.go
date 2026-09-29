package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"github.com/gorilla/websocket"
	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/internal/identity"
	"github.com/iDoris-ai/hyphae/internal/messaging"
	"github.com/iDoris-ai/hyphae/pkg/crypto"
	"github.com/iDoris-ai/hyphae/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func autoReplyTestIdentity(t *testing.T) (*types.Identity, *types.KeyStore, nostr.SecretKey, string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	messaging.ResetStoreForTest()
	t.Cleanup(messaging.ResetStoreForTest)
	require.NoError(t, messaging.InitStorage())
	ks := &types.KeyStore{Identities: make(map[string]*types.Identity), Contacts: make(map[string]*types.Contact)}
	identityRecord, err := identity.CreateIdentity(ks, "alice")
	require.NoError(t, err)
	recipientSK := nostr.Generate()
	return identityRecord, ks, recipientSK, common.EncodeNpub(recipientSK.Public())
}

func startAutoReplyRelay(t *testing.T, acknowledge bool) (string, *atomic.Bool, chan nostr.Event, *atomic.Int32) {
	t.Helper()
	shouldAck := &atomic.Bool{}
	shouldAck.Store(acknowledge)
	connections := &atomic.Int32{}
	captured := make(chan nostr.Event, 8)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		_ = conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
		connections.Add(1)
		_, payload, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var envelope []json.RawMessage
		if json.Unmarshal(payload, &envelope) != nil || len(envelope) != 2 {
			return
		}
		var command string
		if json.Unmarshal(envelope[0], &command) != nil || command != "EVENT" {
			return
		}
		var event nostr.Event
		if json.Unmarshal(envelope[1], &event) != nil {
			return
		}
		select {
		case captured <- event:
		default:
		}
		if !shouldAck.Load() {
			return // simulate a relay disconnect before ACK
		}
		ack := []any{"OK", event.ID.Hex(), true, ""}
		ackJSON, _ := json.Marshal(ack)
		_ = conn.WriteMessage(websocket.TextMessage, ackJSON)
		_, _, _ = conn.ReadMessage()
	}))
	t.Cleanup(server.Close)
	return "ws" + strings.TrimPrefix(server.URL, "http"), shouldAck, captured, connections
}

func TestSendAutoReplyQueuedSendPublishesEncryptedHistory(t *testing.T) {
	myIdentity, ks, recipientSK, toNpub := autoReplyTestIdentity(t)
	recipientPK := recipientSK.Public()
	url, _, captured, _ := startAutoReplyRelay(t, true)
	result, err := sendAutoReplyWithEncryptor(context.Background(), myIdentity, ks, toNpub, "hello there", []string{url}, crypto.EncryptMessage)
	require.NoError(t, err)
	assert.Equal(t, 1, result.PublishedTo)
	assert.Equal(t, 1, result.RelayCount)
	assert.True(t, result.HistoryStored)
	assert.False(t, result.QueuedForRetry)
	assert.NotEmpty(t, result.EventID)

	var event nostr.Event
	select {
	case event = <-captured:
	default:
		t.Fatal("relay did not receive auto-reply")
	}
	assert.True(t, event.VerifySignature())
	plaintext, encrypted, err := messaging.DecodeMessageContent(&event, recipientSK)
	require.NoError(t, err)
	assert.True(t, encrypted)
	assert.Equal(t, "[auto-reply] alice received your message: hello there", plaintext)
	assert.Equal(t, "nip44", autoReplyTag(t, event.Tags, "enc"))
	assert.Equal(t, messaging.CompressTag, autoReplyTag(t, event.Tags, "z"))
	assert.Equal(t, "agent", autoReplyTag(t, event.Tags, "c"))
	assert.Equal(t, messaging.AgentVersion, autoReplyTag(t, event.Tags, "v"))
	dTag := autoReplyTag(t, event.Tags, "d")
	assert.Len(t, dTag, 32, "d must contain at least 128 random bits")
	assert.Equal(t, common.PubKeyToHex(recipientPK), autoReplyTag(t, event.Tags, "p"))

	messages, err := messaging.GetConversation(nil, myIdentity.Npub, toNpub, 10)
	require.NoError(t, err)
	require.Len(t, messages, 1)
	assert.Equal(t, plaintext, messages[0].Plaintext)
	assert.True(t, messages[0].IsEncrypted)
	outbox, err := messaging.LoadOutbox()
	require.NoError(t, err)
	assert.Empty(t, outbox.Entries)
}

func TestSendAutoReplyQueuesAndRetriesSameSignedEvent(t *testing.T) {
	myIdentity, ks, recipientSK, toNpub := autoReplyTestIdentity(t)
	url, shouldAck, captured, _ := startAutoReplyRelay(t, false)
	result, err := sendAutoReplyWithEncryptor(context.Background(), myIdentity, ks, toNpub, "retry me", []string{url}, crypto.EncryptMessage)
	require.NoError(t, err)
	assert.Equal(t, 0, result.PublishedTo)
	assert.True(t, result.QueuedForRetry)
	require.Len(t, result.Relays, 1)
	assert.NotEmpty(t, result.Relays[0].Error, "the relay disconnect reason must remain visible")

	outbox, err := messaging.LoadOutbox()
	require.NoError(t, err)
	require.Len(t, outbox.Entries, 1)
	var queuedEvent nostr.Event
	require.NoError(t, json.Unmarshal([]byte(outbox.Entries[0].EventJSON), &queuedEvent))
	firstAttempt := awaitAutoReplyEvent(t, captured)
	assert.Equal(t, queuedEvent.ID, firstAttempt.ID)

	shouldAck.Store(true)
	resultRetry, err := messaging.AttemptSend(context.Background(), outbox, outbox.Entries[0], nil, relayDialTimeout)
	require.NoError(t, err)
	assert.True(t, resultRetry.Sent)
	assert.True(t, resultRetry.HistoryStored)
	secondAttempt := awaitAutoReplyEvent(t, captured)
	assert.Equal(t, queuedEvent.ID, secondAttempt.ID, "retry must publish the persisted signed event")
	assert.Equal(t, queuedEvent.Sig, secondAttempt.Sig)

	messages, err := messaging.GetConversation(nil, myIdentity.Npub, toNpub, 10)
	require.NoError(t, err)
	require.Len(t, messages, 1)
	assert.Equal(t, "[auto-reply] alice received your message: retry me", messages[0].Plaintext)
	assert.True(t, messages[0].IsEncrypted)
	remaining, err := messaging.LoadOutbox()
	require.NoError(t, err)
	assert.Empty(t, remaining.Entries)
	decoded, encrypted, err := messaging.DecodeMessageContent(&secondAttempt, recipientSK)
	require.NoError(t, err)
	assert.True(t, encrypted)
	assert.Equal(t, messages[0].Plaintext, decoded)
}

func awaitAutoReplyEvent(t *testing.T, captured <-chan nostr.Event) nostr.Event {
	t.Helper()
	select {
	case event := <-captured:
		return event
	case <-time.After(2 * time.Second):
		t.Fatal("relay did not receive auto-reply event before timeout")
		return nostr.Event{}
	}
}

func TestSendAutoReplyFailsClosedWhenEncryptionFails(t *testing.T) {
	myIdentity, ks, _, toNpub := autoReplyTestIdentity(t)
	url, _, _, connections := startAutoReplyRelay(t, true)
	result, err := sendAutoReplyWithEncryptor(context.Background(), myIdentity, ks, toNpub, "secret", []string{url}, func(string, nostr.SecretKey, nostr.PubKey) (string, error) {
		return "", errors.New("forced encryption failure")
	})
	require.ErrorContains(t, err, "encrypt NIP-44 auto-reply")
	assert.Empty(t, result.EventID)
	assert.Zero(t, connections.Load(), "failed encryption must stop before relay publishing")
	outbox, err := messaging.LoadOutbox()
	require.NoError(t, err)
	assert.Empty(t, outbox.Entries, "failed encryption must not queue plaintext")
	messages, err := messaging.GetConversation(nil, myIdentity.Npub, toNpub, 10)
	require.NoError(t, err)
	assert.Empty(t, messages, "failed encryption must not persist plaintext history")
}

func TestAutoReplyEventsHaveIndependentDTags(t *testing.T) {
	myIdentity := &types.Identity{Nickname: "alice"}
	senderSK, recipientSK := nostr.Generate(), nostr.Generate()
	_, first, err := buildAutoReplyEvent(myIdentity, senderSK, recipientSK.Public(), "same", crypto.EncryptMessage)
	require.NoError(t, err)
	_, second, err := buildAutoReplyEvent(myIdentity, senderSK, recipientSK.Public(), "same", crypto.EncryptMessage)
	require.NoError(t, err)
	assert.NotEqual(t, autoReplyTag(t, first.Tags, "d"), autoReplyTag(t, second.Tags, "d"))
}

func TestAutoReplyOutcomeReportsACKAndUncertainQueueState(t *testing.T) {
	lines := autoReplyOutcomeLines(messaging.QueuedAgentMessageResult{
		EventID: "event-1", PublishedTo: 1, RelayCount: 1, QueueStateUnknown: true,
	}, errors.New("outbox fsync failed after relay ACK"))
	joined := strings.Join(lines, "\n")
	assert.Contains(t, joined, "relay ACKs: 1/1")
	assert.Contains(t, joined, "outbox state is unknown")
	assert.Contains(t, joined, "outbox fsync failed")
	assert.NotContains(t, joined, "outbox entry removed")

	lines = autoReplyOutcomeLines(messaging.QueuedAgentMessageResult{
		EventID: "event-2", PublishedTo: 1, RelayCount: 1,
	}, errors.New("audit write failed"))
	joined = strings.Join(lines, "\n")
	assert.Contains(t, joined, "outbox entry removed")
	assert.Contains(t, joined, "audit write failed")
	assert.NotContains(t, joined, "not confirmed queued")
}

func autoReplyTag(t *testing.T, tags nostr.Tags, name string) string {
	t.Helper()
	var found string
	for _, tag := range tags {
		if len(tag) > 1 && tag[0] == name {
			require.Empty(t, found, "expected a single %s tag", name)
			found = tag[1]
		}
	}
	require.NotEmpty(t, found, "missing %s tag", name)
	return found
}

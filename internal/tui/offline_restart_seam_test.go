package tui

import (
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/gorilla/websocket"
	"github.com/iDoris-ai/hyphae/internal/identity"
	"github.com/iDoris-ai/hyphae/internal/messaging"
	"github.com/stretchr/testify/require"
)

// TestTUIOfflineRestartSameSignedEventThroughSendSeam drives the production
// chat command path: KeyEnter returns sendMessage, and the worker result is
// applied as outboxDeliveryUpdateMsg. It does not inject outboxSendRequest,
// call the CLI, or start a daemon. The relay is a loopback WebSocket that is
// down for the original send and later accepts the same signed event.
func TestTUIOfflineRestartSameSignedEventThroughSendSeam(t *testing.T) {
	cleanup := setupTestEnv(t)
	defer cleanup()

	const (
		body      = "offline-restart-sealed-dm-7f3a"
		groupBody = "group-history-viewport-leak-9c2e"
		groupID   = "group-offline-restart-seam"
	)

	ks, err := identity.LoadKeyStore()
	require.NoError(t, err)
	peer, err := identity.CreateIdentity(ks, "offline-restart-peer")
	require.NoError(t, err)
	ks, err = identity.LoadKeyStore()
	require.NoError(t, err)
	peer, err = identity.GetIdentity(ks, peer.Nickname)
	require.NoError(t, err)
	require.NoError(t, identity.AddContact(ks, peer.Nickname, peer.Npub))
	peerSK, err := identity.GetSecretKey(ks, peer.Nickname)
	require.NoError(t, err)

	relay := newRestartableTUIRelay(t)
	model, err := NewChatModel(peer.Nickname, relay.url())
	require.NoError(t, err)
	resizeTUI(model)
	startProductionOutboxWorker(t, model)

	driveEnterSend(t, model, body)
	queued := waitTUIOutboxUpdate(t, model.outboxUpdates, func(update outboxDeliveryUpdate) bool {
		return update.requestID == model.activeSendID && update.state == messaging.AgentMessageQueued
	})
	applyDeliveryAndLoad(t, model, queued)
	require.Contains(t, model.outboxStatus, "not delivered")
	require.Contains(t, model.outboxStatus, "awaiting relay ACK")
	require.NotContains(t, model.outboxStatus, "relay accepted")
	require.Empty(t, model.input.Value(), "queued send clears the submitted text")

	original := requirePersistedSignedDM(t, queued.eventID, peerSK, body)
	seedGroupHistoryOutsideDM(t, model, groupBody, groupID)
	assertEncryptedDMViewport(t, model, body, groupBody, groupID)
	require.NoError(t, model.Close())

	// The signed event must still be the original one while the relay is down
	// and no TUI process is running.
	reloaded := requirePersistedSignedDM(t, original.ID.Hex(), peerSK, body)
	require.Equal(t, original.ID, reloaded.ID)
	require.Equal(t, original.Sig, reloaded.Sig)
	require.Equal(t, original.Content, reloaded.Content)

	relay.connect(t)
	reopened, err := NewChatModel(peer.Nickname, relay.url())
	require.NoError(t, err)
	defer reopened.Close()
	resizeTUI(reopened)
	startProductionOutboxWorker(t, reopened)

	restored := waitTUIOutboxUpdate(t, reopened.outboxUpdates, func(update outboxDeliveryUpdate) bool {
		return update.eventID == original.ID.Hex() && update.state == messaging.AgentMessageQueued
	})
	applyDeliveryAndLoad(t, reopened, restored)
	require.Contains(t, reopened.outboxStatus, original.ID.Hex()[:12])
	assertEncryptedDMViewport(t, reopened, body, groupBody, groupID)

	accepted := waitTUIOutboxUpdate(t, reopened.outboxUpdates, func(update outboxDeliveryUpdate) bool {
		return update.eventID == original.ID.Hex() && update.state == messaging.AgentMessageRelayAccepted
	})
	applyDeliveryAndLoad(t, reopened, accepted)
	require.Contains(t, reopened.outboxStatus, "recipient delivery/read not confirmed")
	require.NotContains(t, reopened.outboxStatus, "recipient received")

	published := relay.receive(t)
	require.Equal(t, original.ID, published.ID, "reconnect must retry the original event id")
	require.Equal(t, original.Sig, published.Sig, "reconnect must retry the original signature")
	require.Equal(t, original.Content, published.Content)
	require.Equal(t, original.PubKey, published.PubKey)
	require.True(t, published.VerifySignature())
	require.NotContains(t, published.Content, body)

	assertEncryptedDMViewport(t, reopened, body, groupBody, groupID)
	requireOneHistoryRow(t, reopened, original.ID.Hex())
	outbox, err := messaging.LoadOutbox()
	require.NoError(t, err)
	require.Empty(t, outbox.Entries, "ACK removes the retried entry")
	requireGroupHistoryUntouched(t, reopened, groupBody)
}

func resizeTUI(model *ChatModel) {
	model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
}

func startProductionOutboxWorker(t *testing.T, model *ChatModel) {
	t.Helper()
	msg := model.startOutboxWorker()()
	require.IsType(t, outboxWorkerStartedMsg{}, msg)
	// The started handler only schedules waitOutboxUpdate. The test reads that
	// same channel and feeds outboxDeliveryUpdateMsg back through Update, which
	// is the production result message. Running the waiter here would steal it.
}

func driveEnterSend(t *testing.T, model *ChatModel, body string) {
	t.Helper()
	model.input.SetValue(body)
	_, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	require.NotNil(t, cmd, "enter must return the production send command")
	require.True(t, model.outboxSending)
	require.Contains(t, model.outboxStatus, "durable queue not yet confirmed")
	require.Equal(t, body, model.sendingContent)
	runProductionSendCmd(cmd)
}

// runProductionSendCmd executes the KeyEnter command. tea.Batch does not run
// its children, so a multi-command result has to be unpacked. The send command
// only enqueues; a cursor blink in the same batch returns later.
func runProductionSendCmd(cmd tea.Cmd) {
	go func() {
		msg := cmd()
		batch, ok := msg.(tea.BatchMsg)
		if !ok {
			return
		}
		for _, child := range batch {
			if child != nil {
				go func(c tea.Cmd) { _ = c() }(child)
			}
		}
	}()
}

func applyDeliveryAndLoad(t *testing.T, model *ChatModel, update outboxDeliveryUpdate) {
	t.Helper()
	_, cmd := model.Update(outboxDeliveryUpdateMsg{update: update})
	require.NotNil(t, cmd, "a delivery result must schedule history refresh and the next waiter")
	requireScheduledHistoryRefresh(t, model, cmd)
}

// requireScheduledHistoryRefresh runs the history command scheduled by Update.
// The following outbox waiter is left unstarted so it cannot consume the retry
// result the test is about to read from the same channel.
func requireScheduledHistoryRefresh(t *testing.T, model *ChatModel, cmd tea.Cmd) {
	t.Helper()
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		messages, isMessages := msg.(messagesMsg)
		require.Truef(t, isMessages, "delivery follow-up must refresh history, got %T", msg)
		model.Update(messages)
		return
	}
	require.NotEmpty(t, batch)
	refreshed := make(chan tea.Msg, 1)
	go func() { refreshed <- batch[0]() }()
	select {
	case result := <-refreshed:
		messages, isMessages := result.(messagesMsg)
		require.Truef(t, isMessages, "first delivery follow-up must refresh history, got %T", result)
		model.Update(messages)
	case <-time.After(2 * time.Second):
		t.Fatal("delivery follow-up did not refresh history")
	}
}

func assertEncryptedDMViewport(t *testing.T, model *ChatModel, body, groupBody, groupID string) {
	t.Helper()
	loaded := model.loadMessages()()
	messages, ok := loaded.(messagesMsg)
	require.True(t, ok)
	model.Update(messages)
	require.Len(t, model.messages, 1, "DM history must stay one row across queue, reopen, and ACK")
	require.Equal(t, body, model.messages[0].Plaintext)
	require.True(t, model.messages[0].IsEncrypted)
	require.NotEqual(t, body, model.messages[0].Content)
	view := model.View()
	require.Equal(t, 1, strings.Count(view, body), "viewport shows the decrypted DM once")
	require.Contains(t, view, "🔒")
	require.NotContains(t, view, groupBody)
	require.NotContains(t, view, groupID)
}

func requirePersistedSignedDM(t *testing.T, eventID string, peerSK nostr.SecretKey, body string) nostr.Event {
	t.Helper()
	outbox, err := messaging.LoadOutbox()
	require.NoError(t, err)
	require.Len(t, outbox.Entries, 1)
	entry := outbox.Entries[0]
	require.Equal(t, eventID, entry.ID)
	require.Equal(t, "pending", entry.Status)
	require.NotContains(t, entry.EventJSON, body, "durable outbox keeps the ciphertext, not the plaintext")
	var event nostr.Event
	require.NoError(t, json.Unmarshal([]byte(entry.EventJSON), &event))
	require.Equal(t, eventID, event.ID.Hex())
	require.True(t, event.CheckID())
	require.True(t, event.VerifySignature())
	require.NoError(t, messaging.ValidateAgentMessageEvent(&event))
	plaintext, encrypted, err := messaging.DecodeMessageContent(&event, peerSK)
	require.NoError(t, err)
	require.True(t, encrypted)
	require.Equal(t, body, plaintext)
	return event
}

func requireOneHistoryRow(t *testing.T, model *ChatModel, eventID string) {
	t.Helper()
	var count int
	require.NoError(t, model.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE id = ?`, eventID).Scan(&count))
	require.Equal(t, 1, count)
	var total int
	require.NoError(t, model.db.QueryRow(`SELECT COUNT(*) FROM messages`).Scan(&total))
	require.Equal(t, 1, total)
}

func seedGroupHistoryOutsideDM(t *testing.T, model *ChatModel, body, groupID string) {
	t.Helper()
	_, err := model.db.Exec(`
		CREATE TABLE IF NOT EXISTS group_messages (
			id TEXT PRIMARY KEY,
			event_id TEXT UNIQUE,
			group_id TEXT NOT NULL,
			sender TEXT NOT NULL,
			content TEXT,
			plaintext TEXT,
			created_at INTEGER NOT NULL,
			is_encrypted BOOLEAN DEFAULT 0
		)`)
	require.NoError(t, err)
	_, err = model.db.Exec(`
		INSERT INTO group_messages (id, event_id, group_id, sender, content, plaintext, created_at, is_encrypted)
		VALUES ('group-row-offline-restart', 'group-event-offline-restart', ?, 'offline-restart-peer', 'ciphertext', ?, ?, 1)`,
		groupID, body, time.Now().Unix())
	require.NoError(t, err)
	_, err = model.db.Exec(`
		CREATE TABLE IF NOT EXISTS groupchat_messages (
			local_npub TEXT NOT NULL,
			group_id TEXT NOT NULL,
			logical_id TEXT NOT NULL,
			sender_npub TEXT NOT NULL,
			body TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			event_id TEXT,
			PRIMARY KEY (local_npub, group_id, logical_id)
		)`)
	require.NoError(t, err)
	_, err = model.db.Exec(`
		INSERT INTO groupchat_messages (local_npub, group_id, logical_id, sender_npub, body, created_at, event_id)
		VALUES (?, ?, 'logical-offline-restart', ?, ?, ?, 'groupchat-event-offline-restart')`,
		model.myIdentity.Npub, groupID, model.contactNpub, body, time.Now().Unix())
	require.NoError(t, err)
}

func requireGroupHistoryUntouched(t *testing.T, model *ChatModel, body string) {
	t.Helper()
	var groupCount, chatCount int
	require.NoError(t, model.db.QueryRow(`SELECT COUNT(*) FROM group_messages WHERE plaintext = ?`, body).Scan(&groupCount))
	require.NoError(t, model.db.QueryRow(`SELECT COUNT(*) FROM groupchat_messages WHERE body = ?`, body).Scan(&chatCount))
	require.Equal(t, 1, groupCount)
	require.Equal(t, 1, chatCount)
}

// restartableTUIRelay is a real loopback WebSocket. It starts with nothing
// listening, then binds the same address when the TUI should retry.
type restartableTUIRelay struct {
	addr   string
	events chan nostr.Event

	mu     sync.Mutex
	server *http.Server
}

func newRestartableTUIRelay(t *testing.T) *restartableTUIRelay {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	relay := &restartableTUIRelay{
		addr:   ln.Addr().String(),
		events: make(chan nostr.Event, 8),
	}
	require.NoError(t, ln.Close())
	t.Cleanup(func() { relay.disconnect() })
	return relay
}

func (r *restartableTUIRelay) url() string {
	return "ws://" + r.addr
}

func (r *restartableTUIRelay) connect(t *testing.T) {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.server != nil {
		return
	}
	ln, err := net.Listen("tcp", r.addr)
	require.NoError(t, err)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		conn, err := upgrader.Upgrade(w, req, nil)
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
			case r.events <- event:
			default:
			}
			response, _ := json.Marshal([]any{"OK", event.ID.Hex(), true, ""})
			if conn.WriteMessage(websocket.TextMessage, response) != nil {
				return
			}
		}
	})}
	r.server = server
	go func() { _ = server.Serve(ln) }()
}

func (r *restartableTUIRelay) disconnect() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.server != nil {
		_ = r.server.Close()
		r.server = nil
	}
}

func (r *restartableTUIRelay) receive(t *testing.T) nostr.Event {
	t.Helper()
	select {
	case event := <-r.events:
		return event
	case <-time.After(20 * time.Second):
		t.Fatal("relay did not receive the retried signed event")
		return nostr.Event{}
	}
}

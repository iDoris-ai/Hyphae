package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/gorilla/websocket"
	"github.com/iDoris-ai/hyphae/internal/identity"
	"github.com/iDoris-ai/hyphae/internal/messaging"
	"github.com/iDoris-ai/hyphae/pkg/crypto"
	"github.com/iDoris-ai/hyphae/pkg/types"
	"github.com/stretchr/testify/require"
)

func setupExactlyOncePeer(t *testing.T) (*types.Identity, nostr.SecretKey, nostr.SecretKey) {
	t.Helper()
	ks, err := identity.LoadKeyStore()
	require.NoError(t, err)
	_, err = identity.CreateIdentity(ks, "attack-peer")
	require.NoError(t, err)
	ks, err = identity.LoadKeyStore()
	require.NoError(t, err)
	peer, err := identity.GetIdentity(ks, "attack-peer")
	require.NoError(t, err)
	require.NoError(t, identity.AddContact(ks, peer.Nickname, peer.Npub))
	mine, err := identity.GetSecretKey(ks, "testuser")
	require.NoError(t, err)
	theirs, err := identity.GetSecretKey(ks, peer.Nickname)
	require.NoError(t, err)
	return peer, mine, theirs
}

func exactlyOnceEvent(t *testing.T, sender nostr.SecretKey, recipient nostr.PubKey, body string, created nostr.Timestamp, encrypted bool) nostr.Event {
	t.Helper()
	d, err := messaging.NewAgentMessageDTag(body, created)
	require.NoError(t, err)
	event := nostr.Event{Kind: messaging.AgentKind, CreatedAt: created, Content: body, Tags: nostr.Tags{
		{"p", recipient.Hex()}, {"c", messaging.AgentTag}, {"v", messaging.AgentVersion}, {"d", d},
	}}
	if encrypted {
		event.Content, err = crypto.EncryptMessage(body, sender, recipient)
		require.NoError(t, err)
		event.Content, err = messaging.CompressText(event.Content)
		require.NoError(t, err)
		event.Tags = append(event.Tags, nostr.Tag{"enc", "nip44"}, nostr.Tag{"z", messaging.CompressTag})
	}
	require.NoError(t, event.Sign(sender))
	return event
}

// History is empty; the old event is first delivered on the live REQ. The
// signed, invalid-compression sentinel is ordered after every replay and is
// acknowledged by the watcher's error callback, proving the batch was consumed.
func startExactlyOnceReplayRelay(t *testing.T, event, sentinel nostr.Event, repeats int) string {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var fields []json.RawMessage
			if json.Unmarshal(raw, &fields) != nil || len(fields) < 3 {
				continue
			}
			var command, sub string
			var filter nostr.Filter
			if json.Unmarshal(fields[0], &command) != nil || command != "REQ" {
				continue
			}
			if json.Unmarshal(fields[1], &sub) != nil || json.Unmarshal(fields[2], &filter) != nil {
				return
			}
			if filter.Limit == 0 {
				if filter.Matches(event) {
					for i := 0; i < repeats; i++ {
						if conn.WriteJSON([]any{"EVENT", sub, event}) != nil {
							return
						}
					}
				}
				if conn.WriteJSON([]any{"EVENT", sub, sentinel}) != nil {
					return
				}
			}
			if conn.WriteJSON([]any{"EOSE", sub}) != nil {
				return
			}
		}
	}))
	t.Cleanup(server.Close)
	return "ws" + strings.TrimPrefix(server.URL, "http")
}

func TestExactlyOnceRelayReplayAndLateEventAcrossTUIRestart(t *testing.T) {
	cleanup := setupTestEnv(t)
	defer cleanup()
	peer, mine, theirs := setupExactlyOncePeer(t)
	const body = "adversarial-late-offline-event"
	event := exactlyOnceEvent(t, theirs, mine.Public(), body, nostr.Now()-86400, true)
	sentinel := exactlyOnceEvent(t, theirs, mine.Public(), "invalid-compression-barrier", nostr.Now(), false)
	sentinel.Tags = append(sentinel.Tags, nostr.Tag{"z", messaging.CompressTag})
	require.NoError(t, sentinel.Sign(theirs))
	url := startExactlyOnceReplayRelay(t, event, sentinel, 128)
	for run := 0; run < 2; run++ {
		model, err := NewChatModel(peer.Nickname, url, url)
		require.NoError(t, err)
		model.startInboxWatcher()()
		barriers := 0
		deadline := time.NewTimer(10 * time.Second)
		for barriers < 2 {
			select {
			case update := <-model.inboxUpdates:
				if update.Err != nil {
					require.Contains(t, update.Err.Error(), sentinel.ID.Hex())
					barriers++
				}
				model.Update(inboxWatchUpdateMsg{update: update})
			case <-deadline.C:
				_ = model.Close()
				t.Fatal("replay batch was not completely consumed")
			}
		}
		deadline.Stop()
		wantUpdates := 1
		if run == 1 {
			wantUpdates = 0
		}
		require.Equal(t, wantUpdates, model.inboxReceived, "256 relay deliveries must emit at most one durable arrival")
		model.Update(model.loadMessages()())
		require.Len(t, model.messages, 1)
		require.Equal(t, event.ID.Hex(), model.messages[0].ID)
		require.Equal(t, 1, strings.Count(model.View(), body), "history and screen must contain exactly one message")
		require.NoError(t, model.Close())
	}
}

func startExactlyOncePublishRelay(t *testing.T, ack <-chan struct{}) (string, <-chan nostr.Event) {
	t.Helper()
	events := make(chan nostr.Event, 32)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var fields []json.RawMessage
			if json.Unmarshal(raw, &fields) != nil || len(fields) < 2 {
				continue
			}
			var command string
			if json.Unmarshal(fields[0], &command) != nil || command != "EVENT" {
				continue
			}
			var event nostr.Event
			if json.Unmarshal(fields[1], &event) != nil {
				return
			}
			events <- event
			<-ack
			if conn.WriteJSON([]any{"OK", event.ID.Hex(), true, ""}) != nil {
				return
			}
		}
	}))
	t.Cleanup(server.Close)
	return "ws" + strings.TrimPrefix(server.URL, "http"), events
}

func receiveExactlyOnceEvent(t *testing.T, events <-chan nostr.Event) nostr.Event {
	t.Helper()
	select {
	case event := <-events:
		return event
	case <-time.After(10 * time.Second):
		t.Fatal("relay did not receive signed event")
		return nostr.Event{}
	}
}

func TestExactlyOnceTUIRestoresHistoryFromCommittedEncryptedQueue(t *testing.T) {
	cleanup := setupTestEnv(t)
	defer cleanup()
	peer, mine, theirs := setupExactlyOncePeer(t)
	const body = "recover-plaintext-without-signing-again"
	event := exactlyOnceEvent(t, mine, theirs.Public(), body, nostr.Now()-86400, true)
	ack := make(chan struct{})
	defer func() {
		select {
		case <-ack:
		default:
			close(ack)
		}
	}()
	url, events := startExactlyOncePublishRelay(t, ack)
	// Disk state after SIGKILL following queue commit but before history write.
	require.NoError(t, messaging.AddToOutbox(nil, &event, peer.Npub, []string{url}))
	model, err := NewChatModel(peer.Nickname, url)
	require.NoError(t, err)
	defer model.Close()
	stored, err := model.store.GetMessage(event.ID.Hex())
	require.NoError(t, err)
	require.Nil(t, stored)
	model.startOutboxWorker()()
	received := receiveExactlyOnceEvent(t, events)
	stored, err = model.store.GetMessage(event.ID.Hex())
	require.NoError(t, err)
	require.NotNil(t, stored, "TUI recovery must persist history before publishing")
	require.Equal(t, body, stored.Plaintext)
	close(ack)
	update := waitTUIOutboxUpdate(t, model.outboxUpdates, func(update outboxDeliveryUpdate) bool {
		return update.eventID == event.ID.Hex() && update.state == messaging.AgentMessageRelayAccepted
	})
	require.Equal(t, event, received, "every signed field, ciphertext and signature must survive restart")
	_, cmd := model.Update(outboxDeliveryUpdateMsg{update: update})
	// Make the next-update command nonblocking even if the refresh guard is
	// removed. Then require and execute the actual background ACK refresh.
	model.outboxUpdates <- outboxDeliveryUpdate{}
	batch, ok := cmd().(tea.BatchMsg)
	require.True(t, ok, "background ACK must schedule history refresh and next update")
	msg := batch[0]()
	require.IsType(t, messagesMsg{}, msg)
	model.Update(msg)
	require.Len(t, model.messages, 1)
	require.Equal(t, body, model.messages[0].Plaintext)
	require.Equal(t, 1, strings.Count(model.View(), body))
	ob, err := messaging.LoadOutbox()
	require.NoError(t, err)
	require.Empty(t, ob.Entries)
}

func TestExactlyOnceCompetingRetryRestoresHistoryBeforeQueueRemoval(t *testing.T) {
	cleanup := setupTestEnv(t)
	defer cleanup()
	peer, mine, theirs := setupExactlyOncePeer(t)
	const body = "external-retry-must-preserve-sender-history"
	event := exactlyOnceEvent(t, mine, theirs.Public(), body, nostr.Now()-86400, true)
	ack := make(chan struct{})
	close(ack)
	url, events := startExactlyOncePublishRelay(t, ack)
	require.NoError(t, messaging.AddToOutbox(nil, &event, peer.Npub, []string{url}))
	ob, err := messaging.LoadOutbox()
	require.NoError(t, err)
	// The CLI/daemon's shared retry can win before a restarted TUI worker.
	result, err := messaging.AttemptSend(context.Background(), ob, ob.Entries[0], nil, time.Second)
	require.NoError(t, err)
	require.True(t, result.Sent)
	require.True(t, result.HistoryStored)
	require.Equal(t, event, receiveExactlyOnceEvent(t, events))
	model, err := NewChatModel(peer.Nickname, url)
	require.NoError(t, err)
	defer model.Close()
	model.Update(model.loadMessages()())
	require.Len(t, model.messages, 1)
	require.Equal(t, body, model.messages[0].Plaintext)
	require.Equal(t, 1, strings.Count(model.View(), body))
	ob, err = messaging.LoadOutbox()
	require.NoError(t, err)
	require.Empty(t, ob.Entries)
}

func TestExactlyOnceTUIProcessHelper(t *testing.T) {
	if os.Getenv("HYPHAE_TUI_ENQUEUE_HELPER") != "" {
		ks, err := identity.LoadKeyStore()
		require.NoError(t, err)
		secret, err := identity.GetSecretKey(ks, "attack-peer")
		require.NoError(t, err)
		mine, err := identity.GetSecretKey(ks, "testuser")
		require.NoError(t, err)
		event := exactlyOnceEvent(t, secret, mine.Public(), "independent-writer", nostr.Now(), false)
		require.NoError(t, messaging.AddToOutbox(nil, &event, "independent-recipient", nil))
		fmt.Println(event.ID.Hex())
		return
	}
	url := os.Getenv("HYPHAE_TUI_CRASH_RELAY")
	if url == "" {
		return
	}
	model, err := NewChatModel("attack-peer", url)
	require.NoError(t, err)
	model.startOutboxWorker()()
	model.outboxRequests <- outboxSendRequest{requestID: 1, content: "kill-after-relay-received-before-ACK"}
	var gate [1]byte
	_, _ = os.Stdin.Read(gate[:])
	t.Fatal("parent must kill the worker before closing its gate")
}

func TestExactlyOnceTUIWorkerAndProcessWriterDoNotLoseUpdates(t *testing.T) {
	cleanup := setupTestEnv(t)
	defer cleanup()
	peer, _, _ := setupExactlyOncePeer(t)
	ack := make(chan struct{})
	defer func() {
		select {
		case <-ack:
		default:
			close(ack)
		}
	}()
	url, events := startExactlyOncePublishRelay(t, ack)
	model, err := NewChatModel(peer.Nickname, url)
	require.NoError(t, err)
	defer model.Close()
	model.startOutboxWorker()()
	model.outboxRequests <- outboxSendRequest{requestID: 1, content: "TUI-writer-pending-ACK"}
	first := receiveExactlyOnceEvent(t, events)
	// The TUI has captured its queue snapshot and is blocked in network I/O.
	// An independent process commits another signed entry before the ACK.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestExactlyOnceTUIProcessHelper$")
	cmd.Env = append(os.Environ(), "HYPHAE_TUI_ENQUEUE_HELPER=1")
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, string(output))
	otherID := strings.SplitN(string(output), "\n", 2)[0]
	require.Len(t, otherID, 64)
	ob, err := messaging.LoadOutbox()
	require.NoError(t, err)
	require.Len(t, ob.Entries, 2)
	close(ack)
	waitTUIOutboxUpdate(t, model.outboxUpdates, func(update outboxDeliveryUpdate) bool {
		return update.eventID == first.ID.Hex() && update.state == messaging.AgentMessageRelayAccepted
	})
	require.NoError(t, model.Close())
	path, err := messaging.GetOutboxPath()
	require.NoError(t, err)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &ob), "both writers must leave complete JSON")
	require.Len(t, ob.Entries, 1, "ACK cleanup must preserve the other process's enqueue")
	require.Equal(t, otherID, ob.Entries[0].ID)
	require.Equal(t, "pending", ob.Entries[0].Status)
}

func TestExactlyOnceTUIKillAfterPublishRetriesOriginalEvent(t *testing.T) {
	cleanup := setupTestEnv(t)
	defer cleanup()
	peer, _, _ := setupExactlyOncePeer(t)
	ack := make(chan struct{})
	defer func() {
		select {
		case <-ack:
		default:
			close(ack)
		}
	}()
	url, events := startExactlyOncePublishRelay(t, ack)
	cmd := exec.Command(os.Args[0], "-test.run=^TestExactlyOnceTUIProcessHelper$")
	cmd.Env = append(os.Environ(), "HYPHAE_TUI_CRASH_RELAY="+url)
	in, err := cmd.StdinPipe()
	require.NoError(t, err)
	defer in.Close()
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	first := receiveExactlyOnceEvent(t, events)
	require.NoError(t, cmd.Process.Kill())
	require.Error(t, cmd.Wait())
	ob, err := messaging.LoadOutbox()
	require.NoError(t, err)
	require.Len(t, ob.Entries, 1)
	require.Equal(t, first.ID.Hex(), ob.Entries[0].ID)
	close(ack)
	model, err := NewChatModel(peer.Nickname, url)
	require.NoError(t, err)
	defer model.Close()
	model.startOutboxWorker()()
	update := waitTUIOutboxUpdate(t, model.outboxUpdates, func(update outboxDeliveryUpdate) bool {
		return update.state == messaging.AgentMessageRelayAccepted
	})
	require.Equal(t, first, receiveExactlyOnceEvent(t, events), "ACK uncertainty may replay the event, never re-sign it")
	model.Update(outboxDeliveryUpdateMsg{update: update})
	model.Update(model.loadMessages()())
	require.Len(t, model.messages, 1)
	require.Equal(t, first.ID.Hex(), model.messages[0].ID)
	ob, err = messaging.LoadOutbox()
	require.NoError(t, err)
	require.Empty(t, ob.Entries)
}

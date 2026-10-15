package messaging

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"github.com/gorilla/websocket"
	"github.com/iDoris-ai/hyphae/pkg/crypto"
	"github.com/stretchr/testify/require"
)

// startPreEOSEBacklogRelay serves the two connections WatchAgentInboxWithStore
// opens. History pages (Limit > 0, from relayquery.Walk) get EOSE and no
// events, so the walk misses the backlog. The live REQ (Limit == 0) gets
// every event, then EOSE when sendEOSE is set. Dispatch goroutines race, so
// callers must not assume events arrive in slice order.
func startPreEOSEBacklogRelay(t *testing.T, sendEOSE bool, events ...nostr.Event) string {
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
				for _, event := range events {
					if conn.WriteJSON([]any{"EVENT", sub, event}) != nil {
						return
					}
				}
				if !sendEOSE {
					continue
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

func encryptedBacklogDM(t *testing.T, recipient nostr.PubKey, plaintext string) nostr.Event {
	t.Helper()
	sender := nostr.Generate()
	cipher, err := crypto.EncryptMessage(plaintext, sender, recipient)
	require.NoError(t, err)
	return makeInboxEvent(t, recipient, sender, nostr.Now()-3600, cipher, nostr.Tags{{"enc", "nip44"}})
}

func TestWatchAgentInboxKeepsPreEOSEBacklogWhileConsumerBlocked(t *testing.T) {
	recipient, recipientPK := setupAgentInbox(t)
	store, err := GetStore()
	require.NoError(t, err)
	first := encryptedBacklogDM(t, recipientPK, "backlog one")
	second := encryptedBacklogDM(t, recipientPK, "backlog two")
	require.NotEqual(t, first.ID, second.ID)
	require.NotEqual(t, first.PubKey, second.PubKey)
	expected := map[string]string{
		first.ID.Hex():  "backlog one",
		second.ID.Hex(): "backlog two",
	}
	url := startPreEOSEBacklogRelay(t, true, first, second)

	watchCtx, cancelWatch := context.WithCancel(context.Background())
	messages := make(chan ReceivedAgentMessage, 2)
	var blockFirst sync.Once
	watchDone := make(chan error, 1)
	go func() {
		watchDone <- WatchAgentInboxWithStore(watchCtx, recipient.Nickname, []string{url}, store, func(update AgentInboxWatchUpdate) {
			if update.Message == nil {
				return
			}
			messages <- *update.Message
			// Block inside the synchronous consumer. Either DM may arrive
			// first; the other stays in nostr's pre-EOSE dispatch until this
			// returns. 8s is past the library default of 7s.
			blockFirst.Do(func() {
				select {
				case <-time.After(8 * time.Second):
				case <-watchCtx.Done():
				}
			})
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

	delivered := map[string]string{}
	deadline := time.NewTimer(12 * time.Second)
	defer deadline.Stop()
	for len(delivered) < 2 {
		select {
		case message := <-messages:
			plaintext, ok := expected[message.EventID]
			require.True(t, ok, "unexpected event %s", message.EventID)
			require.True(t, message.Decrypted)
			require.True(t, message.IsEncrypted)
			require.Equal(t, plaintext, message.Content)
			delivered[message.EventID] = message.Content
		case <-deadline.C:
			t.Fatalf("pre-EOSE backlog lost while consumer was blocked; delivered %v", delivered)
		}
	}
	require.Equal(t, expected, delivered)
	for id, plaintext := range expected {
		stored, err := mustGetStoredMessage(t, id)
		require.NoError(t, err)
		require.NotNil(t, stored, "event %s must be durable", id)
		require.Equal(t, id, stored.ID)
		require.Equal(t, plaintext, stored.Plaintext)
		require.True(t, stored.IsEncrypted)
		require.True(t, stored.IsIncoming)
	}
}

func nostrGoroutineCount(t *testing.T) int {
	t.Helper()
	buf := make([]byte, 64<<10)
	var n int
	for {
		n = runtime.Stack(buf, true)
		if n < len(buf) {
			break
		}
		if len(buf) >= 64<<20 {
			t.Fatal("goroutine stack dump truncated")
		}
		buf = make([]byte, len(buf)*2)
	}
	count := 0
	for _, stack := range strings.Split(string(buf[:n]), "\n\n") {
		if strings.Contains(stack, "fiatjaf.com/nostr.") {
			count++
		}
	}
	return count
}

func TestWatchAgentInboxShutdownWithoutEOSEReleasesBlockedBacklog(t *testing.T) {
	recipient, recipientPK := setupAgentInbox(t)
	store, err := GetStore()
	require.NoError(t, err)
	events := make([]nostr.Event, 32)
	ids := make(map[string]struct{}, len(events))
	for i := range events {
		events[i] = encryptedBacklogDM(t, recipientPK, fmt.Sprintf("never-eose backlog %02d", i))
		ids[events[i].ID.Hex()] = struct{}{}
	}
	require.Len(t, ids, len(events))
	url := startPreEOSEBacklogRelay(t, false, events...)
	baseline := nostrGoroutineCount(t)

	watchCtx, cancelWatch := context.WithCancel(context.Background())
	firstMessage := make(chan string, 1)
	var signalFirst sync.Once
	watchDone := make(chan error, 1)
	go func() {
		watchDone <- WatchAgentInboxWithStore(watchCtx, recipient.Nickname, []string{url}, store, func(update AgentInboxWatchUpdate) {
			if update.Message == nil {
				return
			}
			signalFirst.Do(func() {
				firstMessage <- update.Message.EventID
			})
			<-watchCtx.Done()
		})
	}()

	var firstID string
	select {
	case firstID = <-firstMessage:
	case <-time.After(5 * time.Second):
		cancelWatch()
		t.Fatal("watcher never delivered the first backlog event")
	}
	_, known := ids[firstID]
	require.True(t, known, "first delivered id %s is not from the backlog", firstID)
	cancelWatch()
	select {
	case err := <-watchDone:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("watcher did not stop with a blocked consumer and no EOSE")
	}
	require.Eventually(t, func() bool {
		return nostrGoroutineCount(t) <= baseline
	}, 3*time.Second, 50*time.Millisecond, "nostr dispatch/subscription goroutines must exit after cancellation")

	stored, err := mustGetStoredMessage(t, firstID)
	require.NoError(t, err)
	require.NotNil(t, stored)
	require.True(t, stored.IsIncoming)
}

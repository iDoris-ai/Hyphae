package daemon

import (
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"github.com/gorilla/websocket"
	"github.com/iDoris-ai/hyphae/internal/groupchat"
	"github.com/iDoris-ai/hyphae/internal/messaging"
	"github.com/iDoris-ai/hyphae/pkg/types"
	"github.com/stretchr/testify/require"
)

type recordingGroupProvider struct {
	calls []groupchat.GroupSendCall
}

func (p *recordingGroupProvider) Send(call groupchat.GroupSendCall) error {
	p.calls = append(p.calls, call)
	return nil
}

func useDaemonGroupProviders(t *testing.T) {
	t.Helper()
	previous := daemonGroupProviders
	daemonGroupProviders = groupchat.NewGroupProviderRegistry()
	t.Cleanup(func() { daemonGroupProviders = previous })
}

func groupOutboxEntry(id, status string, created int64, retry, maxRetry int) types.OutboxEntry {
	return types.OutboxEntry{
		QueueID: "q-" + id, Route: messaging.OutboxRouteGroup, ID: id,
		EventJSON: `{"id":"` + id + `"}`, RecipientNpub: "npub1recipient",
		Status: status, CreatedAt: created, RetryCount: retry, MaxRetries: maxRetry,
	}
}

func saveGroupOutbox(t *testing.T, entries ...types.OutboxEntry) (string, []byte) {
	t.Helper()
	require.NoError(t, messaging.SaveOutbox(&types.Outbox{Entries: entries}))
	path, err := messaging.GetOutboxPath()
	require.NoError(t, err)
	before, err := os.ReadFile(path)
	require.NoError(t, err)
	return path, before
}

func TestDaemonGroupRetryUsesProvider(t *testing.T) {
	t.Run("missing provider", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		useDaemonGroupProviders(t)
		path, before := saveGroupOutbox(t, groupOutboxEntry("evt-live", messaging.OutboxStatusGroupPending, 10, 0, 3))
		out := captureStdout(t, func() {
			processOutbox(t.Context(), &types.Identity{Nickname: "alice"}, nil)
		})
		after, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, string(before), string(after))
		require.Contains(t, out, "group provider group: not_registered")
	})

	t.Run("expired token", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		useDaemonGroupProviders(t)
		provider := &recordingGroupProvider{}
		require.NoError(t, daemonGroupProviders.Register(daemonGroupProviderName, provider))
		liveAt := time.Now().Unix() + 120
		path, before := saveGroupOutbox(t,
			groupOutboxEntry("evt-expired", messaging.OutboxStatusGroupPending, -1, 0, 3),
			groupOutboxEntry("evt-live", messaging.OutboxStatusGroupPending, liveAt, 0, 3),
		)
		out := captureStdout(t, func() {
			processOutbox(t.Context(), &types.Identity{Nickname: "alice"}, nil)
		})
		after, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, string(before), string(after))
		require.Contains(t, out, "group provider group: expired")
		require.Contains(t, out, "group provider group: called")
		require.Len(t, provider.calls, 1)
		require.Equal(t, "evt-live", provider.calls[0].Token.ID)
		require.Greater(t, provider.calls[0].Token.ExpiresAt, time.Now().Unix())
	})

	t.Run("held decision", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		useDaemonGroupProviders(t)
		provider := &recordingGroupProvider{}
		require.NoError(t, daemonGroupProviders.Register(daemonGroupProviderName, provider))
		path, before := saveGroupOutbox(t, groupOutboxEntry("evt-held", messaging.OutboxStatusGroupFailed, time.Now().Unix()+120, 3, 3))
		out := captureStdout(t, func() {
			processOutbox(t.Context(), &types.Identity{Nickname: "alice"}, nil)
		})
		after, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, string(before), string(after))
		require.Contains(t, out, "group provider group: held")
		require.Empty(t, provider.calls)
	})

	t.Run("dm retry", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		useDaemonGroupProviders(t)
		provider := &recordingGroupProvider{}
		require.NoError(t, daemonGroupProviders.Register(daemonGroupProviderName, provider))
		relay, published := startGroupRetryRelay(t)
		sender := nostr.Generate()
		recipient := nostr.Generate().Public()
		event := nostr.Event{CreatedAt: nostr.Now(), Kind: messaging.AgentKind, Content: "queued while the relay is offline", PubKey: sender.Public(), Tags: nostr.Tags{
			{"p", hex.EncodeToString(recipient[:])}, {"c", messaging.AgentTag}, {"v", messaging.AgentVersion},
		}}
		require.NoError(t, event.Sign(sender))
		eventJSON, err := json.Marshal(event)
		require.NoError(t, err)
		require.NoError(t, messaging.SaveOutbox(&types.Outbox{Entries: []types.OutboxEntry{{
			QueueID: "q-dm", ID: event.ID.Hex(), EventJSON: string(eventJSON),
			RecipientNpub: "npub1bob", Relays: []string{relay},
			Status: "pending", RetryCount: 0, MaxRetries: 10, CreatedAt: time.Now().Unix(),
		}}}))
		processOutbox(t.Context(), &types.Identity{Nickname: "alice"}, nil)
		select {
		case got := <-published:
			require.Equal(t, event.ID, got.ID)
		case <-time.After(5 * time.Second):
			t.Fatal("dm publish")
		}
		loaded, err := messaging.LoadOutbox()
		require.NoError(t, err)
		require.Empty(t, loaded.Entries)
		require.Empty(t, provider.calls)
	})

	t.Run("protocol clear", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		useDaemonGroupProviders(t)
		path, groupID := writeProtocolGroupOutbox(t)
		before, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Contains(t, string(before), "fanout_protocol")
		cleanupOutbox()
		after, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Contains(t, string(after), groupID)
		require.Contains(t, string(after), "fanout_protocol")
		require.NotContains(t, string(after), "dm-old-marker")
	})

	t.Run("no second ticker", func(t *testing.T) {
		_, file, _, ok := runtime.Caller(0)
		require.True(t, ok)
		dir := filepath.Dir(file)
		daemonSrc, err := os.ReadFile(filepath.Join(dir, "daemon.go"))
		require.NoError(t, err)
		runtimeSrc, err := os.ReadFile(filepath.Join(dir, "runtime.go"))
		require.NoError(t, err)
		require.NotContains(t, string(daemonSrc), "time.NewTicker(")
		require.Equal(t, 3, strings.Count(string(runtimeSrc), "time.NewTicker("))
	})
}

func startGroupRetryRelay(t *testing.T) (string, <-chan nostr.Event) {
	t.Helper()
	published := make(chan nostr.Event, 1)
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
			case published <- event:
			default:
			}
			response, _ := json.Marshal([]any{"OK", event.ID.Hex(), true, "test relay"})
			if err := conn.WriteMessage(websocket.TextMessage, response); err != nil {
				return
			}
		}
	}))
	t.Cleanup(server.Close)
	return "ws" + strings.TrimPrefix(server.URL, "http"), published
}

func writeProtocolGroupOutbox(t *testing.T) (string, string) {
	t.Helper()
	recipient := nostr.Generate().Public()
	dmID, dmJSON := signedProtocolEvent(t, "dm-old-marker", recipient)
	groupID, groupJSON := signedProtocolEvent(t, "protocol-body", recipient)
	recipientHex := hex.EncodeToString(recipient[:])
	doc := map[string]any{
		"entries": []map[string]any{
			{
				"id": dmID, "event_json": string(dmJSON), "recipient_npub": recipientHex,
				"relays": []string{}, "status": "sent", "last_attempt": 1, "retry_count": 1,
				"max_retries": 10, "created_at": 1,
			},
			{
				"queue_id": "q-protocol", "route": "group", "id": groupID,
				"event_json": string(groupJSON), "recipient_npub": recipientHex, "relays": []string{},
				"status": "group_pending", "retry_count": 0, "max_retries": 10,
				"created_at": 1_700_000_000, "last_attempt": 0, "fanout_protocol": 1,
			},
		},
	}
	raw, err := json.Marshal(doc)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join(os.Getenv("HOME"), ".hyphae"), 0700))
	path := filepath.Join(os.Getenv("HOME"), ".hyphae", "outbox.json")
	require.NoError(t, os.WriteFile(path, raw, 0600))
	return path, groupID
}

func signedProtocolEvent(t *testing.T, content string, recipient nostr.PubKey) (string, []byte) {
	t.Helper()
	sender := nostr.Generate()
	event := nostr.Event{
		CreatedAt: 1_700_000_000, Kind: messaging.AgentKind, Content: content, PubKey: sender.Public(),
		Tags: nostr.Tags{
			{"p", hex.EncodeToString(recipient[:])},
			{"c", messaging.AgentTag},
			{"v", messaging.AgentVersion},
			{"enc", "nip44"},
		},
	}
	require.NoError(t, event.Sign(sender))
	encoded, err := json.Marshal(event)
	require.NoError(t, err)
	return event.ID.Hex(), encoded
}

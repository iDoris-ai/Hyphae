package daemon

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
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

type relockGroupProvider struct {
	calls []groupchat.GroupSendCall
	err   error
}

func (p *relockGroupProvider) Send(call groupchat.GroupSendCall) error {
	p.calls = append(p.calls, call)
	p.err = messaging.WithGroupOutboxLock(func(string) error { return nil })
	return p.err
}

type failGroupProvider struct{}

func (failGroupProvider) Send(groupchat.GroupSendCall) error {
	return errors.New("relay down")
}

type stdoutResult struct {
	out string
	err error
}

func readStdout(fn func()) (string, error) {
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		return "", err
	}
	os.Stdout = w
	fn()
	closeErr := w.Close()
	os.Stdout = orig
	out, readErr := io.ReadAll(r)
	if closeErr != nil {
		return string(out), closeErr
	}
	return string(out), readErr
}

func holdOutboxLock(t *testing.T) (<-chan struct{}, func()) {
	t.Helper()
	started := make(chan struct{})
	unlock := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(unlock) }) }
	done := make(chan error, 1)
	go func() {
		done <- messaging.WithGroupOutboxLock(func(string) error {
			close(started)
			<-unlock
			return nil
		})
	}()
	t.Cleanup(func() {
		release()
		if err := <-done; err != nil {
			t.Errorf("outbox lock holder: %v", err)
		}
	})
	return started, release
}

func waitForLock(t *testing.T, started <-chan struct{}) {
	t.Helper()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("lock holder did not start")
	}
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
		// the retry step on its own, with nothing registered
		outbox, err := messaging.LoadOutbox()
		require.NoError(t, err)
		out := captureStdout(t, func() {
			retryDaemonGroupOutbox(t.Context(), daemonLoggerForContext(t.Context()), outbox, time.Now().Unix())
		})
		after, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, string(before), string(after))
		require.Contains(t, out, "group provider group: not_registered")
	})

	t.Run("daemon pass registers the production publisher", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		useDaemonGroupProviders(t)
		saveGroupOutbox(t, groupOutboxEntry("evt-live", messaging.OutboxStatusGroupPending, 10, 0, 3))
		captureStdout(t, func() {
			processOutbox(t.Context(), &types.Identity{Nickname: "alice"}, nil)
		})
		provider, err := daemonGroupProviders.Lookup(daemonGroupProviderName)
		require.NoError(t, err)
		require.IsType(t, groupPublisher{}, provider)
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
		require.Equal(t, daemonGroupRetryDecision, provider.calls[0].Decision)
		require.NotEqual(t, groupchat.ReconcileAccepted, provider.calls[0].Decision)
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

	t.Run("missing provider skips the lock", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		useDaemonGroupProviders(t)
		path, before := saveGroupOutbox(t,
			groupOutboxEntry("evt-a", messaging.OutboxStatusGroupPending, 10, 0, 3),
			groupOutboxEntry("evt-b", messaging.OutboxStatusGroupPending, 10, 0, 3),
		)
		started, release := holdOutboxLock(t)
		waitForLock(t, started)
		done := make(chan stdoutResult, 1)
		go func() {
			out, err := readStdout(func() {
				outbox, loadErr := messaging.LoadOutbox()
				require.NoError(t, loadErr)
				retryDaemonGroupOutbox(context.Background(), daemonLoggerForContext(context.Background()), outbox, time.Now().Unix())
			})
			done <- stdoutResult{out: out, err: err}
		}()
		select {
		case got := <-done:
			release()
			require.NoError(t, got.err)
			require.Equal(t, 1, strings.Count(got.out, "group provider group: not_registered"))
		case <-time.After(300 * time.Millisecond):
			release()
			<-done
			t.Fatal("missing provider waited on the outbox lock")
		}
		after, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, string(before), string(after))
	})

	t.Run("lock blocks dispatch", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		useDaemonGroupProviders(t)
		provider := &recordingGroupProvider{}
		require.NoError(t, daemonGroupProviders.Register(daemonGroupProviderName, provider))
		now := time.Now().Unix()
		entry := groupOutboxEntry("evt-live", messaging.OutboxStatusGroupPending, now+120, 0, 3)
		_, _ = saveGroupOutbox(t, entry)
		started, release := holdOutboxLock(t)
		waitForLock(t, started)
		updated := make(chan error, 1)
		go func() {
			_, err := messaging.UpdateOutbox(func(*types.Outbox) error { return nil })
			updated <- err
		}()
		returned := make(chan string, 1)
		go func() {
			returned <- dispatchDaemonGroupEntry(entry, now)
		}()
		select {
		case outcome := <-returned:
			release()
			t.Fatalf("dispatch returned %q while the lock was held", outcome)
		case err := <-updated:
			release()
			t.Fatalf("UpdateOutbox returned while the lock was held: %v", err)
		case <-time.After(300 * time.Millisecond):
		}
		release()
		select {
		case outcome := <-returned:
			require.Equal(t, "called", outcome)
		case <-time.After(2 * time.Second):
			t.Fatal("dispatch stayed blocked after the lock was released")
		}
		select {
		case err := <-updated:
			require.NoError(t, err)
		case <-time.After(2 * time.Second):
			t.Fatal("UpdateOutbox stayed blocked after the lock was released")
		}
		require.Len(t, provider.calls, 1)
		require.Equal(t, daemonGroupRetryDecision, provider.calls[0].Decision)
	})

	t.Run("stale snapshot", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		useDaemonGroupProviders(t)
		provider := &recordingGroupProvider{}
		require.NoError(t, daemonGroupProviders.Register(daemonGroupProviderName, provider))
		path, before := saveGroupOutbox(t, groupOutboxEntry("evt-stale", messaging.OutboxStatusGroupPending, 10, 2, 3))
		outcome := dispatchDaemonGroupEntry(groupOutboxEntry("evt-stale", messaging.OutboxStatusGroupPending, 10, 0, 3), time.Now().Unix())
		require.Equal(t, "changed", outcome)
		require.Empty(t, provider.calls)
		after, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, string(before), string(after))
	})

	t.Run("exhausted pending", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		useDaemonGroupProviders(t)
		provider := &recordingGroupProvider{}
		require.NoError(t, daemonGroupProviders.Register(daemonGroupProviderName, provider))
		now := time.Now().Unix()
		path, before := saveGroupOutbox(t, groupOutboxEntry("evt-exhausted", messaging.OutboxStatusGroupPending, now+120, 3, 3))
		out := captureStdout(t, func() {
			processOutbox(t.Context(), &types.Identity{Nickname: "alice"}, nil)
		})
		require.Contains(t, out, "group provider group: held")
		require.Empty(t, provider.calls)
		after, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, string(before), string(after))
	})

	t.Run("send after unlock", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		useDaemonGroupProviders(t)
		provider := &relockGroupProvider{}
		require.NoError(t, daemonGroupProviders.Register(daemonGroupProviderName, provider))
		now := time.Now().Unix()
		entry := groupOutboxEntry("evt-live", messaging.OutboxStatusGroupPending, now+120, 0, 3)
		path, before := saveGroupOutbox(t, entry)
		done := make(chan string, 1)
		go func() {
			done <- dispatchDaemonGroupEntry(entry, now)
		}()
		select {
		case outcome := <-done:
			require.Equal(t, "called", outcome)
		case <-time.After(2 * time.Second):
			t.Fatal("send blocked on the outbox lock")
		}
		require.NoError(t, provider.err)
		require.Len(t, provider.calls, 1)
		require.Equal(t, daemonGroupRetryDecision, provider.calls[0].Decision)
		after, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, string(before), string(after))
	})

	t.Run("send error keeps the file", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		useDaemonGroupProviders(t)
		require.NoError(t, daemonGroupProviders.Register(daemonGroupProviderName, failGroupProvider{}))
		now := time.Now().Unix()
		path, before := saveGroupOutbox(t, groupOutboxEntry("evt-live", messaging.OutboxStatusGroupPending, now+120, 0, 3))
		out := captureStdout(t, func() {
			processOutbox(t.Context(), &types.Identity{Nickname: "alice"}, nil)
		})
		require.Contains(t, out, "group provider group: error: relay down")
		after, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, string(before), string(after))
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

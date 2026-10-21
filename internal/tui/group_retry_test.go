package tui

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/internal/daemon"
	"github.com/iDoris-ai/hyphae/internal/groupchat"
	"github.com/iDoris-ai/hyphae/internal/identity"
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

func useTUIGroupProviders(t *testing.T) *groupchat.GroupProviderRegistry {
	t.Helper()
	registry := groupchat.NewGroupProviderRegistry()
	previous := daemon.SetGroupProviders(registry)
	t.Cleanup(func() { daemon.SetGroupProviders(previous) })
	return registry
}

func groupRetryModel() *ChatModel {
	npub := common.EncodeNpub(nostr.Generate().Public())
	return &ChatModel{
		myIdentity:    &types.Identity{Nickname: "alice", Npub: npub},
		contactNpub:   npub,
		outboxUpdates: make(chan outboxDeliveryUpdate, 8),
	}
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

func groupStatusText(model *ChatModel) []string {
	var got []string
	for {
		select {
		case update := <-model.outboxUpdates:
			got = append(got, formatOutboxStatus(update))
		default:
			return got
		}
	}
}

func TestTUIGroupRetryUsesProvider(t *testing.T) {
	t.Run("missing provider", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		useTUIGroupProviders(t)
		path, before := saveGroupOutbox(t, groupOutboxEntry("evt-live", messaging.OutboxStatusGroupPending, 10, 0, 3))
		model := groupRetryModel()
		model.retryPendingOutbox(context.Background())
		after, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, string(before), string(after))
		require.Equal(t, []string{"Outbox: group provider not_registered"}, groupStatusText(model))
	})

	t.Run("missing provider skips the lock", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		useTUIGroupProviders(t)
		path, before := saveGroupOutbox(t,
			groupOutboxEntry("evt-a", messaging.OutboxStatusGroupPending, 10, 0, 3),
			groupOutboxEntry("evt-b", messaging.OutboxStatusGroupPending, 10, 0, 3),
		)
		started, release := holdOutboxLock(t)
		waitForLock(t, started)
		model := groupRetryModel()
		done := make(chan struct{})
		go func() {
			model.retryPendingOutbox(context.Background())
			close(done)
		}()
		select {
		case <-done:
			release()
		case <-time.After(300 * time.Millisecond):
			release()
			<-done
			t.Fatal("missing provider waited on the outbox lock")
		}
		after, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, string(before), string(after))
		require.Equal(t, []string{"Outbox: group provider not_registered"}, groupStatusText(model))
	})

	t.Run("same provider", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		registry := useTUIGroupProviders(t)
		provider := &recordingGroupProvider{}
		require.NoError(t, registry.Register(daemonGroupProviderName(), provider))
		now := time.Now().Unix()
		path, before := saveGroupOutbox(t, groupOutboxEntry("evt-live", messaging.OutboxStatusGroupPending, now+120, 0, 3))
		model := groupRetryModel()
		model.retryPendingOutbox(context.Background())
		after, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, string(before), string(after))
		require.Len(t, provider.calls, 1)
		require.Equal(t, "evt-live", provider.calls[0].Token.ID)
		require.Equal(t, groupchat.ReconcileDecision("retry"), provider.calls[0].Decision)
		require.NotEqual(t, groupchat.ReconcileAccepted, provider.calls[0].Decision)
		text := groupStatusText(model)
		require.Len(t, text, 1)
		require.Contains(t, text[0], "Outbox: group provider called")
	})

	t.Run("held row", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		registry := useTUIGroupProviders(t)
		provider := &recordingGroupProvider{}
		require.NoError(t, registry.Register(daemonGroupProviderName(), provider))
		path, before := saveGroupOutbox(t, groupOutboxEntry("evt-held", messaging.OutboxStatusGroupFailed, time.Now().Unix()+120, 3, 3))
		model := groupRetryModel()
		model.retryPendingOutbox(context.Background())
		after, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, string(before), string(after))
		require.Empty(t, provider.calls)
		text := groupStatusText(model)
		require.Len(t, text, 1)
		require.Contains(t, text[0], "group provider held")
	})

	t.Run("restart reads the same row", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		useTUIGroupProviders(t)
		path, before := saveGroupOutbox(t, groupOutboxEntry("evt-live", messaging.OutboxStatusGroupPending, 10, 0, 3))
		first := groupRetryModel()
		first.retryPendingOutbox(context.Background())
		require.Equal(t, []string{"Outbox: group provider not_registered"}, groupStatusText(first))
		second := groupRetryModel()
		second.retryPendingOutbox(context.Background())
		after, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, string(before), string(after))
		require.Equal(t, []string{"Outbox: group provider not_registered"}, groupStatusText(second))
	})

	t.Run("group retry does not write dm history", func(t *testing.T) {
		cleanup := setupTestEnv(t)
		defer cleanup()
		useTUIGroupProviders(t)
		ks, err := identity.LoadKeyStore()
		require.NoError(t, err)
		_, err = identity.CreateIdentity(ks, "testcontact")
		require.NoError(t, err)
		ks, err = identity.LoadKeyStore()
		require.NoError(t, err)
		contact, err := identity.GetIdentity(ks, "testcontact")
		require.NoError(t, err)
		require.NoError(t, identity.AddContact(ks, "testcontact", contact.Npub))
		model, err := NewChatModel("testcontact")
		require.NoError(t, err)
		path, before := saveGroupOutbox(t, groupOutboxEntry("evt-live", messaging.OutboxStatusGroupPending, 10, 0, 3))
		model.retryPendingOutbox(context.Background())
		stored, err := model.store.GetMessage("evt-live")
		require.NoError(t, err)
		require.Nil(t, stored)
		after, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, string(before), string(after))
		require.Equal(t, []string{"Outbox: group provider not_registered"}, groupStatusText(model))
	})

	t.Run("dm retry stays on attempt send", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		registry := useTUIGroupProviders(t)
		provider := &recordingGroupProvider{}
		require.NoError(t, registry.Register(daemonGroupProviderName(), provider))
		group := groupOutboxEntry("evt-group", messaging.OutboxStatusGroupPending, time.Now().Unix()+120, 0, 3)
		dm := types.OutboxEntry{
			ID: "evt-dm", EventJSON: "not-json", RecipientNpub: "npub1bob",
			Status: "pending", RetryCount: 0, MaxRetries: 3, CreatedAt: 10,
		}
		path, _ := saveGroupOutbox(t, group, dm)
		model := groupRetryModel()
		model.contactNpub = "npub1bob"
		model.retryPendingOutbox(context.Background())
		loaded, err := messaging.LoadOutbox()
		require.NoError(t, err)
		var sawGroup, sawDM bool
		for _, entry := range loaded.Entries {
			if entry.ID == "evt-group" {
				sawGroup = true
				require.Equal(t, 0, entry.RetryCount)
				require.Equal(t, messaging.OutboxStatusGroupPending, entry.Status)
			}
			if entry.ID == "evt-dm" {
				sawDM = true
			}
		}
		require.True(t, sawGroup)
		require.True(t, sawDM)
		require.Len(t, provider.calls, 1)
		raw, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Contains(t, string(raw), "evt-group")
		_, file, _, ok := runtime.Caller(0)
		require.True(t, ok)
		src, err := os.ReadFile(filepath.Join(filepath.Dir(file), "offline_outbox.go"))
		require.NoError(t, err)
		require.Contains(t, string(src), "messaging.AttemptSend(")
		require.Contains(t, string(src), "daemon.GroupRetryOutcome(")
		require.Equal(t, 1, strings.Count(string(src), "time.NewTicker("))
	})

	t.Run("legacy group command and one outbox file", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		useTUIGroupProviders(t)
		path, _ := saveGroupOutbox(t, groupOutboxEntry("evt-live", messaging.OutboxStatusGroupPending, 10, 0, 3))
		model := groupRetryModel()
		model.retryPendingOutbox(context.Background())
		matches, err := filepath.Glob(filepath.Join(filepath.Dir(path), "outbox*"))
		require.NoError(t, err)
		for _, match := range matches {
			base := filepath.Base(match)
			require.Contains(t, []string{"outbox.json", "outbox.json.lock"}, base)
		}
		require.Contains(t, matches, path)
		_, file, _, ok := runtime.Caller(0)
		require.True(t, ok)
		root := filepath.Dir(filepath.Dir(filepath.Dir(file)))
		legacy, err := os.ReadFile(filepath.Join(root, "internal/group/commands.go"))
		require.NoError(t, err)
		require.Contains(t, string(legacy), "TUI group chat - coming soon!")
	})
}

func daemonGroupProviderName() string {
	return "group"
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

package tui

import (
	"context"
	"os"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/iDoris-ai/hyphae/internal/groupchat"
	"github.com/iDoris-ai/hyphae/internal/identity"
	"github.com/iDoris-ai/hyphae/internal/messaging"
	"github.com/iDoris-ai/hyphae/internal/storage"
	"github.com/iDoris-ai/hyphae/pkg/types"
	"github.com/stretchr/testify/require"
)

// activeGroupEnv gives alice (default identity) and bob in one keystore and an
// active two-member group created by alice, activated through the real CLI.
func activeGroupEnv(t *testing.T) (alice, bob *types.Identity, groupID string) {
	t.Helper()
	messaging.ResetStoreForTest()
	t.Setenv("HOME", t.TempDir())
	t.Cleanup(func() { messaging.ResetStoreForTest(); storage.CloseDB() })
	ks, err := identity.LoadKeyStore()
	require.NoError(t, err)
	alice, err = identity.CreateIdentity(ks, "alice")
	require.NoError(t, err)
	bob, err = identity.CreateIdentity(ks, "bob")
	require.NoError(t, err)

	run := func(args ...string) {
		t.Helper()
		devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
		require.NoError(t, err)
		defer devnull.Close()
		stdout := os.Stdout
		os.Stdout = devnull
		defer func() { os.Stdout = stdout }()
		require.NoError(t, groupchat.Cmd.Run(context.Background(), append([]string{"groupchat"}, args...)))
	}
	run("create", "--name", "room", "--identity", "alice", "--invitee", bob.Npub)

	db, err := storage.OpenDB()
	require.NoError(t, err)
	var inviteID string
	require.NoError(t, db.QueryRow(`SELECT invite_id FROM groupchat_invites WHERE invitee_npub = ?`, bob.Npub).Scan(&inviteID))
	require.NoError(t, db.QueryRow(`SELECT group_id FROM groupchat_groups WHERE local_npub = ?`, alice.Npub).Scan(&groupID))
	require.NoError(t, db.Close())
	run("accept", "--identity", "bob", "--invite", inviteID)
	return alice, bob, groupID
}

// drain runs a command and feeds its message back into the model, expanding
// batches and ignoring the timer so the test never sleeps.
func drain(t *testing.T, m *GroupsModel, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			drain(t, m, c)
		}
		return
	}
	if msg == nil {
		return
	}
	if _, tick := msg.(groupTickMsg); tick {
		return
	}
	_, next := m.Update(msg)
	drain(t, m, next)
}

func key(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "ctrl+u":
		return tea.KeyMsg{Type: tea.KeyCtrlU}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func press(t *testing.T, m *GroupsModel, s string) {
	t.Helper()
	_, cmd := m.Update(key(s))
	drain(t, m, cmd)
}

func TestNewGroupsModelRegistersGroupPublisher(t *testing.T) {
	activeGroupEnv(t)
	registry := useTUIGroupProviders(t)
	_, err := registry.Lookup("group")
	require.ErrorIs(t, err, groupchat.ErrGroupProviderNotRegistered)

	m, err := NewGroupsModel("ws://127.0.0.1:1")
	require.NoError(t, err)
	defer m.Close()

	_, err = registry.Lookup("group")
	require.NoError(t, err, "TUI process must register the group publisher like the daemon does")
}

func TestChatOutboxWorkerRegistersGroupPublisher(t *testing.T) {
	registry := useTUIGroupProviders(t)
	t.Setenv("HOME", t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	m := groupRetryModel()
	m.outboxCtx, m.outboxCancel = ctx, cancel
	m.outboxDone = make(chan struct{})
	m.outboxRequests = make(chan outboxSendRequest, 1)
	m.relays = []string{"ws://127.0.0.1:1"}

	m.startOutboxWorker()()
	_, err := registry.Lookup("group")
	require.NoError(t, err, "chat TUI outbox worker must register the group publisher")
	cancel()
	select {
	case <-m.outboxDone:
	case <-time.After(5 * time.Second):
		t.Fatal("outbox worker did not stop")
	}
}

func TestGroupsModelListMembersHistorySend(t *testing.T) {
	alice, bob, groupID := activeGroupEnv(t)
	useTUIGroupProviders(t)

	m, err := NewGroupsModel("ws://127.0.0.1:1")
	require.NoError(t, err)
	defer m.Close()
	m.publishFn = func(int64) string { return "" } // no network in this test
	drain(t, m, m.loadGroups())

	require.Len(t, m.groups, 1)
	require.Contains(t, m.View(), "room")
	require.Contains(t, m.View(), "active")

	press(t, m, "enter")
	require.Equal(t, groupsViewChat, m.view)
	require.Equal(t, groupID, m.current.ID)
	require.Len(t, m.members, 2)
	require.True(t, m.members[0].IsCreator)
	require.Equal(t, alice.Npub, m.members[0].Npub)
	require.Equal(t, bob.Npub, m.members[1].Npub)
	require.Contains(t, m.View(), "me*")

	m.input.SetValue("hello group")
	press(t, m, "enter")
	require.Len(t, m.messages, 1)
	require.Equal(t, "hello group", m.messages[0].Plaintext)
	require.Equal(t, alice.Npub, m.messages[0].Sender)
	require.Contains(t, m.View(), "hello group")
	require.Contains(t, m.status, "Queued for 1 member")
	require.Empty(t, m.input.Value())

	// the send went through the shared fanout path: one pending group outbox row for bob
	outbox, err := messaging.LoadOutbox()
	require.NoError(t, err)
	rows := 0
	for _, e := range outbox.Entries {
		if e.Route == messaging.OutboxRouteGroup && e.RecipientNpub == bob.Npub && e.Status == messaging.OutboxStatusGroupPending {
			rows++
		}
	}
	require.Equal(t, 1, rows)

	// blank input is not sent
	m.input.SetValue("   ")
	press(t, m, "enter")
	require.Len(t, m.messages, 1)

	press(t, m, "esc")
	require.Equal(t, groupsViewList, m.view)
}

func TestGroupsModelPagesOlderHistory(t *testing.T) {
	alice, _, groupID := activeGroupEnv(t)
	useTUIGroupProviders(t)
	m, err := NewGroupsModel("ws://127.0.0.1:1")
	require.NoError(t, err)
	defer m.Close()
	for i := 0; i < groupHistoryPage+5; i++ {
		id, err := groupchat.NewOpaqueID()
		require.NoError(t, err)
		_, err = m.store.StoreLocalMessage(alice.Npub, groupID, id, "msg", int64(1000+i))
		require.NoError(t, err)
	}
	drain(t, m, m.loadGroups())
	press(t, m, "enter")
	require.Len(t, m.messages, groupHistoryPage)
	require.True(t, m.older)
	press(t, m, "ctrl+u")
	require.Len(t, m.messages, groupHistoryPage+5)
	require.False(t, m.older)
	for i := 1; i < len(m.messages); i++ {
		require.LessOrEqual(t, m.messages[i-1].CreatedAt, m.messages[i].CreatedAt)
	}
}

func TestGroupsModelPendingGroupDoesNotOpen(t *testing.T) {
	m := &GroupsModel{
		groups: []groupchat.Group{{ID: "g1", Name: "later", State: groupchat.StatePending}},
		me:     &types.Identity{Npub: "npub1me"},
	}
	_, cmd := m.updateList(key("enter"))
	require.Nil(t, cmd)
	require.Equal(t, groupsViewList, m.view)
	require.Contains(t, m.status, "pending")
}

func TestPublishGroupOutboxUsesRegisteredProvider(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	registry := useTUIGroupProviders(t)
	saveGroupOutbox(t, groupOutboxEntry("evt-1", messaging.OutboxStatusGroupPending, 10, 0, 3))
	require.Equal(t, "publish: not_registered", publishGroupOutbox(time.Now().Unix()))

	provider := &recordingGroupProvider{}
	require.NoError(t, registry.Register("group", provider))
	require.Contains(t, publishGroupOutbox(time.Now().Unix()), "1 group event(s)")
	require.Len(t, provider.calls, 1)
}

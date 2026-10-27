package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
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
	m.publishFn = func(int64) string { return "" }
	total := groupHistoryPage + 5
	for i := 0; i < total; i++ {
		id, err := groupchat.NewOpaqueID()
		require.NoError(t, err)
		_, err = m.store.StoreLocalMessage(alice.Npub, groupID, id, fmt.Sprintf("body-%02d", i), int64(1000+i))
		require.NoError(t, err)
	}
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	drain(t, m, m.loadGroups())
	press(t, m, "enter")
	require.Len(t, m.messages, groupHistoryPage)
	require.True(t, m.older)
	view := m.View()
	require.Contains(t, view, fmt.Sprintf("body-%02d", total-1))
	require.NotContains(t, view, "body-00")

	// ctrl+u scrolls through the loaded page, then fetches the older one; the
	// rendered view must reach the oldest message, not just grow the model.
	for i := 0; i < 20 && !strings.Contains(m.View(), "body-00"); i++ {
		press(t, m, "ctrl+u")
	}
	require.Len(t, m.messages, total)
	require.False(t, m.older)
	require.Contains(t, m.View(), "body-00")
	require.NotContains(t, m.View(), fmt.Sprintf("body-%02d", total-1))
	for i := 1; i < len(m.messages); i++ {
		require.LessOrEqual(t, m.messages[i-1].CreatedAt, m.messages[i].CreatedAt)
	}

	// ctrl+d scrolls back to the newest
	for i := 0; i < 20 && m.scrollUp > 0; i++ {
		press(t, m, "ctrl+d")
	}
	require.Contains(t, m.View(), fmt.Sprintf("body-%02d", total-1))

	// a send after paging keeps the older page loaded
	m.input.SetValue("fresh")
	m.sendFn = func(gid, body string) (groupchat.FanoutReport, error) {
		_, err := m.store.StoreLocalMessage(alice.Npub, gid, "ffffffffffffffffffffffffffffff01", body, 5000)
		return groupchat.FanoutReport{Queued: 1}, err
	}
	press(t, m, "enter")
	require.Len(t, m.messages, total+1)
	require.Equal(t, "fresh", m.messages[len(m.messages)-1].Plaintext)
	require.False(t, m.older, "reload after send must not re-arm older once the start was reached")
}

func TestGroupsModelEnterWhileSendingIsIgnored(t *testing.T) {
	alice, _, groupID := activeGroupEnv(t)
	useTUIGroupProviders(t)
	m, err := NewGroupsModel("ws://127.0.0.1:1")
	require.NoError(t, err)
	defer m.Close()
	m.publishFn = func(int64) string { return "" }
	drain(t, m, m.loadGroups())
	press(t, m, "enter")

	m.input.SetValue("dup")
	_, first := m.Update(key("enter"))
	require.NotNil(t, first)
	require.Empty(t, m.input.Value(), "input is cleared on press")
	m.input.SetValue("dup") // same text entered again before the first send returns
	_, second := m.Update(key("enter"))
	require.Nil(t, second, "Enter while a send is in flight must be ignored")
	drain(t, m, first)

	stored, err := m.store.GroupMessagesPage(alice.Npub, groupID, 10, "", "")
	require.NoError(t, err)
	require.Len(t, stored, 1)
	require.False(t, m.sending)
}

func TestGroupsModelFailedSendKeepsTextAndStaleResultsAreDropped(t *testing.T) {
	m := &GroupsModel{
		me:      &types.Identity{Npub: "npub1me"},
		view:    groupsViewChat,
		current: groupchat.Group{ID: "B"},
		input:   textinput.New(),
	}
	m.input.SetValue("draft for B")
	m.sending = true
	m.Update(groupSentMsg{groupID: "A", report: groupchat.FanoutReport{Queued: 1}})
	require.Equal(t, "draft for B", m.input.Value(), "A's result must not clear B's draft")
	require.False(t, m.sending)
	m.Update(groupOpenedMsg{groupID: "A", messages: []types.GroupMessage{{ID: "x", Plaintext: "from A"}}})
	require.Empty(t, m.messages, "A's history must not land in B's view")

	m.input.Reset()
	m.sentBody = "lost text"
	m.Update(groupSentMsg{groupID: "B", err: errors.New("boom")})
	require.Equal(t, "lost text", m.input.Value())
}

func TestPublishPassHasOneInFlight(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	release := make(chan struct{})
	entered := make(chan struct{})
	m := &GroupsModel{ctx: ctx}
	m.publishFn = func(int64) string {
		close(entered)
		<-release
		return "done"
	}
	done := make(chan tea.Msg)
	go func() { done <- m.publishPass()() }()
	<-entered
	require.Nil(t, m.publishPass()(), "a second pass must not start while one is in flight")
	close(release)
	require.Equal(t, groupPublishMsg{status: "done"}, <-done)
	m.publishFn = func(int64) string { return "again" }
	require.NotNil(t, m.publishPass()(), "the flag is released after the pass")
}

// relay unreachable: ticking every 2s must not burn the retry budget.
func TestGroupsPublishBacksOffWhenRelayUnreachable(t *testing.T) {
	_, bob, _ := activeGroupEnv(t)
	useTUIGroupProviders(t)
	m, err := NewGroupsModel("ws://127.0.0.1:1")
	require.NoError(t, err)
	defer m.Close()
	drain(t, m, m.loadGroups())
	press(t, m, "enter")
	m.publishFn = func(int64) string { return "" } // sending below must not publish by itself
	m.input.SetValue("hi")
	press(t, m, "enter")

	row := func() types.OutboxEntry {
		t.Helper()
		outbox, err := messaging.LoadOutbox()
		require.NoError(t, err)
		for _, e := range outbox.Entries {
			if e.Route == messaging.OutboxRouteGroup && e.RecipientNpub == bob.Npub {
				return e
			}
		}
		t.Fatal("no group outbox row for bob")
		return types.OutboxEntry{}
	}
	require.Equal(t, 0, row().RetryCount)

	now := time.Now().Unix()
	publishGroupOutbox(now) // first attempt is immediate and fails: nothing listens on port 1
	require.Equal(t, 1, row().RetryCount)
	for tick := int64(1); tick <= 25; tick++ { // ~50s of 2s ticks
		publishGroupOutbox(now + 2*tick)
	}
	e := row()
	require.Equal(t, 1, e.RetryCount, "ticks inside the backoff must not attempt")
	require.Equal(t, messaging.OutboxStatusGroupPending, e.Status)

	publishGroupOutbox(now + 130)
	require.Equal(t, 2, row().RetryCount, "after the backoff one more attempt is made")
	require.Equal(t, messaging.OutboxStatusGroupPending, row().Status)
}

func TestGroupPublishStatusKeepsFailedRowsVisible(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	useTUIGroupProviders(t)
	saveGroupOutbox(t, groupOutboxEntry("evt-f", messaging.OutboxStatusGroupFailed, 10, 10, 10))
	got := publishGroupOutbox(time.Now().Unix())
	require.Contains(t, got, "FAILED")
	require.Contains(t, got, "groupchat retry")
	require.Equal(t, got, publishGroupOutbox(time.Now().Unix()+3600), "still shown on every later pass")
}

func TestChatGroupRetryRespectsBackoff(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	registry := useTUIGroupProviders(t)
	provider := &recordingGroupProvider{}
	require.NoError(t, registry.Register("group", provider))
	now := time.Now().Unix()
	entry := groupOutboxEntry("evt-b", messaging.OutboxStatusGroupPending, 10, 1, 10)
	entry.LastAttempt = now - 5
	saveGroupOutbox(t, entry)
	outbox, err := messaging.LoadOutbox()
	require.NoError(t, err)

	m := groupRetryModel()
	m.outboxUpdates = make(chan outboxDeliveryUpdate, 64) // a missing backoff must fail the assertion, not block
	for i := int64(0); i < 20; i++ {                      // the 500ms worker, 10s worth
		m.retryGroupOutbox(context.Background(), outbox, now+i/2)
	}
	require.Empty(t, provider.calls, "a row attempted 5s ago is not retried by the chat worker")
	m.retryGroupOutbox(context.Background(), outbox, now+int64(groupRetryBackoff/time.Second))
	require.Len(t, provider.calls, 1)
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

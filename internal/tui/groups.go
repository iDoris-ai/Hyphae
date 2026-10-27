package tui

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"fiatjaf.com/nostr"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/internal/daemon"
	"github.com/iDoris-ai/hyphae/internal/groupchat"
	"github.com/iDoris-ai/hyphae/internal/identity"
	"github.com/iDoris-ai/hyphae/internal/messaging"
	"github.com/iDoris-ai/hyphae/internal/storage"
	"github.com/iDoris-ai/hyphae/pkg/types"
	"github.com/urfave/cli/v3"
)

const (
	groupHistoryPage   = 50
	groupPublishPeriod = 2 * time.Second
)

// GroupsCmd opens the group chat TUI: list groups, read members and history,
// send a message. Invitations and acceptance stay in `hyphae groupchat`.
var GroupsCmd = &cli.Command{
	Name:        "groups",
	Usage:       "Group chat TUI (list, members, history, send)",
	Description: `Browse the groups this identity belongs to and send to an active one.`,
	Flags: []cli.Flag{
		&cli.StringSliceFlag{Name: "relay", Aliases: []string{"r"}, Usage: "Relay URL(s) to publish to (repeatable)"},
	},
	Action: func(ctx context.Context, c *cli.Command) error {
		relays, err := common.ResolveRelays(c)
		if err != nil {
			return err
		}
		model, err := NewGroupsModel(relays...)
		if err != nil {
			return fmt.Errorf("failed to start groups: %w", err)
		}
		defer model.Close()
		if _, err := tea.NewProgram(model, tea.WithAltScreen()).Run(); err != nil {
			return fmt.Errorf("error running groups TUI: %w", err)
		}
		return nil
	},
}

type groupsView int

const (
	groupsViewList groupsView = iota
	groupsViewChat
)

type (
	groupsLoadedMsg struct {
		groups []groupchat.Group
		err    error
	}
	groupOpenedMsg struct {
		groupID  string
		members  []groupchat.MemberEntry
		messages []types.GroupMessage
		err      error
	}
	groupOlderMsg struct {
		groupID  string
		messages []types.GroupMessage
		err      error
	}
	groupSentMsg struct {
		groupID  string
		report   groupchat.FanoutReport
		err      error
		messages []types.GroupMessage
	}
	groupPublishMsg struct{ status string }
	groupTickMsg    struct{}
)

// GroupsModel is the group chat TUI. Like ChatModel it does not retain the
// secret key: sendFn loads it per send.
type GroupsModel struct {
	store  *groupchat.Store
	db     *sql.DB
	me     *types.Identity
	relays []string
	ctx    context.Context
	cancel context.CancelFunc

	view     groupsView
	groups   []groupchat.Group
	cursor   int
	current  groupchat.Group
	members  []groupchat.MemberEntry
	messages []types.GroupMessage
	older    bool // false once the oldest stored message is on screen
	scrollUp int  // history lines hidden below the window; 0 = pinned to the newest
	sending  bool // a send is in flight; further Enter presses are ignored
	sentBody string

	publishing atomic.Bool // a publish pass is in flight
	input      textinput.Model
	status     string
	width      int
	height     int

	sendFn    func(groupID, body string) (groupchat.FanoutReport, error)
	publishFn func(now int64) string
}

// NewGroupsModel opens storage and registers the group publisher for this
// process so queued group rows are actually published.
func NewGroupsModel(relays ...string) (*GroupsModel, error) {
	ks, err := identity.LoadKeyStore()
	if err != nil {
		return nil, fmt.Errorf("failed to load keystore: %w", err)
	}
	me, err := identity.GetIdentity(ks, "")
	if err != nil {
		return nil, fmt.Errorf("failed to get identity: %w", err)
	}
	if _, err := identity.GetSecretKey(ks, me.Nickname); err != nil {
		return nil, fmt.Errorf("failed to access sender key: %w", err)
	}
	db, err := storage.InitDB()
	if err != nil {
		return nil, fmt.Errorf("failed to init storage: %w", err)
	}
	store, err := groupchat.NewStore(db)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	if len(relays) == 0 {
		relays = []string{defaultRelay}
	}
	ctx, cancel := context.WithCancel(context.Background())
	m := newGroupsModel(ctx, cancel, store, me, relays)
	m.db = db
	return m, nil
}

func newGroupsModel(ctx context.Context, cancel context.CancelFunc, store *groupchat.Store, me *types.Identity, relays []string) *GroupsModel {
	ti := textinput.New()
	ti.Placeholder = "Message the group..."
	ti.CharLimit = maxMessageLen
	ti.Width = 50
	m := &GroupsModel{store: store, me: me, relays: relays, ctx: ctx, cancel: cancel, input: ti, status: "Loading groups..."}
	m.sendFn = m.sendWithKeystore
	m.publishFn = publishGroupOutbox
	// Same registration the daemon performs, so rows queued by sendFn are
	// published from this process instead of waiting for `hyphae daemon`.
	daemon.EnsureGroupPublisher(ctx, relays)
	return m
}

// Close stops publishing and releases the database.
func (m *GroupsModel) Close() error {
	m.cancel()
	if m.db != nil {
		return m.db.Close()
	}
	return nil
}

func (m *GroupsModel) Init() tea.Cmd {
	return tea.Batch(m.loadGroups(), groupTick())
}

func groupTick() tea.Cmd {
	return tea.Tick(groupPublishPeriod, func(time.Time) tea.Msg { return groupTickMsg{} })
}

func (m *GroupsModel) loadGroups() tea.Cmd {
	return func() tea.Msg {
		groups, err := m.store.ListGroups(m.me.Npub)
		return groupsLoadedMsg{groups: groups, err: err}
	}
}

func (m *GroupsModel) openGroup(g groupchat.Group) tea.Cmd {
	return func() tea.Msg {
		members, err := m.store.ListMembers(m.me.Npub, g.ID)
		if err != nil {
			return groupOpenedMsg{groupID: g.ID, err: err}
		}
		messages, err := m.store.GroupMessagesPage(m.me.Npub, g.ID, groupHistoryPage, "", "")
		return groupOpenedMsg{groupID: g.ID, members: members, messages: messages, err: err}
	}
}

func (m *GroupsModel) loadOlder() tea.Cmd {
	groupID := m.current.ID
	var before string
	if len(m.messages) > 0 {
		before = m.messages[0].ID
	}
	return func() tea.Msg {
		messages, err := m.store.GroupMessagesPage(m.me.Npub, groupID, groupHistoryPage, before, "")
		return groupOlderMsg{groupID: groupID, messages: messages, err: err}
	}
}

func (m *GroupsModel) send(groupID, body string) tea.Cmd {
	lastID := ""
	if n := len(m.messages); n > 0 {
		lastID = m.messages[n-1].ID
	}
	return func() tea.Msg {
		report, err := m.sendFn(groupID, body)
		// A partial fanout still stored the message locally; fetch what is
		// newer than the last message on screen so scrolled-back pages stay.
		messages, _ := m.store.GroupMessagesPage(m.me.Npub, groupID, groupHistoryPage, "", lastID)
		return groupSentMsg{groupID: groupID, report: report, err: err, messages: messages}
	}
}

func (m *GroupsModel) sendWithKeystore(groupID, body string) (groupchat.FanoutReport, error) {
	ks, err := identity.LoadKeyStore()
	if err != nil {
		return groupchat.FanoutReport{}, err
	}
	sk, err := identity.GetSecretKey(ks, m.me.Nickname)
	if err != nil {
		return groupchat.FanoutReport{}, err
	}
	return groupchat.SendGroupMessage(m.store, nostr.SecretKey(sk), groupID, body)
}

// publishGroupOutbox gives every group outbox row to the registered publisher,
// the same hand-off the daemon and the DM chat TUI make, and summarises it.
func publishGroupOutbox(now int64) string {
	outbox, err := messaging.LoadOutbox()
	if err != nil {
		return "outbox unreadable: " + err.Error()
	}
	called, failed, problems := 0, 0, []string{}
	for _, entry := range outbox.Entries {
		if entry.Route != messaging.OutboxRouteGroup {
			continue
		}
		if entry.Status == messaging.OutboxStatusGroupFailed {
			failed++
			continue
		}
		if !groupRowDue(entry, now) {
			continue
		}
		switch outcome := daemon.GroupRetryOutcome(entry, now); {
		case outcome == "called":
			called++
		case outcome == "not_registered" || strings.HasPrefix(outcome, "error"):
			problems = append(problems, outcome)
		}
	}
	suffix := ""
	if failed > 0 {
		// stays on screen every pass: a failed row never retries by itself
		suffix = fmt.Sprintf(" • %d group event(s) FAILED after the retry limit; run `hyphae groupchat retry`", failed)
	}
	switch {
	case len(problems) > 0:
		return "publish: " + problems[0] + suffix
	case called > 0:
		return fmt.Sprintf("publish: %d group event(s) handed to the relay publisher", called) + suffix
	}
	return strings.TrimPrefix(suffix, " • ")
}

func (m *GroupsModel) publishPass() tea.Cmd {
	return func() tea.Msg {
		if m.ctx.Err() != nil || !m.publishing.CompareAndSwap(false, true) {
			return nil
		}
		defer m.publishing.Store(false)
		return groupPublishMsg{status: m.publishFn(time.Now().Unix())}
	}
}

func (m *GroupsModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		if w := msg.Width - 6; w > 10 {
			m.input.Width = w
		}

	case groupsLoadedMsg:
		if msg.err != nil {
			m.status = "Could not list groups: " + msg.err.Error()
			return m, nil
		}
		m.groups = msg.groups
		if m.cursor >= len(m.groups) {
			m.cursor = max(0, len(m.groups)-1)
		}
		m.status = fmt.Sprintf("%d group(s)", len(m.groups))

	case groupOpenedMsg:
		if msg.groupID != m.current.ID || m.view != groupsViewChat {
			return m, nil
		}
		if msg.err != nil {
			m.status = "Could not open group: " + msg.err.Error()
			return m, nil
		}
		m.members, m.messages = msg.members, msg.messages
		m.older = len(msg.messages) >= groupHistoryPage
		m.scrollUp = 0
		m.status = ""

	case groupOlderMsg:
		if msg.groupID != m.current.ID || m.view != groupsViewChat {
			return m, nil
		}
		if msg.err != nil {
			m.status = "Could not load older messages: " + msg.err.Error()
			return m, nil
		}
		m.messages = append(append([]types.GroupMessage{}, msg.messages...), m.messages...)
		m.scrollUp += m.historyKeep() // move the window up onto the page just loaded
		m.older = len(msg.messages) >= groupHistoryPage
		m.status = fmt.Sprintf("loaded %d older message(s)", len(msg.messages))

	case groupSentMsg:
		m.sending = false
		if msg.groupID == m.current.ID && m.view == groupsViewChat {
			if len(msg.messages) > 0 {
				m.messages = append(m.messages, msg.messages...)
				m.scrollUp = 0
			}
			switch {
			case msg.err != nil:
				// Nothing stored: give the text back so it is not lost.
				if len(msg.report.Recipients) == 0 && m.input.Value() == "" {
					m.input.SetValue(m.sentBody)
				}
				m.status = fmt.Sprintf("Send failed: %v (queued %d, failed %d)", msg.err, msg.report.Queued, msg.report.Failed)
			default:
				m.status = fmt.Sprintf("Queued for %d member(s); not delivered until a relay accepts", msg.report.Queued)
			}
		}
		return m, m.publishPass()

	case groupTickMsg:
		return m, tea.Batch(m.publishPass(), groupTick())

	case groupPublishMsg:
		if msg.status != "" {
			m.status = msg.status
		}

	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		if m.view == groupsViewList {
			return m.updateList(msg)
		}
		return m.updateChat(msg)
	}
	return m, nil
}

func (m *GroupsModel) updateList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "esc":
		return m, tea.Quit
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.groups)-1 {
			m.cursor++
		}
	case "r":
		return m, m.loadGroups()
	case "enter":
		if m.cursor >= len(m.groups) {
			return m, nil
		}
		g := m.groups[m.cursor]
		if g.State != groupchat.StateActive {
			m.status = fmt.Sprintf("%s is %s; accept invitations with `hyphae groupchat accept`", g.Name, g.State)
			return m, nil
		}
		m.current, m.view = g, groupsViewChat
		m.members, m.messages, m.older, m.status = nil, nil, false, ""
		m.input.Reset()
		m.input.Focus()
		return m, m.openGroup(g)
	}
	return m, nil
}

func (m *GroupsModel) updateChat(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.view = groupsViewList
		m.input.Blur()
		return m, m.loadGroups()
	case "ctrl+u":
		// Scroll through what is loaded first, then fetch the next older page.
		if keep := m.historyKeep(); keep > 0 && len(m.historyAll())-m.scrollUp > keep {
			m.scrollUp = min(m.scrollUp+keep, len(m.historyAll())-keep)
			return m, nil
		}
		if !m.older {
			m.status = "no older messages"
			return m, nil
		}
		return m, m.loadOlder()
	case "ctrl+d":
		m.scrollUp = max(0, m.scrollUp-max(1, m.historyKeep()))
		return m, nil
	case "enter":
		body := strings.TrimSpace(m.input.Value())
		if body == "" || m.sending {
			return m, nil
		}
		m.sending, m.sentBody = true, body
		m.input.Reset() // cleared on press so a second Enter cannot resend it
		m.status = "Sending..."
		return m, m.send(m.current.ID, body)
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m *GroupsModel) View() string {
	var b strings.Builder
	if m.view == groupsViewList {
		b.WriteString(titleStyle.Render("Groups"))
		b.WriteString("\n\n")
		for i, g := range m.groups {
			cursor := " "
			if i == m.cursor {
				cursor = "▶"
			}
			line := fmt.Sprintf("%s %s [%s] %d members", cursor, g.Name, g.State, len(g.Roster))
			if i == m.cursor {
				line = lipgloss.NewStyle().Foreground(lipgloss.Color("#7D56F4")).Render(line)
			}
			b.WriteString(line + "\n")
		}
		if len(m.groups) == 0 {
			b.WriteString(helpStyle.Render("  No groups. Create one with: hyphae groupchat create --name <n> --invitee <npub>") + "\n")
		}
		b.WriteString("\n" + helpStyle.Render("↑/↓: navigate • enter: open • r: refresh • q: quit"))
	} else {
		b.WriteString(titleStyle.Render(m.current.Name))
		b.WriteString("\n")
		b.WriteString(helpStyle.Render("Members: " + m.memberLine()))
		b.WriteString("\n\n")
		for _, line := range m.historyLines() {
			b.WriteString(line + "\n")
		}
		b.WriteString("\n" + inputStyle.Render(m.input.View()))
		b.WriteString("\n" + helpStyle.Render("enter: send • ctrl+u/ctrl+d: scroll history • esc: back • ctrl+c: quit"))
	}
	if m.status != "" {
		b.WriteString("\n" + lipgloss.NewStyle().Foreground(lipgloss.Color("#F59E0B")).Render(m.status))
	}
	return b.String()
}

func (m *GroupsModel) memberLine() string {
	parts := make([]string, 0, len(m.members))
	for _, mem := range m.members {
		label := m.npubLabel(mem.Npub)
		if mem.IsCreator {
			label += "*"
		}
		if mem.State != "active" {
			label += "(" + mem.State + ")"
		}
		parts = append(parts, label)
	}
	return strings.Join(parts, ", ")
}

func (m *GroupsModel) npubLabel(npub string) string {
	if m.me != nil && npub == m.me.Npub {
		return "me"
	}
	return safeTruncate(npub, 12) + "…"
}

// historyKeep is how many history lines fit on screen; 0 when the terminal
// size is unknown (show everything).
func (m *GroupsModel) historyKeep() int {
	if keep := m.height - 10; m.height > 0 && keep > 0 {
		return keep
	}
	return 0
}

func (m *GroupsModel) historyAll() []string {
	lines := make([]string, 0, len(m.messages)+1)
	if m.older {
		lines = append(lines, helpStyle.Render("-- ctrl+u for older messages --"))
	}
	for _, msg := range m.messages {
		who := senderStyle.Render(m.npubLabel(msg.Sender))
		if m.me != nil && msg.Sender != m.me.Npub {
			who = recipientStyle.Render(m.npubLabel(msg.Sender))
		}
		ts := timestampStyle.Render(time.Unix(msg.CreatedAt, 0).Format("01-02 15:04"))
		lines = append(lines, fmt.Sprintf("%s %s: %s", ts, who, msg.Plaintext))
	}
	return lines
}

// historyLines is the window of history on screen: the newest lines, moved up
// by scrollUp.
func (m *GroupsModel) historyLines() []string {
	lines := m.historyAll()
	keep := m.historyKeep()
	if keep <= 0 || len(lines) <= keep {
		return lines
	}
	end := len(lines) - min(m.scrollUp, len(lines)-keep)
	return lines[end-keep : end]
}

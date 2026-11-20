package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/urfave/cli/v3"

	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/internal/friend"
	"github.com/iDoris-ai/hyphae/internal/identity"
	"github.com/iDoris-ai/hyphae/internal/storage"
	"github.com/iDoris-ai/hyphae/pkg/types"
)

// RequestsCmd shows strangers who wrote to you and lets you accept, ignore
// or block them.
var RequestsCmd = &cli.Command{
	Name:  "requests",
	Usage: "Strangers who wrote to you: accept, ignore or block",
	Flags: []cli.Flag{
		&cli.StringSliceFlag{Name: "relay", Aliases: []string{"r"}, Usage: "Relay URL(s) for the friend_accept"},
	},
	Action: func(ctx context.Context, c *cli.Command) error {
		relays, err := common.ResolveRelays(c)
		if err != nil {
			return err
		}
		model, err := NewRequestsModel(relays)
		if err != nil {
			return err
		}
		_, err = tea.NewProgram(model, tea.WithAltScreen()).Run()
		return err
	},
}

// RequestsModel lists pending stranger requests for the default identity.
type RequestsModel struct {
	ks      *types.KeyStore
	me      *types.Identity
	relays  []string
	items   []friend.Pending
	cursor  int
	confirm string // npub waiting for "y" to confirm a block
	busy    bool
	status  string
	err     error
}

// requestsDoneMsg reports one finished action and the refreshed list.
type requestsDoneMsg struct {
	status string
	items  []friend.Pending
	err    error
}

// NewRequestsModel loads the default identity's pending requests.
func NewRequestsModel(relays []string) (*RequestsModel, error) {
	ks, err := identity.LoadKeyStore()
	if err != nil {
		return nil, err
	}
	me, err := identity.GetIdentity(ks, "")
	if err != nil {
		return nil, err
	}
	m := &RequestsModel{ks: ks, me: me, relays: relays}
	m.items, err = m.load()
	return m, err
}

func (m *RequestsModel) load() ([]friend.Pending, error) {
	db, err := storage.OpenDB()
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	return friend.ListPending(db, m.ks, m.me.Npub)
}

func (m *RequestsModel) Init() tea.Cmd { return nil }

// act runs one action off the UI goroutine and reloads the list.
func (m *RequestsModel) act(peer, what string) tea.Cmd {
	m.busy = true
	m.status = what + "…"
	return func() tea.Msg {
		db, err := storage.OpenDB()
		if err != nil {
			return requestsDoneMsg{err: err}
		}
		defer func() { _ = db.Close() }()
		var status string
		switch what {
		case "accept":
			secret, kerr := identity.GetSecretKey(m.ks, m.me.Nickname)
			if kerr != nil {
				return requestsDoneMsg{err: kerr}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			res, aerr := friend.Accept(ctx, db, m.ks, m.me, secret, m.relays, peer, "")
			err = aerr
			status = fmt.Sprintf("Accepted: %s is now a friend (contact %q).", short(peer), res.Nickname)
			if errors.Is(aerr, friend.ErrNotPublished) {
				status, err = "Accepted locally, but no relay took the friend_accept; press a again to resend.", nil
			}
		case "ignore":
			_, err = friend.Ignore(db, m.me.Npub, peer)
			status = "Ignored " + short(peer) + "; they stay a stranger."
		case "block":
			_, _, err = friend.Block(db, m.ks, m.me.Npub, peer)
			status = "Blocked " + short(peer) + "."
		}
		if err != nil {
			return requestsDoneMsg{err: err}
		}
		items, err := friend.ListPending(db, m.ks, m.me.Npub)
		return requestsDoneMsg{status: status, items: items, err: err}
	}
}

func (m *RequestsModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case requestsDoneMsg:
		m.busy = false
		if msg.err != nil {
			m.status = "Error: " + msg.err.Error()
			break
		}
		m.items, m.status = msg.items, msg.status
		m.cursor = min(m.cursor, max(0, len(m.items)-1))
	case tea.KeyMsg:
		key := msg.String()
		if key == "q" || key == "esc" || key == "ctrl+c" {
			return m, tea.Quit
		}
		if m.busy {
			break
		}
		if m.confirm != "" {
			peer := m.confirm
			m.confirm = ""
			if key == "y" {
				return m, m.act(peer, "block")
			}
			m.status = "Block cancelled."
			break
		}
		switch key {
		case "up", "k":
			m.cursor = max(0, m.cursor-1)
		case "down", "j":
			m.cursor = min(m.cursor+1, max(0, len(m.items)-1))
		case "a", "i", "b":
			if len(m.items) == 0 {
				break
			}
			peer := m.items[m.cursor].Npub
			switch key {
			case "a":
				return m, m.act(peer, "accept")
			case "i":
				return m, m.act(peer, "ignore")
			case "b":
				m.confirm = peer
				m.status = "Block " + short(peer) + "? Their messages disappear and they are removed from contacts. Press y to confirm."
			}
		}
	}
	return m, nil
}

func (m *RequestsModel) View() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("🙋 Stranger requests"))
	b.WriteString("\n\n")
	if len(m.items) == 0 {
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#666666")).Render("  No one is waiting."))
		b.WriteString("\n")
	}
	for i, it := range m.items {
		cursor := " "
		if i == m.cursor {
			cursor = "▶"
		}
		line := fmt.Sprintf("%s %s  %d message(s)", cursor, short(it.Npub), it.Received)
		if it.Suppressed > 0 {
			line += fmt.Sprintf(", %d not shown", it.Suppressed)
		}
		if i == m.cursor {
			line = lipgloss.NewStyle().Foreground(lipgloss.Color("#7D56F4")).Render(line)
		}
		b.WriteString(line + "\n")
		b.WriteString(timestampStyle.Render("    "+oneLine(it.FirstMessage, 120)) + "\n")
	}
	b.WriteString("\n")
	b.WriteString(helpStyle.Render("↑/↓: select • a: accept • i: ignore • b: block • q/esc: quit"))
	if m.status != "" {
		b.WriteString("\n")
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#F59E0B")).Render(m.status))
	}
	return b.String()
}

func short(npub string) string { return safeTruncate(npub, 16) + "…" }

// oneLine flattens a stranger's text to at most n characters and drops
// control characters, so it cannot move the cursor or carry escape sequences.
func oneLine(s string, n int) string {
	out := []rune(strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\t':
			return ' '
		case unicode.IsControl(r):
			return -1
		}
		return r
	}, s))
	if len(out) > n {
		return string(out[:n]) + "…"
	}
	return string(out)
}

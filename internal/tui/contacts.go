package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/AuraAIHQ/agent-speaker/internal/identity"
	"github.com/AuraAIHQ/agent-speaker/pkg/types"
)

// ContactsModel represents the contacts list TUI
type ContactsModel struct {
	contacts   []*types.Contact
	identities []*types.Identity
	cursor     int
	selected   string
	width      int
	height     int
	err        error
}

// NewContactsModel creates a new contacts model
func NewContactsModel() (*ContactsModel, error) {
	ks, err := identity.LoadKeyStore()
	if err != nil {
		return nil, err
	}

	return &ContactsModel{
		contacts:   identity.ListContacts(ks),
		identities: identity.ListIdentities(ks),
		cursor:     0,
	}, nil
}

// Init initializes the model
func (m *ContactsModel) Init() tea.Cmd {
	return nil
}

// Update handles messages
func (m *ContactsModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			return m, tea.Quit

		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}

		case "down", "j":
			total := len(m.contacts) + len(m.identities)
			if m.cursor < total-1 {
				m.cursor++
			}

		case "enter":
			offset := len(m.identities)
			if m.cursor >= offset && m.cursor < offset+len(m.contacts) {
				m.selected = m.contacts[m.cursor-offset].Nickname
			}
			return m, tea.Quit
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	}

	return m, nil
}

// View renders the UI
func (m *ContactsModel) View() string {
	if m.err != nil {
		return fmt.Sprintf("Error: %v\n\nPress any key to exit...", m.err)
	}

	var b strings.Builder

	// Title
	title := titleStyle.Render("📇 Contacts & Identities")
	b.WriteString(title)
	b.WriteString("\n\n")

	// Identities section
	b.WriteString(lipgloss.NewStyle().Bold(true).Render("👤 Your Identities:"))
	b.WriteString("\n")

	for i, id := range m.identities {
		cursor := " "
		if m.cursor == i {
			cursor = "▶"
		}

		line := fmt.Sprintf("%s %s (%s...)",
			cursor,
			id.Nickname,
			safeTruncate(id.Npub, 16),
		)

		if m.cursor == i {
			line = lipgloss.NewStyle().Foreground(lipgloss.Color("#7D56F4")).Render(line)
		}

		b.WriteString(line)
		b.WriteString("\n")
	}

	b.WriteString("\n")

	// Contacts section
	b.WriteString(lipgloss.NewStyle().Bold(true).Render("📇 Contacts:"))
	b.WriteString("\n")

	offset := len(m.identities)
	for i, contact := range m.contacts {
		idx := offset + i
		cursor := " "
		if m.cursor == idx {
			cursor = "▶"
		}

		line := fmt.Sprintf("%s %s (%s...)",
			cursor,
			contact.Nickname,
			safeTruncate(contact.Npub, 16),
		)

		if m.cursor == idx {
			line = lipgloss.NewStyle().Foreground(lipgloss.Color("#7D56F4")).Render(line)
		}

		b.WriteString(line)
		b.WriteString("\n")
	}

	if len(m.contacts) == 0 {
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#666666")).Render("  No contacts. Add one with:"))
		b.WriteString("\n")
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#666666")).Render("  agent-speaker contact add --nickname <name> --npub <npub>"))
		b.WriteString("\n")
	}

	// Help
	b.WriteString("\n")
	help := helpStyle.Render("↑/↓: navigate • enter: select • q/esc: quit")
	b.WriteString(help)

	return b.String()
}

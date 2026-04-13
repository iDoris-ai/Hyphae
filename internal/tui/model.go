package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/AuraAIHQ/agent-speaker/internal/identity"
	"github.com/AuraAIHQ/agent-speaker/internal/storage"
	"github.com/AuraAIHQ/agent-speaker/pkg/types"
)

// Ensure types is used
var _ = types.Identity{}

// Styles
var (
	titleStyle = lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#7D56F4")).
		MarginLeft(2)

	senderStyle = lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#04B575"))

	recipientStyle = lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#F472B6"))

	timestampStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#666666")).
		Italic(true)

	inputStyle = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#7D56F4")).
		PaddingLeft(1).
		PaddingRight(1)

	helpStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#666666"))

	encryptedStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#F59E0B"))
)

// ChatModel represents the TUI chat interface
type ChatModel struct {
	viewport      viewport.Model
	input         textinput.Model
	messages      []types.StoredMessage
	contactName   string
	contactNpub   string
	myIdentity    *types.Identity
	store         *storage.MessageStore
	width         int
	height        int
	err           error
	loading       bool
}

// NewChatModel creates a new chat model
func NewChatModel(contactName string) (*ChatModel, error) {
	// Load identity
	ks, err := identity.LoadKeyStore()
	if err != nil {
		return nil, fmt.Errorf("failed to load keystore: %w", err)
	}

	myIdentity, err := identity.GetIdentity(ks, "")
	if err != nil {
		return nil, fmt.Errorf("failed to get identity: %w", err)
	}

	// Load contact
	contact, err := identity.GetContact(ks, contactName)
	if err != nil {
		return nil, fmt.Errorf("contact '%s' not found: %w", contactName, err)
	}

	// Initialize storage
	db, err := storage.InitDB()
	if err != nil {
		return nil, fmt.Errorf("failed to init storage: %w", err)
	}

	store := storage.NewMessageStore(db)

	// Setup input
	ti := textinput.New()
	ti.Placeholder = "Type a message..."
	ti.Focus()
	ti.CharLimit = 500
	ti.Width = 50

	// Setup viewport
	vp := viewport.New(80, 20)
	vp.SetContent("Loading messages...")

	return &ChatModel{
		viewport:    vp,
		input:       ti,
		contactName: contactName,
		contactNpub: contact.Npub,
		myIdentity:  myIdentity,
		store:       store,
		loading:     true,
	}, nil
}

// Init initializes the model
func (m *ChatModel) Init() tea.Cmd {
	return tea.Batch(
		textinput.Blink,
		m.loadMessages(),
	)
}

// Update handles messages
func (m *ChatModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC, tea.KeyEsc:
			return m, tea.Quit

		case tea.KeyEnter:
			if m.input.Value() != "" {
				// Send message (placeholder - would integrate with messaging)
				m.input.SetValue("")
				// Reload messages
				cmds = append(cmds, m.loadMessages())
			}

		case tea.KeyPgUp:
			m.viewport.LineUp(3)

		case tea.KeyPgDown:
			m.viewport.LineDown(3)
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.viewport.Width = msg.Width - 4
		m.viewport.Height = msg.Height - 8
		m.input.Width = msg.Width - 10

	case messagesMsg:
		m.messages = msg.messages
		m.loading = false
		m.updateViewportContent()

	case errorMsg:
		m.err = msg.err
		m.loading = false
	}

	// Update components
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	cmds = append(cmds, cmd)

	m.viewport, cmd = m.viewport.Update(msg)
	cmds = append(cmds, cmd)

	return m, tea.Batch(cmds...)
}

// View renders the UI
func (m *ChatModel) View() string {
	if m.err != nil {
		return fmt.Sprintf("Error: %v\n\nPress any key to exit...", m.err)
	}

	var b strings.Builder

	// Title
	title := titleStyle.Render(fmt.Sprintf("💬 Chat with %s", m.contactName))
	b.WriteString(title)
	b.WriteString("\n")

	// Subtitle with npub
	subtitle := timestampStyle.Render(fmt.Sprintf("Your npub: %s...", m.myIdentity.Npub[:20]))
	b.WriteString(subtitle)
	b.WriteString("\n\n")

	// Messages viewport
	b.WriteString(m.viewport.View())
	b.WriteString("\n")

	// Input
	b.WriteString(inputStyle.Render(m.input.View()))
	b.WriteString("\n\n")

	// Help
	help := helpStyle.Render("enter: send • pgup/pgdn: scroll • esc/ctrl+c: quit")
	b.WriteString(help)

	return b.String()
}

// updateViewportContent updates the viewport with messages
func (m *ChatModel) updateViewportContent() {
	if len(m.messages) == 0 {
		m.viewport.SetContent("No messages yet. Start the conversation!")
		return
	}

	var content strings.Builder

	// Show messages in chronological order (oldest first)
	for i := len(m.messages) - 1; i >= 0; i-- {
		msg := m.messages[i]
		content.WriteString(m.formatMessage(msg))
		content.WriteString("\n")
	}

	m.viewport.SetContent(content.String())
	m.viewport.GotoBottom()
}

// formatMessage formats a single message
func (m *ChatModel) formatMessage(msg types.StoredMessage) string {
	var b strings.Builder

	// Timestamp
	ts := time.Unix(msg.CreatedAt, 0).Format("15:04")
	b.WriteString(timestampStyle.Render(ts))
	b.WriteString(" ")

	// Sender indicator
	if msg.IsIncoming {
		b.WriteString(recipientStyle.Render(fmt.Sprintf("%s:", m.contactName)))
	} else {
		b.WriteString(senderStyle.Render("You:"))
	}
	b.WriteString(" ")

	// Content
	content := msg.Plaintext
	if content == "" {
		content = msg.Content
	}
	b.WriteString(content)

	// Encryption indicator
	if msg.IsEncrypted {
		b.WriteString(" ")
		b.WriteString(encryptedStyle.Render("🔒"))
	}

	return b.String()
}

// Message types
type messagesMsg struct {
	messages []types.StoredMessage
}

type errorMsg struct {
	err error
}

// loadMessages loads conversation messages
func (m *ChatModel) loadMessages() tea.Cmd {
	return func() tea.Msg {
		messages, err := m.store.GetConversation(m.myIdentity.Npub, m.contactNpub, 100)
		if err != nil {
			return errorMsg{err: err}
		}
		return messagesMsg{messages: messages}
	}
}

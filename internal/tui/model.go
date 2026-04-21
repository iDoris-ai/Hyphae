package tui

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"fiatjaf.com/nostr"
	"github.com/AuraAIHQ/agent-speaker/internal/common"
	"github.com/AuraAIHQ/agent-speaker/internal/identity"
	"github.com/AuraAIHQ/agent-speaker/internal/messaging"
	"github.com/AuraAIHQ/agent-speaker/internal/storage"
	"github.com/AuraAIHQ/agent-speaker/pkg/crypto"
	"github.com/AuraAIHQ/agent-speaker/pkg/types"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

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

// safeTruncate returns the first n bytes of s, or all of s if shorter.
func safeTruncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// ChatModel represents the TUI chat interface
type ChatModel struct {
	viewport    viewport.Model
	input       textinput.Model
	messages    []types.StoredMessage
	contactName string
	contactNpub string
	myIdentity  *types.Identity
	senderSK    nostr.SecretKey
	store       *storage.MessageStore
	db          *sql.DB
	relays      []string
	width       int
	height      int
	err         error
	loading     bool
}

// NewChatModel creates a new chat model
func NewChatModel(contactName string) (*ChatModel, error) {
	ks, err := identity.LoadKeyStore()
	if err != nil {
		return nil, fmt.Errorf("failed to load keystore: %w", err)
	}

	myIdentity, err := identity.GetIdentity(ks, "")
	if err != nil {
		return nil, fmt.Errorf("failed to get identity: %w", err)
	}

	contact, err := identity.GetContact(ks, contactName)
	if err != nil {
		return nil, fmt.Errorf("contact '%s' not found: %w", contactName, err)
	}

	senderSK, err := identity.GetSecretKey(ks, myIdentity.Nickname)
	if err != nil {
		return nil, fmt.Errorf("failed to get sender key: %w", err)
	}

	db, err := storage.InitDB()
	if err != nil {
		return nil, fmt.Errorf("failed to init storage: %w", err)
	}

	store := storage.NewMessageStore(db)

	ti := textinput.New()
	ti.Placeholder = "Type a message..."
	ti.Focus()
	ti.CharLimit = 500
	ti.Width = 50

	vp := viewport.New(80, 20)
	vp.SetContent("Loading messages...")

	return &ChatModel{
		viewport:    vp,
		input:       ti,
		contactName: contactName,
		contactNpub: contact.Npub,
		myIdentity:  myIdentity,
		senderSK:    senderSK,
		store:       store,
		db:          db,
		relays:      []string{"wss://relay.aastar.io"},
		loading:     true,
	}, nil
}

// Close releases the database connection.
func (m *ChatModel) Close() error {
	if m.db != nil {
		return m.db.Close()
	}
	return nil
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
			content := m.input.Value()
			if content != "" {
				m.input.SetValue("")
				m.loading = true
				cmds = append(cmds, m.sendMessage(content))
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

	case messageSentMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err
		} else {
			cmds = append(cmds, m.loadMessages())
		}

	case errorMsg:
		m.err = msg.err
		m.loading = false
	}

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

	title := titleStyle.Render(fmt.Sprintf("💬 Chat with %s", m.contactName))
	b.WriteString(title)
	b.WriteString("\n")

	subtitle := timestampStyle.Render(fmt.Sprintf("Your npub: %s...", safeTruncate(m.myIdentity.Npub, 20)))
	b.WriteString(subtitle)
	b.WriteString("\n\n")

	b.WriteString(m.viewport.View())
	b.WriteString("\n")

	b.WriteString(inputStyle.Render(m.input.View()))
	b.WriteString("\n\n")

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

	ts := time.Unix(msg.CreatedAt, 0).Format("15:04")
	b.WriteString(timestampStyle.Render(ts))
	b.WriteString(" ")

	if msg.IsIncoming {
		b.WriteString(recipientStyle.Render(fmt.Sprintf("%s:", m.contactName)))
	} else {
		b.WriteString(senderStyle.Render("You:"))
	}
	b.WriteString(" ")

	content := msg.Plaintext
	if content == "" {
		content = msg.Content
	}
	b.WriteString(content)

	if msg.IsEncrypted {
		b.WriteString(" ")
		b.WriteString(encryptedStyle.Render("🔒"))
	}

	return b.String()
}

// Message types for tea.Cmd results
type messagesMsg struct {
	messages []types.StoredMessage
}

type messageSentMsg struct {
	err error
}

type errorMsg struct {
	err error
}

// loadMessages loads conversation messages from the database.
func (m *ChatModel) loadMessages() tea.Cmd {
	return func() tea.Msg {
		messages, err := m.store.GetConversation(m.myIdentity.Npub, m.contactNpub, 100)
		if err != nil {
			return errorMsg{err: err}
		}
		return messagesMsg{messages: messages}
	}
}

// sendMessage encrypts, publishes, and locally stores an outgoing message.
func (m *ChatModel) sendMessage(content string) tea.Cmd {
	return func() tea.Msg {
		recipientPK, err := common.ParsePublicKey(m.contactNpub)
		if err != nil {
			return messageSentMsg{err: fmt.Errorf("invalid recipient key: %w", err)}
		}

		encrypted, err := crypto.EncryptMessage(content, m.senderSK, recipientPK)
		if err != nil {
			return messageSentMsg{err: fmt.Errorf("encryption failed: %w", err)}
		}

		compressed, _ := messaging.CompressText(encrypted)

		tags := nostr.Tags{
			{"p", common.PubKeyToHex(recipientPK)},
			{"c", messaging.AgentTag},
			{"z", messaging.CompressTag},
			{"v", messaging.AgentVersion},
			{"enc", "nip44"},
		}

		event := &nostr.Event{
			CreatedAt: nostr.Now(),
			Kind:      messaging.AgentKind,
			Tags:      tags,
			Content:   compressed,
			PubKey:    m.senderSK.Public(),
		}
		event.Sign(m.senderSK)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		for _, relayURL := range m.relays {
			relay, err := nostr.RelayConnect(ctx, relayURL, nostr.RelayOptions{})
			if err != nil {
				continue
			}
			_ = relay.Publish(ctx, *event)
			relay.Close()
		}

		_ = messaging.StoreOutgoingMessage(event, m.contactNpub, content, true)
		return messageSentMsg{err: nil}
	}
}

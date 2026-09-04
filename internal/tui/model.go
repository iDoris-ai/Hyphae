package tui

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"fiatjaf.com/nostr"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/internal/identity"
	"github.com/iDoris-ai/hyphae/internal/messaging"
	"github.com/iDoris-ai/hyphae/internal/storage"
	"github.com/iDoris-ai/hyphae/pkg/crypto"
	"github.com/iDoris-ai/hyphae/pkg/types"
)

const (
	defaultRelay     = "wss://relay.aastar.io"
	maxMessageLen    = 500
	relayDialTimeout = 5 * time.Second
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
// Intended for ASCII strings only (npub/nsec/hex). Do NOT use on UTF-8 text
// that may contain multi-byte runes — it can split a codepoint and corrupt
// output. Use []rune slicing for user-facing nicknames or content.
func safeTruncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// ChatModel represents the TUI chat interface.
//
// SECURITY NOTE: ChatModel intentionally does NOT hold the user's secret key
// as a field. The secret key is loaded on-demand inside sendMessage and only
// lives in a local variable / goroutine stack. This limits exposure via core
// dumps, debuggers, or accidental fmt.Printf("%+v", m) logging.
type ChatModel struct {
	viewport    viewport.Model
	input       textinput.Model
	messages    []types.StoredMessage
	contactName string
	contactNpub string
	myIdentity  *types.Identity
	store       *storage.MessageStore
	db          *sql.DB
	relays      []string
	width       int
	height      int
	err         error
	loading     bool
}

// NewChatModel creates a new chat model. relays may be empty, in which case
// defaultRelay is used.
func NewChatModel(contactName string, relays ...string) (*ChatModel, error) {
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

	// Verify the secret key is loadable before we open the DB, so that we
	// fail early instead of leaking a connection. The key itself is not
	// retained here — sendMessage will load it again on demand.
	if _, err := identity.GetSecretKey(ks, myIdentity.Nickname); err != nil {
		return nil, fmt.Errorf("failed to access sender key: %w", err)
	}

	db, err := storage.InitDB()
	if err != nil {
		return nil, fmt.Errorf("failed to init storage: %w", err)
	}

	store := storage.NewMessageStore(db)

	ti := textinput.New()
	ti.Placeholder = "Type a message..."
	ti.Focus()
	ti.CharLimit = maxMessageLen
	ti.Width = 50

	vp := viewport.New(80, 20)
	vp.SetContent("Loading messages...")

	if len(relays) == 0 {
		relays = []string{defaultRelay}
	}

	return &ChatModel{
		viewport:    vp,
		input:       ti,
		contactName: contactName,
		contactNpub: contact.Npub,
		myIdentity:  myIdentity,
		store:       store,
		db:          db,
		relays:      relays,
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
		// In error state any key exits, otherwise the user would be stuck.
		if m.err != nil {
			return m, tea.Quit
		}

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

// updateViewportContent renders messages oldest-first (top) to newest (bottom).
func (m *ChatModel) updateViewportContent() {
	if len(m.messages) == 0 {
		m.viewport.SetContent("No messages yet. Start the conversation!")
		return
	}

	// store.GetConversation returns DESC; sort ASC for natural reading order.
	ordered := make([]types.StoredMessage, len(m.messages))
	copy(ordered, m.messages)
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].CreatedAt < ordered[j].CreatedAt
	})

	var content strings.Builder
	for _, msg := range ordered {
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
//
// Returns a non-nil error only if *both* all relay publishes fail *and* local
// storage fails. A single successful relay (or successful local store, even
// with all relays failing) is considered a partial success so the user does
// not lose the message — but the error is surfaced so the UI can warn.
func (m *ChatModel) sendMessage(content string) tea.Cmd {
	return func() tea.Msg {
		recipientPK, err := common.ParsePublicKey(m.contactNpub)
		if err != nil {
			return messageSentMsg{err: fmt.Errorf("invalid recipient key: %w", err)}
		}

		// Load the secret key on demand. It lives only in this goroutine's
		// stack and is not retained on ChatModel.
		ks, err := identity.LoadKeyStore()
		if err != nil {
			return messageSentMsg{err: fmt.Errorf("load keystore: %w", err)}
		}
		senderSK, err := identity.GetSecretKey(ks, m.myIdentity.Nickname)
		if err != nil {
			return messageSentMsg{err: fmt.Errorf("get sender key: %w", err)}
		}

		encrypted, err := crypto.EncryptMessage(content, senderSK, recipientPK)
		if err != nil {
			return messageSentMsg{err: fmt.Errorf("encrypt: %w", err)}
		}

		compressed, err := messaging.CompressText(encrypted)
		if err != nil {
			return messageSentMsg{err: fmt.Errorf("compress: %w", err)}
		}

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
			PubKey:    senderSK.Public(),
		}
		event.Sign(senderSK)

		// Publish to each relay with its own timeout so a slow relay does
		// not starve the rest. Collect errors so we can report accurately.
		var relayErrs []error
		published := 0
		for _, relayURL := range m.relays {
			relayCtx, cancel := context.WithTimeout(context.Background(), relayDialTimeout)
			relay, err := nostr.RelayConnect(relayCtx, relayURL, nostr.RelayOptions{})
			if err != nil {
				cancel()
				relayErrs = append(relayErrs, fmt.Errorf("connect %s: %w", relayURL, err))
				continue
			}
			if err := relay.Publish(relayCtx, *event); err != nil {
				relayErrs = append(relayErrs, fmt.Errorf("publish %s: %w", relayURL, err))
			} else {
				published++
			}
			relay.Close()
			cancel()
		}

		storeErr := messaging.StoreOutgoingMessage(event, m.contactNpub, content, true)

		switch {
		case published == 0 && storeErr != nil:
			// Total failure — nothing the user can recover from inside the TUI.
			relayErrs = append(relayErrs, fmt.Errorf("local store: %w", storeErr))
			return messageSentMsg{err: fmt.Errorf("send failed: %w", errors.Join(relayErrs...))}
		case published == 0:
			// Stored locally but not on any relay; warn the user so they
			// know the contact has not received it yet.
			return messageSentMsg{err: fmt.Errorf("no relay accepted the message (saved locally): %w", errors.Join(relayErrs...))}
		case storeErr != nil:
			// Published but local store failed — uncommon. Log and continue.
			log.Printf("tui: published to %d relay(s) but local store failed: %v", published, storeErr)
			return messageSentMsg{err: nil}
		default:
			return messageSentMsg{err: nil}
		}
	}
}

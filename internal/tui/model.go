package tui

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/iDoris-ai/hyphae/internal/identity"
	"github.com/iDoris-ai/hyphae/internal/messaging"
	"github.com/iDoris-ai/hyphae/internal/relayconfig"
	"github.com/iDoris-ai/hyphae/internal/storage"
	"github.com/iDoris-ai/hyphae/pkg/types"
)

const (
	defaultRelay     = relayconfig.DefaultRelay
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
// as a field. The secret key is loaded on-demand inside the outbox worker and only
// lives in a local variable / goroutine stack. This limits exposure via core
// dumps, debuggers, or accidental fmt.Printf("%+v", m) logging.
type ChatModel struct {
	viewport              viewport.Model
	input                 textinput.Model
	messages              []types.StoredMessage
	contactName           string
	contactNpub           string
	myIdentity            *types.Identity
	store                 *storage.MessageStore
	db                    *sql.DB
	relays                []string
	width                 int
	height                int
	err                   error
	loading               bool
	inboxCtx              context.Context
	inboxCancel           context.CancelFunc
	inboxUpdates          chan messaging.AgentInboxWatchUpdate
	inboxDone             chan struct{}
	inboxMu               sync.Mutex
	inboxStarted          bool
	inboxClosed           bool
	inboxStatus           string
	inboxReceived         int
	outboxCtx             context.Context
	outboxCancel          context.CancelFunc
	outboxRequests        chan outboxSendRequest
	outboxUpdates         chan outboxDeliveryUpdate
	outboxDone            chan struct{}
	outboxStarted         bool
	outboxSending         bool
	outboxStatus          string
	activeSendID          uint64
	nextSendID            uint64
	sendingContent        string
	messageLoadGeneration uint64
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

	inboxCtx, inboxCancel := context.WithCancel(context.Background())
	outboxCtx, outboxCancel := context.WithCancel(context.Background())
	return &ChatModel{
		viewport:       vp,
		input:          ti,
		contactName:    contactName,
		contactNpub:    contact.Npub,
		myIdentity:     myIdentity,
		store:          store,
		db:             db,
		relays:         relays,
		loading:        true,
		inboxCtx:       inboxCtx,
		inboxCancel:    inboxCancel,
		inboxUpdates:   make(chan messaging.AgentInboxWatchUpdate, 256),
		inboxDone:      make(chan struct{}),
		inboxStatus:    "Connecting to relay…",
		outboxCtx:      outboxCtx,
		outboxCancel:   outboxCancel,
		outboxRequests: make(chan outboxSendRequest, 16),
		outboxUpdates:  make(chan outboxDeliveryUpdate, 64),
		outboxDone:     make(chan struct{}),
		outboxStatus:   "Outbox: no pending messages in this conversation",
	}, nil
}

// Close cancels and joins the inbox watcher before releasing the database.
func (m *ChatModel) Close() error {
	m.stopInboxWatcher()
	m.inboxMu.Lock()
	started := m.inboxStarted
	m.inboxMu.Unlock()
	if started {
		select {
		case <-m.inboxDone:
		case <-time.After(5 * time.Second):
			return errors.New("timed out stopping inbox watcher; database left open")
		}
	}
	m.inboxMu.Lock()
	outboxStarted := m.outboxStarted
	m.inboxMu.Unlock()
	if outboxStarted {
		select {
		case <-m.outboxDone:
		case <-time.After(5 * time.Second):
			return errors.New("timed out stopping TUI outbox worker; database left open")
		}
	}
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
		m.startInboxWatcher(),
		m.startOutboxWorker(),
	)
}

type inboxWatchStartedMsg struct{}

type inboxWatchUpdateMsg struct {
	update messaging.AgentInboxWatchUpdate
}

func (m *ChatModel) startInboxWatcher() tea.Cmd {
	return func() tea.Msg {
		m.inboxMu.Lock()
		if m.inboxClosed {
			m.inboxMu.Unlock()
			return inboxWatchStartedMsg{}
		}
		if m.inboxStarted {
			m.inboxMu.Unlock()
			return inboxWatchStartedMsg{}
		}
		m.inboxStarted = true
		m.inboxMu.Unlock()

		go func() {
			defer close(m.inboxDone)
			err := messaging.WatchAgentInboxWithStore(m.inboxCtx, m.myIdentity.Nickname, m.relays, m.store, func(update messaging.AgentInboxWatchUpdate) {
				select {
				case m.inboxUpdates <- update:
				case <-m.inboxCtx.Done():
				}
			})
			if err != nil && m.inboxCtx.Err() == nil {
				select {
				case m.inboxUpdates <- messaging.AgentInboxWatchUpdate{Err: err}:
				case <-m.inboxCtx.Done():
				}
			}
		}()
		return inboxWatchStartedMsg{}
	}
}

func (m *ChatModel) waitInboxUpdate() tea.Cmd {
	return func() tea.Msg {
		select {
		case update := <-m.inboxUpdates:
			return inboxWatchUpdateMsg{update: update}
		case <-m.inboxCtx.Done():
			return nil
		}
	}
}

func (m *ChatModel) stopInboxWatcher() {
	m.inboxMu.Lock()
	m.inboxClosed = true
	if m.inboxCancel != nil {
		m.inboxCancel()
	}
	if m.outboxCancel != nil {
		m.outboxCancel()
	}
	m.inboxMu.Unlock()
}

// Update handles messages
func (m *ChatModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		// In error state any key exits, otherwise the user would be stuck.
		if m.err != nil {
			m.stopInboxWatcher()
			return m, tea.Quit
		}

		switch msg.Type {
		case tea.KeyCtrlC, tea.KeyEsc:
			m.stopInboxWatcher()
			return m, tea.Quit

		case tea.KeyEnter:
			content := m.input.Value()
			if content != "" && !m.outboxSending {
				m.inboxMu.Lock()
				closed := m.inboxClosed
				m.inboxMu.Unlock()
				if closed {
					m.outboxStatus = "Outbox: closed; message was not submitted"
					break
				}
				m.nextSendID++
				m.activeSendID = m.nextSendID
				m.sendingContent = content
				m.outboxSending = true
				m.outboxStatus = "Outbox: sending; durable queue not yet confirmed"
				cmds = append(cmds, m.sendMessage(m.activeSendID, content))
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
		if msg.generation != m.messageLoadGeneration {
			break
		}
		m.messages = msg.messages
		m.loading = false
		m.updateViewportContent()

	case outboxWorkerStartedMsg:
		cmds = append(cmds, m.waitOutboxUpdate())
	case outboxDeliveryUpdateMsg:
		m.outboxStatus = formatOutboxStatus(msg.update)
		if msg.update.requestID != 0 && msg.update.requestID == m.activeSendID {
			m.outboxSending = false
			if msg.update.state != messaging.AgentMessageFailed {
				if m.input.Value() == m.sendingContent {
					m.input.SetValue("")
				}
				cmds = append(cmds, m.loadMessages())
			}
			m.sendingContent = ""
		}
		cmds = append(cmds, m.waitOutboxUpdate())

	case errorMsg:
		m.err = msg.err
		m.loading = false
	case inboxWatchStartedMsg:
		cmds = append(cmds, m.waitInboxUpdate())
	case inboxWatchUpdateMsg:
		if msg.update.Err != nil {
			if msg.update.Connected {
				m.inboxStatus = "Connected; history sync warning: " + msg.update.Err.Error()
			} else {
				m.inboxStatus = "Relay reconnecting: " + msg.update.Err.Error()
			}
		} else if msg.update.Connected {
			m.inboxStatus = "Connected"
		} else if msg.update.Message != nil {
			m.inboxReceived++
			m.inboxStatus = fmt.Sprintf("Connected • %d new received", m.inboxReceived)
			cmds = append(cmds, m.loadMessages())
		}
		cmds = append(cmds, m.waitInboxUpdate())
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
	b.WriteString(helpStyle.Render("Inbox: " + m.inboxStatus))
	b.WriteString("\n")
	b.WriteString(helpStyle.Render(m.outboxStatus))
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
	messages   []types.StoredMessage
	generation uint64
}

type errorMsg struct {
	err error
}

// loadMessages loads conversation messages from the database.
func (m *ChatModel) loadMessages() tea.Cmd {
	m.messageLoadGeneration++
	generation := m.messageLoadGeneration
	return func() tea.Msg {
		messages, err := m.store.GetConversation(m.myIdentity.Npub, m.contactNpub, -1)
		if err != nil {
			return errorMsg{err: err}
		}
		return messagesMsg{messages: messages, generation: generation}
	}
}

func (m *ChatModel) sendMessage(requestID uint64, content string) tea.Cmd {
	return func() tea.Msg {
		request := outboxSendRequest{requestID: requestID, content: content}
		select {
		case m.outboxRequests <- request:
			return nil
		case <-m.outboxCtx.Done():
			return outboxDeliveryUpdateMsg{update: outboxDeliveryUpdate{
				requestID: requestID, state: messaging.AgentMessageFailed,
				issue: messaging.AgentMessageIssueSendFailed,
			}}
		}
	}
}

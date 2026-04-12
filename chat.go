// Package main provides an interactive chat interface for agent-speaker
// using a split-screen TUI with message history, system info, and input.
package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"fiatjaf.com/nostr"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/fatih/color"
	"github.com/urfave/cli/v3"
)

// ChatModel is the Bubbletea model for the chat interface
type ChatModel struct {
	// UI Components
	viewport  viewport.Model
	textInput textinput.Model

	// State
	messages   []ChatMessage
	peerPubkey string
	myPubkey   string
	peerShort  string
	relayURL   string
	focus      FocusArea
	width      int
	height     int

	// Business logic
	subManager *SubscriptionManager
	heartbeat  *HeartbeatManager
	relays     []string
	keyer      nostr.Keyer

	// Agent mode
	agentMode bool
	agentTask string
}

// ChatMessage represents a message in the chat
type ChatMessage struct {
	ID         string
	From       string
	Content    string
	Timestamp  time.Time
	IsMe       bool
	Compressed bool
}

// FocusArea represents which UI element has focus
type FocusArea int

const (
	FocusInput FocusArea = iota
	FocusHistory
)

// Styles for the UI
var (
	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#7D56F4")).
			Padding(0, 1)

	myMessageStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#04B575")).
			PaddingLeft(2)

	peerMessageStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFFDF5")).
			PaddingLeft(2)

	timestampStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#6B6B6B"))

	infoStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#6B6B6B")).
			Padding(0, 1)

	sidebarStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#3C3C3C")).
			Padding(1).
			Width(30)

	inputStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#7D56F4"))
)

// NewChatModel creates a new chat model
func NewChatModel(peerPubkey, myPubkey, relayURL string, relays []string, keyer nostr.Keyer) *ChatModel {
	// Shorten pubkeys for display
	peerShort := peerPubkey
	if len(peerPubkey) > 16 {
		peerShort = peerPubkey[:12] + "..."
	}

	// Setup text input
	ti := textinput.New()
	ti.Placeholder = "Type a message... (/? for commands)"
	ti.Focus()
	ti.CharLimit = 1000
	ti.Width = 50

	return &ChatModel{
		textInput:  ti,
		peerPubkey: peerPubkey,
		myPubkey:   myPubkey,
		peerShort:  peerShort,
		relayURL:   relayURL,
		relays:     relays,
		keyer:      keyer,
		focus:      FocusInput,
		messages:   make([]ChatMessage, 0),
	}
}

// Init initializes the chat model
func (m *ChatModel) Init() tea.Cmd {
	// Start subscription
	ctx := context.Background()
	handler := func(event nostr.Event) {
		// This will be called when new events arrive
		// We'll use a channel to communicate with the UI
	}

	sm, err := SubscribeToPeer(ctx, sys, m.relays, m.peerPubkey, handler)
	if err != nil {
		color.Red("Failed to subscribe: %v", err)
	} else {
		m.subManager = sm
	}

	// Start heartbeat
	hm := NewHeartbeatManager(sys, m.relays, 30*time.Second)
	go hm.Start(ctx, m.keyer)
	m.heartbeat = hm

	return textinput.Blink
}

// Update handles messages and user input
func (m *ChatModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

		// Calculate layout
		historyWidth := msg.Width - 35 // Leave room for sidebar
		inputHeight := 3
		headerHeight := 1

		m.viewport.Width = historyWidth
		m.viewport.Height = msg.Height - inputHeight - headerHeight - 2
		m.textInput.Width = historyWidth - 4

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			return m, tea.Quit

		case "tab":
			if m.focus == FocusInput {
				m.focus = FocusHistory
				m.textInput.Blur()
			} else {
				m.focus = FocusInput
				m.textInput.Focus()
			}

		case "enter":
			if m.focus == FocusInput {
				input := m.textInput.Value()
				if input != "" {
					cmds = append(cmds, m.handleInput(input))
					m.textInput.SetValue("")
				}
			}

		case "/":
			if m.focus == FocusInput && m.textInput.Value() == "" {
				m.textInput.SetValue("/")
			}

		case "@":
			if m.focus == FocusInput && m.textInput.Value() == "" {
				m.textInput.SetValue("@")
				m.agentMode = true
			}

		case "up", "down":
			if m.focus == FocusHistory {
				// Scroll history
				if msg.String() == "up" {
					m.viewport.LineUp(1)
				} else {
					m.viewport.LineDown(1)
				}
			}

		case "ctrl+l":
			// Clear screen
			m.messages = []ChatMessage{}
			m.updateViewport()
		}

	case NostrEventMsg:
		// New message from subscription
		m.addMessageFromEvent(msg.Event)
		m.updateViewport()

	case SendMessageMsg:
		// Message sent confirmation
		m.addMessage(ChatMessage{
			ID:        msg.ID,
			From:      m.myPubkey,
			Content:   msg.Content,
			Timestamp: time.Now(),
			IsMe:      true,
		})
		m.updateViewport()
	}

	// Update components
	var cmd tea.Cmd
	m.textInput, cmd = m.textInput.Update(msg)
	cmds = append(cmds, cmd)

	m.viewport, cmd = m.viewport.Update(msg)
	cmds = append(cmds, cmd)

	return m, tea.Batch(cmds...)
}

// View renders the UI
func (m *ChatModel) View() string {
	if m.width == 0 || m.height == 0 {
		return "Loading..."
	}

	// Header
	header := headerStyle.Render(fmt.Sprintf("🤖 Agent Chat | Peer: %s", m.peerShort))

	// Main content area (history + sidebar)
	historyView := m.renderHistory()
	sidebarView := m.renderSidebar()

	mainArea := lipgloss.JoinHorizontal(
		lipgloss.Top,
		historyView,
		sidebarView,
	)

	// Input area
	inputView := m.renderInput()

	// Combine all
	return lipgloss.JoinVertical(
		lipgloss.Left,
		header,
		mainArea,
		inputView,
	)
}

// renderHistory renders the message history area
func (m *ChatModel) renderHistory() string {
	content := m.viewport.View()

	style := lipgloss.NewStyle().
		Width(m.viewport.Width).
		Height(m.viewport.Height).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#3C3C3C"))

	if m.focus == FocusHistory {
		style = style.BorderForeground(lipgloss.Color("#7D56F4"))
	}

	return style.Render(content)
}

// renderSidebar renders the sidebar with system info
func (m *ChatModel) renderSidebar() string {
	var sections []string

	// Connection status
	status := "🟢 Online"
	if m.subManager != nil && !m.subManager.IsRunning() {
		status = "🔴 Disconnected"
	}
	sections = append(sections, lipgloss.NewStyle().Bold(true).Render("Status"))
	sections = append(sections, infoStyle.Render(status))
	sections = append(sections, "")

	// Relay info
	sections = append(sections, lipgloss.NewStyle().Bold(true).Render("Relay"))
	relayShort := m.relayURL
	if len(relayShort) > 25 {
		relayShort = relayShort[:22] + "..."
	}
	sections = append(sections, infoStyle.Render(relayShort))
	sections = append(sections, "")

	// Commands
	sections = append(sections, lipgloss.NewStyle().Bold(true).Render("Commands"))
	sections = append(sections, infoStyle.Render("/help - Show help"))
	sections = append(sections, infoStyle.Render("/agent - Delegate task"))
	sections = append(sections, infoStyle.Render("/status - Set status"))
	sections = append(sections, infoStyle.Render("/quit - Exit"))
	sections = append(sections, "")

	// Shortcuts
	sections = append(sections, lipgloss.NewStyle().Bold(true).Render("Shortcuts"))
	sections = append(sections, infoStyle.Render("Tab - Switch focus"))
	sections = append(sections, infoStyle.Render("↑/↓ - Scroll"))
	sections = append(sections, infoStyle.Render("@ - Agent mode"))
	sections = append(sections, infoStyle.Render("Ctrl+L - Clear"))

	content := lipgloss.JoinVertical(lipgloss.Left, sections...)
	return sidebarStyle.Render(content)
}

// renderInput renders the input area
func (m *ChatModel) renderInput() string {
	prompt := ">"
	if m.agentMode {
		prompt = "🤖"
	}

	content := fmt.Sprintf("%s %s", prompt, m.textInput.View())

	style := inputStyle
	if m.focus == FocusInput {
		style = style.BorderForeground(lipgloss.Color("#7D56F4"))
	} else {
		style = style.BorderForeground(lipgloss.Color("#3C3C3C"))
	}

	return style.Width(m.width - 4).Render(content)
}

// handleInput processes user input
func (m *ChatModel) handleInput(input string) tea.Cmd {
	return func() tea.Msg {
		// Handle commands
		if strings.HasPrefix(input, "/") {
			m.handleCommand(input)
			return nil
		}

		// Handle agent mode
		if m.agentMode || strings.HasPrefix(input, "@") {
			return m.handleAgentCommand(input)
		}

		// Send regular message
		return m.sendMessage(input)
	}
}

// handleCommand handles slash commands
func (m *ChatModel) handleCommand(cmd string) {
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return
	}

	switch parts[0] {
	case "/help":
		m.addSystemMessage("Commands: /help, /agent, /status <online|away|busy>, /quit")
	case "/quit":
		// Signal to quit
	case "/agent":
		m.agentMode = true
		m.addSystemMessage("Entering agent mode. Type your task request.")
	case "/status":
		if len(parts) > 1 && m.heartbeat != nil {
			m.heartbeat.SetStatus(parts[1])
			m.addSystemMessage(fmt.Sprintf("Status set to: %s", parts[1]))
		}
	default:
		m.addSystemMessage(fmt.Sprintf("Unknown command: %s", parts[0]))
	}
}

// handleAgentCommand handles agent delegation
func (m *ChatModel) handleAgentCommand(input string) tea.Cmd {
	// Remove @ prefix if present
	task := strings.TrimPrefix(input, "@")
	task = strings.TrimSpace(task)

	return func() tea.Msg {
		// TODO: Delegate to task engine
		m.addSystemMessage(fmt.Sprintf("🤖 Delegating task: %s", task))

		// For now, just echo back
		return SendMessageMsg{
			ID:      "agent-task-1",
			Content: fmt.Sprintf("[Agent Task] %s", task),
		}
	}
}

// sendMessage sends a message to the peer
func (m *ChatModel) sendMessage(content string) tea.Msg {
	ctx := context.Background()

	// Compress content
	compressed, err := compressText(content)
	if err != nil {
		m.addSystemMessage(fmt.Sprintf("Failed to compress: %v", err))
		return nil
	}

	// Build event
	ev := &nostr.Event{
		Kind:      AgentKind,
		Content:   compressed,
		Tags:      nostr.Tags{{"c", AgentTag}, {"z", CompressTag}, {"p", m.peerPubkey}},
		CreatedAt: nostr.Now(),
	}

	if err := m.keyer.SignEvent(ctx, ev); err != nil {
		m.addSystemMessage(fmt.Sprintf("Failed to sign: %v", err))
		return nil
	}

	// Publish to relays
	for _, url := range m.relays {
		relay, err := sys.Pool.EnsureRelay(url)
		if err != nil {
			continue
		}
		relay.Publish(ctx, *ev)
	}

	return SendMessageMsg{
		ID:      ev.ID,
		Content: content,
	}
}

// addMessage adds a message to the history
func (m *ChatModel) addMessage(msg ChatMessage) {
	m.messages = append(m.messages, msg)
}

// addMessageFromEvent adds a message from a Nostr event
func (m *ChatModel) addMessageFromEvent(event nostr.Event) {
	content := event.Content
	isCompressed := false

	// Check for compression
	for _, tag := range event.Tags {
		if len(tag) >= 2 && tag[0] == "z" && tag[1] == CompressTag {
			if decoded, err := decompressText(content); err == nil {
				content = decoded
				isCompressed = true
			}
			break
		}
	}

	msg := ChatMessage{
		ID:         event.ID,
		From:       event.PubKey,
		Content:    content,
		Timestamp:  event.CreatedAt.Time(),
		IsMe:       event.PubKey == m.myPubkey,
		Compressed: isCompressed,
	}

	m.addMessage(msg)
}

// addSystemMessage adds a system message
func (m *ChatModel) addSystemMessage(content string) {
	m.addMessage(ChatMessage{
		From:      "system",
		Content:   content,
		Timestamp: time.Now(),
		IsMe:      false,
	})
	m.updateViewport()
}

// updateViewport updates the viewport content
func (m *ChatModel) updateViewport() {
	var content strings.Builder

	for _, msg := range m.messages {
		timeStr := msg.Timestamp.Format("15:04:05")

		var line string
		if msg.From == "system" {
			line = timestampStyle.Render(fmt.Sprintf("[%s] ", timeStr)) +
				lipgloss.NewStyle().Foreground(lipgloss.Color("#FFA500")).Render(fmt.Sprintf("ℹ %s", msg.Content))
		} else if msg.IsMe {
			line = timestampStyle.Render(fmt.Sprintf("[%s] ", timeStr)) +
				myMessageStyle.Render(fmt.Sprintf("→ %s", msg.Content))
		} else {
			line = timestampStyle.Render(fmt.Sprintf("[%s] ", timeStr)) +
				peerMessageStyle.Render(fmt.Sprintf("← %s", msg.Content))
		}

		content.WriteString(line + "\n")
	}

	m.viewport.SetContent(content.String())
	m.viewport.GotoBottom()
}

// Messages for tea.Cmd
type NostrEventMsg struct {
	Event nostr.Event
}

type SendMessageMsg struct {
	ID      string
	Content string
}

// agentChatCmd is the CLI command for starting chat
var agentChatCmd = &cli.Command{
	Name:      "chat",
	Usage:     "start an interactive chat session with another agent",
	ArgsUsage: "<peer-npub-or-hex>",
	Flags: append(defaultKeyFlags,
		&cli.StringSliceFlag{
			Name:  "relay",
			Usage: "relay URLs to use",
			Value: []string{"wss://relay.damus.io"},
		},
	),
	Action: func(ctx context.Context, c *cli.Command) error {
		peerKey := c.Args().First()
		if peerKey == "" {
			return fmt.Errorf("peer public key is required")
		}

		// Resolve npub if needed
		if strings.HasPrefix(peerKey, "npub") {
			hexKey, err := decodeNpub(peerKey)
			if err != nil {
				return fmt.Errorf("invalid npub: %w", err)
			}
			peerKey = hexKey
		}

		// Get keyer
		kr, pk, err := gatherKeyerFromArguments(ctx, c)
		if err != nil {
			return err
		}

		relays := c.StringSlice("relay")
		if len(relays) == 0 {
			relays = defaultRelays
		}

		// Create and run chat model
		model := NewChatModel(peerKey, pk, relays[0], relays, kr)

		p := tea.NewProgram(model, tea.WithAltScreen())
		if _, err := p.Run(); err != nil {
			return fmt.Errorf("chat failed: %w", err)
		}

		return nil
	},
}

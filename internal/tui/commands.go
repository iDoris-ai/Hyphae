package tui

import (
	"context"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/urfave/cli/v3"
)

// ChatCmd launches the TUI chat interface
var ChatCmd = &cli.Command{
	Name:  "chat",
	Usage: "Start TUI chat with a contact",
	Description: `Launch an interactive terminal chat interface with a contact.
Example: agent-speaker chat --with bob`,
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:     "with",
			Aliases:  []string{"w"},
			Usage:    "Contact nickname to chat with",
			Required: true,
		},
	},
	Action: func(ctx context.Context, c *cli.Command) error {
		contactName := c.String("with")

		// Create chat model
		model, err := NewChatModel(contactName)
		if err != nil {
			return fmt.Errorf("failed to start chat: %w", err)
		}

		// Run the TUI
		p := tea.NewProgram(model, tea.WithAltScreen())
		if _, err := p.Run(); err != nil {
			return fmt.Errorf("error running chat: %w", err)
		}

		return nil
	},
}

// TUICmd provides TUI-related commands
var TUICmd = &cli.Command{
	Name:  "tui",
	Usage: "TUI-based interface",
	Description: `Interactive terminal user interface commands`,
	Commands: []*cli.Command{
		ChatCmd,
		ContactsCmd,
	},
}

// ContactsCmd shows contacts in TUI
var ContactsCmd = &cli.Command{
	Name:  "contacts",
	Usage: "Show contacts in TUI",
	Description: `Interactive contact list with TUI`,
	Action: func(ctx context.Context, c *cli.Command) error {
		model, err := NewContactsModel()
		if err != nil {
			return fmt.Errorf("failed to load contacts: %w", err)
		}

		p := tea.NewProgram(model, tea.WithAltScreen())
		if _, err := p.Run(); err != nil {
			return fmt.Errorf("error running contacts TUI: %w", err)
		}

		return nil
	},
}

package tui

import (
	"context"
	"fmt"
	"log"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/urfave/cli/v3"
)

// ChatCmd launches the TUI chat interface
var ChatCmd = &cli.Command{
	Name:  "chat",
	Usage: "Start TUI chat with a contact",
	Description: `Launch an interactive terminal chat interface with a contact.
Example: hyphae chat --with bob`,
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:     "with",
			Aliases:  []string{"w"},
			Usage:    "Contact nickname to chat with",
			Required: true,
		},
		&cli.StringSliceFlag{
			Name:    "relay",
			Aliases: []string{"r"},
			Usage:   "Relay URL(s) to publish to (repeatable)",
		},
	},
	Action: func(ctx context.Context, c *cli.Command) error {
		relays, err := common.ResolveRelays(c)
		if err != nil {
			return err
		}
		return runChat(c.String("with"), relays)
	},
}

// TUICmd provides TUI-related commands
var TUICmd = &cli.Command{
	Name:        "tui",
	Usage:       "TUI-based interface",
	Description: `Interactive terminal user interface commands`,
	Commands: []*cli.Command{
		ChatCmd,
		ContactsCmd,
	},
}

// ContactsCmd shows contacts in TUI and optionally launches a chat on selection
var ContactsCmd = &cli.Command{
	Name:        "contacts",
	Usage:       "Show contacts in TUI",
	Description: `Interactive contact list with TUI`,
	Flags: []cli.Flag{
		&cli.StringSliceFlag{
			Name:    "relay",
			Aliases: []string{"r"},
			Usage:   "Relay URL(s) for the chat launched on selection",
		},
	},
	Action: func(ctx context.Context, c *cli.Command) error {
		relays, err := common.ResolveRelays(c)
		if err != nil {
			return err
		}
		model, err := NewContactsModel()
		if err != nil {
			return fmt.Errorf("failed to load contacts: %w", err)
		}

		p := tea.NewProgram(model, tea.WithAltScreen())
		finalModel, err := p.Run()
		if err != nil {
			return fmt.Errorf("error running contacts TUI: %w", err)
		}

		if cm, ok := finalModel.(*ContactsModel); ok && cm.selected != "" {
			return runChat(cm.selected, relays)
		}
		return nil
	},
}

// runChat starts the chat TUI for a given contact and closes the DB when done.
// relays may be nil/empty — NewChatModel will substitute the default.
func runChat(contactName string, relays []string) error {
	model, err := NewChatModel(contactName, relays...)
	if err != nil {
		return fmt.Errorf("failed to start chat: %w", err)
	}
	defer func() {
		if err := model.Close(); err != nil {
			log.Printf("tui: chat DB close failed: %v", err)
		}
	}()

	p := tea.NewProgram(model, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("error running chat: %w", err)
	}
	return nil
}

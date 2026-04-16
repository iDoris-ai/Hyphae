package messaging

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/AuraAIHQ/agent-speaker/internal/common"
	"github.com/AuraAIHQ/agent-speaker/internal/identity"
	"github.com/urfave/cli/v3"
)

// HistoryCmd manages message history
var HistoryCmd = &cli.Command{
	Name:  "history",
	Usage: "View message history",
	Description: `View local message history stored in SQLite database`,
	Commands: []*cli.Command{
		{
			Name:  "conversation",
			Usage: "View conversation with a contact",
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:     "with",
					Aliases:  []string{"w"},
					Usage:    "Contact nickname",
					Required: true,
				},
				&cli.IntFlag{
					Name:    "limit",
					Aliases: []string{"l"},
					Usage:   "Number of messages",
					Value:   50,
				},
			},
			Action: func(ctx context.Context, c *cli.Command) error {
				// Ensure storage is initialized
				if err := InitStorage(); err != nil {
					return fmt.Errorf("failed to initialize storage: %w", err)
				}

				ks, err := identity.LoadKeyStore()
				if err != nil {
					return err
				}

				myIdentity, err := identity.GetIdentity(ks, "")
				if err != nil {
					return err
				}

				contact, err := identity.GetContact(ks, c.String("with"))
				if err != nil {
					return err
				}

				store, err := GetStore()
				if err != nil {
					return err
				}

				messages, err := store.GetConversation(myIdentity.Npub, contact.Npub, int(c.Int("limit")))
				if err != nil {
					return err
				}

				if len(messages) == 0 {
					fmt.Println("No messages found")
					return nil
				}

				fmt.Printf("📜 Conversation with %s (%d messages)\n\n", c.String("with"), len(messages))

				w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
				// Reverse order (oldest first)
				for i := len(messages) - 1; i >= 0; i-- {
					msg := messages[i]
					direction := "→"
					if msg.IsIncoming {
						direction = "←"
					}

					content := msg.Plaintext
					if content == "" {
						content = msg.Content
					}

					encrypted := ""
					if msg.IsEncrypted {
						encrypted = "🔒"
					}

					fmt.Fprintf(w, "%d %s\t%s\t%s\n",
						msg.CreatedAt,
						direction,
						encrypted,
						common.TruncateString(content, 40))
				}
				w.Flush()

				return nil
			},
		},
		{
			Name:  "stats",
			Usage: "Show message statistics",
			Action: func(ctx context.Context, c *cli.Command) error {
				// Ensure storage is initialized
				if err := InitStorage(); err != nil {
					return fmt.Errorf("failed to initialize storage: %w", err)
				}

				stats, err := GetStats()
				if err != nil {
					return err
				}

				fmt.Println("📊 Message Statistics")
				fmt.Println("=====================")
				fmt.Printf("Total messages: %d\n", stats["total"])
				fmt.Printf("Incoming:       %d\n", stats["incoming"])
				fmt.Printf("Outgoing:       %d\n", stats["outgoing"])
				fmt.Printf("Encrypted:      %d\n", stats["encrypted"])

				return nil
			},
		},
		{
			Name:  "search",
			Usage: "Search messages",
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:     "query",
					Aliases:  []string{"q"},
					Usage:    "Search query",
					Required: true,
				},
				&cli.IntFlag{
					Name:  "limit",
					Value: 20,
				},
			},
			Action: func(ctx context.Context, c *cli.Command) error {
				// Ensure storage is initialized
				if err := InitStorage(); err != nil {
					return fmt.Errorf("failed to initialize storage: %w", err)
				}

				ks, err := identity.LoadKeyStore()
				if err != nil {
					return err
				}

				myIdentity, err := identity.GetIdentity(ks, "")
				if err != nil {
					return err
				}

				store, err := GetStore()
				if err != nil {
					return err
				}

				query := c.String("query")
				messages, err := store.SearchMessages(myIdentity.Npub, query, int(c.Int("limit")))
				if err != nil {
					return err
				}

				if len(messages) == 0 {
					fmt.Println("No messages found")
					return nil
				}

				for _, msg := range messages {
					content := msg.Plaintext
					if content == "" {
						content = msg.Content
					}

					direction := "→"
					if msg.IsIncoming {
						direction = "←"
					}

					fmt.Printf("[%s] %s %d: %s\n",
						msg.SenderNpub[:20],
						direction,
						msg.CreatedAt,
						common.TruncateString(content, 50))
				}

				fmt.Printf("\nFound %d results\n", len(messages))
				return nil
			},
		},
		{
			Name:  "inbox",
			Usage: "Show inbox (received messages)",
			Flags: []cli.Flag{
				&cli.IntFlag{
					Name:    "limit",
					Aliases: []string{"l"},
					Usage:   "Number of messages",
					Value:   20,
				},
			},
			Action: func(ctx context.Context, c *cli.Command) error {
				// Ensure storage is initialized
				if err := InitStorage(); err != nil {
					return fmt.Errorf("failed to initialize storage: %w", err)
				}

				ks, err := identity.LoadKeyStore()
				if err != nil {
					return err
				}

				myIdentity, err := identity.GetIdentity(ks, "")
				if err != nil {
					return err
				}

				store, err := GetStore()
				if err != nil {
					return err
				}

				messages, err := store.GetInbox(myIdentity.Npub, int(c.Int("limit")))
				if err != nil {
					return err
				}

				if len(messages) == 0 {
					fmt.Println("📭 Inbox is empty")
					return nil
				}

				fmt.Printf("📬 Inbox (%d messages)\n\n", len(messages))

				for _, msg := range messages {
					content := msg.Plaintext
					if content == "" {
						content = msg.Content
					}

					encrypted := ""
					if msg.IsEncrypted {
						encrypted = "🔒"
					}

					// Try to get sender nickname
					senderName := msg.SenderNpub[:16] + "..."
					for _, contact := range identity.ListContacts(ks) {
						if contact.Npub == msg.SenderNpub {
							senderName = contact.Nickname
							break
						}
					}

					fmt.Printf("[%d] %s: %s %s\n",
						msg.CreatedAt,
						senderName,
						encrypted,
						common.TruncateString(content, 40))
				}

				return nil
			},
		},
	},
}

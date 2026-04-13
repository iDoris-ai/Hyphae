package messaging

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/urfave/cli/v3"
	"github.com/jason/agent-speaker/internal/common"
	"github.com/jason/agent-speaker/internal/identity"
)

// HistoryCmd manages message history
var HistoryCmd = &cli.Command{
	Name:  "history",
	Usage: "View message history",
	Description: `View local message history stored in ~/.agent-speaker/`,
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
				ks, err := identity.LoadKeyStore()
				if err != nil {
					return err
				}

				myIdentity, _ := identity.GetIdentity(ks, "")
				contact, _ := identity.GetContact(ks, c.String("with"))

				ms, _ := LoadMessageStore()
				messages := GetConversation(ms, myIdentity.Npub, contact.Npub, int(c.Int("limit")))

				if len(messages) == 0 {
					fmt.Println("No messages found")
					return nil
				}

				fmt.Printf("📜 Conversation with %s (%d messages)\n\n", c.String("with"), len(messages))

				w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
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
				ms, err := LoadMessageStore()
				if err != nil {
					return err
				}

				incoming := 0
				outgoing := 0
				encrypted := 0

				for _, msg := range ms.Messages {
					if msg.IsIncoming {
						incoming++
					} else {
						outgoing++
					}
					if msg.IsEncrypted {
						encrypted++
					}
				}

				fmt.Println("📊 Message Statistics")
				fmt.Println("=====================")
				fmt.Printf("Total messages: %d\n", len(ms.Messages))
				fmt.Printf("Incoming:       %d\n", incoming)
				fmt.Printf("Outgoing:       %d\n", outgoing)
				fmt.Printf("Encrypted:      %d\n", encrypted)

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
			},
			Action: func(ctx context.Context, c *cli.Command) error {
				ms, _ := LoadMessageStore()
				query := c.String("query")

				results := 0
				for _, msg := range ms.Messages {
					if contains(msg.Plaintext, query) || contains(msg.Content, query) {
						fmt.Printf("[%s] %d...: %s\n",
							msg.SenderNpub[:20],
							msg.CreatedAt,
							common.TruncateString(msg.Plaintext, 50))
						results++
					}
				}

				fmt.Printf("\nFound %d results\n", results)
				return nil
			},
		},
	},
}

func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 &&
		(s == substr || len(s) > len(substr) &&
			(s[:len(substr)] == substr || s[len(s)-len(substr):] == substr ||
				findInString(s, substr)))
}

func findInString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

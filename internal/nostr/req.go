package nostr

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"fiatjaf.com/nostr"
	"github.com/AuraAIHQ/agent-speaker/internal/common"
	"github.com/urfave/cli/v3"
)

var ReqCmd = &cli.Command{
	Name:    "req",
	Aliases: []string{"query"},
	Usage:   "Query events from relays",
	Description: `Query nostr relays for events matching filters.
Example: agent-speaker req --kinds 1 --authors <npub> --limit 10`,
	Flags: []cli.Flag{
		&cli.StringSliceFlag{
			Name:    "kinds",
			Aliases: []string{"k"},
			Usage:   "Event kinds to query",
		},
		&cli.StringSliceFlag{
			Name:    "authors",
			Aliases: []string{"a"},
			Usage:   "Filter by author public keys",
		},
		&cli.StringSliceFlag{
			Name:    "relay",
			Aliases: []string{"r"},
			Usage:   "Relay URLs to query",
			Value:   []string{"wss://relay.aastar.io"},
		},
		&cli.IntFlag{
			Name:    "limit",
			Aliases: []string{"l"},
			Usage:   "Maximum number of events",
			Value:   10,
		},
		&cli.BoolFlag{
			Name:    "json",
			Aliases: []string{"j"},
			Usage:   "Output as JSON",
		},
	},
	Action: func(ctx context.Context, c *cli.Command) error {
		// Build filter
		filter := nostr.Filter{
			Limit: int(c.Int("limit")),
		}

		// Parse kinds
		for _, k := range c.StringSlice("kinds") {
			var kind int
			fmt.Sscanf(k, "%d", &kind)
			filter.Kinds = append(filter.Kinds, nostr.Kind(kind))
		}

		// Parse authors
		for _, a := range c.StringSlice("authors") {
			pk, err := common.ParsePublicKey(a)
			if err == nil {
				filter.Authors = append(filter.Authors, pk)
			}
		}

		relays := c.StringSlice("relay")
		fmt.Printf("Querying %d relay(s)...\n", len(relays))

		allEvents := make([]nostr.Event, 0)
		for _, relayURL := range relays {
			relay, err := nostr.RelayConnect(ctx, relayURL, nostr.RelayOptions{})
			if err != nil {
				fmt.Printf("  ⚠️  %s: connection failed\n", relayURL)
				continue
			}
			defer relay.Close()

			sub, err := relay.Subscribe(ctx, filter, nostr.SubscriptionOptions{})
			if err != nil {
				continue
			}

			timeout := time.AfterFunc(5*time.Second, func() {
				sub.Unsub()
			})

			for evt := range sub.Events {
				allEvents = append(allEvents, evt)
			}
			timeout.Stop()
		}

		fmt.Printf("Found %d events\n\n", len(allEvents))

		outputJSON := c.Bool("json")
		for i, evt := range allEvents {
			if outputJSON {
				data, _ := json.MarshalIndent(evt, "", "  ")
				fmt.Println(string(data))
			} else {
				fmt.Printf("[%d] Kind %d by %s at %s\n",
					i+1, evt.Kind,
					common.EncodeNpub(evt.PubKey)[:20]+"...",
					evt.CreatedAt.Time().Format("2006-01-02 15:04"))
				fmt.Printf("    %s\n", common.TruncateString(evt.Content, 100))
				fmt.Println()
			}
		}

		return nil
	},
}

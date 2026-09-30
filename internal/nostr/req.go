package nostr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"fiatjaf.com/nostr"
	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/internal/relayquery"
	"github.com/urfave/cli/v3"
)

var ReqCmd = &cli.Command{
	Name:    "req",
	Aliases: []string{"query"},
	Usage:   "Query events from relays",
	Description: `Query nostr relays for events matching filters.
Example: hyphae req --kinds 1 --authors <npub> --limit 10`,
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

		relays, err := common.ResolveRelays(c)
		if err != nil {
			return err
		}
		fmt.Printf("Querying %d relay(s)...\n", len(relays))

		allEvents := make([]nostr.Event, 0)
		var relayErrors []error
		for _, relayURL := range relays {
			if err := ctx.Err(); err != nil {
				relayErrors = append(relayErrors, fmt.Errorf("query canceled: %w", err))
				break
			}
			page, err := relayquery.Fetch(ctx, relayURL, filter)
			allEvents = append(allEvents, page.Events...)
			if err != nil {
				relayErrors = append(relayErrors, err)
				if ctx.Err() != nil {
					break
				}
			}
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

		if len(relayErrors) > 0 {
			return fmt.Errorf("one or more relay queries failed: %w", errors.Join(relayErrors...))
		}
		return nil
	},
}

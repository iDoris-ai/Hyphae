package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"fiatjaf.com/nostr"
	"github.com/urfave/cli/v3"
)

var eventCmd = &cli.Command{
	Name:  "event",
	Usage: "Create and publish nostr events",
	Description: `Create and publish nostr events to relays.
Example: agent-speaker event --kind 1 --content "Hello world!"`,
	Flags: []cli.Flag{
		&cli.IntFlag{
			Name:    "kind",
			Aliases: []string{"k"},
			Usage:   "Event kind",
			Value:   1,
		},
		&cli.StringFlag{
			Name:    "content",
			Aliases: []string{"c"},
			Usage:   "Event content",
		},
		&cli.StringFlag{
			Name:     "sec",
			Aliases:  []string{"s"},
			Usage:    "Secret key (will prompt if not provided)",
		},
		&cli.StringSliceFlag{
			Name:    "tag",
			Aliases: []string{"t"},
			Usage:   "Tags (format: key=value)",
		},
		&cli.StringSliceFlag{
			Name:    "relay",
			Aliases: []string{"r"},
			Usage:   "Relay URLs to publish to",
			Value:   []string{"wss://relay.aastar.io"},
		},
		&cli.BoolFlag{
			Name:  "json",
			Usage: "Output event as JSON only (don't publish)",
		},
	},
	Action: func(ctx context.Context, c *cli.Command) error {
		// Get secret key
		secKeyStr := c.String("sec")
		if secKeyStr == "" {
			var err error
			secKeyStr, err = readSecretKey("")
			if err != nil {
				return err
			}
		}

		secKey, err := parseSecretKey(secKeyStr)
		if err != nil {
			return fmt.Errorf("invalid secret key: %w", err)
		}
		pubKey := secKey.Public()

		// Build tags
		tags := nostr.Tags{}
		for _, t := range c.StringSlice("tag") {
			parts := strings.SplitN(t, "=", 2)
			if len(parts) == 2 {
				tags = append(tags, nostr.Tag{parts[0], parts[1]})
			} else {
				tags = append(tags, nostr.Tag{parts[0]})
			}
		}

		// Create event
		event := &nostr.Event{
			CreatedAt: nostr.Timestamp(time.Now().Unix()),
			Kind:      nostr.Kind(c.Int("kind")),
			Tags:      tags,
			Content:   c.String("content"),
			PubKey:    pubKey,
		}

		event.Sign(secKey)

		// Output JSON if requested
		if c.Bool("json") {
			data, _ := json.MarshalIndent(event, "", "  ")
			fmt.Println(string(data))
			return nil
		}

		// Publish
		relays := c.StringSlice("relay")
		fmt.Printf("Publishing Kind %d event to %d relay(s)...\n", event.Kind, len(relays))

		results := publishToRelays(ctx, event, relays)
		success := 0
		for relay, err := range results {
			if err != nil {
				fmt.Printf("  ❌ %s: %v\n", relay, err)
			} else {
				fmt.Printf("  ✅ %s\n", relay)
				success++
			}
		}

		fmt.Printf("\nPublished to %d/%d relays\n", success, len(relays))
		fmt.Printf("Event ID: %s\n", event.ID)

		return nil
	},
}

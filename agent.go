package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"fiatjaf.com/nostr"
	"github.com/klauspost/compress/zstd"
	"github.com/urfave/cli/v3"
)

const (
	AgentKind    = 30078
	AgentVersion = "v1"
	CompressTag  = "zstd"
	AgentTag     = "agent"
)

// compressText compresses text using zstd + base64
func compressText(text string) (string, error) {
	encoder, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedFastest))
	if err != nil {
		return "", fmt.Errorf("failed to create zstd encoder: %w", err)
	}
	defer encoder.Close()

	compressed := encoder.EncodeAll([]byte(text), nil)
	encoded := base64.StdEncoding.EncodeToString(compressed)
	return encoded, nil
}

// decompressText decompresses text from base64 + zstd
func decompressText(encoded string) (string, error) {
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("failed to decode base64: %w", err)
	}

	decoder, err := zstd.NewReader(nil)
	if err != nil {
		return "", fmt.Errorf("failed to create zstd decoder: %w", err)
	}
	defer decoder.Close()

	decompressed, err := decoder.DecodeAll(decoded, nil)
	if err != nil {
		return "", fmt.Errorf("failed to decompress: %w", err)
	}

	return string(decompressed), nil
}

// promptSecretKey prompts for a secret key
func promptSecretKey() (string, error) {
	fmt.Print("Enter your nsec or hex secret key: ")
	var secKey string
	fmt.Scanln(&secKey)
	secKey = strings.TrimSpace(secKey)
	if secKey == "" {
		return "", fmt.Errorf("secret key is required")
	}
	return secKey, nil
}

// agentMsgCmd - Send a message to another agent (uses Kind 30078)
var agentMsgCmd = &cli.Command{
	Name:  "msg",
	Usage: "Send a message to another agent using Kind 30078",
	Description: `Send a compressed message to another agent.
Example: agent-speaker agent msg --to <npub> --content "Hello agent!"`,
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:     "to",
			Aliases:  []string{"t"},
			Usage:    "Recipient's public key (hex or npub)",
			Required: true,
		},
		&cli.StringFlag{
			Name:     "content",
			Aliases:  []string{"c"},
			Usage:    "Message content",
			Required: true,
		},
		&cli.StringFlag{
			Name:    "sec",
			Aliases: []string{"s"},
			Usage:   "Secret key (hex or nsec) - will prompt if not provided",
		},
		&cli.StringSliceFlag{
			Name:    "relay",
			Aliases: []string{"r"},
			Usage:   "Relay URLs to publish to",
			Value:   []string{"wss://relay.aastar.io"},
		},
	},
	Action: func(ctx context.Context, c *cli.Command) error {
		content := c.String("content")
		if content == "" {
			return fmt.Errorf("message content is required")
		}

		// Get or prompt for secret key
		secKeyStr := c.String("sec")
		if secKeyStr == "" {
			var err error
			secKeyStr, err = promptSecretKey()
			if err != nil {
				return err
			}
		}

		secKey, err := parseSecretKey(secKeyStr)
		if err != nil {
			return fmt.Errorf("invalid secret key: %w", err)
		}
		pubKey := secKey.Public()

		// Parse recipient
		toKey := c.String("to")
		toPub, err := parsePublicKey(toKey)
		if err != nil {
			return fmt.Errorf("invalid recipient key: %w", err)
		}

		// Compress content
		compressed, err := compressText(content)
		if err != nil {
			return fmt.Errorf("failed to compress content: %w", err)
		}

		// Create tags
		tags := nostr.Tags{
			{"p", pubKeyToHex(toPub)},
			{"c", AgentTag},
			{"z", CompressTag},
			{"v", AgentVersion},
		}

		// Create event
		event := &nostr.Event{
			CreatedAt: nostr.Now(),
			Kind:      AgentKind,
			Tags:      tags,
			Content:   compressed,
			PubKey:    pubKey,
		}
		event.Sign(secKey)

		// Publish to relays
		relays := c.StringSlice("relay")
		results := publishToRelays(ctx, event, relays)

		// Print results
		fmt.Printf("📤 Message sent (Kind %d)\n", AgentKind)
		fmt.Printf("   From: %s\n", encodeNpub(pubKey))
		fmt.Printf("   To:   %s\n", encodeNpub(toPub))
		fmt.Printf("   Size: %d → %d bytes (compressed)\n", len(content), len(compressed))

		successCount := 0
		for relay, err := range results {
			if err != nil {
				fmt.Printf("   ❌ %s: %v\n", relay, err)
			} else {
				fmt.Printf("   ✅ %s\n", relay)
				successCount++
			}
		}

		fmt.Printf("   Published to %d/%d relays\n", successCount, len(relays))
		fmt.Printf("   Event ID: %s\n", event.ID)

		return nil
	},
}

// agentQueryCmd - Query for agent messages
var agentQueryCmd = &cli.Command{
	Name:  "query",
	Usage: "Query for agent messages (Kind 30078)",
	Description: `Query relays for agent messages with optional filtering.
Example: agent-speaker agent query --kinds 30078 --authors <npub> --decompress`,
	Flags: []cli.Flag{
		&cli.StringSliceFlag{
			Name:    "kinds",
			Aliases: []string{"k"},
			Usage:   "Event kinds to query",
			Value:   []string{"30078"},
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
			Usage:   "Maximum number of events to fetch",
			Value:   20,
		},
		&cli.BoolFlag{
			Name:    "decompress",
			Aliases: []string{"d"},
			Usage:   "Decompress message content",
		},
		&cli.BoolFlag{
			Name:    "json",
			Aliases: []string{"j"},
			Usage:   "Output as JSON",
		},
	},
	Action: func(ctx context.Context, c *cli.Command) error {
		relays := c.StringSlice("relay")

		// Parse kinds
		kindStrs := c.StringSlice("kinds")
		var kinds []nostr.Kind
		for _, k := range kindStrs {
			var kind int
			fmt.Sscanf(k, "%d", &kind)
			kinds = append(kinds, nostr.Kind(kind))
		}
		if len(kinds) == 0 {
			kinds = []nostr.Kind{AgentKind}
		}

		// Parse authors
		var authors []nostr.PubKey
		for _, a := range c.StringSlice("authors") {
			pk, err := parsePublicKey(a)
			if err != nil {
				return fmt.Errorf("invalid author: %s", a)
			}
			authors = append(authors, pk)
		}

		// Create filter
		filter := nostr.Filter{
			Kinds: kinds,
			Limit: int(c.Int("limit")),
		}
		if len(authors) > 0 {
			filter.Authors = authors
		}

		// Query relays
		fmt.Printf("🔍 Querying %d relay(s) for Kind %d events...\n", len(relays), kinds[0])

		allEvents := make([]nostr.Event, 0)
		for _, relayURL := range relays {
			relay, err := nostr.RelayConnect(ctx, relayURL, nostr.RelayOptions{})
			if err != nil {
				fmt.Printf("   ⚠️  Failed to connect to %s: %v\n", relayURL, err)
				continue
			}
			defer relay.Close()

			sub, err := relay.Subscribe(ctx, filter, nostr.SubscriptionOptions{})
			if err != nil {
				fmt.Printf("   ⚠️  Failed to subscribe to %s: %v\n", relayURL, err)
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

		// Display results
		if len(allEvents) == 0 {
			fmt.Println("   No events found")
			return nil
		}

		fmt.Printf("   Found %d event(s)\n\n", len(allEvents))

		decompress := c.Bool("decompress")
		outputJSON := c.Bool("json")

		for i, evt := range allEvents {
			if outputJSON {
				data, _ := json.MarshalIndent(evt, "", "  ")
				fmt.Println(string(data))
			} else {
				fmt.Printf("[%d] Event %s\n", i+1, evt.ID)
				fmt.Printf("    Author: %s\n", encodeNpub(evt.PubKey))
				fmt.Printf("    Created: %s\n", evt.CreatedAt.Time().Format("2006-01-02 15:04:05"))
				fmt.Printf("    Kind: %d\n", evt.Kind)

				// Check if compressed
				isCompressed := false
				for _, tag := range evt.Tags {
					if len(tag) >= 2 && tag[0] == "z" && tag[1] == CompressTag {
						isCompressed = true
						break
					}
				}

				content := evt.Content
				if isCompressed && decompress {
					decompressed, err := decompressText(content)
					if err == nil {
						content = decompressed
						fmt.Printf("    Content: %s\n", content)
						fmt.Printf("    (decompressed from %d bytes)\n", len(evt.Content))
					} else {
						fmt.Printf("    Content: %s\n", content)
						fmt.Printf("    (decompression failed: %v)\n", err)
					}
				} else {
					fmt.Printf("    Content: %s\n", content)
					if isCompressed {
						fmt.Printf("    (use --decompress to decode)\n")
					}
				}
				fmt.Println()
			}
		}

		return nil
	},
}

// agentRelayCmd - Manage agent relays
var agentRelayCmd = &cli.Command{
	Name:  "relay",
	Usage: "Manage default relays for agent communication",
	Description: "Show or configure default relays for agent messages.",
	Flags: []cli.Flag{
		&cli.StringSliceFlag{
			Name:  "add",
			Usage: "Add a relay to the default list",
		},
		&cli.BoolFlag{
			Name:  "list",
			Usage: "List current default relays",
		},
	},
	Action: func(ctx context.Context, c *cli.Command) error {
		if c.Bool("list") || (!c.IsSet("add")) {
			fmt.Println("🌐 Default Agent Relays:")
			fmt.Println("   wss://relay.aastar.io (recommended)")
			fmt.Println("   wss://relay.nostr.band")
			fmt.Println("   wss://relay.damus.io")
			fmt.Println("\n💡 Use --relay flag to specify custom relays for each command")
		}
		return nil
	},
}

// agentTimelineCmd - Show personal timeline
var agentTimelineCmd = &cli.Command{
	Name:  "timeline",
	Usage: "Show your personal agent message timeline",
	Description: `Display your recent agent messages and mentions.
Example: agent-speaker agent timeline --limit 10`,
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:    "sec",
			Aliases: []string{"s"},
			Usage:   "Your secret key (will prompt if not provided)",
		},
		&cli.IntFlag{
			Name:    "limit",
			Aliases: []string{"l"},
			Usage:   "Number of events to show",
			Value:   10,
		},
		&cli.StringSliceFlag{
			Name:    "relay",
			Aliases: []string{"r"},
			Usage:   "Relay URLs to query",
			Value:   []string{"wss://relay.aastar.io"},
		},
		&cli.BoolFlag{
			Name:    "decompress",
			Aliases: []string{"d"},
			Usage:   "Decompress message content",
		},
	},
	Action: func(ctx context.Context, c *cli.Command) error {
		// Get secret key
		secKeyStr := c.String("sec")
		if secKeyStr == "" {
			var err error
			secKeyStr, err = promptSecretKey()
			if err != nil {
				return err
			}
		}

		secKey, err := parseSecretKey(secKeyStr)
		if err != nil {
			return fmt.Errorf("invalid secret key: %w", err)
		}
		pubKey := secKey.Public()

		fmt.Printf("📜 Timeline for %s\n\n", encodeNpub(pubKey))

		relays := c.StringSlice("relay")
		filter := nostr.Filter{
			Kinds: []nostr.Kind{AgentKind},
			Tags: nostr.TagMap{
				"p": []string{pubKeyToHex(pubKey)},
			},
			Limit: int(c.Int("limit")),
		}

		// Query relays
		allEvents := make([]nostr.Event, 0)
		for _, relayURL := range relays {
			relay, err := nostr.RelayConnect(ctx, relayURL, nostr.RelayOptions{})
			if err != nil {
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

		if len(allEvents) == 0 {
			fmt.Println("No messages in your timeline")
			return nil
		}

		decompress := c.Bool("decompress")

		for _, evt := range allEvents {
			// Extract sender
			sender := encodeNpub(evt.PubKey)

			content := evt.Content
			isCompressed := false
			for _, tag := range evt.Tags {
				if len(tag) >= 2 && tag[0] == "z" && tag[1] == CompressTag {
					isCompressed = true
					break
				}
			}

			if isCompressed && decompress {
				if decompressed, err := decompressText(content); err == nil {
					content = decompressed
				}
			}

			// Truncate long content
			displayContent := content
			if len(displayContent) > 80 {
				displayContent = displayContent[:77] + "..."
			}

			fmt.Printf("[%s] %s: %s\n",
				evt.CreatedAt.Time().Format("15:04"),
				strings.TrimPrefix(sender, "npub1")[:8]+"...",
				displayContent)
		}

		return nil
	},
}

// Main agent command
var agentCmd = &cli.Command{
	Name:  "agent",
	Usage: "Agent-to-agent communication commands",
	Description: `Commands for agent communication using Kind 30078 events.
These commands use compressed, tagged messages for AI agent coordination.`,
	Commands: []*cli.Command{
		agentMsgCmd,
		agentQueryCmd,
		agentRelayCmd,
		agentTimelineCmd,
	},
}

// publishToRelays publishes an event to multiple relays
func publishToRelays(ctx context.Context, event *nostr.Event, relays []string) map[string]error {
	results := make(map[string]error)

	for _, relayURL := range relays {
		relay, err := nostr.RelayConnect(ctx, relayURL, nostr.RelayOptions{})
		if err != nil {
			results[relayURL] = fmt.Errorf("connection failed: %w", err)
			continue
		}
		defer relay.Close()

		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err = relay.Publish(ctx, *event)
		cancel()

		if err != nil {
			results[relayURL] = fmt.Errorf("publish failed: %w", err)
		} else {
			results[relayURL] = nil
		}
	}

	return results
}

package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/nip19"
	"github.com/fatih/color"
	"github.com/klauspost/compress/zstd"
	"github.com/urfave/cli/v3"
)

const (
	AgentKind    = 30078
	AgentVersion = "v1"
	CompressTag  = "zstd"
	AgentTag     = "agent"
)

var agentCmd = &cli.Command{
	Name:  "agent",
	Usage: "agent-specific nostr communication tools",
	Description: `Commands for agent-to-agent communication using compressed data and optimized queries.
	
Examples:
  # Send a compressed message to another agent
  agent-speaker agent msg --to <pubkey> "Hello, this is a long message that will be compressed"

  # Query multiple relays in parallel
  agent-speaker agent query --kinds "1,30078" --authors <pubkey>

  # Start a mini relay for direct agent communication
  agent-speaker agent relay --port 7777`,
	Commands: []*cli.Command{
		agentMsgCmd,
		agentQueryCmd,
		agentRelayCmd,
		agentTimelineCmd,
	},
}

// decodeNpub decodes an npub to hex pubkey
func decodeNpub(npub string) (string, error) {
	prefix, data, err := nip19.Decode(npub)
	if err != nil {
		return "", err
	}
	if prefix != "npub" {
		return "", fmt.Errorf("expected npub, got %s", prefix)
	}
	pubkey, ok := data.(nostr.PubKey)
	if !ok {
		return "", fmt.Errorf("invalid pubkey data")
	}
	return pubkey.String(), nil
}

// agentMsgCmd sends compressed messages to other agents
var agentMsgCmd = &cli.Command{
	Name:      "msg",
	Usage:     "send a compressed message to another agent",
	ArgsUsage: "<message>",
	Flags: append(defaultKeyFlags,
		&cli.StringFlag{
			Name:     "to",
			Usage:    "recipient public key (hex or npub)",
			Required: true,
		},
		&cli.StringSliceFlag{
			Name:  "relay",
			Usage: "relay URLs to publish to",
			Value: []string{"wss://relay.damus.io", "wss://nos.lol"},
		},
		&cli.BoolFlag{
			Name:  "compress",
			Usage: "compress the message content",
			Value: true,
		},
	),
	Action: func(ctx context.Context, c *cli.Command) error {
		message := strings.Join(c.Args().Slice(), " ")
		if message == "" {
			return fmt.Errorf("message is required")
		}

		toPubkey := c.String("to")
		if strings.HasPrefix(toPubkey, "npub") {
			pubkey, err := decodeNpub(toPubkey)
			if err != nil {
				return fmt.Errorf("invalid npub: %w", err)
			}
			toPubkey = pubkey
		}

		var content string
		var tags nostr.Tags

		if c.Bool("compress") {
			compressed, err := compressText(message)
			if err != nil {
				return fmt.Errorf("failed to compress message: %w", err)
			}
			content = compressed
			tags = append(tags, nostr.Tag{"c", AgentTag})
			tags = append(tags, nostr.Tag{"z", CompressTag})
		} else {
			content = message
		}

		tags = append(tags, nostr.Tag{"p", toPubkey})

		ev := &nostr.Event{
			Kind:      AgentKind,
			Content:   content,
			Tags:      tags,
			CreatedAt: nostr.Now(),
		}

		// Get signer
		kr, _, err := gatherKeyerFromArguments(ctx, c)
		if err != nil {
			return fmt.Errorf("failed to get signer: %w", err)
		}

		// Sign the event
		if err := kr.SignEvent(ctx, ev); err != nil {
			return fmt.Errorf("failed to sign event: %w", err)
		}

		relays := c.StringSlice("relay")
		if len(relays) == 0 {
			relays = defaultRelays
		}

		log("Sending compressed message to %s...\n", color.CyanString(toPubkey[:16]+"..."))

		for _, url := range relays {
			relay, err := sys.Pool.EnsureRelay(url)
			if err != nil {
				log("Failed to connect to %s: %v\n", url, err)
				continue
			}
			if err := relay.Publish(ctx, *ev); err != nil {
				log("Failed to publish to %s: %v\n", url, err)
			} else {
				log("✓ Published to %s\n", color.GreenString(url))
			}
		}

		return nil
	},
}

// compressText compresses text using zstd and encodes as base64
func compressText(text string) (string, error) {
	encoder, err := zstd.NewWriter(nil)
	if err != nil {
		return "", fmt.Errorf("zstd encoder init: %w", err)
	}
	compressed := encoder.EncodeAll([]byte(text), nil)
	return base64.StdEncoding.EncodeToString(compressed), nil
}

// decompressText decompresses base64 zstd data
func decompressText(encoded string) (string, error) {
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("base64 decode: %w", err)
	}
	decoder, err := zstd.NewReader(nil)
	if err != nil {
		return "", fmt.Errorf("zstd decoder init: %w", err)
	}
	defer decoder.Close()
	out, err := decoder.DecodeAll(data, nil)
	if err != nil {
		return "", fmt.Errorf("zstd decompress: %w", err)
	}
	return string(out), nil
}

// agentQueryCmd queries multiple relays in parallel
var agentQueryCmd = &cli.Command{
	Name:  "query",
	Usage: "query events from multiple relays in parallel",
	Flags: []cli.Flag{
		&cli.StringSliceFlag{
			Name:  "kinds",
			Usage: "event kinds to query (comma-separated)",
			Value: []string{"1", "30078"},
		},
		&cli.StringSliceFlag{
			Name:  "authors",
			Usage: "author public keys",
		},
		&cli.StringSliceFlag{
			Name:  "relay",
			Usage: "relay URLs to query",
			Value: []string{"wss://relay.damus.io", "wss://nos.lol", "wss://relay.aastar.io"},
		},
		&cli.IntFlag{
			Name:  "limit",
			Usage: "maximum number of events to return",
			Value: 50,
		},
		&cli.BoolFlag{
			Name:  "decompress",
			Usage: "automatically decompress agent messages",
			Value: true,
		},
	},
	Action: func(ctx context.Context, c *cli.Command) error {
		kinds := c.StringSlice("kinds")
		authors := c.StringSlice("authors")
		relays := c.StringSlice("relay")
		limit := c.Int("limit")
		decompress := c.Bool("decompress")

		var kindKinds []nostr.Kind
		for _, k := range kinds {
			var ki int
			if _, err := fmt.Sscanf(k, "%d", &ki); err == nil {
				kindKinds = append(kindKinds, nostr.Kind(ki))
			}
		}

		filter := nostr.Filter{
			Kinds: kindKinds,
			Limit: int(limit),
		}

		if len(authors) > 0 {
			authorKeys := make([]nostr.PubKey, len(authors))
			for i, a := range authors {
				if strings.HasPrefix(a, "npub") {
					pkHex, err := decodeNpub(a)
					if err != nil {
						return fmt.Errorf("invalid npub %s: %w", a, err)
					}
					pk, err := parsePubKey(pkHex)
					if err != nil {
						return fmt.Errorf("invalid pubkey %s: %w", pkHex, err)
					}
					authorKeys[i] = pk
				} else {
					pk, err := parsePubKey(a)
					if err != nil {
						return fmt.Errorf("invalid pubkey %s: %w", a, err)
					}
					authorKeys[i] = pk
				}
			}
			filter.Authors = authorKeys
		}

		log("Querying %d relays for kinds %v...\n", len(relays), kinds)
		start := time.Now()

		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()

		// Use FetchMany to get events
		events := sys.Pool.FetchMany(ctx, relays, filter, nostr.SubscriptionOptions{})

		for ie := range events {
			outputEvent(&ie.Event, decompress)
		}

		log("Query completed in %s\n", time.Since(start))
		return nil
	},
}

// agentRelayCmd manages mini relay for direct communication
var agentRelayCmd = &cli.Command{
	Name:  "relay",
	Usage: "manage local mini relay for agent communication",
	Commands: []*cli.Command{
		{
			Name:   "start",
			Usage:  "start a local mini relay",
			Action: startMiniRelay,
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:  "host",
					Usage: "host to bind to",
					Value: "0.0.0.0",
				},
				&cli.IntFlag{
					Name:  "port",
					Usage: "port to listen on",
					Value: 7777,
				},
				&cli.StringFlag{
					Name:  "data-dir",
					Usage: "directory to store relay data",
					Value: "./agent-relay-data",
				},
			},
		},
		{
			Name: "status",
			Usage: "check local relay status",
			Action: func(ctx context.Context, c *cli.Command) error {
				fmt.Println("Relay status: not implemented yet")
				return nil
			},
		},
	},
}

// agentTimelineCmd shows agent-specific timeline
var agentTimelineCmd = &cli.Command{
	Name:    "timeline",
	Aliases: []string{"tl"},
	Usage:   "show agent-specific timeline",
	Flags: []cli.Flag{
		&cli.IntFlag{
			Name:  "limit",
			Usage: "number of events to show",
			Value: 20,
		},
		&cli.BoolFlag{
			Name:  "decompress",
			Usage: "automatically decompress messages",
			Value: true,
		},
	},
	Action: func(ctx context.Context, c *cli.Command) error {
		filter := nostr.Filter{
			Kinds: []nostr.Kind{AgentKind},
			Limit: int(c.Int("limit")),
		}

		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()

		events := sys.Pool.FetchMany(ctx, defaultRelays, filter, nostr.SubscriptionOptions{})

		for ie := range events {
			outputEvent(&ie.Event, c.Bool("decompress"))
		}

		return nil
	},
}

func outputEvent(event *nostr.Event, decompress bool) {
	content := event.Content

	// Check if this is a compressed agent message
	if decompress && event.Kind == AgentKind {
		isCompressed := false
		for _, tag := range event.Tags {
			if len(tag) >= 2 && tag[0] == "z" && tag[1] == CompressTag {
				isCompressed = true
				break
			}
		}

		if isCompressed {
			if decoded, err := decompressText(content); err == nil {
				content = decoded
			}
		}
	}

	stdout("%s %s %d %s\n",
		event.ID,
		event.PubKey,
		event.Kind,
		content,
	)
}

func startMiniRelay(ctx context.Context, c *cli.Command) error {
	host := c.String("host")
	port := c.Int("port")
	dataDir := c.String("data-dir")

	fmt.Printf("Starting mini relay on %s:%d...\n", host, port)
	fmt.Printf("Data directory: %s\n", dataDir)
	fmt.Println("\nNote: Full mini relay implementation requires strfry or similar.")
	fmt.Println("For now, use Docker:")
	fmt.Printf("  docker run -d -p %d:7777 hoytech/strfry:latest\n", port)

	return nil
}

var defaultRelays = []string{
	"wss://relay.damus.io",
	"wss://nos.lol",
	"wss://relay.aastar.io",
}

package main

import (
	"context"
	"encoding/base64"
	"fmt"
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

func compressText(text string) (string, error) {
	encoder, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedFastest))
	if err != nil {
		return "", err
	}
	defer encoder.Close()
	compressed := encoder.EncodeAll([]byte(text), nil)
	return base64.StdEncoding.EncodeToString(compressed), nil
}

func decompressText(encoded string) (string, error) {
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", err
	}
	decoder, err := zstd.NewReader(nil)
	if err != nil {
		return "", err
	}
	defer decoder.Close()
	decompressed, err := decoder.DecodeAll(decoded, nil)
	if err != nil {
		return "", err
	}
	return string(decompressed), nil
}

// agentMsgCmd - Send message using nicknames
var agentMsgCmd = &cli.Command{
	Name:  "msg",
	Usage: "Send a message to another agent",
	Description: `Send a message using nicknames instead of keys.
Example: agent-speaker agent msg --from alice --to bob --content "Hello!"`,
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:     "from",
			Aliases:  []string{"f"},
			Usage:    "Your nickname (identity)",
		},
		&cli.StringFlag{
			Name:     "to",
			Aliases:  []string{"t"},
			Usage:    "Recipient nickname or npub",
			Required: true,
		},
		&cli.StringFlag{
			Name:     "content",
			Aliases:  []string{"c"},
			Usage:    "Message content",
			Required: true,
		},
		&cli.StringSliceFlag{
			Name:    "relay",
			Aliases: []string{"r"},
			Usage:   "Relay URLs",
			Value:   []string{"wss://relay.aastar.io"},
		},
	},
	Action: func(ctx context.Context, c *cli.Command) error {
		content := c.String("content")
		if content == "" {
			return fmt.Errorf("message content is required")
		}

		ks, err := LoadKeyStore()
		if err != nil {
			return err
		}

		sender, err := ks.GetIdentity(c.String("from"))
		if err != nil {
			return fmt.Errorf("sender not found: %w", err)
		}

		recipientNpub, err := ks.ResolveRecipient(c.String("to"))
		if err != nil {
			return err
		}

		senderSK, _ := parseSecretKey(sender.Nsec)
		recipientPK, _ := parsePublicKey(recipientNpub)

		compressed, _ := compressText(content)
		tags := nostr.Tags{
			{"p", pubKeyToHex(recipientPK)},
			{"c", AgentTag},
			{"z", CompressTag},
			{"v", AgentVersion},
		}

		event := &nostr.Event{
			CreatedAt: nostr.Now(),
			Kind:      AgentKind,
			Tags:      tags,
			Content:   compressed,
			PubKey:    senderSK.Public(),
		}
		event.Sign(senderSK)

		relays := c.StringSlice("relay")
		results := publishToRelays(ctx, event, relays)

		success := 0
		for _, err := range results {
			if err == nil {
				success++
			}
		}

		fmt.Printf("📤 Message from '%s' to '%s'\n", sender.Nickname, c.String("to"))
		fmt.Printf("   Published to %d/%d relays\n", success, len(relays))
		return nil
	},
}

// agentInboxCmd - Show inbox
var agentInboxCmd = &cli.Command{
	Name:  "inbox",
	Usage: "Show your inbox",
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:     "as",
			Aliases:  []string{"a"},
			Usage:    "Your nickname",
		},
		&cli.StringSliceFlag{
			Name:    "relay",
			Aliases: []string{"r"},
			Value:   []string{"wss://relay.aastar.io"},
		},
		&cli.IntFlag{
			Name:    "limit",
			Value:   10,
		},
	},
	Action: func(ctx context.Context, c *cli.Command) error {
		ks, err := LoadKeyStore()
		if err != nil {
			return err
		}

		recipient, err := ks.GetIdentity(c.String("as"))
		if err != nil {
			return err
		}

		recipientPK, _ := parsePublicKey(recipient.Npub)
		filter := nostr.Filter{
			Kinds: []nostr.Kind{AgentKind},
			Tags:  nostr.TagMap{"p": []string{pubKeyToHex(recipientPK)}},
			Limit: int(c.Int("limit")),
		}

		relays := c.StringSlice("relay")
		fmt.Printf("📬 Inbox for '%s'\n\n", recipient.Nickname)

		allEvents := make([]nostr.Event, 0)
		for _, url := range relays {
			relay, err := nostr.RelayConnect(ctx, url, nostr.RelayOptions{})
			if err != nil {
				continue
			}
			defer relay.Close()
			sub, _ := relay.Subscribe(ctx, filter, nostr.SubscriptionOptions{})
			timeout := time.AfterFunc(5*time.Second, func() { sub.Unsub() })
			for evt := range sub.Events {
				allEvents = append(allEvents, evt)
			}
			timeout.Stop()
		}

		if len(allEvents) == 0 {
			fmt.Println("   Empty")
			return nil
		}

		for _, evt := range allEvents {
			senderNpub := encodeNpub(evt.PubKey)
			senderName := senderNpub[:16] + "..."
			for _, contact := range ks.ListContacts() {
				if contact.Npub == senderNpub {
					senderName = contact.Nickname
					break
				}
			}
			content, _ := decompressText(evt.Content)
			fmt.Printf("[%s] %s: %s\n", evt.CreatedAt.Time().Format("15:04"), senderName, truncateString(content, 50))
		}
		return nil
	},
}

// agentCmd - Main agent command
var agentCmd = &cli.Command{
	Name:  "agent",
	Usage: "Agent communication",
	Commands: []*cli.Command{
		agentMsgCmd,
		agentInboxCmd,
	},
}

// Keep backward compatibility
var agentCmdV2 = agentCmd

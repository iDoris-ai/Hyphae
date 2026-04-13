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
	EncryptTag   = "encrypted"
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
	Description: `Send a message using nicknames with optional E2E encryption.
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
		&cli.BoolFlag{
			Name:    "encrypt",
			Aliases: []string{"e"},
			Usage:   "Enable NIP-44 end-to-end encryption",
			Value:   true,
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

		// Encrypt if enabled
		messageContent := content
		isEncrypted := false
		if c.Bool("encrypt") {
			encrypted, err := EncryptMessage(content, senderSK, recipientPK)
			if err != nil {
				return fmt.Errorf("failed to encrypt: %w", err)
			}
			messageContent = encrypted
			isEncrypted = true
		}

		compressed, _ := compressText(messageContent)
		tags := nostr.Tags{
			{"p", pubKeyToHex(recipientPK)},
			{"c", AgentTag},
			{"z", CompressTag},
			{"v", AgentVersion},
		}
		// Use "encryption" tag to mark encrypted messages (not "e" which is reserved)
		if isEncrypted {
			tags = append(tags, nostr.Tag{"enc", "nip44"})
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
		
		// Publish with detailed error output
		success := 0
		for _, url := range relays {
			relay, err := nostr.RelayConnect(ctx, url, nostr.RelayOptions{})
			if err != nil {
				fmt.Printf("   ❌ %s: connect failed: %v\n", url, err)
				continue
			}
			
			pubCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err = relay.Publish(pubCtx, *event)
			cancel()
			relay.Close()
			
			if err != nil {
				fmt.Printf("   ❌ %s: publish failed: %v\n", url, err)
			} else {
				fmt.Printf("   ✅ %s\n", url)
				success++
			}
		}

		// Store in local history and outbox
		if success > 0 {
			StoreOutgoingMessage(event, recipientNpub, content, isEncrypted)
			// Remove from outbox if it was there
			outbox, _ := LoadOutbox()
			outbox.Remove(string(event.ID[:]))
		} else {
			// Add to outbox for retry
			outbox, _ := LoadOutbox()
			outbox.Add(event, recipientNpub, relays)
			fmt.Println("   📝 Added to outbox for retry")
		}

		encryptionStatus := "plaintext"
		if isEncrypted {
			encryptionStatus = "🔒 NIP-44 encrypted"
		}
		fmt.Printf("📤 Message from '%s' to '%s' (%s)\n", sender.Nickname, c.String("to"), encryptionStatus)
		fmt.Printf("   Published to %d/%d relays\n", success, len(relays))
		
		if success == 0 {
			fmt.Println("   ⚠️  Warning: Message not published to any relay")
		} else {
			fmt.Printf("   💾 Stored in local history\n")
		}
		
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
		&cli.BoolFlag{
			Name:    "decrypt",
			Aliases: []string{"d"},
			Usage:   "Auto-decrypt NIP-44 messages",
			Value:   true,
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
		recipientSK, _ := parseSecretKey(recipient.Nsec)
		
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
				fmt.Printf("   ⚠️  Failed to connect to %s: %v\n", url, err)
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

		autoDecrypt := c.Bool("decrypt")

		for _, evt := range allEvents {
			senderNpub := encodeNpub(evt.PubKey)
			senderName := senderNpub[:16] + "..."
			for _, contact := range ks.ListContacts() {
				if contact.Npub == senderNpub {
					senderName = contact.Nickname
					break
				}
			}

			// Check if encrypted
			isEncrypted := false
			for _, tag := range evt.Tags {
				if len(tag) >= 2 && tag[0] == "enc" && tag[1] == "nip44" {
					isEncrypted = true
					break
				}
			}

			content, _ := decompressText(evt.Content)
			
			// Decrypt if needed
			if isEncrypted && autoDecrypt {
				decrypted, err := DecryptMessage(content, recipientSK, evt.PubKey)
				if err == nil {
					content = decrypted
					content = "🔓 " + content
				} else {
					content = "🔒 [encrypted - cannot decrypt]"
				}
			} else if isEncrypted {
				content = "🔒 [encrypted message]"
			}

			// Store in local history
			StoreIncomingMessage(&evt, content, isEncrypted)

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

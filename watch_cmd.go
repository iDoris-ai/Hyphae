package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"fiatjaf.com/nostr"
	"github.com/urfave/cli/v3"
)

// watchCmd monitors for new messages
var watchCmd = &cli.Command{
	Name:  "watch",
	Usage: "Watch for new messages",
	Description: `Continuously monitor for new messages and send notifications.
Press Ctrl+C to stop.`,
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:     "as",
			Aliases:  []string{"a"},
			Usage:    "Your nickname to watch",
		},
		&cli.IntFlag{
			Name:    "interval",
			Aliases: []string{"i"},
			Usage:   "Check interval in seconds",
			Value:   30,
		},
		&cli.BoolFlag{
			Name:    "notify",
			Aliases: []string{"n"},
			Usage:   "Send desktop notifications",
			Value:   true,
		},
		&cli.BoolFlag{
			Name:    "sound",
			Aliases: []string{"s"},
			Usage:   "Play sound on new message",
			Value:   true,
		},
	},
	Action: func(ctx context.Context, c *cli.Command) error {
		ks, err := LoadKeyStore()
		if err != nil {
			return err
		}

		identity, err := ks.GetIdentity(c.String("as"))
		if err != nil {
			return err
		}

		recipientPK, _ := parsePublicKey(identity.Npub)
		interval := time.Duration(c.Int("interval")) * time.Second
		useNotify := c.Bool("notify")
		useSound := c.Bool("sound")

		// Track seen messages
		seenMessages := make(map[string]bool)
		
		// Load existing messages
		ms, _ := LoadMessageStore()
		for _, msg := range ms.Messages {
			if msg.RecipientNpub == identity.Npub {
				seenMessages[msg.ID] = true
			}
		}

		fmt.Printf("🔔 Watching for messages as '%s'\n", identity.Nickname)
		fmt.Printf("   Check interval: %v\n", interval)
		fmt.Printf("   Desktop notifications: %v\n", useNotify)
		fmt.Printf("   Sound: %v\n", useSound)
		fmt.Printf("   Press Ctrl+C to stop\n\n")

		// Setup signal handling
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

		// Create ticker
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		// Check immediately on start
		checkAndNotify(ctx, recipientPK, identity, ks, seenMessages, useNotify, useSound)

		for {
			select {
			case <-ticker.C:
				checkAndNotify(ctx, recipientPK, identity, ks, seenMessages, useNotify, useSound)
			case <-sigChan:
				fmt.Println("\n👋 Stopping watch...")
				return nil
			case <-ctx.Done():
				return nil
			}
		}
	},
}

func checkAndNotify(ctx context.Context, recipientPK nostr.PubKey, identity *Identity, ks *KeyStore, seenMessages map[string]bool, useNotify, useSound bool) {
	filter := nostr.Filter{
		Kinds: []nostr.Kind{AgentKind},
		Tags:  nostr.TagMap{"p": []string{pubKeyToHex(recipientPK)}},
		Limit: 10,
	}

	relays := []string{"wss://relay.aastar.io"}
	newCount := 0

	for _, url := range relays {
		relay, err := nostr.RelayConnect(ctx, url, nostr.RelayOptions{})
		if err != nil {
			continue
		}
		defer relay.Close()

		sub, _ := relay.Subscribe(ctx, filter, nostr.SubscriptionOptions{})
		timeout := time.AfterFunc(3*time.Second, func() { sub.Unsub() })

		for evt := range sub.Events {
			eventID := string(evt.ID[:])
			if seenMessages[eventID] {
				continue
			}
			
			// New message!
			seenMessages[eventID] = true
			newCount++

			// Get sender name
			senderNpub := encodeNpub(evt.PubKey)
			senderName := senderNpub[:16] + "..."
			for _, contact := range ks.ListContacts() {
				if contact.Npub == senderNpub {
					senderName = contact.Nickname
					break
				}
			}

			// Decrypt if needed
			content, _ := decompressText(evt.Content)
			isEncrypted := false
			for _, tag := range evt.Tags {
				if len(tag) >= 2 && tag[0] == "e" && tag[1] == EncryptTag {
					isEncrypted = true
					break
				}
			}
			
			if isEncrypted {
				recipientSK, _ := parseSecretKey(identity.Nsec)
				if decrypted, err := DecryptMessage(content, recipientSK, evt.PubKey); err == nil {
					content = decrypted
				}
			}

			// Store
			StoreIncomingMessage(&evt, content, isEncrypted)

			// Notify
			fmt.Printf("\n📨 New message from %s: %s\n", senderName, truncateString(content, 40))
			
			if useNotify {
				DesktopNotification("Agent Speaker - "+senderName, truncateString(content, 100))
			}
			if useSound {
				PlaySound()
			}
		}
		timeout.Stop()
	}

	if newCount == 0 {
		fmt.Printf("[%s] No new messages\r", time.Now().Format("15:04:05"))
	}
}

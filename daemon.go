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

// daemonCmd runs the background daemon
var daemonCmd = &cli.Command{
	Name:  "daemon",
	Usage: "Run background daemon",
	Description: `Background daemon that:
1. Retries failed outgoing messages from outbox
2. Watches for new incoming messages
3. Cleans up old entries

Run this in a separate terminal or as a system service.`,
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:     "identity",
			Aliases:  []string{"i"},
			Usage:    "Identity to run daemon for (default: use default identity)",
		},
		&cli.IntFlag{
			Name:    "retry-interval",
			Aliases: []string{"r"},
			Usage:   "Outbox retry interval (seconds)",
			Value:   60,
		},
		&cli.IntFlag{
			Name:    "watch-interval",
			Aliases: []string{"w"},
			Usage:   "Inbox watch interval (seconds)",
			Value:   30,
		},
		&cli.BoolFlag{
			Name:    "notify",
			Aliases: []string{"n"},
			Usage:   "Send desktop notifications for new messages",
			Value:   true,
		},
	},
	Action: func(ctx context.Context, c *cli.Command) error {
		ks, err := LoadKeyStore()
		if err != nil {
			return fmt.Errorf("failed to load keystore: %w", err)
		}

		identity, err := ks.GetIdentity(c.String("identity"))
		if err != nil {
			return err
		}

		retryInterval := time.Duration(c.Int("retry-interval")) * time.Second
		watchInterval := time.Duration(c.Int("watch-interval")) * time.Second
		useNotify := c.Bool("notify")

		fmt.Printf("🚀 Starting daemon for '%s'\n", identity.Nickname)
		fmt.Printf("   Outbox retry interval: %v\n", retryInterval)
		fmt.Printf("   Inbox watch interval: %v\n", watchInterval)
		fmt.Printf("   Notifications: %v\n", useNotify)
		fmt.Println("   Press Ctrl+C to stop\n")

		// Setup signal handling
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

		// Create tickers
		retryTicker := time.NewTicker(retryInterval)
		watchTicker := time.NewTicker(watchInterval)
		cleanupTicker := time.NewTicker(1 * time.Hour) // Cleanup every hour
		defer retryTicker.Stop()
		defer watchTicker.Stop()
		defer cleanupTicker.Stop()

		// Track seen messages for watch
		seenMessages := make(map[string]bool)
		loadSeenMessages(seenMessages, identity.Npub)

		// Run immediately
		processOutbox(identity)
		watchInbox(ctx, identity, ks, seenMessages, useNotify)

		for {
			select {
			case <-retryTicker.C:
				processOutbox(identity)
			case <-watchTicker.C:
				watchInbox(ctx, identity, ks, seenMessages, useNotify)
			case <-cleanupTicker.C:
				cleanupOutbox()
			case <-sigChan:
				fmt.Println("\n👋 Stopping daemon...")
				return nil
			case <-ctx.Done():
				return nil
			}
		}
	},
}

// processOutbox retries failed messages
func processOutbox(identity *Identity) {
	outbox, err := LoadOutbox()
	if err != nil {
		return
	}

	pending := outbox.GetPending()
	if len(pending) == 0 {
		return
	}

	fmt.Printf("[%s] 📤 Processing %d pending messages...\n", 
		time.Now().Format("15:04:05"), len(pending))

	successCount := 0
	failCount := 0

	for _, entry := range pending {
		// Check if it's time to retry (exponential backoff)
		if entry.LastAttempt > 0 {
			backoff := time.Duration(entry.RetryCount*entry.RetryCount) * time.Second
			if backoff > 5*time.Minute {
				backoff = 5 * time.Minute
			}
			if time.Now().Unix()-entry.LastAttempt < int64(backoff.Seconds()) {
				continue // Skip, not time yet
			}
		}

		// Try to publish
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		
		success := false
		for _, url := range entry.Relays {
			relay, err := nostr.RelayConnect(ctx, url, nostr.RelayOptions{})
			if err != nil {
				continue
			}
			
			err = relay.Publish(ctx, *entry.Event)
			relay.Close()
			
			if err == nil {
				success = true
				break
			}
		}
		cancel()

		if success {
			outbox.UpdateStatus(entry.ID, "sent")
			outbox.Remove(entry.ID) // Remove sent messages
			StoreOutgoingMessage(entry.Event, entry.RecipientNpub, entry.Event.Content, true)
			successCount++
			fmt.Printf("   ✅ Sent: %s...\n", entry.ID[:16])
		} else {
			outbox.IncrementRetry(entry.ID)
			if entry.RetryCount >= entry.MaxRetries-1 {
				outbox.UpdateStatus(entry.ID, "failed")
				fmt.Printf("   ❌ Failed (max retries): %s...\n", entry.ID[:16])
			}
			failCount++
		}
	}

	if successCount > 0 || failCount > 0 {
		fmt.Printf("   Result: %d sent, %d failed\n", successCount, failCount)
	}
}

// watchInbox monitors for new messages
func watchInbox(ctx context.Context, identity *Identity, ks *KeyStore, seenMessages map[string]bool, useNotify bool) {
	recipientPK, _ := parsePublicKey(identity.Npub)
	recipientSK, _ := parseSecretKey(identity.Nsec)

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

		sub, _ := relay.Subscribe(ctx, filter, nostr.SubscriptionOptions{})
		timeout := time.AfterFunc(3*time.Second, func() { sub.Unsub() })

		for evt := range sub.Events {
			eventID := string(evt.ID[:])
			if seenMessages[eventID] {
				continue
			}
			
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
				if len(tag) >= 2 && tag[0] == "enc" && tag[1] == "nip44" {
					isEncrypted = true
					break
				}
			}
			
			if isEncrypted {
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
				PlaySound()
			}
		}
		timeout.Stop()
		relay.Close()
	}

	if newCount == 0 {
		fmt.Printf("[%s] Watching... (no new messages)\r", time.Now().Format("15:04:05"))
	}
}

func cleanupOutbox() {
	outbox, err := LoadOutbox()
	if err != nil {
		return
	}
	// Remove entries older than 7 days
	outbox.Cleanup(7 * 24 * time.Hour)
}

func loadSeenMessages(seen map[string]bool, npub string) {
	ms, err := LoadMessageStore()
	if err != nil {
		return
	}
	for _, msg := range ms.Messages {
		if msg.RecipientNpub == npub {
			seen[msg.ID] = true
		}
	}
}

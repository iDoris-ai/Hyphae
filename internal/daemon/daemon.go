package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"fiatjaf.com/nostr"
	"github.com/AuraAIHQ/agent-speaker/internal/common"
	"github.com/AuraAIHQ/agent-speaker/internal/identity"
	"github.com/AuraAIHQ/agent-speaker/internal/messaging"
	"github.com/AuraAIHQ/agent-speaker/internal/notify"
	"github.com/AuraAIHQ/agent-speaker/pkg/crypto"
	"github.com/AuraAIHQ/agent-speaker/pkg/types"
	"github.com/urfave/cli/v3"
)

// DaemonCmd runs the background daemon
var DaemonCmd = &cli.Command{
	Name:  "daemon",
	Usage: "Run background daemon",
	Description: `Background daemon that:
1. Retries failed outgoing messages from outbox
2. Watches for new incoming messages
3. Cleans up old entries

Run this in a separate terminal or as a system service.`,
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:    "identity",
			Aliases: []string{"i"},
			Usage:   "Identity to run daemon for (default: use default identity)",
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
		&cli.BoolFlag{
			Name:    "auto-reply",
			Aliases: []string{"a"},
			Usage:   "Automatically reply to incoming messages",
			Value:   false,
		},
	},
	Action: func(ctx context.Context, c *cli.Command) error {
		ks, err := identity.LoadAndUnlockKeyStore()
		if err != nil {
			return fmt.Errorf("failed to load keystore: %w", err)
		}

		myIdentity, err := identity.GetIdentity(ks, c.String("identity"))
		if err != nil {
			return err
		}

		retryInterval := time.Duration(c.Int("retry-interval")) * time.Second
		watchInterval := time.Duration(c.Int("watch-interval")) * time.Second
		useNotify := c.Bool("notify")
		autoReply := c.Bool("auto-reply")

		fmt.Printf("🚀 Starting daemon for '%s'\n", myIdentity.Nickname)
		fmt.Printf("   Outbox retry interval: %v\n", retryInterval)
		fmt.Printf("   Inbox watch interval: %v\n", watchInterval)
		fmt.Printf("   Notifications: %v\n", useNotify)
		fmt.Printf("   Auto-reply: %v\n", autoReply)
		fmt.Println("   Press Ctrl+C to stop")

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
		loadSeenMessages(seenMessages, myIdentity.Npub)

		// Run immediately
		processOutbox(ctx, myIdentity)
		watchInbox(ctx, myIdentity, ks, seenMessages, useNotify, autoReply)

		for {
			select {
			case <-retryTicker.C:
				processOutbox(ctx, myIdentity)
			case <-watchTicker.C:
				watchInbox(ctx, myIdentity, ks, seenMessages, useNotify, autoReply)
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
func processOutbox(ctx context.Context, myIdentity *types.Identity) {
	outbox, err := messaging.LoadOutbox()
	if err != nil {
		fmt.Printf("[%s] ⚠️  Failed to load outbox: %v\n", time.Now().Format("15:04:05"), err)
		return
	}

	pending := messaging.GetPendingOutbox(outbox)
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

		// Parse event
		var event nostr.Event
		if err := json.Unmarshal([]byte(entry.EventJSON), &event); err != nil {
			fmt.Printf("   ⚠️  Failed to parse event %s...: %v\n", entry.ID[:16], err)
			continue
		}

		// Try to publish
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)

		success := false
		for _, url := range entry.Relays {
			relay, err := nostr.RelayConnect(ctx, url, nostr.RelayOptions{})
			if err != nil {
				continue
			}

			err = relay.Publish(ctx, event)
			relay.Close()

			if err == nil {
				success = true
				break
			}
		}
		cancel()

		if success {
			messaging.UpdateOutboxStatus(outbox, entry.ID, "sent")
			messaging.RemoveFromOutbox(outbox, entry.ID) // Remove sent messages
			messaging.StoreOutgoingMessage(&event, entry.RecipientNpub, event.Content, true)
			successCount++
			fmt.Printf("   ✅ Sent: %s...\n", entry.ID[:16])
		} else {
			messaging.IncrementOutboxRetry(outbox, entry.ID)
			if entry.RetryCount >= entry.MaxRetries-1 {
				messaging.UpdateOutboxStatus(outbox, entry.ID, "failed")
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
func watchInbox(ctx context.Context, myIdentity *types.Identity, ks *types.KeyStore, seenMessages map[string]bool, useNotify bool, autoReply bool) {
	recipientPK, err := identity.GetPublicKey(ks, myIdentity.Nickname)
	if err != nil {
		fmt.Printf("[%s] ⚠️  Failed to get public key: %v\n", time.Now().Format("15:04:05"), err)
		return
	}
	recipientSK, err := identity.GetSecretKey(ks, myIdentity.Nickname)
	if err != nil {
		fmt.Printf("[%s] ⚠️  Failed to get secret key: %v\n", time.Now().Format("15:04:05"), err)
		return
	}

	filter := nostr.Filter{
		Kinds: []nostr.Kind{messaging.AgentKind},
		Tags:  nostr.TagMap{"p": []string{common.PubKeyToHex(recipientPK)}},
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
			senderNpub := common.EncodeNpub(evt.PubKey)
			senderName := senderNpub[:16] + "..."
			for _, contact := range identity.ListContacts(ks) {
				if contact.Npub == senderNpub {
					senderName = contact.Nickname
					break
				}
			}

			// Decrypt if needed
			content, _ := messaging.DecompressText(evt.Content)
			isEncrypted := false
			for _, tag := range evt.Tags {
				if len(tag) >= 2 && tag[0] == "enc" && tag[1] == "nip44" {
					isEncrypted = true
					break
				}
			}

			if isEncrypted {
				if decrypted, err := crypto.DecryptMessage(content, recipientSK, evt.PubKey); err == nil {
					content = decrypted
				}
			}

			// Store
			messaging.StoreIncomingMessage(&evt, content, isEncrypted)

			// Notify
			fmt.Printf("\n📨 New message from %s: %s\n", senderName, common.TruncateString(content, 40))

			if useNotify {
				notify.DesktopNotification("Agent Speaker - "+senderName, common.TruncateString(content, 100))
				notify.PlaySound()
			}

			// Auto-reply
			if autoReply && !isAutoReplyMessage(content) {
				go sendAutoReply(ctx, myIdentity, ks, senderNpub, content)
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
	outbox, err := messaging.LoadOutbox()
	if err != nil {
		return
	}
	// Remove entries older than 7 days
	messaging.CleanupOutbox(outbox, 7*24*time.Hour)
}

func loadSeenMessages(seen map[string]bool, npub string) {
	ms, err := messaging.LoadMessageStore()
	if err != nil {
		return
	}
	for _, msg := range ms.Messages {
		if msg.RecipientNpub == npub {
			seen[msg.ID] = true
		}
	}
}

func isAutoReplyMessage(content string) bool {
	return len(content) > 0 && (content[:1] == "[" && len(content) > 13 && content[:13] == "[auto-reply] ")
}

func sendAutoReply(ctx context.Context, myIdentity *types.Identity, ks *types.KeyStore, toNpub string, originalContent string) {
	mySK, err := identity.GetSecretKey(ks, myIdentity.Nickname)
	if err != nil {
		return
	}

	toPK, err := common.ParsePublicKey(toNpub)
	if err != nil {
		return
	}

	replyText := fmt.Sprintf("[auto-reply] %s received your message: %s", myIdentity.Nickname, common.TruncateString(originalContent, 30))

	var messageContent string
	encrypted, err := crypto.EncryptMessage(replyText, mySK, toPK)
	if err == nil {
		messageContent = encrypted
	} else {
		messageContent = replyText
	}

	compressed, _ := messaging.CompressText(messageContent)
	tags := nostr.Tags{
		{"p", common.PubKeyToHex(toPK)},
		{"c", messaging.AgentTag},
		{"z", messaging.CompressTag},
		{"v", messaging.AgentVersion},
	}
	if err == nil {
		tags = append(tags, nostr.Tag{"enc", "nip44"})
	}

	event := &nostr.Event{
		CreatedAt: nostr.Now(),
		Kind:      messaging.AgentKind,
		Tags:      tags,
		Content:   compressed,
		PubKey:    mySK.Public(),
	}
	event.Sign(mySK)

	relays := []string{"wss://relay.aastar.io"}
	success := false
	for _, url := range relays {
		relay, err := nostr.RelayConnect(ctx, url, nostr.RelayOptions{})
		if err != nil {
			continue
		}
		pubCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err = relay.Publish(pubCtx, *event)
		cancel()
		relay.Close()
		if err == nil {
			success = true
			break
		}
	}

	messaging.StoreOutgoingMessage(event, toNpub, replyText, success)
	fmt.Printf("🤖 Auto-replied to %s\n", toNpub[:20]+"...")
}

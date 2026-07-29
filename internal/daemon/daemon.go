package daemon

import (
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"fiatjaf.com/nostr"
	"github.com/AuraAIHQ/agent-speaker/internal/audit"
	"github.com/AuraAIHQ/agent-speaker/internal/common"
	"github.com/AuraAIHQ/agent-speaker/internal/identity"
	"github.com/AuraAIHQ/agent-speaker/internal/messaging"
	"github.com/AuraAIHQ/agent-speaker/internal/notify"
	"github.com/AuraAIHQ/agent-speaker/pkg/crypto"
	"github.com/AuraAIHQ/agent-speaker/pkg/types"
	"github.com/urfave/cli/v3"
)

const (
	defaultRelay     = "wss://relay.aastar.io"
	maxSeenMessages  = 10000
	relayDialTimeout = 5 * time.Second
	subscribeWindow  = 3 * time.Second
)

// seenSet is a bounded set of recently-seen event IDs with FIFO eviction.
// When the underlying map exceeds maxSeenMessages, the oldest 10% are evicted.
// This bounds memory while keeping recent dedup cheap.
type seenSet struct {
	seen  map[string]bool
	order []string
}

func newSeenSet() *seenSet {
	return &seenSet{
		seen:  make(map[string]bool, maxSeenMessages),
		order: make([]string, 0, maxSeenMessages),
	}
}

func (s *seenSet) Has(id string) bool {
	return s.seen[id]
}

func (s *seenSet) Add(id string) {
	if s.seen[id] {
		return
	}
	s.seen[id] = true
	s.order = append(s.order, id)
	if len(s.order) > maxSeenMessages {
		evictN := maxSeenMessages / 10
		for i := 0; i < evictN; i++ {
			delete(s.seen, s.order[i])
		}
		s.order = s.order[evictN:]
	}
}

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
		&cli.StringSliceFlag{
			Name:    "relay",
			Aliases: []string{"r"},
			Usage:   "Relay URL(s) to watch and publish auto-replies through (repeatable). Default: " + defaultRelay,
		},
		&cli.IntFlag{
			Name:    "retry-interval",
			Aliases: []string{"R"},
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

		relays := c.StringSlice("relay")
		if len(relays) == 0 {
			relays = []string{defaultRelay}
		}
		retryInterval := time.Duration(c.Int("retry-interval")) * time.Second
		watchInterval := time.Duration(c.Int("watch-interval")) * time.Second
		useNotify := c.Bool("notify")
		autoReply := c.Bool("auto-reply")

		fmt.Printf("🚀 Starting daemon for '%s'\n", myIdentity.Nickname)
		fmt.Printf("   Relays: %v\n", relays)
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

		// Track seen messages for this watch session. Bounded size to avoid
		// unbounded growth on long-running daemons. Cross-restart dedup is
		// handled by `since` below (filter only fetches events newer than
		// daemon start) plus storage.MessageStore's INSERT OR REPLACE guard.
		seen := newSeenSet()
		preloadRecentSeen(seen, myIdentity.Npub)

		// Only fetch events strictly newer than daemon start, so a restart
		// does not re-pull every historical event the relay still holds.
		since := nostr.Now()

		// Run immediately
		processOutbox(ctx, myIdentity, relays)
		watchInbox(ctx, myIdentity, ks, seen, since, relays, useNotify, autoReply)

		for {
			select {
			case <-retryTicker.C:
				processOutbox(ctx, myIdentity, relays)
			case <-watchTicker.C:
				watchInbox(ctx, myIdentity, ks, seen, since, relays, useNotify, autoReply)
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

// processOutbox retries failed messages. Each relay attempt gets its own
// timeout so a slow relay does not starve the rest.
//
// Note on persistence: messaging.UpdateOutboxStatus, IncrementOutboxRetry,
// and RemoveFromOutbox all call SaveOutbox internally, so a daemon crash
// after a successful publish does not double-send.
func processOutbox(ctx context.Context, myIdentity *types.Identity, relays []string) {
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

		result, err := messaging.AttemptSend(ctx, outbox, entry, relays, relayDialTimeout)
		if !result.Attempted {
			// Never got as far as dialing a relay (e.g. unparseable
			// EventJSON) -- skip without counting as a send failure, same
			// as the original inline "continue" on a parse error.
			fmt.Printf("   ⚠️  %s...: %v\n", safePrefix(entry.ID, 16), err)
			continue
		}
		if err != nil {
			// A bookkeeping error (status update/remove/history-store)
			// alongside an already-known Sent/MarkedFailed outcome --
			// surfaced, but doesn't change how this attempt is counted.
			fmt.Printf("   ⚠️  %s...: %v\n", safePrefix(entry.ID, 16), err)
		}

		if result.Sent {
			successCount++
			fmt.Printf("   ✅ Sent: %s...\n", safePrefix(entry.ID, 16))
		} else {
			if result.MarkedFailed {
				fmt.Printf("   ❌ Failed (max retries): %s...\n", safePrefix(entry.ID, 16))
			}
			failCount++
		}
	}

	if successCount > 0 || failCount > 0 {
		fmt.Printf("   Result: %d sent, %d failed\n", successCount, failCount)
	}
}

// watchInbox monitors for new messages.
func watchInbox(
	ctx context.Context,
	myIdentity *types.Identity,
	ks *types.KeyStore,
	seen *seenSet,
	since nostr.Timestamp,
	relays []string,
	useNotify bool,
	autoReply bool,
) {
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
		Since: since,
	}

	newCount := 0

	for _, url := range relays {
		newCount += watchOneRelay(ctx, url, filter, ks, recipientSK, seen, useNotify, autoReply, myIdentity, relays)
	}

	if newCount == 0 {
		fmt.Printf("[%s] Watching... (no new messages)\r", time.Now().Format("15:04:05"))
	}
}

// watchOneRelay polls a single relay and returns the count of newly-processed
// events. Subscribe errors are reported and the relay is skipped (returns 0)
// instead of panicking on a nil sub.
func watchOneRelay(
	ctx context.Context,
	url string,
	filter nostr.Filter,
	ks *types.KeyStore,
	recipientSK nostr.SecretKey,
	seen *seenSet,
	useNotify bool,
	autoReply bool,
	myIdentity *types.Identity,
	relays []string,
) int {
	relayCtx, cancel := context.WithTimeout(ctx, relayDialTimeout)
	defer cancel()

	relay, err := nostr.RelayConnect(relayCtx, url, nostr.RelayOptions{})
	if err != nil {
		return 0
	}
	defer relay.Close()

	sub, err := relay.Subscribe(ctx, filter, nostr.SubscriptionOptions{})
	if err != nil {
		fmt.Printf("[%s] ⚠️  Subscribe %s failed: %v\n", time.Now().Format("15:04:05"), url, err)
		return 0
	}
	timeout := time.AfterFunc(subscribeWindow, func() { sub.Unsub() })
	defer timeout.Stop()

	newCount := 0
	for evt := range sub.Events {
		// Hex-encoded to match messaging.RecentIncomingEventIDs, which reads
		// the same encoding back out of SQLite's "id" column (see
		// preloadRecentSeen below) -- a mismatched encoding here would make
		// every preloaded ID a no-op, silently defeating restart dedup.
		eventID := hex.EncodeToString(evt.ID[:])
		if seen.Has(eventID) {
			continue
		}
		seen.Add(eventID)
		newCount++

		// Resolve sender display name
		senderNpub := common.EncodeNpub(evt.PubKey)
		senderName := safePrefix(senderNpub, 16) + "..."
		for _, contact := range identity.ListContacts(ks) {
			if contact.Npub == senderNpub {
				senderName = contact.Nickname
				break
			}
		}

		// Decompress, then decrypt if needed. Track decrypt success so we
		// can refuse to auto-reply against unrecognised ciphertext (which
		// would otherwise bypass the [auto-reply] guard and storm).
		content, _ := messaging.DecompressText(evt.Content)
		isEncrypted := false
		for _, tag := range evt.Tags {
			if len(tag) >= 2 && tag[0] == "enc" && tag[1] == "nip44" {
				isEncrypted = true
				break
			}
		}

		decryptedOK := !isEncrypted
		if isEncrypted {
			if decrypted, derr := crypto.DecryptMessage(content, recipientSK, evt.PubKey); derr == nil {
				content = decrypted
				decryptedOK = true
			}
		}

		if err := messaging.StoreIncomingMessage(&evt, content, isEncrypted); err != nil {
			fmt.Printf("   ⚠️  Store incoming message: %v\n", err)
		}

		fmt.Printf("\n📨 New message from %s: %s\n", senderName, common.TruncateString(content, 40))

		if useNotify {
			notify.DesktopNotification("Agent Speaker - "+senderName, common.TruncateString(content, 100))
			notify.PlaySound()
		}

		if shouldAutoReply(autoReply, decryptedOK, content) {
			go sendAutoReply(ctx, myIdentity, ks, senderNpub, content, relays)
		}
	}
	return newCount
}

func cleanupOutbox() {
	outbox, err := messaging.LoadOutbox()
	if err != nil {
		return
	}
	// Remove entries older than 7 days
	if err := messaging.CleanupOutbox(outbox, 7*24*time.Hour); err != nil {
		fmt.Printf("   ⚠️  Cleanup outbox: %v\n", err)
	}
}

// preloadRecentSeen primes the in-memory dedup set with recent event IDs
// already stored in SQLite. Without this, a daemon restart would re-process
// the most recent N events the relay still holds (Limit:10 per watch tick),
// even though we have records of them.
//
// Note: the previous implementation used messaging.LoadMessageStore which is
// a compatibility shim that returns an empty slice — so it was a no-op. We
// now query SQLite directly via messaging.RecentIncomingEventIDs.
func preloadRecentSeen(seen *seenSet, npub string) {
	ids, err := messaging.RecentIncomingEventIDs(npub, maxSeenMessages)
	if err != nil {
		// Non-fatal — worst case we re-notify recent messages once.
		fmt.Printf("[%s] ⚠️  preload seen: %v\n", time.Now().Format("15:04:05"), err)
		return
	}
	for _, id := range ids {
		seen.Add(id)
	}
}

// isAutoReplyMessage returns true for our own auto-reply convention.
//
// Uses strings.HasPrefix so we cannot match opaque ciphertext or random
// base64 that happens to start with "[". The trailing space is intentional:
// a user message of exactly "[auto-reply]" (no space) is NOT one of ours.
func isAutoReplyMessage(content string) bool {
	return strings.HasPrefix(content, "[auto-reply] ")
}

// shouldAutoReply is the anti-storm gate from watchOneRelay's per-event
// loop, extracted so it can be unit tested without a live relay connection.
// All three conditions matter: the daemon must have --auto-reply enabled,
// the incoming message must have actually been understood (an
// enc=nip44-tagged message that failed to decrypt is NOT eligible --
// replying to opaque ciphertext would itself fail the recipient's prefix
// check and could storm), and the message itself must not already be one of
// our own auto-replies.
func shouldAutoReply(autoReplyEnabled, decryptedOK bool, content string) bool {
	return autoReplyEnabled && decryptedOK && !isAutoReplyMessage(content)
}

// buildAutoReplyText composes the auto-reply body sendAutoReply publishes.
// The "[auto-reply] " prefix is exactly what isAutoReplyMessage checks for
// on the receiving side -- this is the other half of the anti-storm
// contract, so the two must stay in sync (see the round-trip test).
func buildAutoReplyText(nickname, originalContent string) string {
	return fmt.Sprintf("[auto-reply] %s received your message: %s", nickname, common.TruncateString(originalContent, 30))
}

// safePrefix returns s[:n] when len(s) >= n, otherwise s. Avoids the panic
// the previous code would hit if an event ID came back shorter than expected.
func safePrefix(s string, n int) string {
	if len(s) < n {
		return s
	}
	return s[:n]
}

func sendAutoReply(ctx context.Context, myIdentity *types.Identity, ks *types.KeyStore, toNpub string, originalContent string, relays []string) {
	mySK, err := identity.GetSecretKey(ks, myIdentity.Nickname)
	if err != nil {
		return
	}

	toPK, err := common.ParsePublicKey(toNpub)
	if err != nil {
		return
	}

	replyText := buildAutoReplyText(myIdentity.Nickname, originalContent)

	var messageContent string
	encrypted, encErr := crypto.EncryptMessage(replyText, mySK, toPK)
	if encErr == nil {
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
	if encErr == nil {
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

	if len(relays) == 0 {
		relays = []string{defaultRelay}
	}
	success := false
	for _, url := range relays {
		relayCtx, cancel := context.WithTimeout(ctx, relayDialTimeout)
		relay, err := nostr.RelayConnect(relayCtx, url, nostr.RelayOptions{})
		if err != nil {
			cancel()
			continue
		}
		err = relay.Publish(relayCtx, *event)
		relay.Close()
		cancel()
		if err == nil {
			success = true
			break
		}
	}

	if err := messaging.StoreOutgoingMessage(event, toNpub, replyText, success); err != nil {
		fmt.Printf("   ⚠️  Store auto-reply: %v\n", err)
	}
	if err := audit.LogAction(myIdentity.Nickname, audit.ActionAutoReplySent, map[string]any{
		"to": toNpub, "published": success, "event_id": event.ID.Hex(),
	}); err != nil {
		fmt.Printf("   ⚠️  audit log failed: %v\n", err)
	}
	fmt.Printf("🤖 Auto-replied to %s\n", safePrefix(toNpub, 20)+"...")
}

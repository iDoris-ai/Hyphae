package daemon

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"fiatjaf.com/nostr"
	"github.com/iDoris-ai/hyphae/internal/audit"
	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/internal/identity"
	"github.com/iDoris-ai/hyphae/internal/messaging"
	"github.com/iDoris-ai/hyphae/internal/notify"
	"github.com/iDoris-ai/hyphae/internal/relayconfig"
	"github.com/iDoris-ai/hyphae/internal/relayquery"
	"github.com/iDoris-ai/hyphae/pkg/crypto"
	"github.com/iDoris-ai/hyphae/pkg/types"
	"github.com/urfave/cli/v3"
)

const (
	maxSeenMessages  = 10000
	relayDialTimeout = 5 * time.Second
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
			Usage:   "Relay URL(s) to watch and publish auto-replies through (repeatable). Default: " + relayconfig.DefaultRelay,
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
		&cli.BoolFlag{
			Name:  "password-stdin",
			Usage: "Read an encrypted keystore password from stdin",
		},
	},
	Action: func(ctx context.Context, c *cli.Command) error {
		retryInterval, watchInterval, err := validateDaemonIntervals(int64(c.Int("retry-interval")), int64(c.Int("watch-interval")))
		if err != nil {
			return common.NewExitError(common.ErrCodeUser, err)
		}

		ks, err := identity.LoadKeyStoreForCommand(identity.KeyStoreCommandOptions{
			JSONMode: common.JSONMode(c), RequireSecret: true, PasswordStdin: c.Bool("password-stdin"), Stdin: os.Stdin,
		})
		if err != nil {
			return err
		}

		myIdentity, err := identity.GetIdentity(ks, c.String("identity"))
		if err != nil {
			return err
		}

		relays, err := common.ResolveRelays(c)
		if err != nil {
			return err
		}
		if len(relays) == 0 {
			relays = []string{relayconfig.DefaultRelay}
		}
		daemonCtx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		useNotify := c.Bool("notify")
		autoReply := c.Bool("auto-reply")

		fmt.Printf("🚀 Starting daemon for '%s'\n", myIdentity.Nickname)
		fmt.Printf("   Relays: %v\n", relays)
		fmt.Printf("   Outbox retry interval: %v\n", retryInterval)
		fmt.Printf("   Inbox watch interval: %v\n", watchInterval)
		fmt.Printf("   Notifications: %v\n", useNotify)
		fmt.Printf("   Auto-reply: %v\n", autoReply)
		fmt.Println("   Press Ctrl+C to stop")

		// Create tickers
		retryTicker := time.NewTicker(retryInterval)
		watchTicker := time.NewTicker(watchInterval)
		cleanupTicker := time.NewTicker(1 * time.Hour) // Cleanup every hour
		defer retryTicker.Stop()
		defer watchTicker.Stop()
		defer cleanupTicker.Stop()

		// The in-memory set avoids repeated work during this process. The
		// atomic SQLite incoming-once write is the durable duplicate guard
		// when events are delivered again after a restart.
		seen := newSeenSet()
		preloadRecentSeen(seen, myIdentity.Npub)

		// Run immediately
		processOutbox(daemonCtx, myIdentity, relays)
		if daemonCtx.Err() != nil {
			fmt.Println("\n👋 Stopping daemon...")
			return nil
		}
		if _, err := watchInbox(daemonCtx, myIdentity, ks, seen, relays, useNotify, autoReply); err != nil && daemonCtx.Err() == nil {
			fmt.Printf("[%s] ⚠️  Inbox scan incomplete: %v\n", time.Now().Format("15:04:05"), err)
		}
		if daemonCtx.Err() != nil {
			fmt.Println("\n👋 Stopping daemon...")
			return nil
		}

		for {
			select {
			case <-retryTicker.C:
				processOutbox(daemonCtx, myIdentity, relays)
			case <-watchTicker.C:
				if _, err := watchInbox(daemonCtx, myIdentity, ks, seen, relays, useNotify, autoReply); err != nil && daemonCtx.Err() == nil {
					fmt.Printf("[%s] ⚠️  Inbox scan incomplete: %v\n", time.Now().Format("15:04:05"), err)
				}
			case <-cleanupTicker.C:
				cleanupOutbox()
			case <-daemonCtx.Done():
				fmt.Println("\n👋 Stopping daemon...")
				return nil
			}
		}
	},
}

func validateDaemonIntervals(retrySeconds, watchSeconds int64) (time.Duration, time.Duration, error) {
	convert := func(name string, seconds int64) (time.Duration, error) {
		if seconds <= 0 || seconds > math.MaxInt64/int64(time.Second) {
			return 0, fmt.Errorf("%s must be a positive number of seconds within range", name)
		}
		return time.Duration(seconds) * time.Second, nil
	}
	retry, err := convert("retry-interval", retrySeconds)
	if err != nil {
		return 0, 0, err
	}
	watch, err := convert("watch-interval", watchSeconds)
	if err != nil {
		return 0, 0, err
	}
	return retry, watch, nil
}

// processOutbox retries failed messages. Each relay attempt gets its own
// timeout so a slow relay does not starve the rest.
//
// Outbox transitions are saved as they occur. A crash after a relay ACK but
// before the matching entry is removed can still cause a later retry; this
// loop does not claim exactly-once delivery.
func processOutbox(ctx context.Context, myIdentity *types.Identity, relays []string) {
	if ctx.Err() != nil {
		return
	}
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
		if ctx.Err() != nil {
			return
		}
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
	relays []string,
	useNotify bool,
	autoReply bool,
) (int, error) {
	if ctx.Err() != nil {
		return 0, ctx.Err()
	}
	recipientPK, err := identity.GetPublicKey(ks, myIdentity.Nickname)
	if err != nil {
		fmt.Printf("[%s] ⚠️  Failed to get public key: %v\n", time.Now().Format("15:04:05"), err)
		return 0, fmt.Errorf("get recipient public key: %w", err)
	}
	recipientSK, err := identity.GetSecretKey(ks, myIdentity.Nickname)
	if err != nil {
		fmt.Printf("[%s] ⚠️  Failed to get secret key: %v\n", time.Now().Format("15:04:05"), err)
		return 0, fmt.Errorf("get recipient secret key: %w", err)
	}

	filter := nostr.Filter{
		Kinds: []nostr.Kind{messaging.AgentKind},
		Tags:  nostr.TagMap{"p": []string{common.PubKeyToHex(recipientPK)}},
	}

	newCount := 0
	var scanErrors []error

	for _, url := range relays {
		if ctx.Err() != nil {
			return newCount, errors.Join(append(scanErrors, ctx.Err())...)
		}
		count, err := watchOneRelay(ctx, url, filter, ks, recipientSK, seen, useNotify, autoReply, myIdentity, relays)
		newCount += count
		if err != nil {
			scanErrors = append(scanErrors, fmt.Errorf("relay %s: %w", url, err))
			if ctx.Err() != nil {
				break
			}
		}
	}

	if len(scanErrors) > 0 {
		return newCount, errors.Join(scanErrors...)
	}
	if newCount == 0 {
		fmt.Printf("[%s] Watching... (no new messages)\r", time.Now().Format("15:04:05"))
	}
	return newCount, nil
}

// watchOneRelay scans available history from a single relay and returns the
// count of durably stored new events plus any query or processing error.
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
) (int, error) {
	var replies sync.WaitGroup
	processed, err := watchOneRelayWithHooks(ctx, url, filter, ks, recipientSK, seen, useNotify, autoReply, myIdentity, relays, incomingReceiveHooks{
		store: messaging.StoreIncomingMessageOnce,
		notify: func(title, message string) {
			notify.DesktopNotification(title, message)
			notify.PlaySound()
		},
		reply: func(senderNpub, content string) {
			replies.Add(1)
			go func() {
				defer replies.Done()
				sendAutoReply(ctx, myIdentity, ks, senderNpub, content, relays)
			}()
		},
	})
	replies.Wait()
	return processed, err
}

type incomingReceiveHooks struct {
	store  func(*nostr.Event, string, string, bool) (bool, error)
	notify func(title, message string)
	reply  func(senderNpub, content string)
}

func watchOneRelayWithHooks(
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
	hooks incomingReceiveHooks,
) (int, error) {
	newCount := 0
	errorCount := 0
	errorSamples := make([]string, 0, 5)
	filter.Limit = 0 // Walk owns pagination limits and bounds.
	_, queryErr := relayquery.Walk(ctx, url, filter, func(evt nostr.Event) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		eventID := hex.EncodeToString(evt.ID[:])
		processed, err := processIncomingEvent(&evt, recipientSK, seen, useNotify, autoReply, myIdentity, ks, hooks)
		if err != nil {
			errorCount++
			if len(errorSamples) < cap(errorSamples) {
				errorSamples = append(errorSamples, safePrefix(eventID, 16)+":"+incomingErrorKind(err))
			}
			return nil // A malformed event must not block later valid inbox events.
		}
		if processed {
			newCount++
		}
		return nil
	})

	var receiveErr error
	if errorCount > 0 {
		receiveErr = fmt.Errorf("%d incoming event(s) failed processing (samples: %s)", errorCount, strings.Join(errorSamples, ", "))
	}
	return newCount, errors.Join(queryErr, receiveErr)
}

func incomingErrorKind(err error) string {
	switch {
	case strings.HasPrefix(err.Error(), "decode message:"):
		return "decode-failed"
	case strings.HasPrefix(err.Error(), "store incoming message:"):
		return "storage-failed"
	default:
		return "processing-failed"
	}
}

func processIncomingEvent(
	event *nostr.Event,
	recipientSK nostr.SecretKey,
	seen *seenSet,
	useNotify bool,
	autoReply bool,
	myIdentity *types.Identity,
	ks *types.KeyStore,
	hooks incomingReceiveHooks,
) (bool, error) {
	eventID := hex.EncodeToString(event.ID[:])
	if seen.Has(eventID) {
		return false, nil
	}
	content, isEncrypted, err := messaging.DecodeMessageContent(event, recipientSK)
	if err != nil {
		return false, fmt.Errorf("decode message: %w", err)
	}
	first, err := hooks.store(event, myIdentity.Npub, content, isEncrypted)
	if err != nil {
		return false, fmt.Errorf("store incoming message: %w", err)
	}
	// The durable unique write is authoritative across restarts and processes.
	seen.Add(eventID)
	if !first {
		return false, nil
	}

	senderNpub := common.EncodeNpub(event.PubKey)
	senderName := safePrefix(senderNpub, 16) + "..."
	for _, contact := range identity.ListContacts(ks) {
		if contact.Npub == senderNpub {
			senderName = contact.Nickname
			break
		}
	}
	fmt.Printf("\n📨 New message from %s: %s\n", senderName, common.TruncateString(content, 40))
	if useNotify && hooks.notify != nil {
		hooks.notify("Hyphae - "+senderName, common.TruncateString(content, 100))
	}
	if shouldAutoReply(autoReply, true, content) && hooks.reply != nil {
		hooks.reply(senderNpub, content)
	}
	return true, nil
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

// preloadRecentSeen primes the in-memory dedup set with event IDs already
// stored in SQLite. StoreIncomingMessageOnce remains the durable authority,
// including when older events fall outside this in-memory cache.
//
// This cache is an optimization; SQLite's unique incoming write handles IDs
// that are not loaded here.
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
	result, err := sendAutoReplyWithEncryptor(ctx, myIdentity, ks, toNpub, originalContent, relays, crypto.EncryptMessage)
	for _, line := range autoReplyOutcomeLines(result, err) {
		fmt.Println(line)
	}
}

func autoReplyOutcomeLines(result messaging.QueuedAgentMessageResult, sendErr error) []string {
	var lines []string
	if result.PublishedTo > 0 {
		lines = append(lines, fmt.Sprintf("🤖 Auto-reply relay ACKs: %d/%d (relay acceptance does not confirm recipient receipt)", result.PublishedTo, result.RelayCount))
	}
	switch {
	case result.QueueStateUnknown:
		lines = append(lines, "   ⚠️  Auto-reply outbox state is unknown")
	case result.Superseded:
		lines = append(lines, "   ⚠️  Auto-reply outbox entry was superseded")
	case result.QueuedForRetry:
		lines = append(lines, "   📝 Auto-reply is queued for retry")
	case result.PublishedTo > 0:
		lines = append(lines, "   ✅ Auto-reply ACK recorded and outbox entry removed")
	case sendErr != nil:
		lines = append(lines, "   ⚠️  Auto-reply was not confirmed queued")
	case result.EventID != "":
		lines = append(lines, "   ⚠️  Auto-reply received no relay ACK and is not queued")
	}
	for _, relay := range result.Relays {
		if !relay.OK && relay.Error != "" {
			lines = append(lines, fmt.Sprintf("   ❌ %s: %s", relay.URL, relay.Error))
		}
	}
	if sendErr != nil {
		if result.EventID == "" {
			lines = append(lines, fmt.Sprintf("   ⚠️  Auto-reply failed: %v", sendErr))
		} else {
			lines = append(lines, fmt.Sprintf("   ⚠️  Auto-reply event %s: %v", result.EventID, sendErr))
		}
	}
	return lines
}

type autoReplyEncryptor func(string, nostr.SecretKey, nostr.PubKey) (string, error)

func buildAutoReplyEvent(myIdentity *types.Identity, senderSK nostr.SecretKey, recipientPK nostr.PubKey, originalContent string, encrypt autoReplyEncryptor) (string, *nostr.Event, error) {
	replyText := buildAutoReplyText(myIdentity.Nickname, originalContent)
	ciphertext, err := encrypt(replyText, senderSK, recipientPK)
	if err != nil {
		return "", nil, fmt.Errorf("encrypt NIP-44 auto-reply: %w", err)
	}
	compressed, err := messaging.CompressText(ciphertext)
	if err != nil {
		return "", nil, fmt.Errorf("compress auto-reply: %w", err)
	}
	var nonce [16]byte
	if _, err := cryptorand.Read(nonce[:]); err != nil {
		return "", nil, fmt.Errorf("generate auto-reply d tag: %w", err)
	}
	createdAt := nostr.Now()
	event := &nostr.Event{
		CreatedAt: createdAt,
		Kind:      messaging.AgentKind,
		Tags: nostr.Tags{
			{"p", common.PubKeyToHex(recipientPK)},
			{"c", messaging.AgentTag},
			{"z", messaging.CompressTag},
			{"v", messaging.AgentVersion},
			{"d", hex.EncodeToString(nonce[:])},
			{"enc", "nip44"},
		},
		Content: compressed,
		PubKey:  senderSK.Public(),
	}
	if err := event.Sign(senderSK); err != nil {
		return "", nil, fmt.Errorf("sign auto-reply event: %w", err)
	}
	return replyText, event, nil
}

func sendAutoReplyWithEncryptor(ctx context.Context, myIdentity *types.Identity, ks *types.KeyStore, toNpub string, originalContent string, relays []string, encrypt autoReplyEncryptor) (messaging.QueuedAgentMessageResult, error) {
	mySK, err := identity.GetSecretKey(ks, myIdentity.Nickname)
	if err != nil {
		return messaging.QueuedAgentMessageResult{}, fmt.Errorf("load sender key: %w", err)
	}

	toPK, err := common.ParsePublicKey(toNpub)
	if err != nil {
		return messaging.QueuedAgentMessageResult{}, fmt.Errorf("parse recipient key: %w", err)
	}

	replyText, event, err := buildAutoReplyEvent(myIdentity, mySK, toPK, originalContent, encrypt)
	if err != nil {
		return messaging.QueuedAgentMessageResult{}, err
	}

	if len(relays) == 0 {
		relays = []string{relayconfig.DefaultRelay}
	}
	result, sendErr := messaging.SendQueuedAgentMessage(ctx, event, toNpub, replyText, true, relays, relayDialTimeout)
	if result.PublishedTo > 0 {
		if err := audit.LogAction(myIdentity.Nickname, audit.ActionAutoReplySent, map[string]any{
			"to": toNpub, "relay_acknowledged": true, "relay_acks": result.PublishedTo,
			"relay_count": result.RelayCount, "event_id": result.EventID,
		}); err != nil {
			if sendErr == nil {
				sendErr = fmt.Errorf("audit auto-reply ACK: %w", err)
			} else {
				sendErr = fmt.Errorf("%w; audit auto-reply ACK: %v", sendErr, err)
			}
		}
	}
	return result, sendErr
}

package messaging

import (
	"context"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"fiatjaf.com/nostr"
	"github.com/iDoris-ai/hyphae/internal/audit"
	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/internal/identity"
	"github.com/iDoris-ai/hyphae/internal/relayquery"
	"github.com/iDoris-ai/hyphae/pkg/crypto"
	"github.com/iDoris-ai/hyphae/pkg/types"
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

type agentMsgRelayResult struct {
	URL   string `json:"url"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

type agentMsgResult struct {
	From              string                `json:"from"`
	To                string                `json:"to"`
	Encrypted         bool                  `json:"encrypted"`
	EventID           string                `json:"event_id"`
	Relays            []agentMsgRelayResult `json:"relays"`
	PublishedTo       int                   `json:"published_to"`
	RelayCount        int                   `json:"relay_count"`
	QueuedForRetry    bool                  `json:"queued_for_retry"`
	HistoryStored     bool                  `json:"history_stored"`
	Superseded        bool                  `json:"superseded"`
	QueueStateUnknown bool                  `json:"queue_state_unknown"`
	AuditError        string                `json:"audit_error,omitempty"`
}

type agentMsgRelayPublisher func(context.Context, []string, nostr.Event, time.Duration) ([]agentMsgRelayResult, int)
type agentMsgEnqueuer func(*types.Outbox, *nostr.Event, string, []string) (types.OutboxEntry, error)

// CompressText compresses text using zstd
func CompressText(text string) (string, error) {
	encoder, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedFastest))
	if err != nil {
		return "", err
	}
	defer encoder.Close()
	compressed := encoder.EncodeAll([]byte(text), nil)
	return base64.StdEncoding.EncodeToString(compressed), nil
}

// DecompressText decompresses zstd compressed text
func DecompressText(encoded string) (string, error) {
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

// deriveMessageDTag returns a per-message "d" tag value for kind 30078
// agent messages. Kind 30078 falls in NIP-01's addressable/parameterized-
// replaceable range (30000-39999): a compliant relay keeps only the latest
// event per (pubkey, kind, d) coordinate, treating a missing "d" as "". Since
// profile publish (internal/profile/profile.go) uses a fixed ProfileDTag on
// the same kind to intentionally keep one replaceable profile per identity,
// agent messages need a value that's unique per message instead, or
// consecutive messages from the same sender would evict each other on such
// relays -- see CC-82. The random component guarantees uniqueness even for
// byte-identical content sent twice within the same second (e.g. retries
// with encryption disabled, where the ciphertext wouldn't otherwise differ).
func deriveMessageDTag(content string, createdAt nostr.Timestamp) (string, error) {
	nonce := make([]byte, 8)
	if _, err := cryptorand.Read(nonce); err != nil {
		return "", err
	}
	h := sha256.New()
	h.Write([]byte(content))
	h.Write([]byte(strconv.FormatInt(int64(createdAt), 10)))
	h.Write(nonce)
	return hex.EncodeToString(h.Sum(nil))[:16], nil
}

// AgentMsgCmd - Send message using nicknames
var AgentMsgCmd = &cli.Command{
	Name:  "msg",
	Usage: "Send a message to another agent",
	Description: `Send a message using nicknames with optional E2E encryption.
Example: hyphae agent msg --from alice --to bob --content "Hello!"`,
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:    "from",
			Aliases: []string{"f"},
			Usage:   "Your nickname (identity)",
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
		},
		&cli.BoolFlag{
			Name:    "encrypt",
			Aliases: []string{"e"},
			Usage:   "Enable NIP-44 end-to-end encryption",
			Value:   true,
		},
	},
	Action: func(ctx context.Context, c *cli.Command) error {
		to := c.String("to")
		if strings.TrimSpace(to) == "" {
			return common.NewExitError(common.ErrCodeUser, fmt.Errorf("recipient is required"))
		}
		content := c.String("content")
		if content == "" {
			return common.NewExitError(common.ErrCodeUser, fmt.Errorf("message content is required"))
		}

		ks, err := identity.LoadKeyStore()
		if err != nil {
			return err
		}

		sender, err := identity.GetIdentity(ks, c.String("from"))
		if err != nil {
			return common.NewExitError(common.ErrCodeUser, fmt.Errorf("sender not found: %w", err))
		}

		recipientNpub, err := identity.ResolveRecipient(ks, to)
		if err != nil {
			return common.NewExitError(common.ErrCodeUser, err)
		}

		senderSK, err := identity.GetSecretKey(ks, sender.Nickname)
		if err != nil {
			return common.NewExitError(common.ErrCodeAuth, fmt.Errorf("failed to load sender key (is the keystore unlocked?): %w", err))
		}
		recipientPK, err := common.ParsePublicKey(recipientNpub)
		if err != nil {
			return common.NewExitError(common.ErrCodeUser, fmt.Errorf("invalid recipient npub: %w", err))
		}

		// Encrypt if enabled
		messageContent := content
		isEncrypted := false
		if c.Bool("encrypt") {
			encrypted, err := crypto.EncryptMessage(content, senderSK, recipientPK)
			if err != nil {
				return fmt.Errorf("failed to encrypt: %w", err)
			}
			messageContent = encrypted
			isEncrypted = true
		}

		compressed, err := CompressText(messageContent)
		if err != nil {
			return fmt.Errorf("failed to compress message: %w", err)
		}
		createdAt := nostr.Now()
		dTag, err := deriveMessageDTag(compressed, createdAt)
		if err != nil {
			return fmt.Errorf("failed to derive d tag: %w", err)
		}
		tags := nostr.Tags{
			{"p", common.PubKeyToHex(recipientPK)},
			{"c", AgentTag},
			{"z", CompressTag},
			{"v", AgentVersion},
			// Kind 30078 is a NIP-01 addressable/parameterized-replaceable
			// kind range (30000-39999): relays that follow the spec keep
			// only the latest event per (pubkey, kind, d) coordinate. A
			// unique per-message d (unlike profile's fixed ProfileDTag,
			// see internal/profile/profile.go) keeps every message its own
			// coordinate so consecutive messages from the same sender
			// don't silently evict each other -- see CC-82 discussion.
			{"d", dTag},
		}
		// Use "enc" tag to mark encrypted messages
		if isEncrypted {
			tags = append(tags, nostr.Tag{"enc", "nip44"})
		}

		event := &nostr.Event{
			CreatedAt: createdAt,
			Kind:      AgentKind,
			Tags:      tags,
			Content:   compressed,
			PubKey:    senderSK.Public(),
		}
		if err := event.Sign(senderSK); err != nil {
			return fmt.Errorf("failed to sign event: %w", err)
		}

		relays, err := common.ResolveRelays(c)
		if err != nil {
			return err
		}
		jsonMode := common.JSONMode(c)
		result, sendErr := sendQueuedAgentMessage(ctx, event, recipientNpub, content, sender.Nickname, to, isEncrypted, relays, 5*time.Second, StoreOutgoingMessage, enqueueOutboxEntry, publishAgentMessageRelays)
		if !jsonMode {
			printAgentMessageRelays(result.Relays)
			if result.QueuedForRetry {
				fmt.Println("   📝 Added to outbox for retry")
			}
		}
		if result.PublishedTo > 0 {
			if err := audit.LogAction(sender.Nickname, audit.ActionMessageSent, map[string]any{
				"to": recipientNpub, "encrypted": isEncrypted, "event_id": event.ID.Hex(),
			}); err != nil {
				result.AuditError = err.Error()
				if sendErr != nil {
					sendErr = errors.Join(sendErr, fmt.Errorf("audit log failed: %w", err))
				} else if !jsonMode {
					fmt.Fprintf(os.Stderr, "⚠️  audit log failed: %v\n", err)
				}
			}
		}
		if sendErr != nil {
			queueState := "not queued"
			switch {
			case result.QueueStateUnknown:
				queueState = "queue state unknown"
			case result.Superseded:
				queueState = "queue entry superseded"
			case result.QueuedForRetry:
				queueState = "queued for retry"
			}
			sendErr = fmt.Errorf("event %s; relay ACKs %d/%d; %s: %w", result.EventID, result.PublishedTo, result.RelayCount, queueState, sendErr)
			return common.NewExitErrorWithData(common.ErrCodeOther, sendErr, result)
		}

		common.Emit(jsonMode, result, func() {
			encryptionStatus := "plaintext"
			if isEncrypted {
				encryptionStatus = "🔒 NIP-44 encrypted"
			}
			fmt.Printf("📤 Message from '%s' to '%s' (%s)\n", sender.Nickname, to, encryptionStatus)
			fmt.Printf("   Published to %d/%d relays\n", result.PublishedTo, result.RelayCount)
			if result.PublishedTo == 0 {
				fmt.Println("   ⚠️  Warning: Message not published to any relay")
			} else {
				fmt.Println("   💾 Stored in local history")
			}
		})
		return nil
	},
}

func sendQueuedAgentMessage(
	ctx context.Context,
	event *nostr.Event,
	recipientNpub, plaintext, sender, recipient string,
	isEncrypted bool,
	relays []string,
	dialTimeout time.Duration,
	store outgoingMessageStore,
	enqueue agentMsgEnqueuer,
	publish agentMsgRelayPublisher,
) (agentMsgResult, error) {
	result := agentMsgResult{
		From: sender, To: recipient, Encrypted: isEncrypted, EventID: event.ID.Hex(),
		Relays: make([]agentMsgRelayResult, 0, len(relays)), RelayCount: len(relays),
	}
	if err := store(event, recipientNpub, plaintext, isEncrypted); err != nil {
		return result, fmt.Errorf("store local message history: %w", err)
	}
	result.HistoryStored = true

	entry, err := enqueue(nil, event, recipientNpub, relays)
	if err != nil {
		var uncertain *outboxCommitUncertainError
		result.QueueStateUnknown = errors.As(err, &uncertain)
		return result, fmt.Errorf("enqueue message before publishing: %w", err)
	}

	relayResults := make([]agentMsgRelayResult, 0, len(relays))
	var publishedTo int
	sendResult, err := attemptSend(ctx, nil, entry, nil, dialTimeout,
		func(ctx context.Context, targets []string, queued nostr.Event, timeout time.Duration) bool {
			relayResults, publishedTo = publish(ctx, targets, queued, timeout)
			return publishedTo > 0
		}, func(event *nostr.Event, _ string, _ string, _ bool) error {
			return store(event, recipientNpub, plaintext, isEncrypted)
		})
	result.Relays = relayResults
	result.PublishedTo = publishedTo
	result.QueuedForRetry = sendResult.Queued
	result.Superseded = sendResult.Superseded
	result.QueueStateUnknown = sendResult.QueueStateUnknown
	// History was stored before enqueue and therefore remains present even if
	// the post-ACK upsert reports an error.
	result.HistoryStored = true

	if err != nil {
		if !result.Superseded && !result.QueueStateUnknown && !result.QueuedForRetry {
			result.QueuedForRetry, result.Superseded, result.QueueStateUnknown = inspectAttemptQueue(entry)
		}
		return result, fmt.Errorf("send queued message: %w", err)
	}
	if sendResult.Superseded {
		return result, errors.New("queued message was removed or replaced while publishing")
	}
	if sendResult.QueueStateUnknown {
		return result, errors.New("outbox state is unknown after the publish outcome")
	}
	return result, nil
}

func publishAgentMessageRelays(ctx context.Context, targets []string, event nostr.Event, timeout time.Duration) ([]agentMsgRelayResult, int) {
	results := make([]agentMsgRelayResult, 0, len(targets))
	success := 0
	for _, url := range targets {
		pubCtx, cancel := context.WithTimeout(ctx, timeout)
		relay, err := nostr.RelayConnect(pubCtx, url, nostr.RelayOptions{})
		if err != nil {
			if relay != nil {
				relay.Close()
			}
			cancel()
			results = append(results, agentMsgRelayResult{URL: url, Error: fmt.Sprintf("connect failed: %v", err)})
			continue
		}
		err = relay.Publish(pubCtx, event)
		relay.Close()
		cancel()
		if err != nil {
			results = append(results, agentMsgRelayResult{URL: url, Error: fmt.Sprintf("publish failed: %v", err)})
			continue
		}
		results = append(results, agentMsgRelayResult{URL: url, OK: true})
		success++
	}
	return results, success
}

func printAgentMessageRelays(results []agentMsgRelayResult) {
	for _, result := range results {
		if result.OK {
			fmt.Printf("   ✅ %s\n", result.URL)
		} else {
			fmt.Printf("   ❌ %s: %s\n", result.URL, result.Error)
		}
	}
}

// AgentInboxCmd - Show inbox
var AgentInboxCmd = &cli.Command{
	Name:  "inbox",
	Usage: "Show your inbox",
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:    "as",
			Aliases: []string{"a"},
			Usage:   "Your nickname",
		},
		&cli.StringSliceFlag{
			Name:    "relay",
			Aliases: []string{"r"},
		},
		&cli.IntFlag{
			Name:  "limit",
			Value: 10,
		},
		&cli.BoolFlag{
			Name:    "decrypt",
			Aliases: []string{"d"},
			Usage:   "Auto-decrypt NIP-44 messages",
			Value:   true,
		},
	},
	Action: func(ctx context.Context, c *cli.Command) error {
		limit := int(c.Int("limit"))
		if limit <= 0 {
			return common.NewExitError(common.ErrCodeUser, fmt.Errorf("limit must be positive"))
		}

		ks, err := identity.LoadKeyStore()
		if err != nil {
			return err
		}

		recipient, err := identity.GetIdentity(ks, c.String("as"))
		if err != nil {
			return common.NewExitError(common.ErrCodeUser, err)
		}

		autoDecrypt := c.Bool("decrypt")

		recipientPK, err := identity.GetPublicKey(ks, recipient.Nickname)
		if err != nil {
			return common.NewExitError(common.ErrCodeOther, fmt.Errorf("failed to load recipient public key: %w", err))
		}
		recipientSK, err := identity.GetSecretKey(ks, recipient.Nickname)
		if err != nil && autoDecrypt {
			return common.NewExitError(common.ErrCodeAuth, fmt.Errorf("failed to load recipient key (is the keystore unlocked?): %w", err))
		}

		relays, err := common.ResolveRelays(c)
		if err != nil {
			return err
		}
		jsonMode := common.JSONMode(c)
		filter := nostr.Filter{
			Kinds: []nostr.Kind{AgentKind},
			Tags:  nostr.TagMap{"p": []string{common.PubKeyToHex(recipientPK)}},
			Limit: limit,
		}

		allEvents := make(map[string]nostr.Event)
		successfulRelays := 0
		var relayErrors []string
		var eventErrors []string
		recipientHex := common.PubKeyToHex(recipientPK)
		for _, url := range relays {
			page, fetchErr := relayquery.Fetch(ctx, url, filter)
			if fetchErr != nil {
				relayErrors = append(relayErrors, fmt.Sprintf("%s: %v", url, fetchErr))
			} else {
				successfulRelays++
			}
			for _, evt := range page.Events {
				if err := validateInboxEvent(evt, recipientHex); err != nil {
					eventErrors = append(eventErrors, fmt.Sprintf("event %s: %v", evt.ID.Hex(), err))
					continue
				}
				allEvents[evt.ID.Hex()] = evt
			}
		}

		events := make([]nostr.Event, 0, len(allEvents))
		for _, evt := range allEvents {
			events = append(events, evt)
		}
		sort.Slice(events, func(i, j int) bool {
			if events[i].CreatedAt != events[j].CreatedAt {
				return events[i].CreatedAt > events[j].CreatedAt
			}
			return events[i].ID.Hex() < events[j].ID.Hex()
		})
		if len(events) > limit {
			events = events[:limit]
		}

		entries := make([]agentInboxEntry, 0, len(events))
		var auditWarnings []string
		for _, evt := range events {
			senderNpub := common.EncodeNpub(evt.PubKey)
			senderName := senderNpub[:16] + "..."
			for _, contact := range identity.ListContacts(ks) {
				if contact.Npub == senderNpub {
					senderName = contact.Nickname
					break
				}
			}

			content, isEncrypted, decrypted, err := decodeInboxContent(&evt, recipientSK, autoDecrypt)
			if err != nil {
				eventErrors = append(eventErrors, fmt.Sprintf("event %s: decode message: %v", evt.ID.Hex(), err))
				continue
			}

			first := false
			if !isEncrypted || autoDecrypt {
				first, err = StoreIncomingMessageOnce(&evt, recipient.Npub, content, isEncrypted)
				if err != nil {
					eventErrors = append(eventErrors, fmt.Sprintf("event %s: store received message: %v", evt.ID.Hex(), err))
					continue
				}
			}
			if first {
				if err := audit.LogAction(recipient.Nickname, audit.ActionMessageReceived, map[string]any{
					"from": senderNpub, "encrypted": isEncrypted, "event_id": evt.ID.Hex(),
				}); err != nil {
					auditWarnings = append(auditWarnings, fmt.Sprintf("audit log failed for event %s: %v", evt.ID.Hex(), err))
				}
			}

			entries = append(entries, agentInboxEntry{
				Time: evt.CreatedAt.Time().Format("15:04"), From: senderName, Content: content,
				Encrypted: isEncrypted, Decrypted: decrypted, EventID: evt.ID.Hex(), SenderNpub: senderNpub,
			})
		}

		if len(eventErrors) > 0 || successfulRelays == 0 {
			messageParts := make([]string, 0, len(relayErrors)+len(eventErrors)+1)
			if successfulRelays == 0 {
				messageParts = append(messageParts, "all relays failed")
			}
			messageParts = append(messageParts, relayErrors...)
			messageParts = append(messageParts, eventErrors...)
			messageParts = append(messageParts, auditWarnings...)
			code := common.ErrCodeOther
			if successfulRelays == 0 {
				code = common.ErrCodeNetwork
			}
			return common.NewExitErrorWithData(code, fmt.Errorf("%s", strings.Join(messageParts, "; ")), entries)
		}
		for _, warning := range append(relayErrors, auditWarnings...) {
			fmt.Fprintf(os.Stderr, "⚠️  %s\n", warning)
		}

		common.Emit(jsonMode, entries, func() {
			fmt.Printf("📬 Inbox for '%s'\n\n", recipient.Nickname)
			if len(entries) == 0 {
				fmt.Println("   Empty")
			}
			for _, e := range entries {
				prefix := ""
				switch {
				case e.Encrypted && e.Decrypted:
					prefix = "🔓 "
				case e.Encrypted:
					prefix = "🔒 "
				}
				fmt.Printf("[%s] %s: %s\n", e.Time, e.From, common.TruncateString(prefix+e.Content, 50))
			}
		})
		return nil
	},
}

type agentInboxEntry struct {
	Time       string `json:"time"`
	From       string `json:"from"`
	Content    string `json:"content"`
	Encrypted  bool   `json:"encrypted"`
	Decrypted  bool   `json:"decrypted"`
	EventID    string `json:"event_id"`
	SenderNpub string `json:"sender_npub"`
}

func validateInboxEvent(event nostr.Event, recipientHex string) error {
	if event.Kind != AgentKind {
		return fmt.Errorf("unexpected event kind %d", event.Kind)
	}
	if !event.CheckID() || !event.VerifySignature() {
		return fmt.Errorf("invalid event ID or signature")
	}
	pTags := 0
	matchedRecipient := false
	for _, tag := range event.Tags {
		if len(tag) > 0 && tag[0] == "p" {
			pTags++
			if len(tag) < 2 {
				return fmt.Errorf("recipient filter tag is malformed")
			}
			matchedRecipient = tag[1] == recipientHex
		}
	}
	if pTags != 1 || !matchedRecipient {
		return fmt.Errorf("event does not match recipient filter with exactly one p tag")
	}
	return nil
}

// AgentCmd - Main agent command
var AgentCmd = &cli.Command{
	Name:  "agent",
	Usage: "Agent communication",
	Commands: []*cli.Command{
		AgentMsgCmd,
		AgentInboxCmd,
	},
}

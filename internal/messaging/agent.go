package messaging

import (
	"context"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"time"

	"fiatjaf.com/nostr"
	"github.com/AuraAIHQ/agent-speaker/internal/audit"
	"github.com/AuraAIHQ/agent-speaker/internal/common"
	"github.com/AuraAIHQ/agent-speaker/internal/identity"
	"github.com/AuraAIHQ/agent-speaker/pkg/crypto"
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
Example: agent-speaker agent msg --from alice --to bob --content "Hello!"`,
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

		recipientNpub, err := identity.ResolveRecipient(ks, c.String("to"))
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

		compressed, _ := CompressText(messageContent)
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

		relays := c.StringSlice("relay")
		jsonMode := common.JSONMode(c)

		// Publish with detailed error output
		type relayResult struct {
			URL   string `json:"url"`
			OK    bool   `json:"ok"`
			Error string `json:"error,omitempty"`
		}
		results := make([]relayResult, 0, len(relays))
		success := 0
		for _, url := range relays {
			relay, err := nostr.RelayConnect(ctx, url, nostr.RelayOptions{})
			if err != nil {
				if !jsonMode {
					fmt.Printf("   ❌ %s: connect failed: %v\n", url, err)
				}
				results = append(results, relayResult{URL: url, OK: false, Error: fmt.Sprintf("connect failed: %v", err)})
				continue
			}

			pubCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err = relay.Publish(pubCtx, *event)
			cancel()
			relay.Close()

			if err != nil {
				if !jsonMode {
					fmt.Printf("   ❌ %s: publish failed: %v\n", url, err)
				}
				results = append(results, relayResult{URL: url, OK: false, Error: fmt.Sprintf("publish failed: %v", err)})
			} else {
				if !jsonMode {
					fmt.Printf("   ✅ %s\n", url)
				}
				results = append(results, relayResult{URL: url, OK: true})
				success++
			}
		}

		// Store in local history and outbox
		queuedForRetry := false
		if success > 0 {
			StoreOutgoingMessage(event, recipientNpub, content, isEncrypted)
			if err := audit.LogAction(sender.Nickname, audit.ActionMessageSent, map[string]any{
				"to": recipientNpub, "encrypted": isEncrypted, "event_id": event.ID.Hex(),
			}); err != nil {
				fmt.Fprintf(os.Stderr, "⚠️  audit log failed: %v\n", err)
			}
			// Remove from outbox if it was there. AddToOutbox stores IDs
			// hex-encoded (see its doc comment), so the lookup key here
			// must match that encoding, not the raw event.ID bytes.
			ob, _ := LoadOutbox()
			RemoveFromOutbox(ob, hex.EncodeToString(event.ID[:]))
		} else {
			// Add to outbox for retry
			ob, _ := LoadOutbox()
			AddToOutbox(ob, event, recipientNpub, relays)
			queuedForRetry = true
			if !jsonMode {
				fmt.Println("   📝 Added to outbox for retry")
			}
		}

		isEncryptedFinal := isEncrypted
		common.Emit(jsonMode, map[string]any{
			"from":             sender.Nickname,
			"to":               c.String("to"),
			"encrypted":        isEncryptedFinal,
			"event_id":         event.ID.Hex(),
			"relays":           results,
			"published_to":     success,
			"relay_count":      len(relays),
			"queued_for_retry": queuedForRetry,
		}, func() {
			encryptionStatus := "plaintext"
			if isEncryptedFinal {
				encryptionStatus = "🔒 NIP-44 encrypted"
			}
			fmt.Printf("📤 Message from '%s' to '%s' (%s)\n", sender.Nickname, c.String("to"), encryptionStatus)
			fmt.Printf("   Published to %d/%d relays\n", success, len(relays))

			if success == 0 {
				fmt.Println("   ⚠️  Warning: Message not published to any relay")
			} else {
				fmt.Printf("   💾 Stored in local history\n")
			}
		})

		return nil
	},
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
			Value:   []string{"wss://relay.aastar.io"},
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

		filter := nostr.Filter{
			Kinds: []nostr.Kind{AgentKind},
			Tags:  nostr.TagMap{"p": []string{common.PubKeyToHex(recipientPK)}},
			Limit: int(c.Int("limit")),
		}

		relays := c.StringSlice("relay")
		jsonMode := common.JSONMode(c)
		if !jsonMode {
			fmt.Printf("📬 Inbox for '%s'\n\n", recipient.Nickname)
		}

		allEvents := make([]nostr.Event, 0)
		for _, url := range relays {
			relay, err := nostr.RelayConnect(ctx, url, nostr.RelayOptions{})
			if err != nil {
				if !jsonMode {
					fmt.Printf("   ⚠️  Failed to connect to %s: %v\n", url, err)
				}
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
			common.Emit(jsonMode, []any{}, func() {
				fmt.Println("   Empty")
			})
			return nil
		}

		type inboxEntry struct {
			Time      string `json:"time"`
			From      string `json:"from"`
			Content   string `json:"content"`
			Encrypted bool   `json:"encrypted"`
			Decrypted bool   `json:"decrypted"`
		}
		entries := make([]inboxEntry, 0, len(allEvents))

		for _, evt := range allEvents {
			senderNpub := common.EncodeNpub(evt.PubKey)
			senderName := senderNpub[:16] + "..."
			for _, contact := range identity.ListContacts(ks) {
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

			content, _ := DecompressText(evt.Content)
			decrypted := false

			// Decrypt if needed
			if isEncrypted && autoDecrypt {
				plain, err := crypto.DecryptMessage(content, recipientSK, evt.PubKey)
				if err == nil {
					content = plain
					decrypted = true
				} else {
					content = "[encrypted - cannot decrypt]"
				}
			} else if isEncrypted {
				content = "[encrypted message]"
			}

			// Store in local history
			StoreIncomingMessage(&evt, content, isEncrypted)
			if err := audit.LogAction(recipient.Nickname, audit.ActionMessageReceived, map[string]any{
				"from": senderNpub, "encrypted": isEncrypted,
			}); err != nil {
				fmt.Fprintf(os.Stderr, "⚠️  audit log failed: %v\n", err)
			}

			entries = append(entries, inboxEntry{
				Time:      evt.CreatedAt.Time().Format("15:04"),
				From:      senderName,
				Content:   content,
				Encrypted: isEncrypted,
				Decrypted: decrypted,
			})
		}

		common.Emit(jsonMode, entries, func() {
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

// AgentCmd - Main agent command
var AgentCmd = &cli.Command{
	Name:  "agent",
	Usage: "Agent communication",
	Commands: []*cli.Command{
		AgentMsgCmd,
		AgentInboxCmd,
	},
}

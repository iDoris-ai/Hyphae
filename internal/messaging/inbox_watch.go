package messaging

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"fiatjaf.com/nostr"
	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/internal/identity"
	"github.com/iDoris-ai/hyphae/internal/relayquery"
	"github.com/iDoris-ai/hyphae/internal/storage"
	"github.com/iDoris-ai/hyphae/pkg/types"
)

const (
	inboxConnectTimeout = 5 * time.Second
	inboxRetryMin       = 250 * time.Millisecond
	inboxRetryMax       = 5 * time.Second
)

// AgentInboxWatchUpdate reports a receiver status change or a durably stored
// first-arrival message. Callbacks may be invoked concurrently by relays.
type AgentInboxWatchUpdate struct {
	RelayURL  string
	Connected bool
	Message   *ReceivedAgentMessage
	Err       error
}

// ReceivedAgentMessage describes a message after it has been validated,
// decrypted, and committed through StoreIncomingMessageOnce.
type ReceivedAgentMessage struct {
	EventID     string
	SenderNpub  string
	Content     string
	IsEncrypted bool
	Decrypted   bool
}

// WatchAgentInbox consumes historical and live kind-30078 messages for one
// local identity. It returns after ctx is canceled and waits for every relay
// worker to close its subscription and connection. Relay failures are
// reported through emit and retried with bounded backoff.
func WatchAgentInbox(ctx context.Context, nickname string, relays []string, emit func(AgentInboxWatchUpdate)) error {
	store, err := GetStore()
	if err != nil {
		return fmt.Errorf("open inbox message store: %w", err)
	}
	return WatchAgentInboxWithStore(ctx, nickname, relays, store, emit)
}

// WatchAgentInboxWithStore is WatchAgentInbox with an explicit message store.
// A TUI can pass the same store used to query its conversation, so durable
// receives and view refreshes share one connection owner.
func WatchAgentInboxWithStore(ctx context.Context, nickname string, relays []string, store *storage.MessageStore, emit func(AgentInboxWatchUpdate)) error {
	if ctx == nil {
		return errors.New("inbox watch context is required")
	}
	if nickname == "" {
		return errors.New("inbox watch identity is required")
	}
	if emit == nil {
		return errors.New("inbox watch update callback is required")
	}
	if store == nil {
		return errors.New("inbox watch message store is required")
	}
	if len(relays) == 0 {
		return errors.New("at least one inbox relay is required")
	}
	usableRelay := false
	for _, relayURL := range relays {
		if relayURL != "" {
			usableRelay = true
			break
		}
	}
	if !usableRelay {
		return errors.New("at least one non-empty inbox relay is required")
	}

	ks, err := identity.LoadKeyStore()
	if err != nil {
		return fmt.Errorf("load inbox identity store: %w", err)
	}
	recipient, err := identity.GetIdentity(ks, nickname)
	if err != nil {
		return fmt.Errorf("load inbox identity %q: %w", nickname, err)
	}
	recipientSK, err := identity.GetSecretKey(ks, recipient.Nickname)
	if err != nil {
		return fmt.Errorf("load inbox identity key: %w", err)
	}
	recipientPK := recipientSK.Public()
	recipientHex := common.PubKeyToHex(recipientPK)
	filter := BuildAgentMessageFilter(recipientHex)

	var workers sync.WaitGroup
	for _, relayURL := range relays {
		if relayURL == "" {
			continue
		}
		workers.Add(1)
		go func(url string) {
			defer workers.Done()
			watchAgentInboxRelay(ctx, url, filter, recipient, recipientSK, recipientHex, store, emit)
		}(relayURL)
	}
	workers.Wait()
	if ctx.Err() != nil {
		return nil
	}
	return nil
}

func watchAgentInboxRelay(
	ctx context.Context,
	url string,
	filter nostr.Filter,
	recipient *types.Identity,
	recipientSK nostr.SecretKey,
	recipientHex string,
	store *storage.MessageStore,
	emit func(AgentInboxWatchUpdate),
) {
	backoff := inboxRetryMin
	for ctx.Err() == nil {
		connCtx, cancelConn := context.WithCancel(ctx)
		connectCtx, cancelConnect := context.WithTimeout(connCtx, inboxConnectTimeout)
		relay, err := nostr.RelayConnect(connectCtx, url, nostr.RelayOptions{})
		cancelConnect()
		if err != nil {
			cancelConn()
			emit(AgentInboxWatchUpdate{RelayURL: url, Err: fmt.Errorf("connect to inbox relay: %w", err)})
			if !waitInboxRetry(ctx, backoff) {
				return
			}
			backoff = nextInboxBackoff(backoff)
			continue
		}

		// Recover all stored messages before entering the live stream. Keep the
		// live subscription unbounded by CreatedAt: an offline sender may publish
		// an already-signed event whose timestamp predates this history walk.
		// StoreIncomingMessageOnce makes the overlap with this full walk
		// idempotent.
		_, walkErr := relayquery.Walk(ctx, url, filter, func(event nostr.Event) error {
			storeIncomingWatchEvent(&event, recipient, recipientSK, recipientHex, store, url, emit)
			return nil // Invalid events are reported but must not block later events.
		})
		if ctx.Err() != nil {
			relay.Close()
			cancelConn()
			return
		}
		if walkErr != nil {
			walkErr = fmt.Errorf("recover inbox history: %w", walkErr)
		}

		sub, err := relay.Subscribe(connCtx, filter, nostr.SubscriptionOptions{Label: "hyphae-tui-inbox"})
		if err != nil {
			relay.Close()
			cancelConn()
			emit(AgentInboxWatchUpdate{RelayURL: url, Err: fmt.Errorf("subscribe to inbox relay: %w", err)})
			if !waitInboxRetry(ctx, backoff) {
				return
			}
			backoff = nextInboxBackoff(backoff)
			continue
		}

		backoff = inboxRetryMin
		emit(AgentInboxWatchUpdate{RelayURL: url, Connected: true, Err: walkErr})
		reconnect := false
		for !reconnect {
			select {
			case event, ok := <-sub.Events:
				if !ok {
					reconnect = true
					continue
				}
				storeIncomingWatchEvent(&event, recipient, recipientSK, recipientHex, store, url, emit)
			case reason := <-sub.ClosedReason:
				emit(AgentInboxWatchUpdate{RelayURL: url, Err: fmt.Errorf("relay closed inbox subscription: %s", reason)})
				reconnect = true
			case <-sub.Context.Done():
				if ctx.Err() == nil {
					emit(AgentInboxWatchUpdate{RelayURL: url, Err: fmt.Errorf("inbox subscription ended: %w", sub.Context.Err())})
				}
				reconnect = true
			case <-relay.Context().Done():
				emit(AgentInboxWatchUpdate{RelayURL: url, Err: fmt.Errorf("inbox relay disconnected: %w", relay.Context().Err())})
				reconnect = true
			case <-ctx.Done():
				reconnect = true
			}
		}

		sub.Unsub()
		relay.Close()
		cancelConn()
		if ctx.Err() != nil || !waitInboxRetry(ctx, backoff) {
			return
		}
		backoff = nextInboxBackoff(backoff)
	}
}

func storeIncomingWatchEvent(
	event *nostr.Event,
	recipient *types.Identity,
	recipientSK nostr.SecretKey,
	recipientHex string,
	store *storage.MessageStore,
	relayURL string,
	emit func(AgentInboxWatchUpdate),
) {
	if err := validateInboxEvent(*event, recipientHex); err != nil {
		emit(AgentInboxWatchUpdate{RelayURL: relayURL, Err: fmt.Errorf("reject incoming event %s: %w", event.ID.Hex(), err)})
		return
	}
	content, encrypted, decrypted, err := decodeInboxContent(event, recipientSK, true)
	if err != nil {
		emit(AgentInboxWatchUpdate{RelayURL: relayURL, Err: fmt.Errorf("decode incoming event %s: %w", event.ID.Hex(), err)})
		return
	}
	if err := RejectReservedGroupPayload(content); err != nil {
		emit(AgentInboxWatchUpdate{RelayURL: relayURL, Err: fmt.Errorf("reject incoming event %s: %w", event.ID.Hex(), err)})
		return
	}
	first, err := store.StoreIncomingMessageOnce(event, recipient.Npub, content, encrypted)
	if err != nil {
		emit(AgentInboxWatchUpdate{RelayURL: relayURL, Err: fmt.Errorf("store incoming event %s: %w", event.ID.Hex(), err)})
		return
	}
	if !first {
		return
	}
	emit(AgentInboxWatchUpdate{
		RelayURL: relayURL,
		Message: &ReceivedAgentMessage{
			EventID: event.ID.Hex(), SenderNpub: common.EncodeNpub(event.PubKey),
			Content: content, IsEncrypted: encrypted, Decrypted: decrypted,
		},
	})
}

func waitInboxRetry(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return ctx.Err() == nil
	case <-ctx.Done():
		return false
	}
}

func nextInboxBackoff(current time.Duration) time.Duration {
	if current >= inboxRetryMax/2 {
		return inboxRetryMax
	}
	return current * 2
}

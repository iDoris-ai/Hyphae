package messaging

import (
	"context"
	"errors"
	"fmt"
	"math"
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
	// One cache per watch call. Relay workers share it. Return discards it,
	// so a restarted watch replays persisted rows through the full path.
	cache := newWatchValidatedCache(store, recipient.Npub)

	var workers sync.WaitGroup
	for _, relayURL := range relays {
		if relayURL == "" {
			continue
		}
		workers.Add(1)
		go func(url string) {
			defer workers.Done()
			watchAgentInboxRelay(ctx, url, filter, recipient, recipientSK, recipientHex, store, emit, cache)
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
	cache *watchValidatedCache,
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
			storeIncomingWatchEvent(&event, recipient, recipientSK, recipientHex, store, url, emit, cache)
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

		// The watcher never reads EndOfStoredEvents. nostr's default 7s fake
		// EOSE drops pre-EOSE events the synchronous consumer has not taken,
		// including after a real EOSE. time.Duration(math.MaxInt64) is the
		// library's documented disabled value and starts no timer goroutine.
		// Cancelling connCtx still releases every dispatch goroutine.
		sub, err := relay.Subscribe(connCtx, filter, nostr.SubscriptionOptions{
			Label:          "hyphae-tui-inbox",
			MaxWaitForEOSE: time.Duration(math.MaxInt64),
		})
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
				storeIncomingWatchEvent(&event, recipient, recipientSK, recipientHex, store, url, emit, cache)
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
	cache *watchValidatedCache,
) {
	err := validateInboxEvent(*event, recipientHex)
	if err != nil {
		emit(AgentInboxWatchUpdate{RelayURL: relayURL, Err: fmt.Errorf("reject incoming event %s: %w", event.ID.Hex(), err)})
		return
	}
	// Signature, kind, classifier, and the exact p tag already matched.
	// A hit still re-reads the current row. The row is not the proof.
	if cache.duplicateHit(store, recipient.Npub, event) {
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
	cache.remember(store, recipient.Npub, event, encrypted)
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

// 2MiB max content × 256 entries would be 512MiB. The 4MiB cap counts raw
// event.Content only. 256 × (64-byte id + two ~63-byte npubs) is ~48KB of
// bookkeeping and stays outside that cap. Map membership means this watch
// already passed decode and the reserved-group guard. Kind is not stored:
// CheckID binds kind 30078, and StoredMessage has no kind field to fence.
const (
	watchCacheMaxEntries = 256
	watchCacheMaxBytes   = 4 << 20
)

type watchCacheEntry struct {
	sender, recipient, content string
	encrypted                  bool
	generation                 uint64
}

type watchCacheSlot struct {
	id         string
	generation uint64
}

// Per-watch successes only. Discarded when the watch returns. Not global.
type watchValidatedCache struct {
	mu                   sync.Mutex
	store                *storage.MessageStore
	recipient            string
	byID                 map[string]*watchCacheEntry
	ring                 [watchCacheMaxEntries]watchCacheSlot
	head, tail, occupied int
	live, bytes          int
	nextGen              uint64
}

func newWatchValidatedCache(store *storage.MessageStore, recipientNpub string) *watchValidatedCache {
	return &watchValidatedCache{
		store: store, recipient: recipientNpub,
		byID: make(map[string]*watchCacheEntry, watchCacheMaxEntries),
	}
}

func (c *watchValidatedCache) duplicateHit(store *storage.MessageStore, recipientNpub string, event *nostr.Event) bool {
	if c == nil || event == nil || store == nil || recipientNpub == "" {
		return false
	}
	c.mu.Lock()
	sameCall := c.store == store && c.recipient == recipientNpub
	c.mu.Unlock()
	if !sameCall {
		return false
	}
	encrypted, err := watchEncryptionClass(event)
	if err != nil {
		return false
	}
	id := event.ID.Hex()
	sender := common.EncodeNpub(event.PubKey)
	c.mu.Lock()
	ent, ok := c.byID[id]
	match := ok && ent.generation != 0 && ent.sender == sender && ent.recipient == recipientNpub &&
		ent.content == event.Content && ent.encrypted == encrypted
	c.mu.Unlock()
	if !match {
		return false
	}
	return watchRowFence(store, event, recipientNpub, encrypted)
}

func (c *watchValidatedCache) remember(store *storage.MessageStore, recipientNpub string, event *nostr.Event, encrypted bool) {
	if c == nil || event == nil || store == nil || recipientNpub == "" {
		return
	}
	c.mu.Lock()
	sameCall := c.store == store && c.recipient == recipientNpub
	c.mu.Unlock()
	if !sameCall || !watchRowFence(store, event, recipientNpub, encrypted) {
		return
	}
	c.mu.Lock()
	if c.store != store || c.recipient != recipientNpub {
		c.mu.Unlock()
		return
	}
	c.insertLocked(event.ID.Hex(), common.EncodeNpub(event.PubKey), event.Content, encrypted)
	c.mu.Unlock()
}

func (c *watchValidatedCache) insertLocked(id, sender, content string, encrypted bool) {
	if len(content) > watchCacheMaxBytes {
		return
	}
	if old, ok := c.byID[id]; ok {
		c.bytes -= len(old.content)
		c.live--
		delete(c.byID, id)
	}
	for c.occupied >= watchCacheMaxEntries || (c.bytes+len(content) > watchCacheMaxBytes && c.occupied > 0) {
		if !c.evictOldestLocked() {
			break
		}
	}
	if c.occupied >= watchCacheMaxEntries || c.bytes+len(content) > watchCacheMaxBytes || c.bytes < 0 || c.live < 0 {
		if c.bytes < 0 {
			c.bytes = 0
		}
		if c.live < 0 {
			c.live = 0
		}
		return
	}
	c.nextGen++
	c.byID[id] = &watchCacheEntry{
		sender: sender, recipient: c.recipient, content: content,
		encrypted: encrypted, generation: c.nextGen,
	}
	c.ring[c.tail] = watchCacheSlot{id: id, generation: c.nextGen}
	c.tail = (c.tail + 1) % watchCacheMaxEntries
	c.occupied++
	c.live++
	c.bytes += len(content)
}

// A stale generation must not delete a refreshed same-id entry.
func (c *watchValidatedCache) evictOldestLocked() bool {
	if c.occupied == 0 {
		return false
	}
	slot := c.ring[c.head]
	c.ring[c.head] = watchCacheSlot{}
	c.head = (c.head + 1) % watchCacheMaxEntries
	c.occupied--
	if ent, ok := c.byID[slot.id]; ok && ent.generation == slot.generation {
		c.bytes -= len(ent.content)
		c.live--
		delete(c.byID, slot.id)
	}
	if c.bytes < 0 {
		c.bytes = 0
	}
	if c.live < 0 {
		c.live = 0
	}
	return true
}

func watchEncryptionClass(event *nostr.Event) (bool, error) {
	if event == nil {
		return false, fmt.Errorf("event is required")
	}
	encryption, has, err := outboxTagValue(event.Tags, "enc")
	if err != nil {
		return false, err
	}
	if has && encryption != "nip44" {
		return false, fmt.Errorf("unsupported encryption tag")
	}
	return has, nil
}

// Read-only GetMessage fence. Plaintext is ignored and must not be logged.
// Kind and event_id columns are not read.
func watchRowFence(store *storage.MessageStore, event *nostr.Event, recipient string, encrypted bool) bool {
	if store == nil || event == nil || recipient == "" {
		return false
	}
	msg, err := store.GetMessage(event.ID.Hex())
	if err != nil || msg == nil {
		return false
	}
	return msg.ID == event.ID.Hex() &&
		msg.SenderNpub == common.EncodeNpub(event.PubKey) &&
		msg.RecipientNpub == recipient &&
		msg.Content == event.Content &&
		msg.IsEncrypted == encrypted &&
		msg.IsIncoming
}

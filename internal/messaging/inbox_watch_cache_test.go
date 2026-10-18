package messaging

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"github.com/gorilla/websocket"
	"github.com/iDoris-ai/hyphae/internal/identity"
	"github.com/iDoris-ai/hyphae/internal/storage"
	"github.com/iDoris-ai/hyphae/pkg/crypto"
	"github.com/iDoris-ai/hyphae/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const watchForeignNpub = "npub1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq8n5c8s"

type watchFixture struct {
	recipient   *types.Identity
	recipientSK nostr.SecretKey
	recipientPK nostr.PubKey
	sender      nostr.SecretKey
	store       *storage.MessageStore
	cache       *watchValidatedCache
}

func newWatchFixture(t *testing.T) watchFixture {
	t.Helper()
	recipient, recipientPK := setupAgentInbox(t)
	ResetStoreForTest()
	t.Cleanup(ResetStoreForTest)
	store, err := GetStore()
	require.NoError(t, err)
	ks, err := identity.LoadKeyStore()
	require.NoError(t, err)
	recipientSK, err := identity.GetSecretKey(ks, recipient.Nickname)
	require.NoError(t, err)
	return watchFixture{
		recipient:   recipient,
		recipientSK: recipientSK,
		recipientPK: recipientPK,
		sender:      nostr.Generate(),
		store:       store,
		cache:       newWatchValidatedCache(store, recipient.Npub),
	}
}

func (f watchFixture) deliver(t *testing.T, event nostr.Event, cache *watchValidatedCache) []AgentInboxWatchUpdate {
	t.Helper()
	return f.deliverSK(t, event, cache, f.store, f.recipientSK)
}

func (f watchFixture) deliverSK(t *testing.T, event nostr.Event, cache *watchValidatedCache, store *storage.MessageStore, sk nostr.SecretKey) []AgentInboxWatchUpdate {
	t.Helper()
	var updates []AgentInboxWatchUpdate
	storeIncomingWatchEvent(&event, f.recipient, sk, f.recipientPK.Hex(), store, "ws://127.0.0.1:1", func(update AgentInboxWatchUpdate) {
		updates = append(updates, update)
	}, cache)
	return updates
}

func (f watchFixture) saved(t *testing.T, id string) types.StoredMessage {
	t.Helper()
	msg, err := f.store.GetMessage(id)
	require.NoError(t, err)
	require.NotNil(t, msg)
	return *msg
}

func (f watchFixture) plain(t *testing.T, body string, created nostr.Timestamp) nostr.Event {
	t.Helper()
	return makeInboxEvent(t, f.recipientPK, f.sender, created, body, nil)
}

func (f watchFixture) encrypted(t *testing.T, body string, created nostr.Timestamp) nostr.Event {
	t.Helper()
	cipher, err := crypto.EncryptMessage(body, f.sender, f.recipientPK)
	require.NoError(t, err)
	return makeInboxEvent(t, f.recipientPK, f.sender, created, cipher, nostr.Tags{{"enc", "nip44"}})
}

func (f watchFixture) encryptedRaw(t *testing.T, content string, created nostr.Timestamp) nostr.Event {
	t.Helper()
	return makeInboxEvent(t, f.recipientPK, f.sender, created, content, nostr.Tags{{"enc", "nip44"}})
}

func (f watchFixture) otherStore(t *testing.T) *storage.MessageStore {
	t.Helper()
	db, err := storage.OpenDB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return storage.NewMessageStore(db)
}

func TestWatchCachePlainAndEncryptedDuplicateSkipsDecodeAndWrite(t *testing.T) {
	f := newWatchFixture(t)
	plain := f.plain(t, "ordinary direct", nostr.Now())
	first := f.deliver(t, plain, f.cache)
	require.Len(t, first, 1)
	require.NoError(t, first[0].Err)
	require.NotNil(t, first[0].Message)
	assert.Equal(t, "ordinary direct", first[0].Message.Content)
	assert.False(t, first[0].Message.IsEncrypted)

	second := f.deliver(t, plain, f.cache)
	assert.Empty(t, second, "warm plaintext duplicate is a no-op")
	stored, err := f.store.GetMessage(plain.ID.Hex())
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.Equal(t, "ordinary direct", stored.Plaintext)
	assert.Equal(t, plain.Content, stored.Content)
	assert.True(t, stored.IsIncoming)
	assert.Equal(t, 1, f.cache.liveCount())

	encBody := "encrypted direct"
	enc := f.encrypted(t, encBody, nostr.Now()+1)
	encFirst := f.deliver(t, enc, f.cache)
	require.Len(t, encFirst, 1)
	require.NotNil(t, encFirst[0].Message)
	assert.Equal(t, encBody, encFirst[0].Message.Content)
	assert.True(t, encFirst[0].Message.IsEncrypted)
	assert.True(t, encFirst[0].Message.Decrypted)
	assert.Empty(t, f.deliver(t, enc, f.cache))

	// A wrong recipient key fails closed inside NIP-44 decrypt. A warm hit
	// returns before that call, so the error never appears. Row equality is
	// only a supplement: StoreIncomingMessageOnce on an existing incoming row
	// is a conflict no-op and can leave the same bytes without proving the
	// write was skipped.
	before := f.saved(t, enc.ID.Hex())
	wrong := f.deliverSK(t, enc, f.cache, f.store, nostr.Generate())
	assert.Empty(t, wrong, "warm hit skips NIP-44 decrypt")
	assert.Equal(t, before, f.saved(t, enc.ID.Hex()))
	assert.Equal(t, 2, f.cache.liveCount())
}

func TestWatchCacheNilIsCandidateFullPathNotFrozenBaseline(t *testing.T) {
	// Nil uses the production nil-safe path on this candidate. It is a
	// diagnostic control only. The frozen 77dd tree remains the authoritative
	// external cost baseline and is not reconstructed here.
	f := newWatchFixture(t)
	event := f.encrypted(t, "baseline cost", nostr.Now())
	const n = 8
	notes := 0
	for i := 0; i < n; i++ {
		updates := f.deliver(t, event, nil)
		if len(updates) == 1 && updates[0].Message != nil {
			notes++
			assert.Equal(t, "baseline cost", updates[0].Message.Content)
			assert.True(t, updates[0].Message.IsEncrypted)
		} else {
			assert.Empty(t, updates)
		}
	}
	assert.Equal(t, 1, notes, "nil cache still notifies on the first store only")
	wrong := f.deliverSK(t, event, nil, f.store, nostr.Generate())
	require.Len(t, wrong, 1)
	require.Error(t, wrong[0].Err)
	assert.Nil(t, wrong[0].Message)
	assert.Contains(t, wrong[0].Err.Error(), "decrypt NIP-44", "nil cache still decrypts every duplicate")

	warm := newWatchValidatedCache(f.store, f.recipient.Npub)
	assert.Empty(t, f.deliver(t, event, warm))
	assert.Equal(t, 1, warm.liveCount())
	assert.Empty(t, f.deliverSK(t, event, warm, f.store, nostr.Generate()), "the same wrong key is silent once the watch has cached the event")
}

func TestWatchCacheRestartAndLegacyRowAreNotCertificates(t *testing.T) {
	f := newWatchFixture(t)
	event := f.encrypted(t, "cold replay", nostr.Now())
	require.NotEmpty(t, f.deliver(t, event, f.cache))
	require.Empty(t, f.deliver(t, event, f.cache))
	assert.Empty(t, f.deliverSK(t, event, f.cache, f.store, nostr.Generate()))

	restarted := newWatchValidatedCache(f.store, f.recipient.Npub)
	coldWrong := f.deliverSK(t, event, restarted, f.store, nostr.Generate())
	require.Len(t, coldWrong, 1)
	require.Error(t, coldWrong[0].Err)
	assert.Contains(t, coldWrong[0].Err.Error(), "decrypt NIP-44")
	assert.Nil(t, coldWrong[0].Message)
	assert.Zero(t, restarted.liveCount(), "a new watch has not cached the row yet")
	again := f.deliver(t, event, restarted)
	assert.Empty(t, again, "persisted duplicate still does not notify")
	assert.Equal(t, 1, restarted.liveCount(), "full decode plus the row fence may cache after the cold pass")
	assert.Empty(t, f.deliverSK(t, event, restarted, f.store, nostr.Generate()))

	legacyBody := "legacy-row-marker"
	legacy := f.encryptedRaw(t, "not-a-nip44-ciphertext", nostr.Now()+2)
	require.NoError(t, f.store.StoreIncomingMessage(&legacy, legacyBody, true))
	cold := newWatchValidatedCache(f.store, f.recipient.Npub)
	updates := f.deliver(t, legacy, cold)
	require.Len(t, updates, 1)
	require.Error(t, updates[0].Err)
	assert.Nil(t, updates[0].Message)
	assert.NotContains(t, updates[0].Err.Error(), legacyBody)
	assert.Contains(t, updates[0].Err.Error(), "decrypt NIP-44")
	assert.Zero(t, cold.liveCount())
	repeat := f.deliver(t, legacy, cold)
	require.Len(t, repeat, 1)
	require.Error(t, repeat[0].Err)
	assert.Contains(t, repeat[0].Err.Error(), "decrypt NIP-44")
	assert.Zero(t, cold.liveCount(), "a signed legacy row is not a validation certificate")
}

func TestWatchCacheRejectsMalformedAndReservedBeforeInsert(t *testing.T) {
	f := newWatchFixture(t)
	groupBody := "hyphae.group/v1\n{}"
	group := f.plain(t, groupBody, nostr.Now())
	require.NoError(t, f.store.StoreIncomingMessage(&group, groupBody, false))
	groupCache := newWatchValidatedCache(f.store, f.recipient.Npub)
	groupUpdates := f.deliver(t, group, groupCache)
	require.Len(t, groupUpdates, 1)
	assert.ErrorIs(t, groupUpdates[0].Err, ErrReservedGroupRequiresHandler)
	assert.Nil(t, groupUpdates[0].Message)
	assert.NotContains(t, groupUpdates[0].Err.Error(), groupBody)
	assert.Zero(t, groupCache.liveCount())
	repeatGroup := f.deliver(t, group, groupCache)
	require.Len(t, repeatGroup, 1)
	assert.ErrorIs(t, repeatGroup[0].Err, ErrReservedGroupRequiresHandler)
	assert.Zero(t, groupCache.liveCount())

	compressed := makeInboxEvent(t, f.recipientPK, f.sender, nostr.Now()+1, "aGVsbG8=", nostr.Tags{{"z", CompressTag}})
	require.NoError(t, f.store.StoreIncomingMessage(&compressed, "hello", false))
	compressCache := newWatchValidatedCache(f.store, f.recipient.Npub)
	compressUpdates := f.deliver(t, compressed, compressCache)
	require.Len(t, compressUpdates, 1)
	require.Error(t, compressUpdates[0].Err)
	assert.Nil(t, compressUpdates[0].Message)
	assert.Contains(t, compressUpdates[0].Err.Error(), "decompress zstd")
	assert.Zero(t, compressCache.liveCount())
	repeatCompressed := f.deliver(t, compressed, compressCache)
	require.Len(t, repeatCompressed, 1)
	require.Error(t, repeatCompressed[0].Err)
	assert.Contains(t, repeatCompressed[0].Err.Error(), "decompress zstd")
	assert.Zero(t, compressCache.liveCount())
}

func TestWatchCacheRowMismatchFallsThroughAndBadSignatureNeverHits(t *testing.T) {
	f := newWatchFixture(t)
	event := f.plain(t, "fence me", nostr.Now())
	require.Len(t, f.deliver(t, event, f.cache), 1)
	require.Empty(t, f.deliver(t, event, f.cache))

	badSig := event
	badSig.Sig[0] ^= 0xff
	sigUpdates := f.deliver(t, badSig, f.cache)
	require.Len(t, sigUpdates, 1)
	require.Error(t, sigUpdates[0].Err)
	assert.Contains(t, sigUpdates[0].Err.Error(), "invalid event ID or signature")
	assert.Nil(t, sigUpdates[0].Message)
	assert.Empty(t, f.deliver(t, event, f.cache), "a bad signature does not evict the known id")

	badID := event
	badID.ID[0] ^= 0xff
	idUpdates := f.deliver(t, badID, f.cache)
	require.Len(t, idUpdates, 1)
	require.Error(t, idUpdates[0].Err)
	assert.Contains(t, idUpdates[0].Err.Error(), "invalid event ID or signature")
	assert.Empty(t, f.deliver(t, event, f.cache))

	badKind := event
	badKind.Kind = 1
	kindUpdates := f.deliver(t, badKind, f.cache)
	require.Len(t, kindUpdates, 1)
	require.Error(t, kindUpdates[0].Err)
	assert.Contains(t, kindUpdates[0].Err.Error(), "unexpected event kind")
	assert.Empty(t, f.deliver(t, event, f.cache))

	db, err := storage.OpenDB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	res, err := db.Exec(`UPDATE messages SET content = ? WHERE id = ?`, "tampered-raw", event.ID.Hex())
	require.NoError(t, err)
	changed, err := res.RowsAffected()
	require.NoError(t, err)
	require.Equal(t, int64(1), changed)

	mismatch := f.deliver(t, event, f.cache)
	assert.Empty(t, mismatch, "changed incoming row is not a new notification")
	assert.Equal(t, "tampered-raw", f.saved(t, event.ID.Hex()).Content)
	again := f.deliver(t, event, f.cache)
	assert.Empty(t, again)
	assert.Equal(t, "tampered-raw", f.saved(t, event.ID.Hex()).Content, "the mismatched raw content stays in place")

	require.NoError(t, f.store.DeleteMessage(event.ID.Hex()))
	deleted := f.deliver(t, event, f.cache)
	require.Len(t, deleted, 1)
	require.NotNil(t, deleted[0].Message)
	assert.Equal(t, "fence me", deleted[0].Message.Content)
	warm := f.deliver(t, event, f.cache)
	assert.Empty(t, warm)

	enc := f.encrypted(t, "cipher fence", nostr.Now()+1)
	require.Len(t, f.deliver(t, enc, f.cache), 1)
	assert.Empty(t, f.deliverSK(t, enc, f.cache, f.store, nostr.Generate()))
	encBad := enc
	encBad.Sig[0] ^= 0xff
	encSig := f.deliver(t, encBad, f.cache)
	require.Len(t, encSig, 1)
	require.Error(t, encSig[0].Err)
	assert.Contains(t, encSig[0].Err.Error(), "invalid event ID or signature")
	assert.Empty(t, f.deliverSK(t, enc, f.cache, f.store, nostr.Generate()), "known encrypted id stays a hit after a bad signature")

	_, err = db.Exec(`UPDATE messages SET content = ? WHERE id = ?`, "tampered-cipher", enc.ID.Hex())
	require.NoError(t, err)
	miss := f.deliverSK(t, enc, f.cache, f.store, nostr.Generate())
	require.Len(t, miss, 1)
	require.Error(t, miss[0].Err)
	assert.Nil(t, miss[0].Message)
	assert.Contains(t, miss[0].Err.Error(), "decrypt NIP-44", "mutated ciphertext misses the fence and reaches decrypt")
	assert.Equal(t, "tampered-cipher", f.saved(t, enc.ID.Hex()).Content)
}

func TestWatchCacheOutgoingAndForeignFlagsMiss(t *testing.T) {
	f := newWatchFixture(t)
	outgoing := f.plain(t, "was outgoing", nostr.Now())
	require.NoError(t, f.store.StoreOutgoingMessage(&outgoing, f.recipient.Npub, "was outgoing", false))
	cold := newWatchValidatedCache(f.store, f.recipient.Npub)
	updates := f.deliver(t, outgoing, cold)
	require.Len(t, updates, 1)
	require.NotNil(t, updates[0].Message, "outgoing row must take the full upgrade path")
	assert.Equal(t, "was outgoing", updates[0].Message.Content)
	assert.Equal(t, 1, cold.liveCount())
	assert.Empty(t, f.deliver(t, outgoing, cold))

	db, err := storage.OpenDB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	kept := f.plain(t, "drop incoming flag", nostr.Now()+1)
	require.Len(t, f.deliver(t, kept, f.cache), 1)
	_, err = db.Exec(`UPDATE messages SET is_incoming = 0 WHERE id = ?`, kept.ID.Hex())
	require.NoError(t, err)
	upgraded := f.deliver(t, kept, f.cache)
	require.Len(t, upgraded, 1)
	require.NotNil(t, upgraded[0].Message, "incoming flag mismatch uses the full path")
	assert.Equal(t, "drop incoming flag", upgraded[0].Message.Content)

	encFlag := f.plain(t, "flip encryption", nostr.Now()+2)
	require.Len(t, f.deliver(t, encFlag, f.cache), 1)
	_, err = db.Exec(`UPDATE messages SET is_encrypted = 1 WHERE id = ?`, encFlag.ID.Hex())
	require.NoError(t, err)
	assert.Empty(t, f.deliver(t, encFlag, f.cache))
	assert.True(t, f.saved(t, encFlag.ID.Hex()).IsEncrypted, "conflict no-op leaves the flipped flag")

	sender := f.plain(t, "flip sender", nostr.Now()+3)
	require.Len(t, f.deliver(t, sender, f.cache), 1)
	_, err = db.Exec(`UPDATE messages SET sender_npub = ? WHERE id = ?`, watchForeignNpub, sender.ID.Hex())
	require.NoError(t, err)
	assert.Empty(t, f.deliver(t, sender, f.cache))
	assert.Equal(t, watchForeignNpub, f.saved(t, sender.ID.Hex()).SenderNpub)

	foreignEvent := f.plain(t, "flip recipient", nostr.Now()+4)
	require.Len(t, f.deliver(t, foreignEvent, f.cache), 1)
	_, err = db.Exec(`UPDATE messages SET recipient_npub = ? WHERE id = ?`, watchForeignNpub, foreignEvent.ID.Hex())
	require.NoError(t, err)
	foreign := f.deliver(t, foreignEvent, f.cache)
	require.Len(t, foreign, 1)
	require.Error(t, foreign[0].Err)
	assert.Nil(t, foreign[0].Message)
	assert.Contains(t, foreign[0].Err.Error(), "different recipient")

	enc := f.encrypted(t, "flag and sender", nostr.Now()+5)
	require.Len(t, f.deliver(t, enc, f.cache), 1)
	assert.Empty(t, f.deliverSK(t, enc, f.cache, f.store, nostr.Generate()))
	_, err = db.Exec(`UPDATE messages SET is_encrypted = 0 WHERE id = ?`, enc.ID.Hex())
	require.NoError(t, err)
	flagMiss := f.deliverSK(t, enc, f.cache, f.store, nostr.Generate())
	require.Len(t, flagMiss, 1)
	require.Error(t, flagMiss[0].Err)
	assert.Contains(t, flagMiss[0].Err.Error(), "decrypt NIP-44")
	assert.False(t, f.saved(t, enc.ID.Hex()).IsEncrypted)

	senderEnc := f.encrypted(t, "encrypted sender", nostr.Now()+6)
	require.Len(t, f.deliver(t, senderEnc, f.cache), 1)
	_, err = db.Exec(`UPDATE messages SET sender_npub = ? WHERE id = ?`, watchForeignNpub, senderEnc.ID.Hex())
	require.NoError(t, err)
	senderMiss := f.deliverSK(t, senderEnc, f.cache, f.store, nostr.Generate())
	require.Len(t, senderMiss, 1)
	require.Error(t, senderMiss[0].Err)
	assert.Contains(t, senderMiss[0].Err.Error(), "decrypt NIP-44")
	assert.Equal(t, watchForeignNpub, f.saved(t, senderEnc.ID.Hex()).SenderNpub)
}

func TestWatchCachePrivateWrongStoreAndRecipientMiss(t *testing.T) {
	// Defensive private-API coverage. WatchAgentInboxWithStore binds one store
	// pointer and the local owner for the whole watch. A different pointer or
	// recipient must miss. This is not a public exploit.
	f := newWatchFixture(t)
	event := f.encrypted(t, "bound call", nostr.Now())
	require.Len(t, f.deliver(t, event, f.cache), 1)
	assert.Empty(t, f.deliverSK(t, event, f.cache, f.store, nostr.Generate()))
	require.True(t, f.cache.duplicateHit(f.store, f.recipient.Npub, &event))

	otherStore := f.otherStore(t)
	require.False(t, f.cache.duplicateHit(otherStore, f.recipient.Npub, &event))
	require.False(t, f.cache.duplicateHit(f.store, watchForeignNpub, &event))
	require.True(t, f.cache.duplicateHit(f.store, f.recipient.Npub, &event))

	wrongStore := f.deliverSK(t, event, f.cache, otherStore, nostr.Generate())
	require.Len(t, wrongStore, 1)
	require.Error(t, wrongStore[0].Err)
	assert.Contains(t, wrongStore[0].Err.Error(), "decrypt NIP-44", "another store pointer must not reuse this watch hit")
	assert.Empty(t, f.deliverSK(t, event, f.cache, f.store, nostr.Generate()), "the owner binding is unchanged")

	unrelated := f.encrypted(t, "unrelated row", nostr.Now()+1)
	firstUnrelated := f.deliver(t, unrelated, nil)
	require.Len(t, firstUnrelated, 1)
	require.NotNil(t, firstUnrelated[0].Message)
	refused := newWatchValidatedCache(f.store, f.recipient.Npub)
	refused.remember(otherStore, f.recipient.Npub, &unrelated, true)
	refused.remember(f.store, watchForeignNpub, &unrelated, true)
	assert.False(t, refused.hasLive(unrelated.ID.Hex()))
	assert.False(t, refused.duplicateHit(f.store, f.recipient.Npub, &unrelated))
	refused.remember(f.store, f.recipient.Npub, &unrelated, true)
	assert.True(t, refused.hasLive(unrelated.ID.Hex()))
	assert.False(t, refused.duplicateHit(otherStore, f.recipient.Npub, &unrelated))
	assert.False(t, refused.duplicateHit(f.store, watchForeignNpub, &unrelated))
	assert.True(t, refused.duplicateHit(f.store, f.recipient.Npub, &unrelated))

	misbound := newWatchValidatedCache(f.store, watchForeignNpub)
	noted := f.deliver(t, unrelated, misbound)
	assert.Empty(t, noted, "the row already exists, so a miss does not notify again")
	assert.False(t, misbound.hasLive(unrelated.ID.Hex()), "remember rejects a recipient other than the captured owner")
	miss := f.deliverSK(t, unrelated, misbound, f.store, nostr.Generate())
	require.Len(t, miss, 1)
	require.Error(t, miss[0].Err)
	assert.Contains(t, miss[0].Err.Error(), "decrypt NIP-44")
}

func TestWatchCacheBoundsEvictWithoutABA(t *testing.T) {
	f := newWatchFixture(t)
	cache := f.cache
	insert := func(id, content string, encrypted bool) {
		t.Helper()
		cache.mu.Lock()
		cache.insertLocked(id, "sender", content, encrypted)
		cache.mu.Unlock()
	}
	first := fmt.Sprintf("%064d", 0)
	for i := 0; i < watchCacheMaxEntries; i++ {
		insert(fmt.Sprintf("%064d", i), "body", false)
	}
	require.Equal(t, watchCacheMaxEntries, cache.liveCount())
	require.True(t, cache.hasLive(first))
	insert(first, "body", false)
	insert(fmt.Sprintf("%064d", watchCacheMaxEntries), "fresh", false)
	require.True(t, cache.hasLive(first), "stale FIFO slot must not drop the refreshed id")
	require.False(t, cache.hasLive(fmt.Sprintf("%064d", 1)))
	require.Equal(t, watchCacheMaxEntries, cache.liveCount())
	require.LessOrEqual(t, cache.retainedBytes(), watchCacheMaxBytes)

	bytesCache := newWatchValidatedCache(f.store, f.recipient.Npub)
	chunk := strings.Repeat("a", 1<<20)
	put := func(id string, content string) {
		t.Helper()
		bytesCache.mu.Lock()
		bytesCache.insertLocked(id, "sender", content, true)
		bytesCache.mu.Unlock()
	}
	for i := 0; i < 4; i++ {
		put(fmt.Sprintf("byte-%d", i), chunk)
	}
	require.Equal(t, watchCacheMaxBytes, bytesCache.retainedBytes())
	require.Equal(t, 4, bytesCache.liveCount())
	put("byte-4", chunk)
	require.Equal(t, watchCacheMaxBytes, bytesCache.retainedBytes())
	require.Equal(t, 4, bytesCache.liveCount())
	require.False(t, bytesCache.hasLive("byte-0"))
	require.True(t, bytesCache.hasLive("byte-4"))

	over := strings.Repeat("b", watchCacheMaxBytes+1)
	beforeLive := bytesCache.liveCount()
	beforeBytes := bytesCache.retainedBytes()
	put("too-big", over)
	require.False(t, bytesCache.hasLive("too-big"))
	require.Equal(t, beforeLive, bytesCache.liveCount())
	require.Equal(t, beforeBytes, bytesCache.retainedBytes())
}

func TestWatchCacheTwoRelaysOneInsert(t *testing.T) {
	f := newWatchFixture(t)
	event := f.encrypted(t, "one insert", nostr.Now())
	start := make(chan struct{})
	var wg sync.WaitGroup
	var mu sync.Mutex
	notes := 0
	errs := 0
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			var updates []AgentInboxWatchUpdate
			storeIncomingWatchEvent(&event, f.recipient, f.recipientSK, f.recipientPK.Hex(), f.store, "ws://127.0.0.1:1", func(update AgentInboxWatchUpdate) {
				updates = append(updates, update)
			}, f.cache)
			mu.Lock()
			defer mu.Unlock()
			for _, update := range updates {
				if update.Message != nil {
					notes++
					assert.Equal(t, "one insert", update.Message.Content)
				}
				if update.Err != nil {
					errs++
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	assert.Equal(t, 1, notes)
	assert.Zero(t, errs)
	assert.Equal(t, 1, f.cache.liveCount())
	assert.Empty(t, f.deliverSK(t, event, f.cache, f.store, nostr.Generate()), "the single insert is cached for both callers")
	stored, err := f.store.GetMessage(event.ID.Hex())
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.Equal(t, event.Content, stored.Content)
	assert.True(t, stored.IsIncoming)
	assert.True(t, stored.IsEncrypted)
}

func TestWatchProcessesFourBy129BeforeStop(t *testing.T) {
	// Four direct relay workers share one test-built cache. There is no
	// context seam. Waiting for the workers only proves that calls which
	// entered storeIncomingWatchEvent have returned. Sentinel arrival is not
	// a FIFO proof that all 128 replays were consumed; that count stays out
	// of this deliverable.
	f := newWatchFixture(t)
	const replays = 128
	const workers = 4
	event := f.plain(t, "replay-body", nostr.Now()-10)
	sentinel := makeInboxEvent(t, f.recipientPK, f.sender, nostr.Now(), "aGVsbG8=", nostr.Tags{{"z", CompressTag}})
	url := startReplayRelay(t, event, sentinel, replays)
	cache := newWatchValidatedCache(f.store, f.recipient.Npub)
	filter := BuildAgentMessageFilter(f.recipientPK.Hex())

	watchCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	updates := make(chan AgentInboxWatchUpdate, 256)
	var relayWorkers sync.WaitGroup
	for i := 0; i < workers; i++ {
		relayWorkers.Add(1)
		go func() {
			defer relayWorkers.Done()
			watchAgentInboxRelay(watchCtx, url, filter, f.recipient, f.recipientSK, f.recipientPK.Hex(), f.store, func(update AgentInboxWatchUpdate) {
				updates <- update
			}, cache)
		}()
	}

	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	notes := 0
	barriers := 0
	for notes < 1 || barriers < workers {
		select {
		case update := <-updates:
			if update.Message != nil {
				notes++
				assert.Equal(t, event.ID.Hex(), update.Message.EventID)
				assert.Equal(t, "replay-body", update.Message.Content)
			}
			if update.Err != nil && strings.Contains(update.Err.Error(), sentinel.ID.Hex()) {
				barriers++
			}
		case <-deadline.C:
			cancel()
			t.Fatalf("notes=%d barriers=%d before deadline", notes, barriers)
		}
	}
	cancel()
	finished := make(chan struct{})
	go func() {
		relayWorkers.Wait()
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("watch did not return")
	}
	for {
		select {
		case update := <-updates:
			if update.Message != nil {
				notes++
				assert.Equal(t, event.ID.Hex(), update.Message.EventID)
				assert.Equal(t, "replay-body", update.Message.Content)
			}
			if update.Err != nil && strings.Contains(update.Err.Error(), sentinel.ID.Hex()) {
				barriers++
			}
		default:
			goto drained
		}
	}
drained:
	assert.Equal(t, 1, notes)
	assert.Equal(t, workers, barriers)
	assert.True(t, cache.hasLive(event.ID.Hex()))
	stored, err := f.store.GetMessage(event.ID.Hex())
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.Equal(t, "replay-body", stored.Plaintext)
	assert.Equal(t, event.Content, stored.Content)
	assert.True(t, stored.IsIncoming)
	sentinelRow, err := f.store.GetMessage(sentinel.ID.Hex())
	require.NoError(t, err)
	assert.Nil(t, sentinelRow)
}

func (c *watchValidatedCache) liveCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.live
}

func (c *watchValidatedCache) retainedBytes() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.bytes
}

func (c *watchValidatedCache) hasLive(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.byID[id]
	return ok
}

func startReplayRelay(t *testing.T, event, sentinel nostr.Event, repeats int) string {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var fields []json.RawMessage
			if json.Unmarshal(raw, &fields) != nil || len(fields) < 3 {
				continue
			}
			var command, sub string
			var filter nostr.Filter
			if json.Unmarshal(fields[0], &command) != nil || command != "REQ" {
				continue
			}
			if json.Unmarshal(fields[1], &sub) != nil || json.Unmarshal(fields[2], &filter) != nil {
				return
			}
			if filter.Limit == 0 {
				for i := 0; i < repeats; i++ {
					if conn.WriteJSON([]any{"EVENT", sub, event}) != nil {
						return
					}
				}
				if conn.WriteJSON([]any{"EVENT", sub, sentinel}) != nil {
					return
				}
			}
			if conn.WriteJSON([]any{"EOSE", sub}) != nil {
				return
			}
		}
	}))
	t.Cleanup(server.Close)
	return "ws" + strings.TrimPrefix(server.URL, "http")
}

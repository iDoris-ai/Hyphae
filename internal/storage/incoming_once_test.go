package storage

import (
	"database/sql"
	"encoding/hex"
	"errors"
	"sync"
	"testing"

	"fiatjaf.com/nostr"
	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func makeIncomingEvent(recipient nostr.PubKey, idByte byte) *nostr.Event {
	sender := nostr.Generate()
	event := &nostr.Event{PubKey: sender.Public(), Content: "ciphertext", Tags: nostr.Tags{{"p", hex.EncodeToString(recipient[:])}}}
	for i := range event.ID {
		event.ID[i] = idByte
	}
	return event
}

func TestStoreIncomingMessageOnceAndDuplicateDoesNotChangePlaintext(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()
	recipient := nostr.Generate().Public()
	npub := common.EncodeNpub(recipient)
	event := makeIncomingEvent(recipient, 1)

	first, err := store.StoreIncomingMessageOnce(event, npub, "first plaintext", true)
	require.NoError(t, err)
	assert.True(t, first)
	second, err := store.StoreIncomingMessageOnce(event, npub, "replacement plaintext", true)
	require.NoError(t, err)
	assert.False(t, second)
	msg, err := store.GetMessage(hex.EncodeToString(event.ID[:]))
	require.NoError(t, err)
	require.NotNil(t, msg)
	assert.Equal(t, "first plaintext", msg.Plaintext)
	assert.True(t, msg.IsIncoming)
}

func TestStoreIncomingMessageOnceUpgradesOutgoingAndNeverDowngrades(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()
	recipient := nostr.Generate().Public()
	npub := common.EncodeNpub(recipient)
	event := makeIncomingEvent(recipient, 2)
	require.NoError(t, store.StoreOutgoingMessage(event, npub, "sent plaintext", false))

	first, err := store.StoreIncomingMessageOnce(event, npub, "arrived plaintext", false)
	require.NoError(t, err)
	assert.True(t, first)
	require.NoError(t, store.StoreOutgoingMessage(event, npub, "", false))
	msg, err := store.GetMessage(hex.EncodeToString(event.ID[:]))
	require.NoError(t, err)
	require.NotNil(t, msg)
	assert.True(t, msg.IsIncoming)
	assert.Equal(t, "arrived plaintext", msg.Plaintext)
}

func TestStoreIncomingMessageOnceRejectsDifferentRecipientWithoutOverwrite(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()
	firstRecipient := nostr.Generate().Public()
	otherRecipient := nostr.Generate().Public()
	event := makeIncomingEvent(firstRecipient, 3)
	firstNpub := common.EncodeNpub(firstRecipient)
	otherNpub := common.EncodeNpub(otherRecipient)
	require.NoError(t, store.StoreOutgoingMessage(event, firstNpub, "original", false))
	conflictingEvent := *event
	conflictingEvent.Tags = nostr.Tags{{"p", hex.EncodeToString(otherRecipient[:])}}
	// The API assumes event ID/signature verification happened above storage;
	// this fixtures a legacy/corrupt same-ID row with a different recipient.
	_, err := store.StoreIncomingMessageOnce(&conflictingEvent, otherNpub, "overwrite", false)
	require.ErrorContains(t, err, "different recipient")
	msg, err := store.GetMessage(hex.EncodeToString(event.ID[:]))
	require.NoError(t, err)
	require.NotNil(t, msg)
	assert.Equal(t, firstNpub, msg.RecipientNpub)
	assert.Equal(t, "original", msg.Plaintext)
}

func TestStoreIncomingMessageOnceValidatesRecipientTag(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()
	recipient := nostr.Generate().Public()
	npub := common.EncodeNpub(recipient)
	event := makeIncomingEvent(recipient, 4)
	mismatchKey := nostr.Generate().Public()
	cases := []struct {
		name string
		tags nostr.Tags
		npub string
	}{
		{name: "missing", tags: nil, npub: npub},
		{name: "duplicate", tags: nostr.Tags{{"p", hex.EncodeToString(recipient[:])}, {"p", hex.EncodeToString(recipient[:])}}, npub: npub},
		{name: "invalid hex", tags: nostr.Tags{{"p", "not-hex"}}, npub: npub},
		{name: "mismatch", tags: nostr.Tags{{"p", hex.EncodeToString(mismatchKey[:])}}, npub: npub},
		{name: "invalid recipient", tags: event.Tags, npub: "not-a-key"},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			trial := *event
			trial.Tags = tc.tags
			for j := range trial.ID {
				trial.ID[j] = byte(10 + i)
			}
			_, err := store.StoreIncomingMessageOnce(&trial, tc.npub, "plain", false)
			require.Error(t, err)
		})
	}
	withRelayHint := *event
	withRelayHint.ID[0] = 8
	withRelayHint.Tags = nostr.Tags{{"p", hex.EncodeToString(recipient[:]), "wss://relay.example"}}
	first, err := store.StoreIncomingMessageOnce(&withRelayHint, npub, "plain", false)
	require.NoError(t, err)
	assert.True(t, first)
}

func TestStoreIncomingMessageOnceConcurrentAndAfterReopen(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("HOME", tempDir)
	db, err := InitDB()
	require.NoError(t, err)
	store := NewMessageStore(db)
	recipient := nostr.Generate().Public()
	npub := common.EncodeNpub(recipient)
	event := makeIncomingEvent(recipient, 5)
	const workers = 16
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstCount int
	var errs []error
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			first, err := store.StoreIncomingMessageOnce(event, npub, "same plaintext", false)
			mu.Lock()
			defer mu.Unlock()
			if first {
				firstCount++
			}
			if err != nil {
				errs = append(errs, err)
			}
		}()
	}
	wg.Wait()
	require.Empty(t, errs)
	assert.Equal(t, 1, firstCount)
	require.NoError(t, db.Close())

	db, err = InitDB()
	require.NoError(t, err)
	defer db.Close()
	reopened := NewMessageStore(db)
	first, err := reopened.StoreIncomingMessageOnce(event, npub, "changed plaintext", false)
	require.NoError(t, err)
	assert.False(t, first)
	msg, err := reopened.GetMessage(hex.EncodeToString(event.ID[:]))
	require.NoError(t, err)
	require.NotNil(t, msg)
	assert.Equal(t, "same plaintext", msg.Plaintext)
}

type failingMessageExecutor struct{}

func (failingMessageExecutor) Exec(string, ...any) (sql.Result, error) {
	return nil, errors.New("disk unavailable")
}
func (failingMessageExecutor) Query(string, ...any) (*sql.Rows, error) {
	return nil, errors.New("disk unavailable")
}
func (failingMessageExecutor) QueryRow(string, ...any) *sql.Row { return nil }

func TestStoreIncomingMessageOnceReturnsStorageFailure(t *testing.T) {
	recipient := nostr.Generate().Public()
	event := makeIncomingEvent(recipient, 6)
	store := NewMessageStore(failingMessageExecutor{})
	first, err := store.StoreIncomingMessageOnce(event, common.EncodeNpub(recipient), "plain", false)
	require.ErrorContains(t, err, "disk unavailable")
	assert.False(t, first)
}

func TestStoreIncomingMessageOnceKeepsExistingPlaintextOnEmptyUpdate(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()
	recipient := nostr.Generate().Public()
	npub := common.EncodeNpub(recipient)
	event := makeIncomingEvent(recipient, 7)
	first, err := store.StoreIncomingMessageOnce(event, npub, "decrypted", true)
	require.NoError(t, err)
	assert.True(t, first)
	require.NoError(t, store.StoreMessage(&types.StoredMessage{ID: hex.EncodeToString(event.ID[:]), SenderNpub: common.EncodeNpub(event.PubKey), RecipientNpub: npub, IsIncoming: false}))
	msg, err := store.GetMessage(hex.EncodeToString(event.ID[:]))
	require.NoError(t, err)
	require.NotNil(t, msg)
	assert.Equal(t, "decrypted", msg.Plaintext)
	assert.True(t, msg.IsIncoming)
}

package storage

import (
	"os"
	"testing"

	"fiatjaf.com/nostr"
	"github.com/jason/agent-speaker/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestDB(t *testing.T) (*MessageStore, func()) {
	tempDir := t.TempDir()
	os.Setenv("HOME", tempDir)

	db, err := InitDB()
	require.NoError(t, err)

	store := NewMessageStore(db)

	cleanup := func() {
		db.Close()
		os.RemoveAll(tempDir)
		os.Unsetenv("HOME")
	}

	return store, cleanup
}

func TestStoreMessage(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	msg := &types.StoredMessage{
		ID:            "test-msg-1",
		SenderNpub:    "npub1sender",
		RecipientNpub: "npub1recipient",
		Content:       "encrypted content",
		Plaintext:     "Hello World",
		CreatedAt:     1234567890,
		IsEncrypted:   true,
		IsIncoming:    false,
		Relay:         "wss://relay.test",
	}

	err := store.StoreMessage(msg)
	require.NoError(t, err)

	// Verify stored
	retrieved, err := store.GetMessage("test-msg-1")
	require.NoError(t, err)
	assert.NotNil(t, retrieved)
	assert.Equal(t, msg.SenderNpub, retrieved.SenderNpub)
	assert.Equal(t, msg.Plaintext, retrieved.Plaintext)
}

func TestGetConversation(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	alice := "npub1alice"
	bob := "npub1bob"

	// Store messages in conversation
	messages := []*types.StoredMessage{
		{
			ID:            "msg-1",
			SenderNpub:    alice,
			RecipientNpub: bob,
			Plaintext:     "Hi Bob",
			CreatedAt:     1000,
			IsIncoming:    false,
		},
		{
			ID:            "msg-2",
			SenderNpub:    bob,
			RecipientNpub: alice,
			Plaintext:     "Hi Alice",
			CreatedAt:     2000,
			IsIncoming:    true,
		},
		{
			ID:            "msg-3",
			SenderNpub:    alice,
			RecipientNpub: bob,
			Plaintext:     "How are you?",
			CreatedAt:     3000,
			IsIncoming:    false,
		},
		{
			ID:            "msg-4",
			SenderNpub:    "npub1other",
			RecipientNpub: alice,
			Plaintext:     "Unrelated",
			CreatedAt:     4000,
			IsIncoming:    true,
		},
	}

	for _, msg := range messages {
		err := store.StoreMessage(msg)
		require.NoError(t, err)
	}

	// Get conversation
	conv, err := store.GetConversation(alice, bob, 10)
	require.NoError(t, err)
	assert.Len(t, conv, 3)

	// Should be sorted by created_at DESC
	assert.Equal(t, "msg-3", conv[0].ID)
	assert.Equal(t, "msg-1", conv[2].ID)
}

func TestGetInbox(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	user := "npub1user"

	messages := []*types.StoredMessage{
		{
			ID:            "inbox-1",
			SenderNpub:    "npub1sender1",
			RecipientNpub: user,
			Plaintext:     "Message 1",
			CreatedAt:     1000,
			IsIncoming:    true,
		},
		{
			ID:            "inbox-2",
			SenderNpub:    "npub1sender2",
			RecipientNpub: user,
			Plaintext:     "Message 2",
			CreatedAt:     2000,
			IsIncoming:    true,
		},
		{
			ID:            "outbox-1",
			SenderNpub:    user,
			RecipientNpub: "npub1recipient",
			Plaintext:     "Sent message",
			CreatedAt:     3000,
			IsIncoming:    false,
		},
	}

	for _, msg := range messages {
		err := store.StoreMessage(msg)
		require.NoError(t, err)
	}

	inbox, err := store.GetInbox(user, 10)
	require.NoError(t, err)
	assert.Len(t, inbox, 2)

	// Should be sorted by created_at DESC
	assert.Equal(t, "inbox-2", inbox[0].ID)
}

func TestSearchMessages(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	user := "npub1user"

	messages := []*types.StoredMessage{
		{
			ID:            "search-1",
			SenderNpub:    "npub1sender",
			RecipientNpub: user,
			Plaintext:     "Hello world",
			Content:       "encrypted",
			CreatedAt:     1000,
		},
		{
			ID:            "search-2",
			SenderNpub:    user,
			RecipientNpub: "npub1recipient",
			Plaintext:     "Goodbye world",
			Content:       "encrypted",
			CreatedAt:     2000,
		},
		{
			ID:            "search-3",
			SenderNpub:    "npub1other",
			RecipientNpub: user,
			Plaintext:     "Something else",
			Content:       "encrypted",
			CreatedAt:     3000,
		},
	}

	for _, msg := range messages {
		err := store.StoreMessage(msg)
		require.NoError(t, err)
	}

	// Search for "world"
	results, err := store.SearchMessages(user, "world", 10)
	require.NoError(t, err)
	assert.Len(t, results, 2)

	// Search for "Hello"
	results, err = store.SearchMessages(user, "Hello", 10)
	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, "search-1", results[0].ID)
}

func TestGetStats(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	user := "npub1user"

	messages := []*types.StoredMessage{
		{
			ID:            "stats-1",
			SenderNpub:    user,
			RecipientNpub: "npub1recipient",
			Plaintext:     "Outgoing 1",
			CreatedAt:     1000,
			IsEncrypted:   true,
			IsIncoming:    false,
		},
		{
			ID:            "stats-2",
			SenderNpub:    "npub1sender",
			RecipientNpub: user,
			Plaintext:     "Incoming 1",
			CreatedAt:     2000,
			IsEncrypted:   true,
			IsIncoming:    true,
		},
		{
			ID:            "stats-3",
			SenderNpub:    user,
			RecipientNpub: "npub1recipient2",
			Plaintext:     "Outgoing 2 plain",
			CreatedAt:     3000,
			IsEncrypted:   false,
			IsIncoming:    false,
		},
	}

	for _, msg := range messages {
		err := store.StoreMessage(msg)
		require.NoError(t, err)
	}

	stats, err := store.GetStats(user)
	require.NoError(t, err)

	assert.Equal(t, 3, stats["total"])
	assert.Equal(t, 1, stats["incoming"])
	assert.Equal(t, 2, stats["outgoing"])
	assert.Equal(t, 2, stats["encrypted"])
}

func TestDeleteMessage(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	msg := &types.StoredMessage{
		ID:            "delete-me",
		SenderNpub:    "npub1sender",
		RecipientNpub: "npub1recipient",
		Plaintext:     "To be deleted",
		CreatedAt:     1000,
	}

	err := store.StoreMessage(msg)
	require.NoError(t, err)

	// Verify exists
	retrieved, err := store.GetMessage("delete-me")
	require.NoError(t, err)
	assert.NotNil(t, retrieved)

	// Delete
	err = store.DeleteMessage("delete-me")
	require.NoError(t, err)

	// Verify deleted
	retrieved, err = store.GetMessage("delete-me")
	require.NoError(t, err)
	assert.Nil(t, retrieved)
}

func TestStoreOutgoingMessage(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	// Create a mock event
	var sk nostr.SecretKey
	sk[0] = 1 // Just a test key
	event := &nostr.Event{
		PubKey:    sk.Public(),
		Content:   "encrypted-content",
		CreatedAt: 1234567890,
	}
	// Set ID after other fields
	copy(event.ID[:], []byte("event-id-1234567890123456789012"))

	err := store.StoreOutgoingMessage(event, "npub1recipient", "plaintext message", true)
	require.NoError(t, err)

	// Verify
	retrieved, err := store.GetMessage(string(event.ID[:]))
	require.NoError(t, err)
	assert.NotNil(t, retrieved)
	assert.Equal(t, "plaintext message", retrieved.Plaintext)
	assert.True(t, retrieved.IsEncrypted)
	assert.False(t, retrieved.IsIncoming)
}

func TestStoreIncomingMessage(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	var sk nostr.SecretKey
	sk[0] = 1

	// Create event with p tag
	event := &nostr.Event{
		PubKey:    sk.Public(),
		Content:   "encrypted",
		CreatedAt: 1234567890,
		Tags:      nostr.Tags{{"p", "c4db8e615a9a056f6a47df26a999fa7d88a735baedba8819ef87814daeaef49a"}},
	}
	copy(event.ID[:], []byte("incoming-event-123456789012"))

	err := store.StoreIncomingMessage(event, "decrypted message", true)
	require.NoError(t, err)

	// Verify
	retrieved, err := store.GetMessage(string(event.ID[:]))
	require.NoError(t, err)
	assert.NotNil(t, retrieved)
	assert.Equal(t, "decrypted message", retrieved.Plaintext)
	assert.True(t, retrieved.IsIncoming)
}

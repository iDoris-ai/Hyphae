package messaging

import (
	"testing"

	"fiatjaf.com/nostr"
	"github.com/AuraAIHQ/agent-speaker/internal/storage"
	"github.com/AuraAIHQ/agent-speaker/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func resetStore(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	// Close and clear global DB
	if storage.DB != nil {
		_ = storage.CloseDB()
		storage.DB = nil
	}

	// Reset package-level globals (sync.Once replaced with mutex, so this is safe)
	storeMu.Lock()
	store = nil
	storeErr = nil
	storeMu.Unlock()

	require.NoError(t, InitStorage())
}

func TestLoadMessageStore_New(t *testing.T) {
	resetStore(t)
	ms, err := LoadMessageStore()
	require.NoError(t, err)
	assert.Empty(t, ms.Messages)
}

func TestAddAndLoadMessage(t *testing.T) {
	resetStore(t)
	ms, err := LoadMessageStore()
	require.NoError(t, err)

	msg := types.StoredMessage{
		ID:            "msg1",
		SenderNpub:    "npub1alice",
		RecipientNpub: "npub1bob",
		Content:       "Hello",
		Plaintext:     "Hello",
		CreatedAt:     1000,
		IsEncrypted:   false,
		IsIncoming:    false,
	}
	err = AddMessage(ms, msg)
	require.NoError(t, err)

	s, err := GetStore()
	require.NoError(t, err)
	loaded, err := s.GetMessage("msg1")
	require.NoError(t, err)
	require.NotNil(t, loaded)
	assert.Equal(t, "Hello", loaded.Content)
	assert.NotZero(t, loaded.ReceivedAt)
}

func TestGetConversation(t *testing.T) {
	resetStore(t)
	ms, _ := LoadMessageStore()

	msgs := []types.StoredMessage{
		{ID: "1", SenderNpub: "npub1alice", RecipientNpub: "npub1bob", Content: "Hi", CreatedAt: 3000},
		{ID: "2", SenderNpub: "npub1bob", RecipientNpub: "npub1alice", Content: "Hey", CreatedAt: 2000},
		{ID: "3", SenderNpub: "npub1alice", RecipientNpub: "npub1jack", Content: "Yo", CreatedAt: 1000},
	}
	for _, m := range msgs {
		require.NoError(t, AddMessage(ms, m))
	}

	conv, err := GetConversation(ms, "npub1alice", "npub1bob", 10)
	require.NoError(t, err)
	require.Len(t, conv, 2)
	assert.Equal(t, "Hi", conv[0].Content) // newest first
	assert.Equal(t, "Hey", conv[1].Content)

	convLimited, err := GetConversation(ms, "npub1alice", "npub1bob", 1)
	require.NoError(t, err)
	require.Len(t, convLimited, 1)
	assert.Equal(t, "Hi", convLimited[0].Content)
}

func TestGetInbox(t *testing.T) {
	resetStore(t)
	ms, _ := LoadMessageStore()

	msgs := []types.StoredMessage{
		{ID: "1", SenderNpub: "npub1alice", RecipientNpub: "npub1bob", Content: "Hi", CreatedAt: 3000},
		{ID: "2", SenderNpub: "npub1jack", RecipientNpub: "npub1bob", Content: "Hey", CreatedAt: 2000},
		{ID: "3", SenderNpub: "npub1bob", RecipientNpub: "npub1alice", Content: "Yo", CreatedAt: 1000},
	}
	for _, m := range msgs {
		require.NoError(t, AddMessage(ms, m))
	}

	inbox, err := GetInbox(ms, "npub1bob", 10)
	require.NoError(t, err)
	require.Len(t, inbox, 2)
	assert.Equal(t, "Hi", inbox[0].Content)
	assert.Equal(t, "Hey", inbox[1].Content)
}

func TestGetReceivedCount(t *testing.T) {
	resetStore(t)
	ms, _ := LoadMessageStore()

	msgs := []types.StoredMessage{
		{ID: "1", SenderNpub: "npub1alice", RecipientNpub: "npub1bob"},
		{ID: "2", SenderNpub: "npub1jack", RecipientNpub: "npub1bob"},
		{ID: "3", SenderNpub: "npub1bob", RecipientNpub: "npub1alice"},
	}
	for _, m := range msgs {
		m.CreatedAt = 1000
		m.ReceivedAt = 1000
		require.NoError(t, AddMessage(ms, m))
	}

	count, err := GetReceivedCount(ms, "npub1bob")
	require.NoError(t, err)
	assert.Equal(t, 2, count)

	count, err = GetReceivedCount(ms, "npub1alice")
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

func TestStoreOutgoingMessage(t *testing.T) {
	resetStore(t)
	event := &nostr.Event{
		Kind:    30078,
		Content: "compressed content",
	}
	event.ID = [32]byte{1}
	event.PubKey = nostr.PubKey{}

	err := StoreOutgoingMessage(event, "npub1bob", "plaintext", true)
	require.NoError(t, err)

	s, err := GetStore()
	require.NoError(t, err)
	msg, err := s.GetMessage(string(event.ID[:]))
	require.NoError(t, err)
	require.NotNil(t, msg)
	assert.False(t, msg.IsIncoming)
	assert.True(t, msg.IsEncrypted)
}

func TestStoreIncomingMessage(t *testing.T) {
	resetStore(t)
	event := &nostr.Event{
		Kind:    30078,
		Content: "compressed content",
		Tags:    nostr.Tags{{"p", "b029a5dc3d6dd7fc6407949053ac55637faf49dba942af908e3ad5e938d30a1f"}},
	}
	event.ID = [32]byte{2}
	event.PubKey = nostr.PubKey{}

	err := StoreIncomingMessage(event, "plaintext", false)
	require.NoError(t, err)

	s, err := GetStore()
	require.NoError(t, err)
	msg, err := s.GetMessage(string(event.ID[:]))
	require.NoError(t, err)
	require.NotNil(t, msg)
	assert.True(t, msg.IsIncoming)
	assert.False(t, msg.IsEncrypted)
}

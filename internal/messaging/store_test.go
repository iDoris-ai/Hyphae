package messaging

import (
	"os"
	"testing"

	"fiatjaf.com/nostr"
	"github.com/AuraAIHQ/agent-speaker/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTempMessageStore(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("HOME", tmpDir)
}

func TestLoadMessageStore_New(t *testing.T) {
	setupTempMessageStore(t)
	ms, err := LoadMessageStore()
	require.NoError(t, err)
	assert.Empty(t, ms.Messages)
}

func TestAddAndLoadMessage(t *testing.T) {
	setupTempMessageStore(t)
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

	ms2, err := LoadMessageStore()
	require.NoError(t, err)
	assert.Len(t, ms2.Messages, 1)
	assert.Equal(t, "Hello", ms2.Messages[0].Content)
	assert.NotZero(t, ms2.Messages[0].ReceivedAt)
}

func TestGetConversation(t *testing.T) {
	ms := &types.MessageStore{
		Messages: []types.StoredMessage{
			{ID: "1", SenderNpub: "npub1alice", RecipientNpub: "npub1bob", Content: "Hi", CreatedAt: 3000},
			{ID: "2", SenderNpub: "npub1bob", RecipientNpub: "npub1alice", Content: "Hey", CreatedAt: 2000},
			{ID: "3", SenderNpub: "npub1alice", RecipientNpub: "npub1jack", Content: "Yo", CreatedAt: 1000},
		},
	}

	conv := GetConversation(ms, "npub1alice", "npub1bob", 10)
	require.Len(t, conv, 2)
	assert.Equal(t, "Hi", conv[0].Content) // newest first
	assert.Equal(t, "Hey", conv[1].Content)

	convLimited := GetConversation(ms, "npub1alice", "npub1bob", 1)
	require.Len(t, convLimited, 1)
	assert.Equal(t, "Hi", convLimited[0].Content)
}

func TestGetInbox(t *testing.T) {
	ms := &types.MessageStore{
		Messages: []types.StoredMessage{
			{ID: "1", SenderNpub: "npub1alice", RecipientNpub: "npub1bob", Content: "Hi", CreatedAt: 3000},
			{ID: "2", SenderNpub: "npub1jack", RecipientNpub: "npub1bob", Content: "Hey", CreatedAt: 2000},
			{ID: "3", SenderNpub: "npub1bob", RecipientNpub: "npub1alice", Content: "Yo", CreatedAt: 1000},
		},
	}

	inbox := GetInbox(ms, "npub1bob", 10)
	require.Len(t, inbox, 2)
	assert.Equal(t, "Hi", inbox[0].Content)
	assert.Equal(t, "Hey", inbox[1].Content)
}

func TestGetUnreadCount(t *testing.T) {
	ms := &types.MessageStore{
		Messages: []types.StoredMessage{
			{SenderNpub: "npub1alice", RecipientNpub: "npub1bob"},
			{SenderNpub: "npub1jack", RecipientNpub: "npub1bob"},
			{SenderNpub: "npub1bob", RecipientNpub: "npub1alice"},
		},
	}
	assert.Equal(t, 2, GetUnreadCount(ms, "npub1bob"))
	assert.Equal(t, 1, GetUnreadCount(ms, "npub1alice"))
}

func TestStoreOutgoingMessage(t *testing.T) {
	setupTempMessageStore(t)
	event := &nostr.Event{
		Kind:    30078,
		Content: "compressed content",
	}
	event.ID = [32]byte{1}
	event.PubKey = nostr.PubKey{}

	err := StoreOutgoingMessage(event, "npub1bob", "plaintext", true)
	require.NoError(t, err)

	ms, _ := LoadMessageStore()
	require.Len(t, ms.Messages, 1)
	assert.False(t, ms.Messages[0].IsIncoming)
	assert.True(t, ms.Messages[0].IsEncrypted)
}

func TestStoreIncomingMessage(t *testing.T) {
	setupTempMessageStore(t)
	event := &nostr.Event{
		Kind:    30078,
		Content: "compressed content",
		Tags:    nostr.Tags{{"p", "b029a5dc3d6dd7fc6407949053ac55637faf49dba942af908e3ad5e938d30a1f"}},
	}
	event.ID = [32]byte{2}
	event.PubKey = nostr.PubKey{}

	err := StoreIncomingMessage(event, "plaintext", false)
	require.NoError(t, err)

	ms, _ := LoadMessageStore()
	require.Len(t, ms.Messages, 1)
	assert.True(t, ms.Messages[0].IsIncoming)
	assert.False(t, ms.Messages[0].IsEncrypted)
}

package messaging

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"fiatjaf.com/nostr"
	"github.com/AuraAIHQ/agent-speaker/internal/common"
	"github.com/AuraAIHQ/agent-speaker/internal/identity"
	"github.com/AuraAIHQ/agent-speaker/pkg/types"
)

// GetMessageStorePath returns the path to message store
func GetMessageStorePath() string {
	path, _ := identity.EnsureKeyStore()
	return filepath.Join(path, "messages.json")
}

// LoadMessageStore loads messages from disk
func LoadMessageStore() (*types.MessageStore, error) {
	file := GetMessageStorePath()

	ms := &types.MessageStore{
		Messages: make([]types.StoredMessage, 0),
	}

	data, err := os.ReadFile(file)
	if err != nil {
		if os.IsNotExist(err) {
			return ms, nil
		}
		return nil, err
	}

	if err := json.Unmarshal(data, ms); err != nil {
		return nil, fmt.Errorf("failed to parse message store: %w", err)
	}

	return ms, nil
}

// SaveMessageStore saves messages to disk
func SaveMessageStore(ms *types.MessageStore) error {
	file := GetMessageStorePath()

	data, err := json.MarshalIndent(ms, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(file, data, 0600)
}

// AddMessage adds a message to store
func AddMessage(ms *types.MessageStore, msg types.StoredMessage) error {
	msg.ReceivedAt = time.Now().Unix()
	ms.Messages = append(ms.Messages, msg)
	return SaveMessageStore(ms)
}

// GetConversation returns messages between two users
func GetConversation(ms *types.MessageStore, user1Npub, user2Npub string, limit int) []types.StoredMessage {
	var result []types.StoredMessage

	for _, msg := range ms.Messages {
		if (msg.SenderNpub == user1Npub && msg.RecipientNpub == user2Npub) ||
			(msg.SenderNpub == user2Npub && msg.RecipientNpub == user1Npub) {
			result = append(result, msg)
		}
	}

	// Sort by time (newest first)
	sort.Slice(result, func(i, j int) bool {
		return result[i].CreatedAt > result[j].CreatedAt
	})

	if limit > 0 && len(result) > limit {
		result = result[:limit]
	}

	return result
}

// GetInbox returns messages for a user
func GetInbox(ms *types.MessageStore, userNpub string, limit int) []types.StoredMessage {
	var result []types.StoredMessage

	for _, msg := range ms.Messages {
		if msg.RecipientNpub == userNpub {
			result = append(result, msg)
		}
	}

	// Sort by time (newest first)
	sort.Slice(result, func(i, j int) bool {
		return result[i].CreatedAt > result[j].CreatedAt
	})

	if limit > 0 && len(result) > limit {
		result = result[:limit]
	}

	return result
}

// GetUnreadCount returns unread message count
func GetUnreadCount(ms *types.MessageStore, userNpub string) int {
	count := 0
	for _, msg := range ms.Messages {
		if msg.RecipientNpub == userNpub {
			count++
		}
	}
	return count
}

// StoreOutgoingMessage stores a sent message
func StoreOutgoingMessage(event *nostr.Event, recipientNpub string, plaintext string, isEncrypted bool) error {
	ms, err := LoadMessageStore()
	if err != nil {
		return err
	}

	msg := types.StoredMessage{
		ID:            string(event.ID[:]),
		SenderNpub:    common.EncodeNpub(event.PubKey),
		RecipientNpub: recipientNpub,
		Content:       event.Content,
		Plaintext:     plaintext,
		CreatedAt:     int64(event.CreatedAt),
		IsEncrypted:   isEncrypted,
		IsIncoming:    false,
	}

	return AddMessage(ms, msg)
}

// StoreIncomingMessage stores a received message
func StoreIncomingMessage(event *nostr.Event, plaintext string, isEncrypted bool) error {
	ms, err := LoadMessageStore()
	if err != nil {
		return err
	}

	// Get recipient from p tag
	recipientNpub := ""
	for _, tag := range event.Tags {
		if len(tag) >= 2 && tag[0] == "p" {
			pk, _ := common.ParsePublicKey(tag[1])
			recipientNpub = common.EncodeNpub(pk)
			break
		}
	}

	msg := types.StoredMessage{
		ID:            string(event.ID[:]),
		SenderNpub:    common.EncodeNpub(event.PubKey),
		RecipientNpub: recipientNpub,
		Content:       event.Content,
		Plaintext:     plaintext,
		CreatedAt:     int64(event.CreatedAt),
		IsEncrypted:   isEncrypted,
		IsIncoming:    true,
	}

	return AddMessage(ms, msg)
}

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"fiatjaf.com/nostr"
)

// StoredMessage represents a message in local storage
type StoredMessage struct {
	ID          string `json:"id"`
	SenderNpub  string `json:"sender_npub"`
	RecipientNpub string `json:"recipient_npub"`
	Content     string `json:"content"`
	Plaintext   string `json:"plaintext,omitempty"` // decrypted content
	CreatedAt   int64  `json:"created_at"`
	ReceivedAt  int64  `json:"received_at"`
	IsEncrypted bool   `json:"is_encrypted"`
	IsIncoming  bool   `json:"is_incoming"` // true if we are recipient
	Relay       string `json:"relay"`
}

// MessageStore manages local message storage
type MessageStore struct {
	Messages []StoredMessage `json:"messages"`
}

// GetMessageStorePath returns the path to message store
func GetMessageStorePath() string {
	path, _ := EnsureKeyStore()
	return filepath.Join(path, "messages.json")
}

// LoadMessageStore loads messages from disk
func LoadMessageStore() (*MessageStore, error) {
	file := GetMessageStorePath()
	
	ms := &MessageStore{
		Messages: make([]StoredMessage, 0),
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

// Save saves messages to disk
func (ms *MessageStore) Save() error {
	file := GetMessageStorePath()
	
	data, err := json.MarshalIndent(ms, "", "  ")
	if err != nil {
		return err
	}
	
	return os.WriteFile(file, data, 0600)
}

// AddMessage adds a message to store
func (ms *MessageStore) AddMessage(msg StoredMessage) error {
	msg.ReceivedAt = time.Now().Unix()
	ms.Messages = append(ms.Messages, msg)
	return ms.Save()
}

// GetConversation returns messages between two users
func (ms *MessageStore) GetConversation(user1Npub, user2Npub string, limit int) []StoredMessage {
	var result []StoredMessage
	
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
func (ms *MessageStore) GetInbox(userNpub string, limit int) []StoredMessage {
	var result []StoredMessage
	
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
func (ms *MessageStore) GetUnreadCount(userNpub string) int {
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
	
	msg := StoredMessage{
		ID:            string(event.ID[:]),
		SenderNpub:    encodeNpub(event.PubKey),
		RecipientNpub: recipientNpub,
		Content:       event.Content,
		Plaintext:     plaintext,
		CreatedAt:     int64(event.CreatedAt),
		IsEncrypted:   isEncrypted,
		IsIncoming:    false,
	}
	
	return ms.AddMessage(msg)
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
			recipientNpub = encodeNpubFromHex(tag[1])
			break
		}
	}
	
	msg := StoredMessage{
		ID:            string(event.ID[:]),
		SenderNpub:    encodeNpub(event.PubKey),
		RecipientNpub: recipientNpub,
		Content:       event.Content,
		Plaintext:     plaintext,
		CreatedAt:     int64(event.CreatedAt),
		IsEncrypted:   isEncrypted,
		IsIncoming:    true,
	}
	
	return ms.AddMessage(msg)
}

// encodeNpubFromHex converts hex pubkey to npub
func encodeNpubFromHex(hexPub string) string {
	pk, _ := parsePublicKey(hexPub)
	return encodeNpub(pk)
}

package messaging

import (
	"sync"

	"fiatjaf.com/nostr"
	"github.com/AuraAIHQ/agent-speaker/internal/identity"
	"github.com/AuraAIHQ/agent-speaker/internal/storage"
	"github.com/AuraAIHQ/agent-speaker/pkg/types"
)

var (
	store     *storage.MessageStore
	storeOnce sync.Once
	storeErr  error
)

// InitStorage initializes the SQLite storage
func InitStorage() error {
	storeOnce.Do(func() {
		// Initialize database
		db, err := storage.InitDB()
		if err != nil {
			storeErr = err
			return
		}

		// Migrate from JSON if needed
		if err := storage.MigrateFromJSON(); err != nil {
			storeErr = err
			return
		}

		store = storage.NewMessageStore(db)
	})

	return storeErr
}

// GetStore returns the message store instance
func GetStore() (*storage.MessageStore, error) {
	if store == nil {
		if err := InitStorage(); err != nil {
			return nil, err
		}
	}
	return store, nil
}

// LoadMessageStore loads messages from database (compatibility function)
func LoadMessageStore() (*types.MessageStore, error) {
	// For compatibility with old code, return an empty struct
	// All operations now go through SQLite
	return &types.MessageStore{
		Messages: make([]types.StoredMessage, 0),
	}, nil
}

// GetConversation returns messages between two users
func GetConversation(ms *types.MessageStore, user1Npub, user2Npub string, limit int) []types.StoredMessage {
	s, err := GetStore()
	if err != nil {
		return nil
	}

	messages, err := s.GetConversation(user1Npub, user2Npub, limit)
	if err != nil {
		return nil
	}

	return messages
}

// GetInbox returns messages for a user
func GetInbox(ms *types.MessageStore, userNpub string, limit int) []types.StoredMessage {
	s, err := GetStore()
	if err != nil {
		return nil
	}

	messages, err := s.GetInbox(userNpub, limit)
	if err != nil {
		return nil
	}

	return messages
}

// GetUnreadCount returns unread message count (simplified - counts all received)
func GetUnreadCount(ms *types.MessageStore, userNpub string) int {
	s, err := GetStore()
	if err != nil {
		return 0
	}

	messages, err := s.GetInbox(userNpub, 10000)
	if err != nil {
		return 0
	}

	return len(messages)
}

// StoreOutgoingMessage stores a sent message
func StoreOutgoingMessage(event *nostr.Event, recipientNpub string, plaintext string, isEncrypted bool) error {
	s, err := GetStore()
	if err != nil {
		return err
	}

	return s.StoreOutgoingMessage(event, recipientNpub, plaintext, isEncrypted)
}

// StoreIncomingMessage stores a received message
func StoreIncomingMessage(event *nostr.Event, plaintext string, isEncrypted bool) error {
	s, err := GetStore()
	if err != nil {
		return err
	}

	return s.StoreIncomingMessage(event, plaintext, isEncrypted)
}

// GetStats returns message statistics
func GetStats() (map[string]int, error) {
	// Get current identity
	ks, err := identity.LoadKeyStore()
	if err != nil {
		return nil, err
	}

	myIdentity, err := identity.GetIdentity(ks, "")
	if err != nil {
		return nil, err
	}

	s, err := GetStore()
	if err != nil {
		return nil, err
	}

	return s.GetStats(myIdentity.Npub)
}

// SearchMessages searches messages
func SearchMessages(query string) ([]types.StoredMessage, error) {
	ks, err := identity.LoadKeyStore()
	if err != nil {
		return nil, err
	}

	myIdentity, err := identity.GetIdentity(ks, "")
	if err != nil {
		return nil, err
	}

	s, err := GetStore()
	if err != nil {
		return nil, err
	}

	return s.SearchMessages(myIdentity.Npub, query, 100)
}

// AddMessage adds a message (legacy compatibility)
func AddMessage(ms *types.MessageStore, msg types.StoredMessage) error {
	s, err := GetStore()
	if err != nil {
		return err
	}

	return s.StoreMessage(&msg)
}

// SaveMessageStore is now a no-op (data saved immediately in SQLite)
func SaveMessageStore(ms *types.MessageStore) error {
	return nil
}

package messaging

import (
	"sync"

	"fiatjaf.com/nostr"
	"github.com/iDoris-ai/hyphae/internal/identity"
	"github.com/iDoris-ai/hyphae/internal/storage"
	"github.com/iDoris-ai/hyphae/pkg/types"
)

var (
	store    *storage.MessageStore
	storeMu  sync.Mutex
	storeErr error
)

// InitStorage initializes the SQLite storage
func InitStorage() error {
	storeMu.Lock()
	defer storeMu.Unlock()

	if store != nil {
		return nil
	}
	if storeErr != nil {
		return storeErr
	}

	// Initialize database
	db, err := storage.InitDB()
	if err != nil {
		storeErr = err
		return err
	}

	// Migrate from JSON if needed
	if err := storage.MigrateFromJSON(db); err != nil {
		storeErr = err
		return err
	}

	store = storage.NewMessageStore(db)
	return nil
}

// ResetStoreForTest clears the package-level store singleton so a
// subsequent InitStorage() call re-opens against whatever HOME/temp
// directory is current, instead of silently continuing to use whichever
// store an earlier test in the same process happened to initialize first.
//
// InitStorage() is a "first call wins" singleton (see above: `if store !=
// nil { return nil }`), which is fine for production (one process, one
// HOME) but means tests in different packages that each set up their own
// t.TempDir() + t.Setenv("HOME", ...) and call InitStorage() can silently
// end up sharing the *first* test's store instead of getting their own --
// exported (not just internal/messaging's own store_test.go resetStore
// helper) so other packages' tests that need real storage, like
// internal/daemon's, can call it too.
func ResetStoreForTest() {
	storeMu.Lock()
	defer storeMu.Unlock()
	if storage.DB != nil {
		_ = storage.CloseDB()
		storage.DB = nil
	}
	store = nil
	storeErr = nil
}

// GetStore returns the message store instance
func GetStore() (*storage.MessageStore, error) {
	if err := InitStorage(); err != nil {
		return nil, err
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
func GetConversation(ms *types.MessageStore, user1Npub, user2Npub string, limit int) ([]types.StoredMessage, error) {
	s, err := GetStore()
	if err != nil {
		return nil, err
	}

	return s.GetConversation(user1Npub, user2Npub, limit)
}

// GetInbox returns messages for a user
func GetInbox(ms *types.MessageStore, userNpub string, limit int) ([]types.StoredMessage, error) {
	s, err := GetStore()
	if err != nil {
		return nil, err
	}

	return s.GetInbox(userNpub, limit)
}

// GetReceivedCount returns the total received message count for a user
func GetReceivedCount(ms *types.MessageStore, userNpub string) (int, error) {
	s, err := GetStore()
	if err != nil {
		return 0, err
	}

	return s.GetReceivedCount(userNpub)
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

// RecentIncomingEventIDs returns Nostr event IDs for the most recent `limit`
// incoming messages stored for `npub`. Pass-through to the SQLite store.
// Used by the daemon to prime its in-memory dedup set on startup.
func RecentIncomingEventIDs(npub string, limit int) ([]string, error) {
	s, err := GetStore()
	if err != nil {
		return nil, err
	}
	return s.GetRecentIncomingEventIDs(npub, limit)
}

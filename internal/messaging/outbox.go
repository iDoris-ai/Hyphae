package messaging

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"fiatjaf.com/nostr"
	"github.com/AuraAIHQ/agent-speaker/internal/identity"
	"github.com/AuraAIHQ/agent-speaker/pkg/types"
)

// GetOutboxPath returns the path to outbox file
func GetOutboxPath() (string, error) {
	path, err := identity.EnsureKeyStore()
	if err != nil {
		return "", fmt.Errorf("failed to ensure keystore: %w", err)
	}
	return filepath.Join(path, "outbox.json"), nil
}

// LoadOutbox loads outbox from disk
func LoadOutbox() (*types.Outbox, error) {
	file, err := GetOutboxPath()
	if err != nil {
		return nil, err
	}

	ob := &types.Outbox{
		Entries: make([]types.OutboxEntry, 0),
	}

	data, err := os.ReadFile(file)
	if err != nil {
		if os.IsNotExist(err) {
			return ob, nil
		}
		return nil, err
	}

	if err := json.Unmarshal(data, ob); err != nil {
		return nil, fmt.Errorf("failed to parse outbox: %w", err)
	}

	return ob, nil
}

// SaveOutbox saves outbox to disk
func SaveOutbox(ob *types.Outbox) error {
	file, err := GetOutboxPath()
	if err != nil {
		return err
	}

	data, err := json.MarshalIndent(ob, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(file, data, 0600)
}

// AddToOutbox adds a message to outbox
func AddToOutbox(ob *types.Outbox, event *nostr.Event, recipientNpub string, relays []string) error {
	eventJSON, err := json.Marshal(event)
	if err != nil {
		return err
	}

	entry := types.OutboxEntry{
		ID:            string(event.ID[:]),
		EventJSON:     string(eventJSON),
		RecipientNpub: recipientNpub,
		Relays:        relays,
		RetryCount:    0,
		MaxRetries:    10,
		CreatedAt:     time.Now().Unix(),
		Status:        "pending",
	}

	ob.Entries = append(ob.Entries, entry)
	return SaveOutbox(ob)
}

// GetPendingOutbox returns pending entries
func GetPendingOutbox(ob *types.Outbox) []types.OutboxEntry {
	var pending []types.OutboxEntry
	for _, entry := range ob.Entries {
		if entry.Status == "pending" && entry.RetryCount < entry.MaxRetries {
			pending = append(pending, entry)
		}
	}
	return pending
}

// UpdateOutboxStatus updates entry status
func UpdateOutboxStatus(ob *types.Outbox, id string, status string) error {
	for i := range ob.Entries {
		if ob.Entries[i].ID == id {
			ob.Entries[i].Status = status
			return SaveOutbox(ob)
		}
	}
	return fmt.Errorf("entry not found")
}

// IncrementOutboxRetry increments retry count
func IncrementOutboxRetry(ob *types.Outbox, id string) error {
	for i := range ob.Entries {
		if ob.Entries[i].ID == id {
			ob.Entries[i].RetryCount++
			ob.Entries[i].LastAttempt = time.Now().Unix()
			return SaveOutbox(ob)
		}
	}
	return fmt.Errorf("entry not found")
}

// RemoveFromOutbox removes a sent entry
func RemoveFromOutbox(ob *types.Outbox, id string) error {
	newEntries := make([]types.OutboxEntry, 0)
	for _, entry := range ob.Entries {
		if entry.ID != id {
			newEntries = append(newEntries, entry)
		}
	}
	ob.Entries = newEntries
	return SaveOutbox(ob)
}

// CleanupOutbox removes old sent entries
func CleanupOutbox(ob *types.Outbox, maxAge time.Duration) error {
	cutoff := time.Now().Add(-maxAge).Unix()
	newEntries := make([]types.OutboxEntry, 0)
	for _, entry := range ob.Entries {
		// Keep pending entries, remove old sent/failed entries
		if entry.Status == "pending" || entry.LastAttempt > cutoff {
			newEntries = append(newEntries, entry)
		}
	}
	ob.Entries = newEntries
	return SaveOutbox(ob)
}

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"fiatjaf.com/nostr"
)

// OutboxEntry represents a message waiting to be sent
type OutboxEntry struct {
	ID            string   `json:"id"`
	Event         *nostr.Event `json:"event"`
	RecipientNpub string   `json:"recipient_npub"`
	Relays        []string `json:"relays"`
	RetryCount    int      `json:"retry_count"`
	MaxRetries    int      `json:"max_retries"`
	LastAttempt   int64    `json:"last_attempt"`
	CreatedAt     int64    `json:"created_at"`
	Status        string   `json:"status"` // "pending", "sent", "failed"
}

// Outbox manages pending messages
type Outbox struct {
	Entries []OutboxEntry `json:"entries"`
}

// GetOutboxPath returns the path to outbox file
func GetOutboxPath() string {
	path, _ := EnsureKeyStore()
	return filepath.Join(path, "outbox.json")
}

// LoadOutbox loads outbox from disk
func LoadOutbox() (*Outbox, error) {
	file := GetOutboxPath()
	
	ob := &Outbox{
		Entries: make([]OutboxEntry, 0),
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

// Save saves outbox to disk
func (ob *Outbox) Save() error {
	file := GetOutboxPath()
	
	data, err := json.MarshalIndent(ob, "", "  ")
	if err != nil {
		return err
	}
	
	return os.WriteFile(file, data, 0600)
}

// Add adds a message to outbox
func (ob *Outbox) Add(event *nostr.Event, recipientNpub string, relays []string) error {
	entry := OutboxEntry{
		ID:            string(event.ID[:]),
		Event:         event,
		RecipientNpub: recipientNpub,
		Relays:        relays,
		RetryCount:    0,
		MaxRetries:    10,
		CreatedAt:     time.Now().Unix(),
		Status:        "pending",
	}
	
	ob.Entries = append(ob.Entries, entry)
	return ob.Save()
}

// GetPending returns pending entries
func (ob *Outbox) GetPending() []OutboxEntry {
	var pending []OutboxEntry
	for _, entry := range ob.Entries {
		if entry.Status == "pending" && entry.RetryCount < entry.MaxRetries {
			pending = append(pending, entry)
		}
	}
	return pending
}

// UpdateStatus updates entry status
func (ob *Outbox) UpdateStatus(id string, status string) error {
	for i := range ob.Entries {
		if ob.Entries[i].ID == id {
			ob.Entries[i].Status = status
			return ob.Save()
		}
	}
	return fmt.Errorf("entry not found")
}

// IncrementRetry increments retry count
func (ob *Outbox) IncrementRetry(id string) error {
	for i := range ob.Entries {
		if ob.Entries[i].ID == id {
			ob.Entries[i].RetryCount++
			ob.Entries[i].LastAttempt = time.Now().Unix()
			return ob.Save()
		}
	}
	return fmt.Errorf("entry not found")
}

// Remove removes a sent entry
func (ob *Outbox) Remove(id string) error {
	newEntries := make([]OutboxEntry, 0)
	for _, entry := range ob.Entries {
		if entry.ID != id {
			newEntries = append(newEntries, entry)
		}
	}
	ob.Entries = newEntries
	return ob.Save()
}

// Cleanup removes old sent entries
func (ob *Outbox) Cleanup(maxAge time.Duration) error {
	cutoff := time.Now().Add(-maxAge).Unix()
	newEntries := make([]OutboxEntry, 0)
	for _, entry := range ob.Entries {
		// Keep pending entries, remove old sent/failed entries
		if entry.Status == "pending" || entry.LastAttempt > cutoff {
			newEntries = append(newEntries, entry)
		}
	}
	ob.Entries = newEntries
	return ob.Save()
}

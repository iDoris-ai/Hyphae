package messaging

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
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

// SaveOutbox saves outbox to disk atomically.
//
// The write path is: marshal → open sibling temp file → write → fsync →
// close → rename. POSIX guarantees rename() within the same directory is
// atomic, so a crash at any point leaves either the old complete file or
// the new complete file on disk — never a half-written outbox.json that
// would deserialize as "everything still pending" and cause duplicate
// sends after restart.
func SaveOutbox(ob *types.Outbox) error {
	file, err := GetOutboxPath()
	if err != nil {
		return err
	}

	data, err := json.MarshalIndent(ob, "", "  ")
	if err != nil {
		return err
	}

	tmp := file + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("open temp outbox: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("write temp outbox: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("fsync temp outbox: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("close temp outbox: %w", err)
	}
	if err := os.Rename(tmp, file); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename outbox: %w", err)
	}
	return nil
}

// AddToOutbox adds a message to outbox. entry.ID is hex-encoded rather than
// the raw event.ID bytes: a Go string holding arbitrary binary content gets
// silently and irreversibly mangled by json.Marshal the moment it's first
// written (any byte sequence that isn't valid UTF-8 becomes U+FFFD), which
// is exactly what happened to 9 of 13 real historical entries found during
// task 8's outbox-diagnostics work (see specs/m1.5/README.md). Hex-encoding
// keeps the stored ID both round-trip-safe through JSON and human-readable.
func AddToOutbox(ob *types.Outbox, event *nostr.Event, recipientNpub string, relays []string) error {
	eventJSON, err := json.Marshal(event)
	if err != nil {
		return err
	}

	entry := types.OutboxEntry{
		ID:            hex.EncodeToString(event.ID[:]),
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

// RemoveFromOutbox removes the single entry with the given ID.
//
// If more than one entry shares that ID, it refuses and removes nothing:
// filtering by "ID != id" would otherwise delete every one of them, not
// just the one the caller meant, and with the pre-existing bug where an
// unsigned event keeps a zero-value ID (see specs/m1.5/README.md), that's a
// real way to silently lose other, unrelated, never-actually-sent entries.
// AttemptSend already guards against this earlier via countByID, but
// RemoveFromOutbox is called directly elsewhere too (e.g. agent.go's normal
// send path cleans up a stale outbox entry after a successful publish), so
// the check belongs here too, not only in one caller.
func RemoveFromOutbox(ob *types.Outbox, id string) error {
	if n := countByID(ob.Entries, id); n > 1 {
		return fmt.Errorf("%d outbox entries share id %q -- refusing to remove any of them", n, id)
	}
	newEntries := make([]types.OutboxEntry, 0)
	for _, entry := range ob.Entries {
		if entry.ID != id {
			newEntries = append(newEntries, entry)
		}
	}
	ob.Entries = newEntries
	return SaveOutbox(ob)
}

// SendResult describes the outcome of a single AttemptSend call.
type SendResult struct {
	Attempted    bool // false only when the entry never got as far as dialing a relay (e.g. unparseable EventJSON, or a duplicate-ID refusal)
	Sent         bool // true if the event was successfully published
	MarkedFailed bool // true if this attempt exhausted retries and the entry was marked "failed"
}

// countByID reports how many entries in entries share the given ID.
func countByID(entries []types.OutboxEntry, id string) int {
	n := 0
	for _, e := range entries {
		if e.ID == id {
			n++
		}
	}
	return n
}

// AttemptSend tries to publish a single outbox entry to its target relays
// (falling back to defaultRelays when entry.Relays is empty), then updates
// and persists ob's status for this entry -- "sent" + removed on success,
// retry count incremented (and marked "failed" once retries are exhausted)
// on failure.
//
// This is the one place that mutates outbox state after a send attempt --
// both the daemon's automatic retry loop (internal/daemon) and the manual
// `storage outbox retry` CLI command call this, so the status-transition
// logic only exists once. Unlike the daemon's automatic loop, AttemptSend
// does NOT perform the exponential-backoff eligibility check: callers that
// want backoff (the daemon's ticker) must check that themselves before
// calling; a manual retry is expected to bypass backoff by design (that's
// the whole point of "don't wait for the daemon's 60s cycle").
//
// The returned error is informational, not a verdict on whether the send
// itself succeeded -- check Sent/MarkedFailed for that. It surfaces
// bookkeeping failures (status update/remove/history-store) that happen
// alongside a send attempt whose own outcome is already reflected in the
// result; callers should log it but must not treat a non-nil error as "the
// send failed" when Result.Attempted is true.
//
// AttemptSend refuses to process an entry whose ID collides with another
// entry in ob.Entries (Attempted stays false, matching a parse error): on
// success it would call RemoveFromOutbox, which deletes every entry sharing
// that ID, not just this one -- with a pre-existing bug where an unsigned
// event keeps a zero-value ID (see specs/m1.5/README.md), that would
// silently delete other, never-actually-sent entries. This check lives here
// rather than only at each call site so it protects the daemon's automatic
// retry loop too, not just an interactive CLI command that happens to add
// its own guard.
func AttemptSend(ctx context.Context, ob *types.Outbox, entry types.OutboxEntry, defaultRelays []string, dialTimeout time.Duration) (SendResult, error) {
	if n := countByID(ob.Entries, entry.ID); n > 1 {
		return SendResult{}, fmt.Errorf("%d outbox entries share this ID -- refusing to send/mutate (see specs/m1.5/README.md's outbox ID-collision note)", n)
	}

	var event nostr.Event
	if err := json.Unmarshal([]byte(entry.EventJSON), &event); err != nil {
		return SendResult{}, fmt.Errorf("parse event: %w", err)
	}

	targets := entry.Relays
	if len(targets) == 0 {
		targets = defaultRelays
	}

	sent := false
	for _, url := range targets {
		relayCtx, cancel := context.WithTimeout(ctx, dialTimeout)
		relay, err := nostr.RelayConnect(relayCtx, url, nostr.RelayOptions{})
		if err != nil {
			cancel()
			continue
		}
		pubErr := relay.Publish(relayCtx, event)
		relay.Close()
		cancel()
		if pubErr == nil {
			sent = true
			break
		}
	}

	result := SendResult{Attempted: true, Sent: sent}
	var errs []error

	if sent {
		// Each of these three is attempted independently, matching the
		// pre-refactor daemon.go behavior exactly: an error updating status
		// must not skip removing the entry or storing the outgoing message,
		// since those are the parts that actually matter once the event has
		// already been published. Errors are collected, not fatal.
		if err := UpdateOutboxStatus(ob, entry.ID, "sent"); err != nil {
			errs = append(errs, fmt.Errorf("update outbox status: %w", err))
		}
		if err := RemoveFromOutbox(ob, entry.ID); err != nil {
			errs = append(errs, fmt.Errorf("remove from outbox: %w", err))
		}
		if err := StoreOutgoingMessage(&event, entry.RecipientNpub, event.Content, true); err != nil {
			errs = append(errs, fmt.Errorf("store outgoing message: %w", err))
		}
		return result, errors.Join(errs...)
	}

	// Same independent-attempt shape on the failure side: a failure to
	// persist the incremented retry count must not skip the "did retries
	// just get exhausted" check below.
	if err := IncrementOutboxRetry(ob, entry.ID); err != nil {
		errs = append(errs, fmt.Errorf("increment retry: %w", err))
	}
	if entry.RetryCount >= entry.MaxRetries-1 {
		if err := UpdateOutboxStatus(ob, entry.ID, "failed"); err != nil {
			errs = append(errs, fmt.Errorf("mark failed: %w", err))
		}
		result.MarkedFailed = true
	}
	return result, errors.Join(errs...)
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

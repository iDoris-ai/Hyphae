package messaging

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"fiatjaf.com/nostr"
	"github.com/iDoris-ai/hyphae/internal/identity"
	"github.com/iDoris-ai/hyphae/pkg/types"
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
	return readOutbox(file)
}

func readOutbox(file string) (*types.Outbox, error) {
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

// SaveOutbox replaces the complete outbox atomically. Production read-modify-
// write operations must use UpdateOutbox so they cannot overwrite another
// process's changes with a stale snapshot. This function is for explicit
// whole-file replacement and test fixtures.
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
	return withOutboxLock(file, func() error { return writeOutbox(file, ob) })
}

// UpdateOutbox serializes a read-modify-write transaction across processes.
// The callback receives the latest disk state while the stable sibling lock
// file is held. Do not perform network I/O in the callback.
func UpdateOutbox(update func(*types.Outbox) error) (*types.Outbox, error) {
	file, err := GetOutboxPath()
	if err != nil {
		return nil, err
	}
	var updated *types.Outbox
	err = withOutboxLock(file, func() error {
		ob, err := readOutbox(file)
		if err != nil {
			return err
		}
		before := cloneOutbox(ob)
		if err := update(ob); err != nil {
			return err
		}
		if reflect.DeepEqual(before, ob) {
			updated = ob
			return nil
		}
		if err := persistOutbox(file, ob); err != nil {
			return err
		}
		updated = ob
		return nil
	})
	return updated, err
}

var persistOutbox = writeOutbox

func cloneOutbox(ob *types.Outbox) *types.Outbox {
	clone := &types.Outbox{Entries: make([]types.OutboxEntry, len(ob.Entries))}
	for i, entry := range ob.Entries {
		if entry.Relays != nil {
			entry.Relays = append([]string{}, entry.Relays...)
		}
		clone.Entries[i] = entry
	}
	return clone
}

func writeOutbox(file string, ob *types.Outbox) error {
	data, err := json.MarshalIndent(ob, "", "  ")
	if err != nil {
		return err
	}

	f, err := os.CreateTemp(filepath.Dir(file), ".outbox-*.tmp")
	if err != nil {
		return fmt.Errorf("open temp outbox: %w", err)
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err := f.Chmod(0600); err != nil {
		_ = f.Close()
		return fmt.Errorf("chmod temp outbox: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("write temp outbox: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("fsync temp outbox: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close temp outbox: %w", err)
	}
	if err := os.Rename(tmp, file); err != nil {
		return fmt.Errorf("rename outbox: %w", err)
	}
	dir, err := os.Open(filepath.Dir(file))
	if err != nil {
		return &outboxCommitUncertainError{fmt.Errorf("open outbox directory after rename: %w", err)}
	}
	if err := dir.Sync(); err != nil {
		_ = dir.Close()
		return &outboxCommitUncertainError{fmt.Errorf("fsync outbox directory after rename: %w", err)}
	}
	if err := dir.Close(); err != nil {
		return &outboxCommitUncertainError{fmt.Errorf("close outbox directory after rename: %w", err)}
	}
	return nil
}

type outboxCommitUncertainError struct{ err error }

func (e *outboxCommitUncertainError) Error() string { return e.err.Error() }
func (e *outboxCommitUncertainError) Unwrap() error { return e.err }

// AddToOutbox adds a message to outbox. entry.ID is hex-encoded rather than
// the raw event.ID bytes: a Go string holding arbitrary binary content gets
// silently and irreversibly mangled by json.Marshal the moment it's first
// written (any byte sequence that isn't valid UTF-8 becomes U+FFFD), which
// is exactly what happened to 9 of 13 real historical entries found during
// task 8's outbox-diagnostics work (see specs/m1.5/README.md). Hex-encoding
// keeps the stored ID both round-trip-safe through JSON and human-readable.
func AddToOutbox(ob *types.Outbox, event *nostr.Event, recipientNpub string, relays []string) error {
	_, err := enqueueOutboxEntry(ob, event, recipientNpub, relays)
	return err
}

func enqueueOutboxEntry(ob *types.Outbox, event *nostr.Event, recipientNpub string, relays []string) (types.OutboxEntry, error) {
	eventJSON, err := json.Marshal(event)
	if err != nil {
		return types.OutboxEntry{}, err
	}
	queueID, err := newOutboxQueueID()
	if err != nil {
		return types.OutboxEntry{}, err
	}

	entry := types.OutboxEntry{
		QueueID:       queueID,
		ID:            hex.EncodeToString(event.ID[:]),
		EventJSON:     string(eventJSON),
		RecipientNpub: recipientNpub,
		Relays:        relays,
		RetryCount:    0,
		MaxRetries:    10,
		CreatedAt:     time.Now().Unix(),
		Status:        "pending",
	}

	updated, err := UpdateOutbox(func(latest *types.Outbox) error {
		latest.Entries = append(latest.Entries, entry)
		return nil
	})
	if err == nil {
		refreshOutbox(ob, updated)
	}
	return entry, err
}

func newOutboxQueueID() (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", fmt.Errorf("generate outbox queue id: %w", err)
	}
	return hex.EncodeToString(id[:]), nil
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
	updated, err := UpdateOutbox(func(latest *types.Outbox) error {
		for i := range latest.Entries {
			if latest.Entries[i].ID == id {
				latest.Entries[i].Status = status
				return nil
			}
		}
		return fmt.Errorf("entry not found")
	})
	refreshOutbox(ob, updated)
	return err
}

// IncrementOutboxRetry increments retry count
func IncrementOutboxRetry(ob *types.Outbox, id string) error {
	updated, _, err := incrementOutboxRetry(id)
	refreshOutbox(ob, updated)
	return err
}

func incrementOutboxRetry(id string) (*types.Outbox, int, error) {
	var retryCount int
	updated, err := UpdateOutbox(func(latest *types.Outbox) error {
		for i := range latest.Entries {
			if latest.Entries[i].ID == id {
				latest.Entries[i].RetryCount++
				latest.Entries[i].LastAttempt = time.Now().Unix()
				retryCount = latest.Entries[i].RetryCount
				return nil
			}
		}
		return fmt.Errorf("entry not found")
	})
	return updated, retryCount, err
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
	updated, err := UpdateOutbox(func(latest *types.Outbox) error {
		if n := countByID(latest.Entries, id); n > 1 {
			return fmt.Errorf("%d outbox entries share id %q -- refusing to remove any of them", n, id)
		}
		newEntries := make([]types.OutboxEntry, 0, len(latest.Entries))
		for _, entry := range latest.Entries {
			if entry.ID != id {
				newEntries = append(newEntries, entry)
			}
		}
		latest.Entries = newEntries
		return nil
	})
	refreshOutbox(ob, updated)
	return err
}

func refreshOutbox(dst, src *types.Outbox) {
	if dst != nil && src != nil {
		dst.Entries = src.Entries
	}
}

// SendResult describes the outcome of a single AttemptSend call.
type SendResult struct {
	Attempted         bool // true once a relay publish was attempted
	Sent              bool // true only when a relay acknowledged the event
	Queued            bool // true when the same entry remains pending and below its retry limit
	MarkedFailed      bool // true when the same entry is marked failed after this outcome
	HistoryStored     bool // true when a successful publish was stored in local history
	Superseded        bool // true when the queue entry was removed or replaced by another operation
	QueueStateUnknown bool // true when a post-rename durability error prevents confirming queue state
}

var errOutboxEntrySuperseded = errors.New("outbox entry was removed or replaced")

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

// AttemptSend validates the queued snapshot against the latest disk state,
// publishes without holding the outbox lock, then records the outcome against
// that same queue identity.
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
// Sent only reports a relay acknowledgment. HistoryStored and Queued describe
// separate local bookkeeping outcomes; a bookkeeping error does not change
// whether the relay acknowledged the event.
//
// AttemptSend refuses to process an entry whose ID collides with another
// entry in the latest disk state (Attempted stays false): a retry must never
// guess which queued payload the caller intended. Checking disk under the
// transaction lock protects both CLI retries and the daemon retry loop.
func AttemptSend(ctx context.Context, ob *types.Outbox, entry types.OutboxEntry, defaultRelays []string, dialTimeout time.Duration) (SendResult, error) {
	return attemptSend(ctx, ob, entry, defaultRelays, dialTimeout, publishToRelays, StoreOutgoingMessage)
}

type outboxPublisher func(context.Context, []string, nostr.Event, time.Duration) bool
type outgoingMessageStore func(*nostr.Event, string, string, bool) error

func attemptSend(
	ctx context.Context,
	ob *types.Outbox,
	entry types.OutboxEntry,
	defaultRelays []string,
	dialTimeout time.Duration,
	publish outboxPublisher,
	store outgoingMessageStore,
) (SendResult, error) {
	current, err := currentOutboxAttempt(entry)
	if errors.Is(err, errOutboxEntrySuperseded) {
		return SendResult{Superseded: true}, err
	}
	if err != nil {
		result := SendResult{}
		var uncertain *outboxCommitUncertainError
		if errors.As(err, &uncertain) {
			result.QueueStateUnknown = true
		}
		return result, err
	}
	refreshOutbox(ob, current.outbox)

	var event nostr.Event
	if err := json.Unmarshal([]byte(current.entry.EventJSON), &event); err != nil {
		return SendResult{}, fmt.Errorf("parse event: %w", err)
	}

	targets := current.entry.Relays
	if len(targets) == 0 {
		targets = defaultRelays
	}
	result := SendResult{Attempted: true, Sent: publish(ctx, targets, event, dialTimeout)}
	if !result.Sent {
		return recordAttemptFailure(ob, current.entry, result)
	}

	plaintext, isEncrypted, err := outgoingHistoryContent(&event)
	if err != nil {
		result.Queued, result.Superseded, result.QueueStateUnknown = inspectAttemptQueue(current.entry)
		return result, fmt.Errorf("prepare outgoing message history: %w", err)
	}
	if err := store(&event, current.entry.RecipientNpub, plaintext, isEncrypted); err != nil {
		result.Queued, result.Superseded, result.QueueStateUnknown = inspectAttemptQueue(current.entry)
		return result, fmt.Errorf("store outgoing message: %w", err)
	}
	result.HistoryStored = true
	updated, err := UpdateOutbox(func(latest *types.Outbox) error {
		index := findOutboxQueueEntry(latest, current.entry)
		if index < 0 || latest.Entries[index].Status == "sent" {
			return errOutboxEntrySuperseded
		}
		latest.Entries = append(latest.Entries[:index], latest.Entries[index+1:]...)
		return nil
	})
	if errors.Is(err, errOutboxEntrySuperseded) {
		result.Superseded = true
		return result, nil
	}
	if err != nil {
		result.Queued, result.Superseded, result.QueueStateUnknown = inspectAttemptQueue(current.entry)
		var uncertain *outboxCommitUncertainError
		if errors.As(err, &uncertain) {
			result.QueueStateUnknown = true
			result.Queued = false
			result.Superseded = false
		}
		return result, fmt.Errorf("remove sent outbox entry: %w", err)
	}
	refreshOutbox(ob, updated)
	return result, nil
}

func outgoingHistoryContent(event *nostr.Event) (plaintext string, isEncrypted bool, err error) {
	encryption, hasEncryption, err := outboxTagValue(event.Tags, "enc")
	if err != nil {
		return "", false, err
	}
	compression, hasCompression, err := outboxTagValue(event.Tags, "z")
	if err != nil {
		return "", false, err
	}
	if hasEncryption && encryption != "nip44" {
		return "", false, fmt.Errorf("unsupported encryption tag %q", encryption)
	}
	if hasCompression && compression != CompressTag {
		return "", false, fmt.Errorf("unsupported compression tag %q", compression)
	}

	if hasEncryption {
		// Validate tagged compression, but never store a ciphertext (compressed
		// or otherwise) in the plaintext column. StoreMessage preserves a
		// plaintext value written earlier by the originating send path.
		if hasCompression {
			if _, err := DecompressText(event.Content); err != nil {
				return "", false, fmt.Errorf("decompress encrypted event content: %w", err)
			}
		}
		return "", true, nil
	}
	if hasCompression {
		decompressed, err := DecompressText(event.Content)
		if err != nil {
			return "", false, fmt.Errorf("decompress event content: %w", err)
		}
		return decompressed, false, nil
	}
	return event.Content, false, nil
}

func outboxTagValue(tags nostr.Tags, name string) (value string, found bool, err error) {
	for _, tag := range tags {
		if len(tag) == 0 || tag[0] != name {
			continue
		}
		if len(tag) < 2 || tag[1] == "" {
			return "", false, fmt.Errorf("malformed %q tag", name)
		}
		if found && value != tag[1] {
			return "", false, fmt.Errorf("conflicting %q tags", name)
		}
		value, found = tag[1], true
	}
	return value, found, nil
}

type currentAttempt struct {
	outbox *types.Outbox
	entry  types.OutboxEntry
}

func currentOutboxAttempt(snapshot types.OutboxEntry) (currentAttempt, error) {
	var selected types.OutboxEntry
	updated, err := UpdateOutbox(func(latest *types.Outbox) error {
		if n := countByID(latest.Entries, snapshot.ID); n > 1 {
			return fmt.Errorf("%d outbox entries share this ID -- refusing to send/mutate (see specs/m1.5/README.md's outbox ID-collision note)", n)
		}
		index := -1
		for i := range latest.Entries {
			if latest.Entries[i].ID == snapshot.ID {
				index = i
				break
			}
		}
		if index < 0 || !sameOutboxAttempt(snapshot, latest.Entries[index]) || latest.Entries[index].Status == "sent" {
			return errOutboxEntrySuperseded
		}
		if latest.Entries[index].QueueID == "" {
			queueID, err := newOutboxQueueID()
			if err != nil {
				return err
			}
			latest.Entries[index].QueueID = queueID
		}
		selected = latest.Entries[index]
		return nil
	})
	if err != nil {
		return currentAttempt{}, err
	}
	return currentAttempt{outbox: updated, entry: selected}, nil
}

func sameOutboxAttempt(snapshot, current types.OutboxEntry) bool {
	if snapshot.ID != current.ID || snapshot.EventJSON != current.EventJSON {
		return false
	}
	if snapshot.QueueID != "" {
		return snapshot.QueueID == current.QueueID
	}
	if current.QueueID != "" {
		return false
	}
	return snapshot.CreatedAt == current.CreatedAt &&
		snapshot.RecipientNpub == current.RecipientNpub &&
		reflect.DeepEqual(snapshot.Relays, current.Relays)
}

func findOutboxQueueEntry(ob *types.Outbox, entry types.OutboxEntry) int {
	for i := range ob.Entries {
		if ob.Entries[i].QueueID == entry.QueueID && ob.Entries[i].ID == entry.ID && ob.Entries[i].EventJSON == entry.EventJSON {
			return i
		}
	}
	return -1
}

func recordAttemptFailure(ob *types.Outbox, entry types.OutboxEntry, result SendResult) (SendResult, error) {
	result.MarkedFailed = false
	result.Queued = false
	updated, err := UpdateOutbox(func(latest *types.Outbox) error {
		index := findOutboxQueueEntry(latest, entry)
		if index < 0 || latest.Entries[index].Status == "sent" {
			return errOutboxEntrySuperseded
		}
		current := &latest.Entries[index]
		current.RetryCount++
		current.LastAttempt = time.Now().Unix()
		if current.RetryCount >= current.MaxRetries {
			current.Status = "failed"
		}
		result.MarkedFailed = current.Status == "failed"
		result.Queued = current.Status == "pending" && current.RetryCount < current.MaxRetries
		return nil
	})
	if errors.Is(err, errOutboxEntrySuperseded) {
		result.Superseded = true
		return result, nil
	}
	if err != nil {
		var uncertain *outboxCommitUncertainError
		if errors.As(err, &uncertain) {
			result.QueueStateUnknown = true
		} else {
			result.Queued, result.Superseded, result.QueueStateUnknown = inspectAttemptQueue(entry)
		}
		result.MarkedFailed = false
		if result.QueueStateUnknown {
			result.Queued = false
		}
		return result, fmt.Errorf("record outbox retry: %w", err)
	}
	refreshOutbox(ob, updated)
	return result, nil
}

func inspectAttemptQueue(entry types.OutboxEntry) (queued, superseded, unknown bool) {
	latest, err := loadOutboxLocked()
	if err != nil {
		return false, false, true
	}
	index := findOutboxQueueEntry(latest, entry)
	if index < 0 {
		return false, true, false
	}
	current := latest.Entries[index]
	return current.Status == "pending" && current.RetryCount < current.MaxRetries, false, false
}

func loadOutboxLocked() (*types.Outbox, error) {
	file, err := GetOutboxPath()
	if err != nil {
		return nil, err
	}
	var latest *types.Outbox
	err = withOutboxLock(file, func() error {
		var readErr error
		latest, readErr = readOutbox(file)
		return readErr
	})
	return latest, err
}

func publishToRelays(ctx context.Context, targets []string, event nostr.Event, dialTimeout time.Duration) bool {
	for _, url := range targets {
		relayCtx, cancel := context.WithTimeout(ctx, dialTimeout)
		relay, err := nostr.RelayConnect(relayCtx, url, nostr.RelayOptions{})
		if err != nil {
			if relay != nil {
				relay.Close()
			}
			cancel()
			continue
		}
		pubErr := relay.Publish(relayCtx, event)
		relay.Close()
		cancel()
		if pubErr == nil {
			return true
		}
	}
	return false
}

// CleanupOutbox removes old sent entries
func CleanupOutbox(ob *types.Outbox, maxAge time.Duration) error {
	cutoff := time.Now().Add(-maxAge).Unix()
	updated, err := UpdateOutbox(func(latest *types.Outbox) error {
		newEntries := make([]types.OutboxEntry, 0)
		for _, entry := range latest.Entries {
			// Keep pending entries, remove old sent/failed entries
			if entry.Status == "pending" || entry.LastAttempt > cutoff {
				newEntries = append(newEntries, entry)
			}
		}
		latest.Entries = newEntries
		return nil
	})
	refreshOutbox(ob, updated)
	return err
}

// removeConfirmedOutboxEntries removes only snapshots captured before a user
// confirmed a clear operation. QueueID distinguishes identical events that
// were enqueued again while the confirmation prompt was open.
func removeConfirmedOutboxEntries(latest *types.Outbox, confirmed []types.OutboxEntry) int {
	matched := make([]bool, len(confirmed))
	kept := make([]types.OutboxEntry, 0, len(latest.Entries))
	removed := 0
	for _, entry := range latest.Entries {
		for i, snapshot := range confirmed {
			if !matched[i] && reflect.DeepEqual(entry, snapshot) {
				matched[i] = true
				removed++
				goto nextEntry
			}
		}
		kept = append(kept, entry)
	nextEntry:
	}
	latest.Entries = kept
	return removed
}

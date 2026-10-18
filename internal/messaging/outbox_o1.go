package messaging

import (
	"errors"
	"fmt"
	"os"

	"github.com/iDoris-ai/hyphae/pkg/types"
)

// GroupEnqueueRecord is the O1 input: the frozen signed event and recipient
// from the prepared fanout row, the relays chosen for it ([] when nil), its
// max_retries and the enqueue time (the entry's created_at).
type GroupEnqueueRecord struct {
	EventJSON, RecipientNpub string
	Relays                   []string
	MaxRetries               int64
	CreatedAt                int64
}

// ErrOutboxEnqueueConflict: the event is already queued under a different
// recipient, route, or frozen bytes, or is no longer an active pending entry.
var ErrOutboxEnqueueConflict = errors.New("outbox already holds a conflicting entry for this event")

// classifyGroupEnqueue reports the reusable queue_id (existed) or a conflict for rec in file; ("", false, nil) means append.
func classifyGroupEnqueue(file *rawOutboxFile, eventID string, rec GroupEnqueueRecord) (queueID string, existed bool, err error) {
	for i := range file.Entries {
		e := file.Entries[i].Parsed
		if e.Entry.ID != eventID {
			continue
		}
		if e.Route == OutboxRouteGroup && e.Entry.EventJSON == rec.EventJSON && e.Entry.RecipientNpub == rec.RecipientNpub &&
			e.Entry.Status == OutboxStatusGroupPending && int64(e.Entry.RetryCount) < int64(e.Entry.MaxRetries) &&
			int64(e.Entry.MaxRetries) == rec.MaxRetries && e.FailureResult == nil && e.RecoveryWitness == nil {
			return e.QueueID, true, nil
		}
		return "", false, fmt.Errorf("%w: event %s", ErrOutboxEnqueueConflict, eventID)
	}
	return "", false, nil
}

// CheckGroupEnqueueLocked runs every deterministic O1 refusal for rec without writing.
func CheckGroupEnqueueLocked(path string, rec GroupEnqueueRecord) (queueID string, existed bool, err error) {
	if rec.RecipientNpub == "" || rec.MaxRetries < 1 || rec.CreatedAt < 0 {
		return "", false, fmt.Errorf("group enqueue requires recipient, positive max_retries and non-negative created_at")
	}
	eventID, err := frozenEventID(rec.EventJSON)
	if err != nil {
		return "", false, err
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	file, err := scanRawOutboxFile(raw)
	if err != nil {
		return "", false, err
	}
	return classifyGroupEnqueue(file, eventID, rec)
}

// CommitGroupEnqueueLocked performs O1 for a caller holding
// WithGroupOutboxLock: it appends a protocol-tagged (fanout_protocol=1)
// group_pending entry with a fresh queue_id, or, when the same frozen event
// is already an active pending group entry for the same recipient and
// max_retries, returns that entry's queue_id with existed=true and writes
// nothing. Any other
// entry carrying the event ID is ErrOutboxEnqueueConflict. A missing file is
// created. The caller's T2 (prepared → queued with the returned queue_id)
// follows in SQLite.
func CommitGroupEnqueueLocked(path string, rec GroupEnqueueRecord) (queueID string, existed bool, err error) {
	if rec.RecipientNpub == "" || rec.MaxRetries < 1 || rec.CreatedAt < 0 {
		return "", false, fmt.Errorf("group enqueue requires recipient, positive max_retries and non-negative created_at")
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		if err := writeRawOutboxAtomic(path, []byte("{\n  \"entries\": []\n}\n")); err != nil {
			return "", false, err
		}
	}
	eventID, err := frozenEventID(rec.EventJSON)
	if err != nil {
		return "", false, err
	}
	err = commitRawOutboxEditLocked(path, func(file *rawOutboxFile) ([]byte, error) {
		q, ex, err := classifyGroupEnqueue(file, eventID, rec)
		if err != nil {
			return nil, err
		}
		if ex {
			queueID, existed = q, true
			return file.Raw, nil
		}
		for {
			candidate, err := newOutboxQueueID()
			if err != nil {
				return nil, err
			}
			if _, taken := file.entryByQueueID(candidate); taken != nil {
				queueID = candidate
				break
			}
		}
		entry := types.OutboxEntry{
			QueueID: queueID, Route: OutboxRouteGroup, ID: eventID, EventJSON: rec.EventJSON, RecipientNpub: rec.RecipientNpub,
			Relays: rec.Relays, MaxRetries: int(rec.MaxRetries), CreatedAt: rec.CreatedAt, Status: OutboxStatusGroupPending,
		}
		encoded := encodeLegacyOutboxEntry(entry)
		// Tag the new epoch: fanout_protocol=1 goes before the closing brace.
		encoded = append(encoded[:len(encoded)-1], []byte(`,"fanout_protocol":1}`)...)
		return ApplyRawOutboxPatches(file.Raw, []RawOutboxPatch{appendEntryPatch(file, encoded)})
	})
	if err != nil {
		return "", false, err
	}
	return queueID, existed, nil
}

// frozenEventID decodes only the id member of a frozen event_json string.
func frozenEventID(eventJSON string) (string, error) {
	fields, err := evidenceObject([]byte(eventJSON), "id pubkey created_at kind tags content sig", "")
	if err != nil {
		return "", fmt.Errorf("frozen event: %w", err)
	}
	return requireLowerHex(fields["id"], 64)
}

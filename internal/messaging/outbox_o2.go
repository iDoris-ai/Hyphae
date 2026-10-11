package messaging

import (
	"errors"
	"fmt"
)

// ErrOutboxCleanupHeld reports an O2 that must not run yet: the entry carries
// a failure_result or recovery_witness the caller has not attested as
// consumed, so the record stays until the barrier consumes it (rev7 §3.5).
var ErrOutboxCleanupHeld = errors.New("outbox entry holds an unconsumed record; cleanup held")

// GroupCleanupRecord is the O2 input derived from the accepted SQLite row.
type GroupCleanupRecord struct {
	EventID, QueueID                     string
	FrozenEventJSON                      string
	MaxRetries                           int64
	LastResultDigest, LastRecoveryDigest string
}

// CommitGroupCleanupLocked performs O2 for a caller holding
// WithGroupOutboxLock: it removes exactly the entry whose queue_id and event
// ID match rec, together with one adjacent array separator, leaving every
// other byte of the file untouched. An entry that is already gone is a
// no-op. Any mismatch or unconsumed record writes nothing.
func CommitGroupCleanupLocked(path string, rec GroupCleanupRecord) error {
	return commitRawOutboxEditLocked(path, func(file *rawOutboxFile) ([]byte, error) {
		return planOutboxO2(file, rec)
	})
}

func planOutboxO2(file *rawOutboxFile, rec GroupCleanupRecord) ([]byte, error) {
	index := -1
	for i := range file.Entries {
		if file.Entries[i].Parsed.Entry.ID == rec.EventID {
			index = i
		}
	}
	if index < 0 {
		return file.Raw, nil
	}
	entry := &file.Entries[index]
	e := entry.Parsed
	if e.Route != OutboxRouteGroup || e.QueueID != rec.QueueID || e.Entry.EventJSON != rec.FrozenEventJSON || int64(e.Entry.MaxRetries) != rec.MaxRetries {
		return nil, fmt.Errorf("O2 target does not match the accepted row's queue, frozen event or max_retries")
	}
	for member, digest := range map[string]string{"failure_result": rec.LastResultDigest, "recovery_witness": rec.LastRecoveryDigest} {
		if err := requireConsumedFragment(entry, member, digest); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrOutboxCleanupHeld, err)
		}
	}
	patch := RawOutboxPatch{Start: entry.Span.Start, End: entry.Span.End}
	switch {
	case index > 0:
		patch.Start = file.Entries[index-1].Span.End
	case index+1 < len(file.Entries):
		patch.End = file.Entries[index+1].Span.Start
	default:
		patch.Start = file.ArrayOpen
	}
	return ApplyRawOutboxPatches(file.Raw, []RawOutboxPatch{patch})
}

package messaging

import (
	"bytes"
	"fmt"
	"reflect"
	"strconv"

	"github.com/iDoris-ai/hyphae/pkg/types"
)

// persistOutboxDiffLocked commits a typed UpdateOutbox transaction as raw
// patches when the file carries protocol records that a typed rewrite would
// drop (F2e-2b). Only DM entries may change, and only in the ways the DM
// writers need: a new DM entry appended, a DM entry removed, or its status /
// retry_count / last_attempt replaced and a missing queue_id assigned. Any
// change to a group entry, any other field, or any reordering is refused and
// nothing is written; those changes belong to the fanout boundaries.
func persistOutboxDiffLocked(file string, before, after *types.Outbox) error {
	return commitRawOutboxEditLocked(file, func(f *rawOutboxFile) ([]byte, error) {
		if len(f.Entries) != len(before.Entries) {
			return nil, fmt.Errorf("typed snapshot has %d entries but the file has %d", len(before.Entries), len(f.Entries))
		}
		var patches []RawOutboxPatch
		removed := make([]bool, len(f.Entries))
		i, j := 0, 0
		for j < len(after.Entries) {
			next := after.Entries[j]
			if i < len(before.Entries) && sameOutboxIdentity(before.Entries[i], next) {
				p, err := diffOutboxEntryPatches(&f.Entries[i], before.Entries[i], next)
				if err != nil {
					return nil, err
				}
				patches = append(patches, p...)
				i, j = i+1, j+1
				continue
			}
			if i < len(before.Entries) && !outboxContains(after.Entries, before.Entries[i]) {
				if f.Entries[i].Parsed.Route != "" {
					return nil, fmt.Errorf("%w: typed removal of %s", errOutboxGroupEntryViaRawDM, before.Entries[i].ID)
				}
				removed[i] = true
				i++
				continue
			}
			if i < len(before.Entries) || outboxContains(before.Entries, next) {
				return nil, fmt.Errorf("typed update reordered outbox entries; refusing to rewrite the file")
			}
			if next.Route != "" {
				return nil, fmt.Errorf("%w: typed enqueue of group entry %s", errOutboxGroupEntryViaRawDM, next.ID)
			}
			if next.QueueID == "" {
				return nil, fmt.Errorf("new DM entry %s has no queue_id", next.ID)
			}
			patches = append(patches, appendEntryPatch(f, encodeLegacyOutboxEntry(next)))
			j++
		}
		for ; i < len(before.Entries); i++ {
			if f.Entries[i].Parsed.Route != "" {
				return nil, fmt.Errorf("%w: typed removal of %s", errOutboxGroupEntryViaRawDM, before.Entries[i].ID)
			}
			removed[i] = true
		}
		patches = append(patches, removalRunPatches(f, removed)...)
		return ApplyRawOutboxPatches(f.Raw, patches)
	})
}

// sameOutboxIdentity: the same queued event, allowing a legacy entry to gain
// its queue_id in this transaction.
func sameOutboxIdentity(before, after types.OutboxEntry) bool {
	return before.ID == after.ID && before.EventJSON == after.EventJSON && (before.QueueID == after.QueueID || before.QueueID == "")
}

func outboxContains(entries []types.OutboxEntry, want types.OutboxEntry) bool {
	for _, e := range entries {
		if sameOutboxIdentity(e, want) || sameOutboxIdentity(want, e) {
			return true
		}
	}
	return false
}

// diffOutboxEntryPatches translates the typed field changes of one DM entry
// into value-token patches; anything else is refused.
func diffOutboxEntryPatches(raw *rawOutboxEntry, before, after types.OutboxEntry) ([]RawOutboxPatch, error) {
	if reflect.DeepEqual(before, after) {
		return nil, nil
	}
	if raw.Parsed.Route != "" || after.Route != "" {
		return nil, fmt.Errorf("%w: typed change to %s", errOutboxGroupEntryViaRawDM, before.ID)
	}
	if before.RecipientNpub != after.RecipientNpub || before.MaxRetries != after.MaxRetries || before.CreatedAt != after.CreatedAt ||
		(before.QueueID != "" && before.QueueID != after.QueueID) {
		return nil, fmt.Errorf("typed update changed an immutable field of %s", before.ID)
	}
	// No raw patch expresses a relay change, including nil versus empty
	// (relays:null versus []); compare exactly instead of normalizing (#224).
	if !reflect.DeepEqual(before.Relays, after.Relays) {
		return nil, fmt.Errorf("typed update changed relays of %s", before.ID)
	}
	var patches []RawOutboxPatch
	if before.Status != after.Status {
		var b bytes.Buffer
		appendJSONString(&b, after.Status)
		patches = append(patches, raw.valuePatch("status", b.String()))
	}
	if before.RetryCount != after.RetryCount {
		patches = append(patches, raw.valuePatch("retry_count", strconv.Itoa(after.RetryCount)))
	}
	if before.LastAttempt != after.LastAttempt {
		patches = append(patches, raw.valuePatch("last_attempt", strconv.FormatInt(after.LastAttempt, 10)))
	}
	if before.QueueID == "" && after.QueueID != "" {
		var b bytes.Buffer
		b.WriteString(`,"queue_id":`)
		appendJSONString(&b, after.QueueID)
		at := raw.Span.End - 1
		patches = append(patches, RawOutboxPatch{Start: at, End: at, Replacement: b.Bytes()})
	}
	// The patches must express the whole typed change; a difference they do
	// not cover would otherwise report success without being persisted.
	covered := before
	covered.Status, covered.RetryCount, covered.LastAttempt, covered.QueueID = after.Status, after.RetryCount, after.LastAttempt, after.QueueID
	if !reflect.DeepEqual(covered, after) {
		return nil, fmt.Errorf("typed update changed an unsupported field of %s", before.ID)
	}
	return patches, nil
}

// removalRunPatches removes each contiguous run of removed entries with one
// patch, so runs never overlap (#224). A run after a retained entry takes the
// separator in front of it; a leading run takes the separator up to the next
// retained entry; a run covering every entry empties the array. Retained
// entries' bytes are never inside a removal range.
func removalRunPatches(f *rawOutboxFile, removed []bool) []RawOutboxPatch {
	var patches []RawOutboxPatch
	for first := 0; first < len(removed); first++ {
		if !removed[first] {
			continue
		}
		last := first
		for last+1 < len(removed) && removed[last+1] {
			last++
		}
		patch := RawOutboxPatch{Start: f.Entries[first].Span.Start, End: f.Entries[last].Span.End}
		switch {
		case first > 0:
			patch.Start = f.Entries[first-1].Span.End
		case last+1 < len(removed):
			patch.End = f.Entries[last+1].Span.Start
		default:
			patch.Start = f.ArrayOpen
		}
		patches = append(patches, patch)
		first = last
	}
	return patches
}

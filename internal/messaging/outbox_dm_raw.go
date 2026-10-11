package messaging

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strconv"

	"github.com/iDoris-ai/hyphae/pkg/types"
)

// DM raw writers (writer inventory F2e-2). They patch only the DM entry they
// are asked about and leave every other byte of the file -- in particular
// group entries and their failure_result / recovery_witness fragments --
// untouched, so DM traffic keeps flowing while the file carries protocol
// records that the typed writers refuse (F2e-1). Group entries change only
// through O1/O3/D6/O2. All functions expect the caller to hold the outbox
// lock (WithGroupOutboxLock / withOutboxLock). Inert: no caller yet.

var errOutboxGroupEntryViaRawDM = errors.New("group outbox entries change only through the fanout boundaries")

// appendOutboxEntryLocked appends a new legacy-schema DM entry. A missing
// file is created. Duplicate event or queue IDs write nothing.
func appendOutboxEntryLocked(path string, entry types.OutboxEntry) error {
	if entry.Route != "" || entry.QueueID == "" || entry.Status != "pending" {
		return fmt.Errorf("raw DM append requires an empty route, a queue_id and pending status")
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		if err := writeRawOutboxAtomic(path, []byte("{\n  \"entries\": []\n}\n")); err != nil {
			return err
		}
	}
	encoded := encodeLegacyOutboxEntry(entry)
	return commitRawOutboxEditLocked(path, func(file *rawOutboxFile) ([]byte, error) {
		return ApplyRawOutboxPatches(file.Raw, []RawOutboxPatch{appendEntryPatch(file, encoded)})
	})
}

// outboxDMPatch names the only DM fields a send outcome may change.
type outboxDMPatch struct {
	Status      *string
	RetryCount  *int64
	LastAttempt *int64
}

// patchOutboxEntryLocked replaces the value tokens of the named fields on
// the DM entry carrying eventID. A missing entry is errOutboxEntrySuperseded.
func patchOutboxEntryLocked(path, eventID string, patch outboxDMPatch) error {
	return commitRawOutboxEditLocked(path, func(file *rawOutboxFile) ([]byte, error) {
		entry, err := file.dmEntryByEventID(eventID)
		if err != nil {
			return nil, err
		}
		var patches []RawOutboxPatch
		if patch.Status != nil {
			var b bytes.Buffer
			appendJSONString(&b, *patch.Status)
			patches = append(patches, entry.valuePatch("status", b.String()))
		}
		if patch.RetryCount != nil {
			patches = append(patches, entry.valuePatch("retry_count", strconv.FormatInt(*patch.RetryCount, 10)))
		}
		if patch.LastAttempt != nil {
			patches = append(patches, entry.valuePatch("last_attempt", strconv.FormatInt(*patch.LastAttempt, 10)))
		}
		return ApplyRawOutboxPatches(file.Raw, patches)
	})
}

// removeOutboxEntryLocked removes the DM entry carrying eventID together
// with one array separator. A missing entry is errOutboxEntrySuperseded.
func removeOutboxEntryLocked(path, eventID string) error {
	return commitRawOutboxEditLocked(path, func(file *rawOutboxFile) ([]byte, error) {
		entry, err := file.dmEntryByEventID(eventID)
		if err != nil {
			return nil, err
		}
		return ApplyRawOutboxPatches(file.Raw, []RawOutboxPatch{removalPatch(file, entry)})
	})
}

func (f *rawOutboxFile) dmEntryByEventID(eventID string) (*rawOutboxEntry, error) {
	for i := range f.Entries {
		if f.Entries[i].Parsed.Entry.ID != eventID {
			continue
		}
		if f.Entries[i].Parsed.Route != "" {
			return nil, errOutboxGroupEntryViaRawDM
		}
		return &f.Entries[i], nil
	}
	return nil, errOutboxEntrySuperseded
}

// appendEntryPatch places encoded after the last entry (or into an empty array).
func appendEntryPatch(file *rawOutboxFile, encoded []byte) RawOutboxPatch {
	if len(file.Entries) == 0 {
		return RawOutboxPatch{Start: file.ArrayOpen, End: file.ArrayOpen, Replacement: encoded}
	}
	at := file.Entries[len(file.Entries)-1].Span.End
	return RawOutboxPatch{Start: at, End: at, Replacement: append([]byte(","), encoded...)}
}

// removalPatch removes an entry object plus one adjacent separator so the
// remaining entries' bytes are untouched.
func removalPatch(file *rawOutboxFile, entry *rawOutboxEntry) RawOutboxPatch {
	index := -1
	for i := range file.Entries {
		if &file.Entries[i] == entry {
			index = i
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
	return patch
}

// encodeLegacyOutboxEntry writes a compact legacy-schema entry. New entries
// write relays as [] (never null) per the schema supplement §1.
func encodeLegacyOutboxEntry(e types.OutboxEntry) []byte {
	var b bytes.Buffer
	o := newRawJSONObject(&b)
	o.str("queue_id", e.QueueID)
	if e.Route != "" {
		o.str("route", e.Route)
	}
	o.str("id", e.ID)
	o.str("event_json", e.EventJSON)
	o.str("recipient_npub", e.RecipientNpub)
	var relays bytes.Buffer
	relays.WriteByte('[')
	for i, relay := range e.Relays {
		if i > 0 {
			relays.WriteByte(',')
		}
		appendJSONString(&relays, relay)
	}
	relays.WriteByte(']')
	o.raw("relays", relays.Bytes())
	o.num("retry_count", int64(e.RetryCount))
	o.num("max_retries", int64(e.MaxRetries))
	o.num("last_attempt", e.LastAttempt)
	o.num("created_at", e.CreatedAt)
	o.str("status", e.Status)
	o.close()
	return b.Bytes()
}

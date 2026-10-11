package messaging

import (
	"bytes"
	"fmt"
)

// outboxD6 is one exact-condition D6 commit (schema supplement §4). Witness's
// typed members are serialized; its Raw and Fields are ignored. For
// reason=missing, EventJSON is the frozen signed event text from the fanout
// row; for the exhausted reasons it must be empty and the old entry's
// event_json fragment is carried over verbatim. Consumed*Digest attest the old
// entry's existing records exactly as in outboxO3. RecoveryID and authorization
// stay the caller's; this type grants no SQLite authorization.
type outboxD6 struct {
	Witness                                     RawRecoveryWitness
	EventJSON                                   string
	ConsumedResultDigest, ConsumedWitnessDigest string
}

// commitOutboxD6 removes the old epoch entry (if any) and adds the new
// group_pending entry carrying recovery_witness in one atomic replace, or
// writes nothing.
func commitOutboxD6(path string, d6 outboxD6) error {
	return commitRawOutboxEdit(path, func(file *rawOutboxFile) ([]byte, error) {
		return planOutboxD6(file, d6)
	})
}

func planOutboxD6(file *rawOutboxFile, d6 outboxD6) ([]byte, error) {
	encoded := encodeRecoveryWitness(&d6.Witness)
	w, err := ParseRawRecoveryWitness(encoded)
	if err != nil {
		return nil, fmt.Errorf("recovery_witness: %w", err)
	}
	if _, err := file.entryByQueueID(w.NewQueueID); err == nil {
		return nil, fmt.Errorf("new_queue_id %q collides with an existing entry", w.NewQueueID)
	}
	var old *rawOutboxEntry
	var eventToken []byte
	eventText := d6.EventJSON
	if w.Reason == witnessReasonMissing {
		for _, entry := range file.Entries {
			if entry.Parsed.Entry.ID == w.EventID || (w.OldQueueID != "" && entry.Parsed.QueueID == w.OldQueueID) {
				return nil, fmt.Errorf("missing witness but the event or old queue is still present")
			}
		}
		if d6.ConsumedResultDigest != "" || d6.ConsumedWitnessDigest != "" || d6.EventJSON == "" {
			return nil, fmt.Errorf("missing witness takes the frozen event and no consumed records")
		}
		var b bytes.Buffer
		appendJSONString(&b, d6.EventJSON)
		eventToken = b.Bytes()
	} else {
		if old, err = file.entryByQueueID(w.OldQueueID); err != nil {
			return nil, err
		}
		e := old.Parsed
		if d6.EventJSON != "" || e.Route != OutboxRouteGroup || e.Entry.ID != w.EventID || e.Entry.RecipientNpub != w.RecipientNpub ||
			int64(e.Entry.MaxRetries) != w.MaxRetries || e.Entry.Status != w.OldStatus || int64(e.Entry.RetryCount) != w.OldRetryCount ||
			sha256Hex(file.Raw[old.Span.Start:old.Span.End]) != w.OldEntryDigest {
			return nil, fmt.Errorf("exhausted witness does not match the old entry pre-image")
		}
		if err := requireConsumedFragment(old, "failure_result", d6.ConsumedResultDigest); err != nil {
			return nil, err
		}
		if err := requireConsumedFragment(old, "recovery_witness", d6.ConsumedWitnessDigest); err != nil {
			return nil, err
		}
		span := old.Values["event_json"]
		eventToken, eventText = file.Raw[span.Start:span.End], e.Entry.EventJSON
	}
	if sha256Hex([]byte(eventText)) != w.EventJSONDigest {
		return nil, fmt.Errorf("event_json_digest does not match the frozen event")
	}
	entry := encodeD6Entry(w, eventToken, encoded)
	switch {
	case old != nil:
		return ApplyRawOutboxPatches(file.Raw, []RawOutboxPatch{{Start: old.Span.Start, End: old.Span.End, Replacement: entry}})
	case len(file.Entries) == 0:
		return ApplyRawOutboxPatches(file.Raw, []RawOutboxPatch{{Start: file.ArrayOpen, End: file.ArrayOpen, Replacement: entry}})
	default:
		at := file.Entries[len(file.Entries)-1].Span.End
		return ApplyRawOutboxPatches(file.Raw, []RawOutboxPatch{{Start: at, End: at, Replacement: append([]byte(","), entry...)}})
	}
}

// encodeD6Entry builds the new epoch entry: group_pending, zero budget,
// created_at=authorized_at (P6), default relays [] and the frozen event.
// Sender, signature and witness binding are re-checked by the strict re-scan.
func encodeD6Entry(w *RawRecoveryWitness, eventToken, witness []byte) []byte {
	var b bytes.Buffer
	o := newRawJSONObject(&b)
	o.str("id", w.EventID)
	o.raw("event_json", eventToken)
	o.str("recipient_npub", w.RecipientNpub)
	o.raw("relays", []byte("[]"))
	o.num("retry_count", 0)
	o.num("max_retries", w.MaxRetries)
	o.num("last_attempt", 0)
	o.num("created_at", w.AuthorizedAt)
	o.str("status", OutboxStatusGroupPending)
	o.str("route", OutboxRouteGroup)
	o.str("queue_id", w.NewQueueID)
	o.num("fanout_protocol", 1)
	o.raw("recovery_witness", witness)
	o.close()
	return b.Bytes()
}

// encodeRecoveryWitness writes the compact v1 object with only the members
// that the reason allows.
func encodeRecoveryWitness(w *RawRecoveryWitness) []byte {
	var b bytes.Buffer
	o := newRawJSONObject(&b)
	o.num("version", w.Version)
	o.str("recovery_id", w.RecoveryID)
	o.raw("fanout_key", encodeFanoutKey(w.FanoutKey))
	o.str("event_id", w.EventID)
	o.str("event_json_digest", w.EventJSONDigest)
	o.str("recipient_npub", w.RecipientNpub)
	o.num("max_retries", w.MaxRetries)
	o.num("expected_row_revision", w.ExpectedRowRevision)
	o.num("expected_attempt_generation", w.ExpectedAttemptGeneration)
	o.str("old_queue_id", w.OldQueueID)
	o.str("new_queue_id", w.NewQueueID)
	o.str("reason", w.Reason)
	if w.Reason == witnessReasonMissing {
		o.raw("observed_absent", []byte("true"))
	} else {
		o.str("old_entry_digest", w.OldEntryDigest)
		o.str("old_status", w.OldStatus)
		o.num("old_retry_count", w.OldRetryCount)
	}
	o.num("authorized_at", w.AuthorizedAt)
	o.close()
	return b.Bytes()
}

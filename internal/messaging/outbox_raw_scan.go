package messaging

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/iDoris-ai/hyphae/internal/common"
)

// rawSpan is the byte range [Start,End) of one JSON value token inside a raw
// outbox snapshot, excluding the whitespace around it.
type rawSpan struct{ Start, End int }

// rawOutboxEntry is one entry of a scanned snapshot: the entry object span,
// the absolute span of each member's value token, and its strict F2b parse.
type rawOutboxEntry struct {
	Span   rawSpan
	Values map[string]rawSpan
	Parsed *RawGroupEntry
}

// rawOutboxFile is a writer's view of a snapshot. ArrayOpen is the offset just
// past the entries array's '['.
type rawOutboxFile struct {
	Raw       []byte
	ArrayOpen int
	Entries   []rawOutboxEntry
}

// scanRawOutboxFile is the strict whole-file gate for raw writers (schema
// supplement §1/§5): every entry, DM or group, must pass ParseRawGroupEntry,
// and outer/event IDs and QueueIDs must be unique across every route and
// status. A failure_result or recovery_witness must bind the entry that carries
// it. Any problem is returned as an error so the caller writes nothing.
func scanRawOutboxFile(raw []byte) (*rawOutboxFile, error) {
	if _, err := evidenceObject(raw, "entries", ""); err != nil {
		return nil, err
	}
	file := &rawOutboxFile{Raw: raw}
	var entrySpans []rawSpan
	if err := walkRawObject(raw, 0, func(key string, value rawSpan) error {
		var err error
		file.ArrayOpen = value.Start + 1
		entrySpans, err = rawArraySpans(raw, value)
		return err
	}); err != nil {
		return nil, err
	}
	var problems []error
	ids, queues := map[string]int{}, map[string]int{}
	for i, span := range entrySpans {
		entry := rawOutboxEntry{Span: span, Values: map[string]rawSpan{}}
		if err := walkRawObject(raw, span.Start, func(key string, value rawSpan) error {
			entry.Values[key] = value
			return nil
		}); err != nil {
			return nil, fmt.Errorf("entry %d: %w", i, err)
		}
		parsed, err := ParseRawGroupEntry(raw[span.Start:span.End])
		entry.Parsed = parsed
		file.Entries = append(file.Entries, entry)
		if err == nil {
			err = bindEntryMetadata(parsed)
		}
		if err != nil {
			problems = append(problems, fmt.Errorf("entry %d: %w", i, err))
			continue
		}
		if previous, exists := ids[parsed.Entry.ID]; exists {
			problems = append(problems, fmt.Errorf("entry %d: duplicate id of entry %d", i, previous))
		}
		ids[parsed.Entry.ID] = i
		if parsed.QueueID == "" {
			continue
		}
		if previous, exists := queues[parsed.QueueID]; exists {
			problems = append(problems, fmt.Errorf("entry %d: duplicate queue_id of entry %d", i, previous))
		}
		queues[parsed.QueueID] = i
	}
	return file, errors.Join(problems...)
}

// bindEntryMetadata checks that a failure_result token and a recovery_witness
// name this entry: event ID, QueueID, recipient, signed sender, and (witness)
// max_retries and frozen event digest.
func bindEntryMetadata(e *RawGroupEntry) error {
	if e.FailureResult == nil && e.RecoveryWitness == nil {
		return nil
	}
	sender, err := entrySenderNpub(e)
	if err != nil {
		return err
	}
	if r := e.FailureResult; r != nil {
		key := r.Token.FanoutKey
		if r.Token.EventID != e.Entry.ID || r.Token.QueueID != e.QueueID || key.RecipientNpub != e.Entry.RecipientNpub || key.LocalNpub != sender {
			return fmt.Errorf("failure_result token does not bind this entry")
		}
	}
	if w := e.RecoveryWitness; w != nil {
		if w.EventID != e.Entry.ID || w.NewQueueID != e.QueueID || w.RecipientNpub != e.Entry.RecipientNpub ||
			w.FanoutKey.LocalNpub != sender || w.MaxRetries != int64(e.Entry.MaxRetries) || w.EventJSONDigest != sha256Hex([]byte(e.Entry.EventJSON)) {
			return fmt.Errorf("recovery_witness does not bind this entry")
		}
	}
	return nil
}

func entrySenderNpub(e *RawGroupEntry) (string, error) {
	fields, err := evidenceObject([]byte(e.Entry.EventJSON), "id pubkey created_at kind tags content sig", "")
	if err != nil {
		return "", err
	}
	var pubkey string
	if err := json.Unmarshal(fields["pubkey"], &pubkey); err != nil {
		return "", err
	}
	pk, err := common.ParsePublicKey(pubkey)
	if err != nil {
		return "", err
	}
	return common.EncodeNpub(pk), nil
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// walkRawObject reports the absolute value span of every member of the object
// that starts at raw[start]. Spans are checked against the decoded bytes.
func walkRawObject(raw []byte, start int, visit func(key string, value rawSpan) error) error {
	decoder := json.NewDecoder(bytes.NewReader(raw[start:]))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return fmt.Errorf("expected object")
	}
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return err
		}
		span, err := nextRawValue(raw, start, decoder)
		if err != nil {
			return err
		}
		if err := visit(key.(string), span); err != nil {
			return err
		}
	}
	return nil
}

func rawArraySpans(raw []byte, array rawSpan) ([]rawSpan, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw[array.Start:array.End]))
	if token, err := decoder.Token(); err != nil || token != json.Delim('[') {
		return nil, fmt.Errorf("expected array")
	}
	var spans []rawSpan
	for decoder.More() {
		span, err := nextRawValue(raw, array.Start, decoder)
		if err != nil {
			return nil, err
		}
		spans = append(spans, span)
	}
	return spans, nil
}

func nextRawValue(raw []byte, base int, decoder *json.Decoder) (rawSpan, error) {
	var value json.RawMessage
	if err := decoder.Decode(&value); err != nil {
		return rawSpan{}, err
	}
	end := base + int(decoder.InputOffset())
	span := rawSpan{Start: end - len(value), End: end}
	if span.Start < 0 || !bytes.Equal(raw[span.Start:span.End], value) {
		return rawSpan{}, fmt.Errorf("value span does not match raw bytes")
	}
	return span, nil
}

// commitRawOutboxEdit reads the snapshot under the sibling outbox lock, runs
// the strict scan, and atomically replaces the file with edit's bytes after
// they pass the same scan. A scan or edit error, or bytes identical to the
// snapshot, writes nothing and leaves the file's mtime untouched. edit may
// mutate file.Raw in place (e.g. a same-length span patch) and return it: the
// no-op comparison is taken against an immutable clone of the on-disk bytes
// made before edit runs, not against raw itself, so an in-place mutation is
// never compared with itself.
func commitRawOutboxEdit(path string, edit func(*rawOutboxFile) ([]byte, error)) error {
	return withOutboxLock(path, func() error {
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		snapshot := bytes.Clone(raw)
		file, err := scanRawOutboxFile(raw)
		if err != nil {
			return err
		}
		next, err := edit(file)
		if err != nil {
			return err
		}
		if bytes.Equal(next, snapshot) {
			return nil
		}
		if _, err := scanRawOutboxFile(next); err != nil {
			return fmt.Errorf("edited outbox fails strict scan: %w", err)
		}
		return writeRawOutboxAtomic(path, next)
	})
}

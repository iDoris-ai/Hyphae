package messaging

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strings"
	"unicode/utf8"

	"fiatjaf.com/nostr"
	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/pkg/types"
)

// RawOutboxEvidence is a read-only review snapshot of the approved legacy schema.
// Raw and Fields retain exact JSON fragments; Event retains the decoded event_json
// string bytes. No normalization, queue identity assignment, or persistence occurs.
// Future protocol/result/witness fields are unsupported and fail closed.
// A non-nil error makes the entire snapshot unsuitable for adoption or sending.
type RawOutboxEvidence struct {
	Raw     []byte
	Entries []RawOutboxEntry
}

type RawOutboxEntry struct {
	Raw    json.RawMessage
	Fields map[string]json.RawMessage
	Event  []byte
	Entry  types.OutboxEntry
}

// ParseRawOutboxEvidence validates every entry (including DM and terminal states)
// and detects collisions across the complete file. It returns available raw
// evidence even on error. Returned bytes are independent of the caller's buffer.
func ParseRawOutboxEvidence(data []byte) (*RawOutboxEvidence, error) {
	evidence := &RawOutboxEvidence{Raw: bytes.Clone(data)}
	root, err := evidenceObject(data, "entries", "")
	if err != nil {
		return evidence, err
	}
	entries, err := evidenceArray(root["entries"])
	if err != nil {
		return evidence, fmt.Errorf("entries: %w", err)
	}
	var problems []error
	ids, queues := map[string]int{}, map[string]int{}
	for i, raw := range entries {
		entry, err := parseRawOutboxEntry(raw)
		evidence.Entries = append(evidence.Entries, entry)
		if err != nil {
			problems = append(problems, fmt.Errorf("entry %d: %w", i, err))
		}
		// Collect identities even from invalid entries; never filter by route/status.
		identities := map[string]json.RawMessage{"id": entry.Fields["id"], "queue_id": entry.Fields["queue_id"]}
		var eventText string
		if json.Unmarshal(entry.Fields["event_json"], &eventText) == nil {
			if event, err := evidenceObject([]byte(eventText), "id pubkey created_at kind tags content sig", ""); err == nil {
				identities["event_id"] = event["id"]
			}
		}
		for field, value := range identities {
			var id string
			if json.Unmarshal(value, &id) != nil || id == "" {
				continue
			}
			seen := ids
			if field == "queue_id" {
				seen = queues
			}
			if previous, exists := seen[id]; exists && previous != i {
				problems = append(problems, fmt.Errorf("entry %d: duplicate %s", i, field))
			}
			seen[id] = i
		}
	}
	return evidence, errors.Join(problems...)
}

func parseRawOutboxEntry(raw json.RawMessage) (RawOutboxEntry, error) {
	result := RawOutboxEntry{Raw: raw}
	fields, err := evidenceObject(raw, "id event_json recipient_npub relays retry_count max_retries last_attempt created_at status", "route queue_id")
	result.Fields = fields
	if err != nil {
		return result, err
	}
	for _, key := range []string{"id", "event_json", "recipient_npub", "status", "route", "queue_id"} {
		value, exists := fields[key]
		if !exists {
			continue
		}
		var s string
		if err := json.Unmarshal(value, &s); err != nil || (s == "" && key != "route" && key != "queue_id") {
			return result, fmt.Errorf("invalid %s string", key)
		}
	}
	for _, key := range []string{"retry_count", "max_retries", "last_attempt", "created_at"} {
		minimum, maximum := int64(0), int64(math.MaxInt64)
		if key == "max_retries" {
			minimum = 1
		}
		if key == "retry_count" || key == "max_retries" {
			maximum = int64(math.MaxInt)
		}
		if err := evidenceInteger(fields[key], minimum, maximum); err != nil {
			return result, fmt.Errorf("%s: %w", key, err)
		}
	}
	if !bytes.Equal(bytes.TrimSpace(fields["relays"]), []byte("null")) {
		relays, err := evidenceArray(fields["relays"])
		if err != nil {
			return result, fmt.Errorf("relays: %w", err)
		}
		for _, relay := range relays {
			var value string
			if len(relay) == 0 || relay[0] != '"' || json.Unmarshal(relay, &value) != nil {
				return result, fmt.Errorf("invalid relay member")
			}
		}
	}
	if err := json.Unmarshal(raw, &result.Entry); err != nil {
		return result, err
	}
	e := result.Entry
	switch e.Route {
	case "":
		if e.Status != "pending" && e.Status != "failed" && e.Status != "sent" {
			return result, fmt.Errorf("invalid DM status")
		}
	case OutboxRouteGroup:
		if e.QueueID == "" || (e.Status != OutboxStatusGroupPending && e.Status != OutboxStatusGroupFailed) || (e.Status == OutboxStatusGroupFailed && e.RetryCount < e.MaxRetries) {
			return result, fmt.Errorf("invalid group identity/status")
		}
	default:
		return result, fmt.Errorf("unsupported route")
	}
	result.Event = []byte(e.EventJSON)
	if err := validateEvidenceEvent(result.Event, e); err != nil {
		return result, err
	}
	return result, nil
}

func validateEvidenceEvent(raw []byte, entry types.OutboxEntry) error {
	fields, err := evidenceObject(raw, "id pubkey created_at kind tags content sig", "")
	if err != nil {
		return fmt.Errorf("event: %w", err)
	}
	for key, size := range map[string]int{"id": 64, "pubkey": 64, "sig": 128, "content": -1} {
		var s string
		if json.Unmarshal(fields[key], &s) != nil || (size >= 0 && (len(s) != size || !isHexString(s))) {
			return fmt.Errorf("invalid event %s", key)
		}
	}
	if err := evidenceInteger(fields["kind"], 0, 65535); err != nil {
		return err
	}
	if err := evidenceInteger(fields["created_at"], 0, math.MaxInt64); err != nil {
		return err
	}
	tags, err := evidenceArray(fields["tags"])
	if err != nil {
		return err
	}
	for _, tag := range tags {
		parts, err := evidenceArray(tag)
		if err != nil {
			return err
		}
		for _, part := range parts {
			var s string
			if len(part) == 0 || part[0] != '"' || json.Unmarshal(part, &s) != nil {
				return fmt.Errorf("invalid event tag member")
			}
		}
	}
	var event nostr.Event
	if err := json.Unmarshal(raw, &event); err != nil {
		return err
	}
	if event.ID.Hex() != entry.ID || !event.CheckID() || !event.VerifySignature() {
		return fmt.Errorf("invalid event identity/signature")
	}
	if err := ValidateAgentMessageEvent(&event); err != nil {
		return err
	}
	if entry.Route == OutboxRouteGroup {
		enc, found, err := outboxTagValue(event.Tags, "enc")
		if err != nil || !found || enc != "nip44" {
			return fmt.Errorf("invalid group encryption tag")
		}
	}
	recipient, err := common.ParsePublicKey(entry.RecipientNpub)
	if err != nil {
		return fmt.Errorf("invalid recipient")
	}
	count := 0
	for _, tag := range event.Tags {
		if len(tag) > 0 && tag[0] == "p" {
			count++
			if len(tag) < 2 || tag[1] != recipient.Hex() {
				return fmt.Errorf("event recipient mismatch")
			}
		}
	}
	if count != 1 {
		return fmt.Errorf("event requires one recipient")
	}
	return nil
}

// Decode keys before comparison so escaped-equivalent spellings collide. Read
// raw values before any typed decode; reject unknown keys, null, and omissions.
func evidenceObject(raw []byte, required, optional string) (map[string]json.RawMessage, error) {
	fields := map[string]json.RawMessage{}
	if !utf8.Valid(raw) {
		return fields, fmt.Errorf("invalid UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return fields, fmt.Errorf("expected object")
	}
	allowed := strings.Fields(required + " " + optional)
	var problems []error
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return fields, err
		}
		name, ok := key.(string)
		if !ok {
			return fields, fmt.Errorf("expected key")
		}
		start := int(decoder.InputOffset())
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return fields, err
		}
		if _, exists := fields[name]; exists {
			problems = append(problems, fmt.Errorf("duplicate key %q", name))
		}
		if !slices.Contains(allowed, name) {
			problems = append(problems, fmt.Errorf("unknown key %q", name))
		}
		if bytes.Equal(value, []byte("null")) && name != "relays" {
			problems = append(problems, fmt.Errorf("null %s", name))
		}
		// Preserve whitespace on both sides of the original value token.
		start += bytes.IndexByte(raw[start:], ':') + 1
		end := int(decoder.InputOffset())
		for end < len(raw) && strings.ContainsRune(" \t\r\n", rune(raw[end])) {
			end++
		}
		fields[name] = bytes.Clone(raw[start:end])
	}
	if _, err := decoder.Token(); err != nil {
		return fields, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return fields, fmt.Errorf("trailing JSON tokens")
	}
	for _, key := range strings.Fields(required) {
		if _, exists := fields[key]; !exists {
			problems = append(problems, fmt.Errorf("missing %s", key))
		}
	}
	return fields, errors.Join(problems...)
}

func evidenceArray(raw []byte) ([]json.RawMessage, error) {
	raw = bytes.TrimSpace(raw)
	var values []json.RawMessage
	if len(raw) == 0 || raw[0] != '[' {
		return nil, fmt.Errorf("expected array")
	}
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, err
	}
	return values, nil
}

func evidenceInteger(raw []byte, minimum, maximum int64) error {
	var value int64
	if json.Unmarshal(raw, &value) != nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || value < minimum || value > maximum {
		return fmt.Errorf("invalid integer/range")
	}
	return nil
}

package messaging

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"fiatjaf.com/nostr"
	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/pkg/types"
)

// Inert F2b-3 reader for the proposed group-entry metadata relations (schema
// supplement §1/§3). ParseRawGroupEntry applies the F2a entry rules to the
// optional fanout_protocol/failure_result/recovery_witness members and reuses
// the F2b-1/F2b-2 parsers for the nested objects. No production path calls it.
type RawGroupEntry struct {
	Raw    []byte
	Fields map[string]json.RawMessage
	Entry  types.OutboxEntry

	Route           string
	QueueID         string
	FanoutProtocol  *int64
	FailureResult   *RawFailureResult
	RecoveryWitness *RawRecoveryWitness
}

func ParseRawGroupEntry(raw []byte) (*RawGroupEntry, error) {
	e := &RawGroupEntry{Raw: bytes.Clone(raw)}
	fields, err := evidenceObject(raw,
		"id event_json recipient_npub relays retry_count max_retries last_attempt created_at status",
		"route queue_id fanout_protocol failure_result recovery_witness")
	e.Fields = fields
	if err != nil {
		return e, err
	}
	for _, key := range []string{"id", "event_json", "recipient_npub", "status"} {
		if s, err := strictJSONString(fields[key]); err != nil || s == "" {
			return e, fmt.Errorf("%s: invalid string", key)
		}
	}
	if v, ok := fields["route"]; ok {
		if e.Route, err = strictJSONString(v); err != nil {
			return e, fmt.Errorf("route: invalid string")
		}
	}
	if v, ok := fields["queue_id"]; ok {
		if e.QueueID, err = strictJSONString(v); err != nil {
			return e, fmt.Errorf("queue_id: invalid string")
		}
	}
	for _, f := range []struct {
		name string
		min  int64
		max  int64
	}{
		{"retry_count", 0, maxCountValue},
		{"max_retries", 1, maxCountValue},
		{"last_attempt", 0, maxSignedInt64},
		{"created_at", 0, maxSignedInt64},
	} {
		if _, err := evidenceIntegerValue(fields[f.name], f.min, f.max); err != nil {
			return e, fmt.Errorf("%s: %w", f.name, err)
		}
	}
	if !bytes.Equal(bytes.TrimSpace(fields["relays"]), []byte("null")) {
		relays, err := evidenceArray(fields["relays"])
		if err != nil {
			return e, fmt.Errorf("relays: %w", err)
		}
		for _, relay := range relays {
			if _, err := strictJSONString(relay); err != nil {
				return e, fmt.Errorf("relays: invalid member")
			}
		}
	}
	if err := json.Unmarshal(raw, &e.Entry); err != nil {
		return e, err
	}
	switch e.Route {
	case "":
		if e.Entry.Status != "pending" && e.Entry.Status != "failed" && e.Entry.Status != "sent" {
			return e, fmt.Errorf("invalid DM status")
		}
	case OutboxRouteGroup:
		if e.QueueID == "" ||
			(e.Entry.Status != OutboxStatusGroupPending && e.Entry.Status != OutboxStatusGroupFailed) ||
			(e.Entry.Status == OutboxStatusGroupFailed && e.Entry.RetryCount < e.Entry.MaxRetries) {
			return e, fmt.Errorf("invalid group identity/status")
		}
	default:
		return e, fmt.Errorf("unsupported route")
	}
	sender, err := validateEvidenceEvent([]byte(e.Entry.EventJSON), e.Entry)
	if err != nil {
		return e, err
	}
	_, hasProtocol := fields["fanout_protocol"]
	hasResult := fields["failure_result"] != nil
	hasWitness := fields["recovery_witness"] != nil
	if hasProtocol {
		value, err := evidenceIntegerValue(fields["fanout_protocol"], 1, 1)
		if err != nil {
			return e, fmt.Errorf("fanout_protocol: %w", err)
		}
		e.FanoutProtocol = &value
	}
	if e.Route != OutboxRouteGroup && (hasProtocol || hasResult || hasWitness) {
		return e, fmt.Errorf("group metadata requires group route")
	}
	if (hasResult || hasWitness) && e.FanoutProtocol == nil {
		return e, fmt.Errorf("failure_result/recovery_witness require fanout_protocol=1")
	}
	if hasResult {
		if e.FailureResult, err = ParseRawFailureResult(fields["failure_result"]); err != nil {
			return e, fmt.Errorf("failure_result: %w", err)
		}
		if err := bindFailureResultToEntry(e.FailureResult, e.Entry, e.QueueID, sender); err != nil {
			return e, fmt.Errorf("failure_result: %w", err)
		}
	}
	if hasWitness {
		if e.RecoveryWitness, err = ParseRawRecoveryWitness(fields["recovery_witness"]); err != nil {
			return e, fmt.Errorf("recovery_witness: %w", err)
		}
		if err := bindRecoveryWitnessToEntry(e.RecoveryWitness, e.Entry, e.QueueID, sender); err != nil {
			return e, fmt.Errorf("recovery_witness: %w", err)
		}
	}
	return e, nil
}

// bindFailureResultToEntry checks the §2/§3 token-to-entry and
// result-to-entry bindings this ParseRawGroupEntry call already has both
// sides of. It does not check expected_row_revision/attempts/relay_count/
// prior_retry_count, which name a SQLite snapshot this JSON file never
// carries (left to F3/F4).
func bindFailureResultToEntry(result *RawFailureResult, entry types.OutboxEntry, queueID string, sender nostr.PubKey) error {
	if result.Token.EventID != entry.ID {
		return fmt.Errorf("token event_id does not match entry id")
	}
	if result.Token.QueueID != queueID {
		return fmt.Errorf("token queue_id does not match entry queue_id")
	}
	if result.Token.FanoutKey.RecipientNpub != entry.RecipientNpub {
		return fmt.Errorf("token recipient_npub does not match entry recipient_npub")
	}
	if local, err := common.ParsePublicKey(result.Token.FanoutKey.LocalNpub); err != nil || local != sender {
		return fmt.Errorf("token local_npub does not match event sender")
	}
	if result.RetryCount != int64(entry.RetryCount) {
		return fmt.Errorf("entry retry_count does not match result retry_count")
	}
	if result.Status != entry.Status {
		return fmt.Errorf("entry status does not match result status")
	}
	// P4: the entry's own retry budget decides whether a result exhausts it,
	// independent of whatever retry_count/status the result itself carries.
	if entry.RetryCount >= entry.MaxRetries && entry.Status != OutboxStatusGroupFailed {
		return fmt.Errorf("entry status must be group_failed once retry_count reaches max_retries")
	}
	if result.LastAttemptAt != entry.LastAttempt {
		return fmt.Errorf("entry last_attempt does not match result last_attempt_at")
	}
	return nil
}

// bindRecoveryWitnessToEntry checks the §4 bindings between a nested
// recovery_witness and the entry it was committed with in the same atomic
// D6 write (event_id, event_json_digest, recipient_npub, max_retries,
// new_queue_id vs this entry's own queue_id). It does not check the
// prior-epoch fields (old_queue_id, old_entry_digest, old_status,
// old_retry_count, expected_row_revision, expected_attempt_generation):
// those describe the replaced row, which this JSON snapshot no longer
// contains.
func bindRecoveryWitnessToEntry(witness *RawRecoveryWitness, entry types.OutboxEntry, queueID string, sender nostr.PubKey) error {
	if witness.EventID != entry.ID {
		return fmt.Errorf("event_id does not match entry id")
	}
	if witness.EventJSONDigest != entryEventJSONDigest(entry) {
		return fmt.Errorf("event_json_digest does not match entry event_json")
	}
	if witness.RecipientNpub != entry.RecipientNpub {
		return fmt.Errorf("recipient_npub does not match entry recipient_npub")
	}
	if local, err := common.ParsePublicKey(witness.FanoutKey.LocalNpub); err != nil || local != sender {
		return fmt.Errorf("fanout_key local_npub does not match event sender")
	}
	if witness.MaxRetries != int64(entry.MaxRetries) {
		return fmt.Errorf("max_retries does not match entry max_retries")
	}
	if witness.NewQueueID != queueID {
		return fmt.Errorf("new_queue_id does not match entry queue_id")
	}
	return nil
}

// entryEventJSONDigest hashes the entry's decoded event_json string bytes
// (§2 P3: UTF-8 bytes after string-decode, no outer quotes, no
// re-serialization), matching how event_json_digest must be derived.
func entryEventJSONDigest(entry types.OutboxEntry) string {
	sum := sha256.Sum256([]byte(entry.EventJSON))
	return hex.EncodeToString(sum[:])
}

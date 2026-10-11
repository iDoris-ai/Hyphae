package messaging

import (
	"bytes"
	"encoding/json"
	"fmt"

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
		var s string
		if json.Unmarshal(fields[key], &s) != nil || s == "" {
			return e, fmt.Errorf("%s: invalid string", key)
		}
	}
	_ = json.Unmarshal(fields["route"], &e.Route)
	_ = json.Unmarshal(fields["queue_id"], &e.QueueID)
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
			var value string
			if len(relay) == 0 || relay[0] != '"' || json.Unmarshal(relay, &value) != nil {
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
	if err := validateEvidenceEvent([]byte(e.Entry.EventJSON), e.Entry); err != nil {
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
	}
	if hasWitness {
		if e.RecoveryWitness, err = ParseRawRecoveryWitness(fields["recovery_witness"]); err != nil {
			return e, fmt.Errorf("recovery_witness: %w", err)
		}
	}
	return e, nil
}

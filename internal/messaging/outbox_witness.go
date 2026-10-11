package messaging

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Inert F2b-2 reader for the proposed recovery_witness v1 schema (§4).
// ParseRawRecoveryWitness reuses the F2b-1/F2a tokenizer, clones the caller's
// bytes into Raw, keeps each member's value fragment in Fields, and accepts
// reason-conditional members that match the parsed reason. Cross-store
// revision/receipt/consumption checks belong to the F3/F4 locked validators.
const (
	witnessReasonMissing          = "missing"
	witnessReasonExhaustedPending = "exhausted_pending"
	witnessReasonExhaustedFailed  = "exhausted_failed"
)

type RawRecoveryWitness struct {
	Raw    []byte
	Fields map[string]json.RawMessage

	Version                   int64
	RecoveryID                string
	FanoutKey                 OutboxFanoutKey
	EventID                   string
	EventJSONDigest           string
	RecipientNpub             string
	MaxRetries                int64
	ExpectedRowRevision       int64
	ExpectedAttemptGeneration int64
	OldQueueID                string
	NewQueueID                string
	Reason                    string
	AuthorizedAt              int64

	ObservedAbsent bool
	OldEntryDigest string
	OldStatus      string
	OldRetryCount  int64
}

// ParseRawRecoveryWitness strictly parses one complete recovery_witness v1 object.
func ParseRawRecoveryWitness(raw []byte) (*RawRecoveryWitness, error) {
	w := &RawRecoveryWitness{Raw: bytes.Clone(raw)}
	fields, err := evidenceObject(raw,
		"version recovery_id fanout_key event_id event_json_digest recipient_npub max_retries expected_row_revision expected_attempt_generation old_queue_id new_queue_id reason authorized_at",
		"observed_absent old_entry_digest old_status old_retry_count")
	w.Fields = fields
	if err != nil {
		return w, err
	}
	if w.Version, err = evidenceIntegerValue(fields["version"], 1, 1); err != nil {
		return w, fmt.Errorf("version: %w", err)
	}
	if w.RecoveryID, err = requireLowerHex(fields["recovery_id"], 32); err != nil {
		return w, fmt.Errorf("recovery_id: %w", err)
	}
	if w.FanoutKey, err = parseRawFanoutKey(fields["fanout_key"]); err != nil {
		return w, fmt.Errorf("fanout_key: %w", err)
	}
	if w.EventID, err = requireLowerHex(fields["event_id"], 64); err != nil {
		return w, fmt.Errorf("event_id: %w", err)
	}
	if w.EventJSONDigest, err = requireLowerHex(fields["event_json_digest"], 64); err != nil {
		return w, fmt.Errorf("event_json_digest: %w", err)
	}
	if w.RecipientNpub, err = requireCanonicalNpub(fields["recipient_npub"]); err != nil {
		return w, fmt.Errorf("recipient_npub: %w", err)
	}
	if w.RecipientNpub != w.FanoutKey.RecipientNpub {
		return w, fmt.Errorf("recipient_npub: does not match fanout_key")
	}
	for _, f := range []struct {
		name   string
		min    int64
		max    int64
		target *int64
	}{
		{"max_retries", 1, maxCountValue, &w.MaxRetries},
		{"expected_row_revision", 0, maxSignedInt64 - 1, &w.ExpectedRowRevision},
		{"expected_attempt_generation", 0, maxSignedInt64, &w.ExpectedAttemptGeneration},
		{"authorized_at", 0, maxSignedInt64, &w.AuthorizedAt},
	} {
		if *f.target, err = evidenceIntegerValue(fields[f.name], f.min, f.max); err != nil {
			return w, fmt.Errorf("%s: %w", f.name, err)
		}
	}
	if json.Unmarshal(fields["old_queue_id"], &w.OldQueueID) != nil {
		return w, fmt.Errorf("old_queue_id: invalid string")
	}
	if json.Unmarshal(fields["new_queue_id"], &w.NewQueueID) != nil || w.NewQueueID == "" {
		return w, fmt.Errorf("new_queue_id: invalid string")
	}
	if w.NewQueueID == w.OldQueueID {
		return w, fmt.Errorf("new_queue_id: must differ from old_queue_id")
	}
	if json.Unmarshal(fields["reason"], &w.Reason) != nil {
		return w, fmt.Errorf("reason: invalid string")
	}
	if err := w.parseConditional(fields); err != nil {
		return w, err
	}
	return w, nil
}

func (w *RawRecoveryWitness) parseConditional(fields map[string]json.RawMessage) error {
	_, hasObserved := fields["observed_absent"]
	_, hasDigest := fields["old_entry_digest"]
	_, hasStatus := fields["old_status"]
	_, hasCount := fields["old_retry_count"]
	switch w.Reason {
	case witnessReasonMissing:
		if !hasObserved || hasDigest || hasStatus || hasCount {
			return fmt.Errorf("reason: missing requires observed_absent and omits old_* members")
		}
		var absent bool
		if json.Unmarshal(fields["observed_absent"], &absent) != nil || !absent {
			return fmt.Errorf("observed_absent: must be a JSON true")
		}
		w.ObservedAbsent = true
	case witnessReasonExhaustedPending, witnessReasonExhaustedFailed:
		if hasObserved || !hasDigest || !hasStatus || !hasCount {
			return fmt.Errorf("reason: exhausted requires old_entry_digest/old_status/old_retry_count and omits observed_absent")
		}
		var err error
		if w.OldEntryDigest, err = requireLowerHex(fields["old_entry_digest"], 64); err != nil {
			return fmt.Errorf("old_entry_digest: %w", err)
		}
		if json.Unmarshal(fields["old_status"], &w.OldStatus) != nil {
			return fmt.Errorf("old_status: invalid string")
		}
		want := "group_pending"
		if w.Reason == witnessReasonExhaustedFailed {
			want = "group_failed"
		}
		if w.OldStatus != want {
			return fmt.Errorf("old_status: must be %s", want)
		}
		if w.OldRetryCount, err = evidenceIntegerValue(fields["old_retry_count"], 0, maxCountValue); err != nil {
			return fmt.Errorf("old_retry_count: %w", err)
		}
		if w.OldRetryCount < w.MaxRetries {
			return fmt.Errorf("old_retry_count: must be >= max_retries")
		}
		if w.OldQueueID == "" {
			return fmt.Errorf("old_queue_id: required for exhausted")
		}
	default:
		return fmt.Errorf("reason: invalid enum")
	}
	return nil
}

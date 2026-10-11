package messaging

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/iDoris-ai/hyphae/internal/common"
)

// Inert F2b-1 reader for the proposed outbox failure-result wire schema
// (docs/agent/m1-outbox-result-witness-schema.md §2–§3). ParseRawFailureResult
// clones the caller's bytes into Raw and keeps each member's exact value
// fragment in Fields; no production path calls this yet and the schema is still
// awaiting external review.
const failureResultDomain = "hyphae.failure-result.v1"

const (
	maxSignedInt64 = int64(math.MaxInt64) // I64
	maxCountValue  = int64(math.MaxInt)   // C = min(I64, max int)
)

type OutboxFanoutKey struct {
	LocalNpub, RecipientNpub, GroupID, SendKey, EnvelopeType string
}

type OutboxAttemptToken struct {
	FanoutKey         OutboxFanoutKey
	EventID, QueueID  string
	AttemptGeneration int64
}

// RawFailureResult is a strictly validated failure_result v1 object (§3).
type RawFailureResult struct {
	Raw    []byte
	Fields map[string]json.RawMessage
	Token  OutboxAttemptToken

	Version, ExpectedRowRevision, Attempts, LastAttemptAt int64
	RelayCount, PriorRetryCount, RetryCount               int64
	ResultID, Status, Issue                               string
}

// ParseRawFailureResult strictly parses one complete failure_result v1 object.
func ParseRawFailureResult(raw []byte) (*RawFailureResult, error) {
	result := &RawFailureResult{Raw: bytes.Clone(raw)}
	fields, err := evidenceObject(raw,
		"version token result_id expected_row_revision attempts last_attempt_at relay_count prior_retry_count retry_count status issue",
		"")
	result.Fields = fields
	if err != nil {
		return result, err
	}
	if result.Version, err = evidenceIntegerValue(fields["version"], 1, 1); err != nil {
		return result, fmt.Errorf("version: %w", err)
	}
	if result.Token, err = parseRawAttemptToken(fields["token"]); err != nil {
		return result, fmt.Errorf("token: %w", err)
	}
	if result.ResultID, err = requireLowerHex(fields["result_id"], 64); err != nil {
		return result, fmt.Errorf("result_id: %w", err)
	}
	if result.ResultID != failureResultID(result.Token) {
		return result, fmt.Errorf("result_id: does not match derived id")
	}
	for _, f := range []struct {
		name   string
		min    int64
		max    int64
		target *int64
	}{
		{"expected_row_revision", 0, maxSignedInt64 - 1, &result.ExpectedRowRevision},
		{"attempts", 1, maxCountValue, &result.Attempts},
		{"last_attempt_at", 0, maxSignedInt64, &result.LastAttemptAt},
		{"relay_count", 0, maxCountValue, &result.RelayCount},
		{"prior_retry_count", 0, maxCountValue - 1, &result.PriorRetryCount},
		{"retry_count", 1, maxCountValue, &result.RetryCount},
	} {
		if *f.target, err = evidenceIntegerValue(fields[f.name], f.min, f.max); err != nil {
			return result, fmt.Errorf("%s: %w", f.name, err)
		}
	}
	if result.RetryCount != result.PriorRetryCount+1 {
		return result, fmt.Errorf("retry_count: must equal prior_retry_count+1")
	}
	if json.Unmarshal(fields["status"], &result.Status) != nil {
		return result, fmt.Errorf("status: invalid string")
	}
	if json.Unmarshal(fields["issue"], &result.Issue) != nil {
		return result, fmt.Errorf("issue: invalid string")
	}
	if !validStatusIssue(result.Status, result.Issue) {
		return result, fmt.Errorf("issue: invalid status/issue pair")
	}
	return result, nil
}

func parseRawAttemptToken(raw []byte) (OutboxAttemptToken, error) {
	var token OutboxAttemptToken
	fields, err := evidenceObject(raw, "fanout_key event_id queue_id attempt_generation", "")
	if err != nil {
		return token, err
	}
	if token.FanoutKey, err = parseRawFanoutKey(fields["fanout_key"]); err != nil {
		return token, fmt.Errorf("fanout_key: %w", err)
	}
	if token.EventID, err = requireLowerHex(fields["event_id"], 64); err != nil {
		return token, fmt.Errorf("event_id: %w", err)
	}
	if json.Unmarshal(fields["queue_id"], &token.QueueID) != nil || token.QueueID == "" {
		return token, fmt.Errorf("queue_id: invalid string")
	}
	if token.AttemptGeneration, err = evidenceIntegerValue(fields["attempt_generation"], 1, maxSignedInt64); err != nil {
		return token, fmt.Errorf("attempt_generation: %w", err)
	}
	return token, nil
}

func parseRawFanoutKey(raw []byte) (OutboxFanoutKey, error) {
	var key OutboxFanoutKey
	fields, err := evidenceObject(raw, "local_npub recipient_npub group_id send_key envelope_type", "")
	if err != nil {
		return key, err
	}
	if key.LocalNpub, err = requireCanonicalNpub(fields["local_npub"]); err != nil {
		return key, fmt.Errorf("local_npub: %w", err)
	}
	if key.RecipientNpub, err = requireCanonicalNpub(fields["recipient_npub"]); err != nil {
		return key, fmt.Errorf("recipient_npub: %w", err)
	}
	if key.GroupID, err = requireLowerHex(fields["group_id"], 32); err != nil {
		return key, fmt.Errorf("group_id: %w", err)
	}
	if key.SendKey, err = requireLowerHex(fields["send_key"], 32); err != nil {
		return key, fmt.Errorf("send_key: %w", err)
	}
	if json.Unmarshal(fields["envelope_type"], &key.EnvelopeType) != nil || !isEnvelopeType(key.EnvelopeType) {
		return key, fmt.Errorf("envelope_type: invalid")
	}
	return key, nil
}

func requireCanonicalNpub(raw []byte) (string, error) {
	var npub string
	if json.Unmarshal(raw, &npub) != nil {
		return "", fmt.Errorf("invalid string")
	}
	pk, err := common.ParsePublicKey(npub)
	if err != nil || common.EncodeNpub(pk) != npub {
		return "", fmt.Errorf("not a canonical npub")
	}
	return npub, nil
}

func requireLowerHex(raw []byte, size int) (string, error) {
	var value string
	if json.Unmarshal(raw, &value) != nil || len(value) != size || value != strings.ToLower(value) || !isHexString(value) {
		return "", fmt.Errorf("expected %d lowercase hex characters", size)
	}
	return value, nil
}

func isEnvelopeType(value string) bool {
	return slices.Contains([]string{"message", "invite", "accept", "decline", "activate", "cancel"}, value)
}

func validStatusIssue(status, issue string) bool {
	if status == "group_pending" {
		return issue == "send_failed"
	}
	return status == "group_failed" && issue == "retry_exhausted"
}

// evidenceIntegerValue rejects non-integer JSON tokens and out-of-range values.
func evidenceIntegerValue(raw []byte, minimum, maximum int64) (int64, error) {
	var value int64
	if json.Unmarshal(raw, &value) != nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || value < minimum || value > maximum {
		return 0, fmt.Errorf("invalid integer/range")
	}
	return value, nil
}

func failureResultID(token OutboxAttemptToken) string {
	var buf bytes.Buffer
	buf.WriteString(`["` + failureResultDomain + `"`)
	for _, value := range []string{
		token.FanoutKey.LocalNpub, token.FanoutKey.GroupID, token.FanoutKey.EnvelopeType,
		token.FanoutKey.SendKey, token.FanoutKey.RecipientNpub, token.EventID, token.QueueID,
	} {
		buf.WriteByte(',')
		appendJSONString(&buf, value)
	}
	buf.WriteByte(',')
	buf.WriteString(strconv.FormatInt(token.AttemptGeneration, 10))
	buf.WriteByte(']')
	sum := sha256.Sum256(buf.Bytes())
	return hex.EncodeToString(sum[:])
}

// appendJSONString uses lowercase \u00xx for control characters (no short
// escapes), escapes quotes and backslashes, and writes every other byte
// verbatim (including "/" and multi-byte UTF-8).
func appendJSONString(dst *bytes.Buffer, value string) {
	const digits = "0123456789abcdef"
	dst.WriteByte('"')
	for i := 0; i < len(value); i++ {
		switch c := value[i]; {
		case c == '"':
			dst.WriteString(`\"`)
		case c == '\\':
			dst.WriteString(`\\`)
		case c < 0x20:
			dst.WriteString(`\u00`)
			dst.WriteByte(digits[c>>4])
			dst.WriteByte(digits[c&0x0f])
		default:
			dst.WriteByte(c)
		}
	}
	dst.WriteByte('"')
}

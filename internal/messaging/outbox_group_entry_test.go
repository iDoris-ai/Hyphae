package messaging

import (
	"os"
	"strings"
	"testing"
)

func readF2BFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/f2b/" + name)
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return data
}

func TestParseRawGroupEntryWithResult(t *testing.T) {
	raw := readF2BFixture(t, "p-o3-entry.json")
	e, err := ParseRawGroupEntry(raw)
	if err != nil {
		t.Fatalf("parse P-O3 entry: %v", err)
	}
	if e.Route != OutboxRouteGroup || e.QueueID != "2fc72e7f8539e9eff9120c979198790b" {
		t.Fatalf("route/queue mismatch: %+v", e)
	}
	if e.FanoutProtocol == nil || *e.FanoutProtocol != 1 {
		t.Fatalf("fanout_protocol mismatch: %+v", e.FanoutProtocol)
	}
	if e.FailureResult == nil || e.RecoveryWitness != nil {
		t.Fatalf("expected only failure_result: %+v", e)
	}
	if e.FailureResult.ResultID != "2397fad13c91fc597e59c7c97d4e8658dd3080187b0ccc9790b0d1f1cf967346" {
		t.Fatalf("nested result_id = %q", e.FailureResult.ResultID)
	}
}

func TestParseRawGroupEntryWithWitness(t *testing.T) {
	raw := readF2BFixture(t, "p-missing-entry.json")
	e, err := ParseRawGroupEntry(raw)
	if err != nil {
		t.Fatalf("parse P-MISSING entry: %v", err)
	}
	if e.RecoveryWitness == nil || e.FailureResult != nil {
		t.Fatalf("expected only recovery_witness: %+v", e)
	}
	if e.RecoveryWitness.Reason != witnessReasonMissing || !e.RecoveryWitness.ObservedAbsent {
		t.Fatalf("nested witness mismatch: %+v", e.RecoveryWitness)
	}
}

func TestParseRawGroupEntryRejects(t *testing.T) {
	base := string(readF2BFixture(t, "p-o3-entry.json"))
	mut := func(old, replacement string) []byte {
		t.Helper()
		if !strings.Contains(base, old) {
			t.Fatalf("fixture does not contain %q", old)
		}
		return []byte(strings.Replace(base, old, replacement, 1))
	}
	dm := strings.Replace(base, `"route": "group",`, `"route": "",`, 1)
	dm = strings.Replace(dm, `"queue_id": "2fc72e7f8539e9eff9120c979198790b",`+"\n", "", 1)
	dm = strings.Replace(dm, `"status": "group_pending"`, `"status": "pending"`, 1)
	if dm == base {
		t.Fatal("DM mutation did not apply")
	}
	cases := []struct {
		name string
		raw  []byte
	}{
		{"protocol 2", mut(`"fanout_protocol": 1`, `"fanout_protocol": 2`)},
		{"protocol float", mut(`"fanout_protocol": 1`, `"fanout_protocol": 1.0`)},
		{"result without protocol", mut(`"fanout_protocol": 1,`+"\n", "")},
		{"unknown key", mut(`"route": "group",`, "\"route\": \"group\",\n  \"extra\": 1,")},
		{"group_failed below limit", mut(`"status": "group_pending"`, `"status": "group_failed"`)},
		{"unsupported route", mut(`"route": "group"`, `"route": "bogus"`)},
		{"empty queue id", mut(`"queue_id": "2fc72e7f8539e9eff9120c979198790b"`, `"queue_id": ""`)},
		{"dm carrying metadata", []byte(dm)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseRawGroupEntry(tc.raw); err == nil {
				t.Fatalf("expected rejection, got none")
			}
		})
	}
}

// TestParseRawGroupEntryRejectsWitnessBindingMismatch covers the external
// reviewer finding on PR #177: a recovery_witness that is internally
// self-consistent (passes ParseRawRecoveryWitness on its own) but whose
// identity fields no longer match the group entry it is nested in must be
// refused, not silently accepted.
func TestParseRawGroupEntryRejectsWitnessBindingMismatch(t *testing.T) {
	base := string(readF2BFixture(t, "p-missing-entry.json"))
	altNpub := "npub1qs84sd0arvhxqn33mx7rlc9sncrjwcsgzdgyzxszcu7y4qh7w3dqw4y824"
	mut := func(old, replacement string) []byte {
		t.Helper()
		if !strings.Contains(base, old) {
			t.Fatalf("fixture does not contain %q", old)
		}
		return []byte(strings.Replace(base, old, replacement, 1))
	}
	cases := []struct {
		name string
		raw  []byte
	}{
		{
			// The exact reviewer repro: only new_queue_id is changed, to a
			// value that still differs from old_queue_id so the witness's
			// own internal checks stay happy.
			"new_queue_id points at another queue",
			mut(`"new_queue_id": "00112233445566778899aabbccddeeff"`, `"new_queue_id": "another-queue"`),
		},
		{
			"event_id does not match entry id",
			mut(`"event_id": "3ab652f4ab22a053e7c17b8b35ab99f903f5086a2b44c459ace8d81844662906"`,
				`"event_id": "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"`),
		},
		{
			"event_json_digest does not match entry event_json",
			mut(`"event_json_digest": "eb40f2dcf24ad0f0cdf0992448498c2f570530e48f649dfbc8f1b92281452d99"`,
				`"event_json_digest": "`+strings.Repeat("0", 64)+`"`),
		},
		{
			"max_retries does not match entry max_retries",
			mut("\"max_retries\": 10,\n    \"expected_row_revision\": 20,", "\"max_retries\": 5,\n    \"expected_row_revision\": 20,"),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseRawGroupEntry(tc.raw); err == nil {
				t.Fatalf("expected rejection, got none")
			}
		})
	}
	// Changing the witness's own top-level recipient_npub alone (leaving
	// fanout_key untouched) would trip ParseRawRecoveryWitness's existing
	// internal cross-check first; mutate both together so only the new
	// entry-binding check is exercised.
	t.Run("witness recipient_npub does not match entry recipient_npub", func(t *testing.T) {
		raw := mut("      \"recipient_npub\": \"npub12puqcn8g9mejt4h5taf6xrh43h4s0haf49d79y8ak3svher4dahq9z8wdj\"\n    },",
			"      \"recipient_npub\": \""+altNpub+"\"\n    },")
		raw = []byte(strings.Replace(string(raw),
			"    \"recipient_npub\": \"npub12puqcn8g9mejt4h5taf6xrh43h4s0haf49d79y8ak3svher4dahq9z8wdj\",\n    \"max_retries\": 10,",
			"    \"recipient_npub\": \""+altNpub+"\",\n    \"max_retries\": 10,", 1))
		if _, err := ParseRawGroupEntry(raw); err == nil {
			t.Fatalf("expected rejection, got none")
		}
	})
}

// TestParseRawGroupEntryRejectsResultBindingMismatch mirrors the witness
// coverage above for the nested failure_result: a token whose event_id,
// queue_id, or fanout_key.recipient_npub no longer addresses this entry must
// be refused even when result_id is recomputed to stay internally
// consistent, and the entry's own retry_count/status/last_attempt must stay
// the result's own atomically-committed projection (§3).
func TestParseRawGroupEntryRejectsResultBindingMismatch(t *testing.T) {
	base := string(readF2BFixture(t, "p-o3-entry.json"))
	baseToken := OutboxAttemptToken{
		FanoutKey: OutboxFanoutKey{
			LocalNpub:     "npub1qs84sd0arvhxqn33mx7rlc9sncrjwcsgzdgyzxszcu7y4qh7w3dqw4y824",
			RecipientNpub: "npub12puqcn8g9mejt4h5taf6xrh43h4s0haf49d79y8ak3svher4dahq9z8wdj",
			GroupID:       "82bd2972cccd0403b59cdaa4412c6664",
			SendKey:       "a52ffd60fa92310846b0cf5869a99fdd",
			EnvelopeType:  "message",
		},
		EventID:           "3ab652f4ab22a053e7c17b8b35ab99f903f5086a2b44c459ace8d81844662906",
		QueueID:           "2fc72e7f8539e9eff9120c979198790b",
		AttemptGeneration: 7,
	}
	replaceResultID := func(raw, resultID string) string {
		t.Helper()
		old := `"result_id": "2397fad13c91fc597e59c7c97d4e8658dd3080187b0ccc9790b0d1f1cf967346"`
		if !strings.Contains(raw, old) {
			t.Fatalf("fixture does not contain result_id")
		}
		return strings.Replace(raw, old, `"result_id": "`+resultID+`"`, 1)
	}
	replaceNestedQueueID := func(raw, queueID string) string {
		t.Helper()
		old := "\"queue_id\": \"2fc72e7f8539e9eff9120c979198790b\",\n      \"attempt_generation\": 7"
		if !strings.Contains(raw, old) {
			t.Fatalf("fixture does not contain nested queue_id")
		}
		return strings.Replace(raw, old, "\"queue_id\": \""+queueID+"\",\n      \"attempt_generation\": 7", 1)
	}

	t.Run("token event_id does not match entry id", func(t *testing.T) {
		token := baseToken
		token.EventID = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
		raw := strings.Replace(base,
			"\"event_id\": \"3ab652f4ab22a053e7c17b8b35ab99f903f5086a2b44c459ace8d81844662906\",\n      \"queue_id\"",
			"\"event_id\": \""+token.EventID+"\",\n      \"queue_id\"", 1)
		raw = replaceResultID(raw, failureResultID(token))
		if _, err := ParseRawGroupEntry([]byte(raw)); err == nil {
			t.Fatalf("expected rejection, got none")
		}
	})

	t.Run("token queue_id does not match entry queue_id", func(t *testing.T) {
		token := baseToken
		token.QueueID = "another-queue"
		raw := replaceNestedQueueID(base, token.QueueID)
		raw = replaceResultID(raw, failureResultID(token))
		if _, err := ParseRawGroupEntry([]byte(raw)); err == nil {
			t.Fatalf("expected rejection, got none")
		}
	})

	t.Run("token fanout_key recipient_npub does not match entry recipient_npub", func(t *testing.T) {
		token := baseToken
		token.FanoutKey.RecipientNpub = "npub1qs84sd0arvhxqn33mx7rlc9sncrjwcsgzdgyzxszcu7y4qh7w3dqw4y824"
		old := "        \"recipient_npub\": \"npub12puqcn8g9mejt4h5taf6xrh43h4s0haf49d79y8ak3svher4dahq9z8wdj\"\n      },"
		if !strings.Contains(base, old) {
			t.Fatalf("fixture does not contain token fanout_key recipient_npub")
		}
		raw := strings.Replace(base, old, "        \"recipient_npub\": \""+token.FanoutKey.RecipientNpub+"\"\n      },", 1)
		raw = replaceResultID(raw, failureResultID(token))
		if _, err := ParseRawGroupEntry([]byte(raw)); err == nil {
			t.Fatalf("expected rejection, got none")
		}
	})

	t.Run("entry retry_count does not match result retry_count", func(t *testing.T) {
		raw := strings.Replace(base, "\"prior_retry_count\": 0,\n    \"retry_count\": 1,", "\"prior_retry_count\": 1,\n    \"retry_count\": 2,", 1)
		if raw == base {
			t.Fatal("mutation did not apply")
		}
		if _, err := ParseRawGroupEntry([]byte(raw)); err == nil {
			t.Fatalf("expected rejection, got none")
		}
	})

	t.Run("entry status does not match result status", func(t *testing.T) {
		raw := strings.Replace(base, "\"status\": \"group_pending\",\n    \"issue\": \"send_failed\"", "\"status\": \"group_failed\",\n    \"issue\": \"retry_exhausted\"", 1)
		if raw == base {
			t.Fatal("mutation did not apply")
		}
		if _, err := ParseRawGroupEntry([]byte(raw)); err == nil {
			t.Fatalf("expected rejection, got none")
		}
	})

	t.Run("entry last_attempt does not match result last_attempt_at", func(t *testing.T) {
		raw := strings.Replace(base, "\"last_attempt_at\": 1791539070,", "\"last_attempt_at\": 1791539099,", 1)
		if raw == base {
			t.Fatal("mutation did not apply")
		}
		if _, err := ParseRawGroupEntry([]byte(raw)); err == nil {
			t.Fatalf("expected rejection, got none")
		}
	})
}

// TestParseRawGroupEntryRejectsSenderMismatch covers the external reviewer
// finding on PR #234: fanout_key.local_npub is never bound to the signed
// event's sender, so a key swapped to the recipient's npub (with result_id
// recomputed to stay internally consistent) must still be refused.
func TestParseRawGroupEntryRejectsSenderMismatch(t *testing.T) {
	const senderNpub = "npub1qs84sd0arvhxqn33mx7rlc9sncrjwcsgzdgyzxszcu7y4qh7w3dqw4y824"
	const recipientNpub = "npub12puqcn8g9mejt4h5taf6xrh43h4s0haf49d79y8ak3svher4dahq9z8wdj"

	t.Run("failure_result local_npub does not match event sender", func(t *testing.T) {
		base := string(readF2BFixture(t, "p-o3-entry.json"))
		token := OutboxAttemptToken{
			FanoutKey: OutboxFanoutKey{
				LocalNpub:     recipientNpub,
				RecipientNpub: recipientNpub,
				GroupID:       "82bd2972cccd0403b59cdaa4412c6664",
				SendKey:       "a52ffd60fa92310846b0cf5869a99fdd",
				EnvelopeType:  "message",
			},
			EventID:           "3ab652f4ab22a053e7c17b8b35ab99f903f5086a2b44c459ace8d81844662906",
			QueueID:           "2fc72e7f8539e9eff9120c979198790b",
			AttemptGeneration: 7,
		}
		raw := strings.Replace(base,
			"\"local_npub\": \""+senderNpub+"\",\n        \"group_id\"",
			"\"local_npub\": \""+recipientNpub+"\",\n        \"group_id\"", 1)
		if raw == base {
			t.Fatal("local_npub swap did not apply")
		}
		raw = strings.Replace(raw,
			`"result_id": "2397fad13c91fc597e59c7c97d4e8658dd3080187b0ccc9790b0d1f1cf967346"`,
			`"result_id": "`+failureResultID(token)+`"`, 1)
		if _, err := ParseRawGroupEntry([]byte(raw)); err == nil {
			t.Fatalf("expected rejection, got none")
		}
	})

	t.Run("recovery_witness fanout_key.local_npub does not match event sender", func(t *testing.T) {
		base := string(readF2BFixture(t, "p-missing-entry.json"))
		raw := strings.Replace(base,
			"\"local_npub\": \""+senderNpub+"\",\n      \"group_id\"",
			"\"local_npub\": \""+recipientNpub+"\",\n      \"group_id\"", 1)
		if raw == base {
			t.Fatal("local_npub swap did not apply")
		}
		if _, err := ParseRawGroupEntry([]byte(raw)); err == nil {
			t.Fatalf("expected rejection, got none")
		}
	})
}

// TestParseRawGroupEntryRejectsBudgetExhaustedPending covers the review's
// non-blocking P4 finding: once the entry's retry_count reaches max_retries,
// the bound result's status must be group_failed, not a "legal" exhausted
// group_pending/send_failed pair.
func TestParseRawGroupEntryRejectsBudgetExhaustedPending(t *testing.T) {
	base := string(readF2BFixture(t, "p-o3-entry.json"))
	raw := strings.Replace(base, "\"retry_count\": 1,\n  \"max_retries\": 10,", "\"retry_count\": 10,\n  \"max_retries\": 10,", 1)
	raw = strings.Replace(raw, "\"prior_retry_count\": 0,\n    \"retry_count\": 1,", "\"prior_retry_count\": 9,\n    \"retry_count\": 10,", 1)
	if raw == base {
		t.Fatal("budget mutation did not apply")
	}
	if _, err := ParseRawGroupEntry([]byte(raw)); err == nil {
		t.Fatalf("expected rejection, got none")
	}
}

func TestParseRawGroupEntryRejectsNormalizingStrings(t *testing.T) {
	base := string(readF2BFixture(t, "p-o3-entry.json"))
	mut := func(old, replacement string) []byte {
		t.Helper()
		if !strings.Contains(base, old) {
			t.Fatalf("fixture does not contain %q", old)
		}
		return []byte(strings.Replace(base, old, replacement, 1))
	}
	cases := map[string][]byte{
		"surrogate route":    mut(`"route": "group"`, `"route": "gro\ud800up"`),
		"surrogate queue id": mut(`"queue_id": "2fc72e7f8539e9eff9120c979198790b"`, `"queue_id": "\udc00"`),
		"surrogate entry id": mut(`"id": "3ab652f4ab22a053e7c17b8b35ab99f903f5086a2b44c459ace8d81844662906"`, `"id": "\ud800"`),
		"non-string route":   mut(`"route": "group"`, `"route": 1`),
	}
	for name, raw := range cases {
		if _, err := ParseRawGroupEntry(raw); err == nil {
			t.Fatalf("%s: expected rejection", name)
		}
	}
}

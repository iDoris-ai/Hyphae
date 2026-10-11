package messaging

import (
	"strings"
	"testing"
)

const pMissingWitness = `{
  "version": 1,
  "recovery_id": "102132435465768798a9bacbdcedfe0f",
  "fanout_key": {
    "local_npub": "npub1qs84sd0arvhxqn33mx7rlc9sncrjwcsgzdgyzxszcu7y4qh7w3dqw4y824",
    "group_id": "82bd2972cccd0403b59cdaa4412c6664",
    "envelope_type": "message",
    "send_key": "a52ffd60fa92310846b0cf5869a99fdd",
    "recipient_npub": "npub12puqcn8g9mejt4h5taf6xrh43h4s0haf49d79y8ak3svher4dahq9z8wdj"
  },
  "event_id": "3ab652f4ab22a053e7c17b8b35ab99f903f5086a2b44c459ace8d81844662906",
  "event_json_digest": "eb40f2dcf24ad0f0cdf0992448498c2f570530e48f649dfbc8f1b92281452d99",
  "recipient_npub": "npub12puqcn8g9mejt4h5taf6xrh43h4s0haf49d79y8ak3svher4dahq9z8wdj",
  "max_retries": 10,
  "expected_row_revision": 20,
  "expected_attempt_generation": 7,
  "old_queue_id": "2fc72e7f8539e9eff9120c979198790b",
  "new_queue_id": "00112233445566778899aabbccddeeff",
  "reason": "missing",
  "observed_absent": true,
  "authorized_at": 1791539080
}`

const pExhaustedPendingWitness = `{
  "version": 1,
  "recovery_id": "2031425364758697a8b9cadbecfd0e1f",
  "fanout_key": {
    "local_npub": "npub1qs84sd0arvhxqn33mx7rlc9sncrjwcsgzdgyzxszcu7y4qh7w3dqw4y824",
    "group_id": "82bd2972cccd0403b59cdaa4412c6664",
    "envelope_type": "message",
    "send_key": "a52ffd60fa92310846b0cf5869a99fdd",
    "recipient_npub": "npub12puqcn8g9mejt4h5taf6xrh43h4s0haf49d79y8ak3svher4dahq9z8wdj"
  },
  "event_id": "3ab652f4ab22a053e7c17b8b35ab99f903f5086a2b44c459ace8d81844662906",
  "event_json_digest": "eb40f2dcf24ad0f0cdf0992448498c2f570530e48f649dfbc8f1b92281452d99",
  "recipient_npub": "npub12puqcn8g9mejt4h5taf6xrh43h4s0haf49d79y8ak3svher4dahq9z8wdj",
  "max_retries": 10,
  "expected_row_revision": 20,
  "expected_attempt_generation": 7,
  "old_queue_id": "2fc72e7f8539e9eff9120c979198790b",
  "new_queue_id": "00112233445566778899aabbccddeeff",
  "reason": "exhausted_pending",
  "authorized_at": 1791539080,
  "old_entry_digest": "eade2d18cf3f488be9c4e79f7d138e8838086cffc597817b9ce14021bb7d1d9e",
  "old_status": "group_pending",
  "old_retry_count": 10
}`

func mutateWitness(t *testing.T, base, old, replacement string) []byte {
	t.Helper()
	if !strings.Contains(base, old) {
		t.Fatalf("fixture does not contain %q", old)
	}
	return []byte(strings.Replace(base, old, replacement, 1))
}

func TestParseRawRecoveryWitnessMissing(t *testing.T) {
	w, err := ParseRawRecoveryWitness([]byte(pMissingWitness))
	if err != nil {
		t.Fatalf("parse missing witness: %v", err)
	}
	if string(w.Raw) != pMissingWitness {
		t.Fatal("raw bytes were not preserved verbatim")
	}
	if w.Reason != witnessReasonMissing || !w.ObservedAbsent {
		t.Fatalf("reason/observed mismatch: %+v", w)
	}
	if w.OldEntryDigest != "" || w.OldStatus != "" || w.OldRetryCount != 0 {
		t.Fatalf("conditional old_* members must be absent for missing: %+v", w)
	}
	if w.RecipientNpub != w.FanoutKey.RecipientNpub || w.MaxRetries != 10 || w.ExpectedAttemptGeneration != 7 {
		t.Fatalf("common members mismatch: %+v", w)
	}
	// A legacy-empty old_queue_id is structurally accepted for missing.
	emptyOld := mutateWitness(t, pMissingWitness, `"old_queue_id": "2fc72e7f8539e9eff9120c979198790b"`, `"old_queue_id": ""`)
	if _, err := ParseRawRecoveryWitness(emptyOld); err != nil {
		t.Fatalf("empty old_queue_id for missing should parse: %v", err)
	}
}

func TestParseRawRecoveryWitnessExhausted(t *testing.T) {
	w, err := ParseRawRecoveryWitness([]byte(pExhaustedPendingWitness))
	if err != nil {
		t.Fatalf("parse exhausted witness: %v", err)
	}
	if w.Reason != witnessReasonExhaustedPending || w.ObservedAbsent {
		t.Fatalf("reason mismatch: %+v", w)
	}
	if w.OldStatus != "group_pending" || w.OldRetryCount != 10 ||
		w.OldEntryDigest != "eade2d18cf3f488be9c4e79f7d138e8838086cffc597817b9ce14021bb7d1d9e" {
		t.Fatalf("conditional members mismatch: %+v", w)
	}
}

func TestParseRawRecoveryWitnessRejects(t *testing.T) {
	const mismatchNpub = `"event_json_digest": "eb40f2dcf24ad0f0cdf0992448498c2f570530e48f649dfbc8f1b92281452d99",` + "\n  " +
		`"recipient_npub": "npub12puqcn8g9mejt4h5taf6xrh43h4s0haf49d79y8ak3svher4dahq9z8wdj"`
	cases := []struct {
		name string
		raw  []byte
	}{
		{"missing without observed_absent", mutateWitness(t, pMissingWitness, ",\n  \"observed_absent\": true", "")},
		{"missing observed_absent false", mutateWitness(t, pMissingWitness, `"observed_absent": true`, `"observed_absent": false`)},
		{"missing observed_absent string", mutateWitness(t, pMissingWitness, `"observed_absent": true`, `"observed_absent": "true"`)},
		{"missing with old_entry_digest", mutateWitness(t, pMissingWitness, `"observed_absent": true,`,
			`"observed_absent": true,`+"\n  "+`"old_entry_digest": "eade2d18cf3f488be9c4e79f7d138e8838086cffc597817b9ce14021bb7d1d9e",`)},
		{"recipient mismatch", mutateWitness(t, pMissingWitness, mismatchNpub,
			`"event_json_digest": "eb40f2dcf24ad0f0cdf0992448498c2f570530e48f649dfbc8f1b92281452d99",`+"\n  "+
				`"recipient_npub": "npub1qs84sd0arvhxqn33mx7rlc9sncrjwcsgzdgyzxszcu7y4qh7w3dqw4y824"`)},
		{"new equals old queue", mutateWitness(t, pMissingWitness, `"new_queue_id": "00112233445566778899aabbccddeeff"`, `"new_queue_id": "2fc72e7f8539e9eff9120c979198790b"`)},
		{"new queue empty", mutateWitness(t, pMissingWitness, `"new_queue_id": "00112233445566778899aabbccddeeff"`, `"new_queue_id": ""`)},
		{"reason invalid", mutateWitness(t, pMissingWitness, `"reason": "missing"`, `"reason": "bogus"`)},
		{"version 2", mutateWitness(t, pMissingWitness, `"version": 1`, `"version": 2`)},
		{"unknown key", mutateWitness(t, pMissingWitness, `"version": 1,`, "\"version\": 1,\n  \"extra\": 1,")},
		{"exhausted without old_status", mutateWitness(t, pExhaustedPendingWitness, ",\n  \"old_status\": \"group_pending\"", "")},
		{"exhausted with observed_absent", mutateWitness(t, pExhaustedPendingWitness, `"reason": "exhausted_pending",`,
			`"reason": "exhausted_pending",`+"\n  "+`"observed_absent": true,`)},
		{"exhausted wrong old_status", mutateWitness(t, pExhaustedPendingWitness, `"old_status": "group_pending"`, `"old_status": "group_failed"`)},
		{"exhausted below max", mutateWitness(t, pExhaustedPendingWitness, `"old_retry_count": 10`, `"old_retry_count": 9`)},
		{"exhausted empty old queue", mutateWitness(t, pExhaustedPendingWitness, `"old_queue_id": "2fc72e7f8539e9eff9120c979198790b"`, `"old_queue_id": ""`)},
		{"exhausted missing old digest", mutateWitness(t, pExhaustedPendingWitness, ",\n  \"old_entry_digest\": \"eade2d18cf3f488be9c4e79f7d138e8838086cffc597817b9ce14021bb7d1d9e\"", "")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseRawRecoveryWitness(tc.raw); err == nil {
				t.Fatalf("expected rejection, got none")
			}
		})
	}
}

func TestParseRawRecoveryWitnessRejectsNormalizingStrings(t *testing.T) {
	raw := mutateWitness(t, pMissingWitness, `"new_queue_id": "00112233445566778899aabbccddeeff"`, `"new_queue_id": "\ud800"`)
	if _, err := ParseRawRecoveryWitness(raw); err == nil {
		t.Fatal("expected a lone-surrogate new_queue_id to be rejected")
	}
	raw = mutateWitness(t, pMissingWitness, `"reason": "missing"`, `"reason": "miss\ud800ing"`)
	if _, err := ParseRawRecoveryWitness(raw); err == nil {
		t.Fatal("expected a lone-surrogate reason to be rejected")
	}
	raw = mutateWitness(t, pExhaustedPendingWitness, `"old_status": "group_pending"`, `"old_status": "group_pend\udc00ing"`)
	if _, err := ParseRawRecoveryWitness(raw); err == nil {
		t.Fatal("expected a lone-surrogate old_status to be rejected")
	}
}

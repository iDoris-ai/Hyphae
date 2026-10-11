package messaging

import (
	"bytes"
	"strings"
	"testing"
)

// pO3Result is the canonical failure_result v1 fixture from the schema
// supplement §6 (P-O3), used verbatim so raw fragments can be asserted.
const pO3Result = `{
  "version": 1,
  "token": {
    "fanout_key": {
      "local_npub": "npub1qs84sd0arvhxqn33mx7rlc9sncrjwcsgzdgyzxszcu7y4qh7w3dqw4y824",
      "group_id": "82bd2972cccd0403b59cdaa4412c6664",
      "envelope_type": "message",
      "send_key": "a52ffd60fa92310846b0cf5869a99fdd",
      "recipient_npub": "npub12puqcn8g9mejt4h5taf6xrh43h4s0haf49d79y8ak3svher4dahq9z8wdj"
    },
    "event_id": "3ab652f4ab22a053e7c17b8b35ab99f903f5086a2b44c459ace8d81844662906",
    "queue_id": "2fc72e7f8539e9eff9120c979198790b",
    "attempt_generation": 7
  },
  "result_id": "2397fad13c91fc597e59c7c97d4e8658dd3080187b0ccc9790b0d1f1cf967346",
  "expected_row_revision": 15,
  "attempts": 4,
  "last_attempt_at": 1791539070,
  "relay_count": 2,
  "prior_retry_count": 0,
  "retry_count": 1,
  "status": "group_pending",
  "issue": "send_failed"
}`

func mutateResult(t *testing.T, old, replacement string) []byte {
	t.Helper()
	if !strings.Contains(pO3Result, old) {
		t.Fatalf("fixture does not contain %q", old)
	}
	return []byte(strings.Replace(pO3Result, old, replacement, 1))
}

func TestParseRawFailureResultCanonical(t *testing.T) {
	result, err := ParseRawFailureResult([]byte(pO3Result))
	if err != nil {
		t.Fatalf("parse canonical result: %v", err)
	}
	if string(result.Raw) != pO3Result {
		t.Fatal("raw bytes were not preserved verbatim")
	}
	// Raw fragments keep surrounding whitespace and are never normalized.
	if got := string(result.Fields["attempts"]); got != " 4" {
		t.Fatalf("attempts fragment = %q, want %q", got, " 4")
	}
	if result.ResultID != "2397fad13c91fc597e59c7c97d4e8658dd3080187b0ccc9790b0d1f1cf967346" {
		t.Fatalf("result_id = %q", result.ResultID)
	}
	if want := failureResultID(result.Token); result.ResultID != want {
		t.Fatalf("result_id does not match derived id")
	}
	token := result.Token
	if token.FanoutKey.GroupID != "82bd2972cccd0403b59cdaa4412c6664" ||
		token.FanoutKey.SendKey != "a52ffd60fa92310846b0cf5869a99fdd" ||
		token.FanoutKey.EnvelopeType != "message" ||
		token.EventID != "3ab652f4ab22a053e7c17b8b35ab99f903f5086a2b44c459ace8d81844662906" ||
		token.QueueID != "2fc72e7f8539e9eff9120c979198790b" ||
		token.AttemptGeneration != 7 {
		t.Fatalf("token mismatch: %+v", token)
	}
	if result.Status != "group_pending" || result.Issue != "send_failed" ||
		result.PriorRetryCount != 0 || result.RetryCount != 1 {
		t.Fatalf("status/issue/counters mismatch: %+v", result)
	}
}

func TestParseRawFailureResultRejects(t *testing.T) {
	cases := []struct {
		name string
		raw  []byte
	}{
		{"version 2", mutateResult(t, `"version": 1`, `"version": 2`)},
		{"version float", mutateResult(t, `"version": 1`, `"version": 1.0`)},
		{"missing issue", mutateResult(t, ",\n  \"issue\": \"send_failed\"", "")},
		{"null attempts", mutateResult(t, `"attempts": 4`, `"attempts": null`)},
		{"string attempts", mutateResult(t, `"attempts": 4`, `"attempts": "4"`)},
		{"unknown key", mutateResult(t, `"version": 1,`, "\"version\": 1,\n  \"extra\": 1,")},
		{"duplicate key", mutateResult(t, `"relay_count": 2,`, "\"relay_count\": 2,\n  \"relay_count\": 2,")},
		{"retry_count mismatch", mutateResult(t, `"retry_count": 1,`, `"retry_count": 3,`)},
		{"status/issue mismatch", mutateResult(t, `"issue": "send_failed"`, `"issue": "retry_exhausted"`)},
		{"recipient hex not npub", mutateResult(t,
			`"recipient_npub": "npub12puqcn8g9mejt4h5taf6xrh43h4s0haf49d79y8ak3svher4dahq9z8wdj"`,
			`"recipient_npub": "50780c4ce82ef325d6f45f53a30ef58deb07dfa9a95be290fdb460cbe4756f6e"`)},
		{"event id uppercase", mutateResult(t,
			`"event_id": "3ab652f4ab22a053e7c17b8b35ab99f903f5086a2b44c459ace8d81844662906"`,
			`"event_id": "3AB652F4AB22A053E7C17B8B35AB99F903F5086A2B44C459ACE8D81844662906"`)},
		{"group id short", mutateResult(t, `"group_id": "82bd2972cccd0403b59cdaa4412c6664"`, `"group_id": "82bd2972cccd0403b59cdaa4412c666"`)},
		{"generation zero", mutateResult(t, `"attempt_generation": 7`, `"attempt_generation": 0`)},
		{"envelope unknown", mutateResult(t, `"envelope_type": "message"`, `"envelope_type": "bogus"`)},
		{"result id wrong", mutateResult(t,
			`"result_id": "2397fad13c91fc597e59c7c97d4e8658dd3080187b0ccc9790b0d1f1cf967346"`,
			`"result_id": "0000000000000000000000000000000000000000000000000000000000000000"`)},
		{"trailing token", append([]byte(pO3Result), []byte(" x")...)},
		{"empty object", []byte(`{}`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseRawFailureResult(tc.raw); err == nil {
				t.Fatalf("expected rejection, got none")
			}
		})
	}
}

func TestAppendJSONStringEscaping(t *testing.T) {
	cases := []struct{ in, want string }{
		{"plain", `"plain"`},
		{`a"b\c`, `"a\"b\\c"`},
		{"a\nb\tc", `"a\u000ab\u0009c"`},
		{"a/b", `"a/b"`},
		{"<&>", `"<&>"`},
		{"héllo→", "\"héllo→\""},
	}
	for _, tc := range cases {
		var buf bytes.Buffer
		appendJSONString(&buf, tc.in)
		if got := buf.String(); got != tc.want {
			t.Fatalf("appendJSONString(%q) = %s, want %s", tc.in, got, tc.want)
		}
	}
}

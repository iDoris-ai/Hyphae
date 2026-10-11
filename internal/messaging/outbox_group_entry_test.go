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

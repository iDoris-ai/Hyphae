package messaging

import (
	"bytes"
	"testing"
)

// V6 nested mutation sweep: every member of failure_result / recovery_witness
// (including the nested token and fanout_key members) is driven through
// null, duplicate, removed, unknown sibling and wrong type -- integers also
// through 1.0 / 1e0 -- and every mutation must be rejected by the entry
// parser and by the whole-file scan without touching the input bytes.
func TestNestedResultWitnessMutationSweep(t *testing.T) {
	integerMembers := map[string]bool{
		"version": true, "attempt_generation": true, "expected_row_revision": true, "attempts": true, "last_attempt_at": true,
		"relay_count": true, "prior_retry_count": true, "retry_count": true, "max_retries": true,
		"expected_attempt_generation": true, "authorized_at": true, "old_retry_count": true,
	}
	for _, fixture := range []struct{ file, member string }{
		{"p-o3-entry.json", "failure_result"},
		{"p-missing-entry.json", "recovery_witness"},
	} {
		entryRaw := bytes.TrimSpace(readF2BFixture(t, fixture.file))
		parsed, err := ParseRawGroupEntry(entryRaw)
		if err != nil {
			t.Fatalf("%s: fixture must parse: %v", fixture.file, err)
		}
		nested := parsed.Fields[fixture.member]
		if bytes.Count(entryRaw, nested) != 1 {
			t.Fatalf("%s: nested fragment is not unique in the entry", fixture.file)
		}
		members := nestedMemberFragments(t, nested)
		if len(members) < 10 {
			t.Fatalf("%s: expected the full member set, found %d", fixture.file, len(members))
		}
		cases := 0
		for key, value := range members {
			needle := []byte(`"` + key + `":` + string(value))
			if !bytes.Contains(nested, needle) {
				t.Fatalf("%s: member %q fragment not found verbatim", fixture.file, key)
			}
			trimmed := bytes.TrimSpace(value)
			wrongType := "true"
			if string(trimmed) == "true" {
				wrongType = "1"
			}
			mutations := map[string][]byte{
				"null":      []byte(`"` + key + `": null` + trailingSpace(value)),
				"duplicate": append(append([]byte{}, needle...), []byte(`,"`+key+`":`+string(value))...),
				"removed":   []byte(`"__removed__":` + string(value)),
				"unknown":   append([]byte(`"__unknown__": 1, `), needle...),
				"wrongtype": []byte(`"` + key + `": ` + wrongType + trailingSpace(value)),
			}
			if integerMembers[key] {
				mutations["float"] = []byte(`"` + key + `": ` + string(trimmed) + `.0` + trailingSpace(value))
				mutations["exponent"] = []byte(`"` + key + `": ` + string(trimmed) + `e0` + trailingSpace(value))
			}
			for name, replacement := range mutations {
				mutatedNested := bytes.Replace(nested, needle, replacement, 1)
				mutated := bytes.Replace(entryRaw, nested, mutatedNested, 1)
				if bytes.Equal(mutated, entryRaw) {
					t.Fatalf("%s %s/%s: mutation did not apply", fixture.file, key, name)
				}
				input := bytes.Clone(mutated)
				got, err := ParseRawGroupEntry(mutated)
				if err == nil {
					t.Errorf("%s %s/%s: accepted:\n%s", fixture.file, key, name, mutatedNested)
				}
				if !bytes.Equal(mutated, input) || (got != nil && !bytes.Equal(got.Raw, input)) {
					t.Errorf("%s %s/%s: parser altered the input bytes", fixture.file, key, name)
				}
				if _, err := scanRawOutboxFile(rawOutboxDoc(mutated)); err == nil {
					t.Errorf("%s %s/%s: whole-file scan accepted the mutation", fixture.file, key, name)
				}
				cases++
			}
		}
		t.Logf("%s: %d nested mutations rejected", fixture.file, cases)
	}
}

// nestedMemberFragments returns every member value fragment of a nested
// object, descending into token and fanout_key, keyed by member name.
func nestedMemberFragments(t *testing.T, object []byte) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	var walk func(raw []byte)
	walk = func(raw []byte) {
		fields, _ := evidenceObject(raw, "", knownNestedMembers)
		for key, value := range fields {
			if key == "token" || key == "fanout_key" {
				walk(value)
				continue
			}
			if _, dup := out[key]; !dup {
				out[key] = value
			}
		}
	}
	walk(object)
	return out
}

const knownNestedMembers = "version token result_id expected_row_revision attempts last_attempt_at relay_count prior_retry_count retry_count status issue " +
	"fanout_key event_id queue_id attempt_generation local_npub recipient_npub group_id send_key envelope_type " +
	"recovery_id event_json_digest max_retries expected_attempt_generation old_queue_id new_queue_id reason authorized_at observed_absent old_entry_digest old_status old_retry_count"

func trailingSpace(fragment []byte) string {
	trimmed := bytes.TrimRight(fragment, " \t\r\n")
	return string(fragment[len(trimmed):])
}

func TestEntryLevelRelaysNullMemberRejected(t *testing.T) {
	group := legacyFixtureEntry(t, "legacy-165-group-nil-relays.json")
	for name, replacement := range map[string]string{
		"[null]":   `"relays": [null]`,
		"[1]":      `"relays": [1]`,
		"object":   `"relays": {}`,
		"missing":  `"relays_":  null`,
		"dup null": `"relays": null, "relays": null`,
	} {
		mutated := bytes.Replace(group, []byte(`"relays": null`), []byte(replacement), 1)
		if bytes.Equal(mutated, group) {
			t.Fatalf("%s: mutation did not apply", name)
		}
		if _, err := ParseRawGroupEntry(mutated); err == nil {
			t.Errorf("%s: accepted", name)
		}
		if _, err := scanRawOutboxFile(rawOutboxDoc(mutated)); err == nil {
			t.Errorf("%s: scan accepted", name)
		}
	}
	if _, err := ParseRawGroupEntry(group); err != nil {
		t.Fatalf("legacy relays:null must stay accepted: %v", err)
	}
}

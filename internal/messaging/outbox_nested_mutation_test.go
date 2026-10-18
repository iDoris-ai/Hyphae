package messaging

import (
	"bytes"
	"encoding/json"
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
		if len(members) != 18 {
			t.Fatalf("%s: expected 18 leaf paths, found %d", fixture.file, len(members))
		}
		cases := 0
		for _, frag := range members {
			key, value := frag.key, frag.value
			needle := frag.parent[frag.at:frag.end]
			if frag.at < 0 || bytes.Count(nested, frag.parent) != 1 {
				t.Fatalf("%s: parent of %q is not unique", fixture.file, frag.path)
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
				mutatedParent := append(append(append([]byte{}, frag.parent[:frag.at]...), replacement...), frag.parent[frag.end:]...)
				mutatedNested := bytes.Replace(nested, frag.parent, mutatedParent, 1)
				mutated := bytes.Replace(entryRaw, nested, mutatedNested, 1)
				if bytes.Equal(mutated, entryRaw) {
					t.Fatalf("%s %s/%s: mutation did not apply", fixture.file, frag.path, name)
				}
				input := bytes.Clone(mutated)
				got, err := ParseRawGroupEntry(mutated)
				if err == nil {
					t.Errorf("%s %s/%s: accepted:\n%s", fixture.file, frag.path, name, mutatedNested)
				}
				if !bytes.Equal(mutated, input) || (got != nil && !bytes.Equal(got.Raw, input)) {
					t.Errorf("%s %s/%s: parser altered the input bytes", fixture.file, frag.path, name)
				}
				if _, err := scanRawOutboxFile(rawOutboxDoc(mutated)); err == nil {
					t.Errorf("%s %s/%s: whole-file scan accepted the mutation", fixture.file, frag.path, name)
				}
				cases++
			}
		}
		t.Logf("%s: %d nested mutations rejected", fixture.file, cases)
	}
}

type nestedFrag struct {
	path, key string
	parent    []byte
	value     []byte
	at, end   int
}

// nestedMemberFragments returns every leaf member of a nested object,
// descending into token and fanout_key. Offsets keep same-named members apart.
func nestedMemberFragments(t *testing.T, object []byte) []nestedFrag {
	t.Helper()
	var out []nestedFrag
	var walk func(path string, raw []byte)
	walk = func(path string, raw []byte) {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		token, err := decoder.Token()
		if err != nil || token != json.Delim('{') {
			t.Fatalf("expected object at %s: %v", path, err)
		}
		for decoder.More() {
			beforeKey := int(decoder.InputOffset())
			keyToken, err := decoder.Token()
			if err != nil {
				t.Fatal(err)
			}
			name, ok := keyToken.(string)
			if !ok {
				t.Fatalf("expected key at %s", path)
			}
			afterKey := int(decoder.InputOffset())
			keyAt := bytes.Index(raw[beforeKey:afterKey], []byte(`"`+name+`"`))
			if keyAt < 0 {
				t.Fatalf("%s: key %q not found at %d:%d", path, name, beforeKey, afterKey)
			}
			keyAt += beforeKey
			var decoded json.RawMessage
			if err := decoder.Decode(&decoded); err != nil {
				t.Fatal(err)
			}
			colon := bytes.IndexByte(raw[afterKey:], ':')
			if colon < 0 {
				t.Fatalf("%s: colon missing after %q", path, name)
			}
			valueStart := afterKey + colon + 1
			end := int(decoder.InputOffset())
			for end < len(raw) && (raw[end] == ' ' || raw[end] == '\t' || raw[end] == '\r' || raw[end] == '\n') {
				end++
			}
			value := raw[valueStart:end]
			child := name
			if path != "" {
				child = path + "." + name
			}
			if name == "token" || name == "fanout_key" {
				walk(child, bytes.TrimSpace(value))
				continue
			}
			out = append(out, nestedFrag{path: child, key: name, parent: raw, value: value, at: keyAt, end: end})
		}
	}
	walk("", object)
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

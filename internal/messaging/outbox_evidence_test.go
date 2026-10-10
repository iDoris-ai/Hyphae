package messaging

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fiatjaf.com/nostr"
	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/stretchr/testify/require"
)

func rawFixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "raw-outbox", name+".json"))
	require.NoError(t, err)
	return raw
}

func editEvidence(t *testing.T, raw []byte, key string, value string) []byte {
	t.Helper()
	var root struct {
		Entries []map[string]json.RawMessage `json:"entries"`
	}
	require.NoError(t, json.Unmarshal(raw, &root))
	if value == "" {
		delete(root.Entries[0], key)
	} else {
		root.Entries[0][key] = json.RawMessage(value)
	}
	edited, err := json.Marshal(root)
	require.NoError(t, err)
	return edited
}

func editEvidenceEvent(t *testing.T, raw []byte, edit func(string) string) []byte {
	t.Helper()
	var root struct {
		Entries []struct {
			Event string `json:"event_json"`
		} `json:"entries"`
	}
	require.NoError(t, json.Unmarshal(raw, &root))
	text, err := json.Marshal(edit(root.Entries[0].Event))
	require.NoError(t, err)
	return editEvidence(t, raw, "event_json", string(text))
}

func signedRawDM(t *testing.T, tags nostr.Tags, created nostr.Timestamp, content string) ([]byte, nostr.Event, string) {
	t.Helper()
	sender := nostr.Generate()
	recipient := nostr.Generate().Public()
	event := nostr.Event{
		PubKey: sender.Public(), CreatedAt: created, Kind: AgentKind,
		Tags:    append(nostr.Tags{{"p", recipient.Hex()}, {"c", "agent"}, {"v", "v1"}, {"d", "agent-message:0123456789abcdef"}}, tags...),
		Content: content,
	}
	require.NoError(t, event.Sign(sender))
	eventJSON, err := json.Marshal(event)
	require.NoError(t, err)
	entry := editEvidence(t, rawFixture(t, "legacy-dm-nil-relays"), "id", `"`+event.ID.Hex()+`"`)
	entry = editEvidence(t, entry, "recipient_npub", `"`+common.EncodeNpub(recipient)+`"`)
	entry = editEvidence(t, entry, "event_json", mustJSONString(t, string(eventJSON)))
	return entry, event, string(eventJSON)
}

func mustJSONString(t *testing.T, value string) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	require.NoError(t, err)
	return string(encoded)
}

func TestLegacyNilRelaysSourceBytes(t *testing.T) {
	for _, name := range []string{"legacy-165-group-nil-relays", "legacy-dm-nil-relays"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			raw := rawFixture(t, name)
			original := bytes.Clone(raw)
			path := filepath.Join(t.TempDir(), "outbox.json")
			require.NoError(t, os.WriteFile(path, raw, 0600))
			before, err := os.Stat(path)
			require.NoError(t, err)
			evidence, err := ParseRawOutboxEvidence(raw)
			require.NoError(t, err)
			require.Len(t, evidence.Entries, 1)
			entry := evidence.Entries[0]
			require.Equal(t, []byte("null"), bytes.TrimSpace(entry.Fields["relays"]))
			require.Contains(t, string(raw), string(entry.Raw))
			require.Contains(t, string(entry.Raw), string(entry.Fields["event_json"]))
			event, err := os.ReadFile(filepath.Join("testdata", "raw-outbox", name+".event.json"))
			require.NoError(t, err)
			require.Equal(t, event, entry.Event)
			require.Equal(t, original, raw)
			require.Equal(t, original, evidence.Raw)
			afterBytes, err := os.ReadFile(path)
			require.NoError(t, err)
			after, err := os.Stat(path)
			require.NoError(t, err)
			require.Equal(t, original, afterBytes)
			require.Equal(t, before.ModTime(), after.ModTime())
			require.Equal(t, before.Mode(), after.Mode())
			raw[0] = 'x'
			require.Equal(t, original, evidence.Raw, "snapshot must not alias caller bytes")
			require.Equal(t, byte('{'), entry.Raw[0])
		})
	}
}

func TestLegacyRelaysFormsAndDMIdentity(t *testing.T) {
	for _, name := range []string{"legacy-165-group-nil-relays", "legacy-dm-nil-relays"} {
		for _, relays := range []string{"null", "[]", `["wss://relay.example"]`} {
			raw := editEvidence(t, rawFixture(t, name), "relays", relays)
			parsed, err := ParseRawOutboxEvidence(raw)
			require.NoError(t, err)
			require.Equal(t, relays, string(bytes.TrimSpace(parsed.Entries[0].Fields["relays"])))
		}
	}
	for _, route := range []string{"", `""`} {
		for _, queue := range []string{"", `""`} {
			raw := editEvidence(t, rawFixture(t, "legacy-dm-nil-relays"), "route", route)
			raw = editEvidence(t, raw, "queue_id", queue)
			parsed, err := ParseRawOutboxEvidence(raw)
			require.NoError(t, err)
			require.Empty(t, parsed.Entries[0].Entry.QueueID)
			require.Empty(t, parsed.Entries[0].Entry.Route)
		}
	}
}

func TestRawOutboxEvidenceMalformed(t *testing.T) {
	base := rawFixture(t, "legacy-165-group-nil-relays")
	cases := map[string][]byte{
		"root-null": []byte("null"), "root-array": []byte("[]"), "root-missing": []byte("{}"),
		"entries-null": []byte(`{"entries":null}`), "entry-null": []byte(`{"entries":[null]}`),
		"entries-object": []byte(`{"entries":{}}`), "truncated": base[:len(base)-1],
		"trailing-value":       append(bytes.Clone(base), []byte(" {}")...),
		"trailing-junk":        append(bytes.Clone(base), 'x'),
		"root-duplicate":       []byte(`{"entries":[],"entr\u0069es":[]}`),
		"root-unknown":         []byte(`{"entries":[],"version":1}`),
		"empty-key":            []byte(`{"entries":[],"":1}`),
		"joined-key":           editEvidence(t, base, "route queue_id", `"oops"`),
		"entry-duplicate":      bytes.Replace(base, []byte(`"route": "group"`), []byte(`"route":"group","rout\u0065":"group"`), 1),
		"entry-unknown":        editEvidence(t, base, "future", `{"a":1,"a":2}`),
		"entry-case":           editEvidence(t, base, "Route", `"group"`),
		"protocol-unsupported": editEvidence(t, base, "fanout_protocol", "1"),
		"result-unsupported":   editEvidence(t, base, "failure_result", `{"version":1}`),
		"witness-malformed":    editEvidence(t, base, "recovery_witness", `{"version":1,"version":null}`),
	}
	for _, key := range []string{"route", "queue_id", "id", "event_json", "recipient_npub", "relays", "retry_count", "max_retries", "last_attempt", "created_at", "status"} {
		for _, value := range []string{"", "null", "true", "{}"} {
			if key == "relays" && value == "null" {
				continue
			}
			cases[key+"/"+value] = editEvidence(t, base, key, value)
		}
	}
	for _, key := range []string{"retry_count", "max_retries", "last_attempt", "created_at"} {
		for _, value := range []string{"-1", "1.5", "1e0", `"1"`, "9223372036854775808"} {
			cases[key+"/"+value] = editEvidence(t, base, key, value)
		}
	}
	for _, value := range []string{`""`, `"group"`, `[null]`, `[1]`, `[true]`, `[{}]`, `[[]]`} {
		cases["relays/"+value] = editEvidence(t, base, "relays", value)
	}
	for _, value := range []string{`""`, `"dm"`, `"GROUP"`} {
		cases["route/"+value] = editEvidence(t, base, "route", value)
	}
	for _, value := range []string{`"pending"`, `"sent"`, `"failed"`, `"GROUP_PENDING"`, `"unknown"`} {
		cases["status/"+value] = editEvidence(t, base, "status", value)
	}
	cases["queue-empty"] = editEvidence(t, base, "queue_id", `""`)
	cases["max-zero"] = editEvidence(t, base, "max_retries", "0")
	cases["recipient-mismatch"] = editEvidence(t, base, "recipient_npub", `"npub1invalid"`)
	cases["event-id-mismatch"] = editEvidence(t, base, "id", `"`+strings.Repeat("0", 64)+`"`)
	eventCases := map[string]func(string) string{
		"duplicate": func(s string) string { return strings.Replace(s, `"kind":30078`, `"kind":30078,"k\u0069nd":30078`, 1) },
		"unknown":   func(s string) string { return strings.Replace(s, `"kind":30078`, `"kind":30078,"extra":0`, 1) },
		"trailing":  func(s string) string { return s + " {}" },
		"tags-null": func(s string) string {
			var m map[string]json.RawMessage
			require.NoError(t, json.Unmarshal([]byte(s), &m))
			m["tags"] = json.RawMessage("null")
			b, e := json.Marshal(m)
			require.NoError(t, e)
			return string(b)
		},
		"tag-null-member":   func(s string) string { return strings.Replace(s, `["p",`, `[null,`, 1) },
		"tag-object-member": func(s string) string { return strings.Replace(s, `["p",`, `[{"x":1,"x":2},`, 1) },
		"bad-kind":          func(s string) string { return strings.Replace(s, `"kind":30078`, `"kind":65536`, 1) },
		"content-null": func(s string) string {
			var m map[string]json.RawMessage
			require.NoError(t, json.Unmarshal([]byte(s), &m))
			m["content"] = json.RawMessage("null")
			b, e := json.Marshal(m)
			require.NoError(t, e)
			return string(b)
		},
		"bad-signature": func(s string) string {
			var m map[string]json.RawMessage
			require.NoError(t, json.Unmarshal([]byte(s), &m))
			m["sig"] = json.RawMessage(`"` + strings.Repeat("0", 128) + `"`)
			b, e := json.Marshal(m)
			require.NoError(t, e)
			return string(b)
		},
	}
	for name, edit := range eventCases {
		cases["event/"+name] = editEvidenceEvent(t, base, edit)
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			before := bytes.Clone(raw)
			parsed, err := ParseRawOutboxEvidence(raw)
			require.Error(t, err)
			require.Equal(t, before, raw)
			require.Equal(t, before, parsed.Raw)
		})
	}
}

func TestRawOutboxEvidenceAllRoutesStatusesAndCollisions(t *testing.T) {
	group := rawFixture(t, "legacy-165-group-nil-relays")
	dm := rawFixture(t, "legacy-dm-nil-relays")
	for _, status := range []string{"pending", "failed", "sent"} {
		_, err := ParseRawOutboxEvidence(editEvidence(t, dm, "status", `"`+status+`"`))
		require.NoError(t, err)
	}
	for _, status := range []string{"group_pending", "group_failed"} {
		for _, retry := range []string{"0", "10", "11"} {
			raw := editEvidence(t, group, "status", `"`+status+`"`)
			raw = editEvidence(t, raw, "retry_count", retry)
			_, err := ParseRawOutboxEvidence(raw)
			if status == "group_failed" && retry == "0" {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		}
	}
	parsed, err := ParseRawOutboxEvidence(group)
	require.NoError(t, err)
	first := parsed.Entries[0]
	for _, route := range []string{`""`, `"group"`, `"unknown"`, "null", ""} {
		second := editEvidence(t, group, "route", route)
		var root struct {
			Entries []json.RawMessage `json:"entries"`
		}
		require.NoError(t, json.Unmarshal(second, &root))
		raw := []byte(`{"entries":[` + string(first.Raw) + `,` + string(root.Entries[0]) + `]}`)
		evidence, err := ParseRawOutboxEvidence(raw)
		require.ErrorContains(t, err, "duplicate")
		require.ErrorContains(t, err, "queue_id")
		require.Len(t, evidence.Entries, 2)
	}
	// Distinct outer IDs cannot hide a duplicate signed EventID.
	second := editEvidence(t, group, "id", `"`+strings.Repeat("1", 64)+`"`)
	second = editEvidence(t, second, "queue_id", `"other-queue"`)
	var root struct {
		Entries []json.RawMessage `json:"entries"`
	}
	require.NoError(t, json.Unmarshal(second, &root))
	_, err = ParseRawOutboxEvidence([]byte(`{"entries":[` + string(first.Raw) + `,` + string(root.Entries[0]) + `]}`))
	require.ErrorContains(t, err, "duplicate event_id")
	// Distinct event IDs cannot hide a duplicate QueueID.
	second = editEvidence(t, dm, "queue_id", `"`+first.Entry.QueueID+`"`)
	require.NoError(t, json.Unmarshal(second, &root))
	_, err = ParseRawOutboxEvidence([]byte(`{"entries":[` + string(first.Raw) + `,` + string(root.Entries[0]) + `]}`))
	require.ErrorContains(t, err, "duplicate queue_id")
}

func TestRawOutboxEvidenceFixtureHashes(t *testing.T) {
	manifest, err := os.ReadFile(filepath.Join("testdata", "raw-outbox", "SHA256SUMS"))
	require.NoError(t, err)
	for _, line := range strings.Split(strings.TrimSpace(string(manifest)), "\n") {
		fields := strings.Fields(line)
		require.Len(t, fields, 2)
		raw, err := os.ReadFile(filepath.Join("testdata", "raw-outbox", fields[1]))
		require.NoError(t, err)
		sum := sha256.Sum256(raw)
		require.Equal(t, fields[0], hex.EncodeToString(sum[:]), fields[1])
	}
}

func TestRawOutboxEvidenceWhitespaceAndEventFieldSchema(t *testing.T) {
	base := rawFixture(t, "legacy-165-group-nil-relays")
	raw := bytes.Replace(base, []byte(`"relays": null,`), []byte("\"relays\": \t null \r\n ,"), 1)
	raw = bytes.Replace(raw, []byte(`"route":`), []byte(`"rout\u0065":`), 1)
	evidence, err := ParseRawOutboxEvidence(raw)
	require.NoError(t, err)
	require.Equal(t, []byte(" \t null \r\n "), []byte(evidence.Entries[0].Fields["relays"]))
	require.Equal(t, raw, evidence.Raw)
	require.Contains(t, string(raw), string(evidence.Entries[0].Raw))
	for _, key := range []string{"id", "pubkey", "sig", "content", "tags", "kind", "created_at"} {
		for _, value := range []string{"", "null", "true", "{}"} {
			t.Run(key+"/"+value, func(t *testing.T) {
				raw := editEvidenceEvent(t, base, func(s string) string {
					var fields map[string]json.RawMessage
					require.NoError(t, json.Unmarshal([]byte(s), &fields))
					if value == "" {
						delete(fields, key)
					} else {
						fields[key] = json.RawMessage(value)
					}
					result, err := json.Marshal(fields)
					require.NoError(t, err)
					return string(result)
				})
				before := bytes.Clone(raw)
				_, err := ParseRawOutboxEvidence(raw)
				require.Error(t, err)
				require.Equal(t, before, raw)
			})
		}
	}
	for _, key := range []string{"route", "queue_id"} {
		_, err := ParseRawOutboxEvidence(editEvidence(t, rawFixture(t, "legacy-dm-nil-relays"), key, "null"))
		require.Error(t, err)
	}
	for _, key := range []string{"created_at", "last_attempt"} {
		_, err := ParseRawOutboxEvidence(editEvidence(t, base, key, "0"))
		require.NoError(t, err)
	}
	empty, err := ParseRawOutboxEvidence([]byte(`{"entries":[]}`))
	require.NoError(t, err)
	require.Empty(t, empty.Entries)
}

func TestRawOutboxEvidenceUsesOneEventFieldInterpretation(t *testing.T) {
	base, event, eventJSON := signedRawDM(t, nil, 0, "")
	require.True(t, event.VerifySignature())
	_, err := ParseRawOutboxEvidence(base)
	require.NoError(t, err)
	// The nostr decoder skips unescaping field names. Before strict values were
	// re-keyed for signature checks, it interpreted this as the original signed
	// created_at=0/content="" event while raw validation saw the replacements.
	forgedEvent := strings.Replace(eventJSON, `"created_at":0`, `"created\u005fat":123`, 1)
	forgedEvent = strings.Replace(forgedEvent, `"content":""`, `"cont\u0065nt":"unsigned replacement"`, 1)
	forged := editEvidence(t, base, "event_json", mustJSONString(t, forgedEvent))
	_, err = ParseRawOutboxEvidence(forged)
	require.Error(t, err)

	for name, tags := range map[string]nostr.Tags{
		"unsupported": {{"enc", "future"}},
		"one-member":  {{"enc"}},
		"conflicting": {{"enc", "nip44"}, {"enc", "future"}},
	} {
		t.Run(name, func(t *testing.T) {
			raw, _, _ := signedRawDM(t, tags, 1, "cipher")
			_, err := ParseRawOutboxEvidence(raw)
			require.Error(t, err)
		})
	}

	// An escaped equivalent duplicate remains invalid even though the decoder
	// used for signature verification now receives canonical field names.
	duplicate := strings.Replace(eventJSON, `"created_at":0`, `"created_at":0,"created\u005fat":0`, 1)
	duplicateRaw := editEvidence(t, base, "event_json", mustJSONString(t, duplicate))
	_, err = ParseRawOutboxEvidence(duplicateRaw)
	require.Error(t, err)
}

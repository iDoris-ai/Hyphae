package messaging

import (
	"bytes"
	"os"
	"reflect"
	"testing"
)

const (
	d6OldQueue = "2fc72e7f8539e9eff9120c979198790b"
	d6NewQueue = "00112233445566778899aabbccddeeff"
)

// P-MISSING fixture witness: reason=missing, old 2fc7…, new 0011…, max 10.
func fixtureMissingWitness(t *testing.T) (*RawGroupEntry, RawRecoveryWitness) {
	t.Helper()
	e, err := ParseRawGroupEntry(readF2BFixture(t, "p-missing-entry.json"))
	if err != nil || e.RecoveryWitness == nil {
		t.Fatalf("P-MISSING fixture: %v", err)
	}
	return e, *e.RecoveryWitness
}

func exhaustedWitness(t *testing.T, oldEntry []byte, reason, status string, count int64) RawRecoveryWitness {
	t.Helper()
	_, w := fixtureMissingWitness(t)
	w.Reason, w.ObservedAbsent = reason, false
	w.OldEntryDigest, w.OldStatus, w.OldRetryCount = sha256Hex(oldEntry), status, count
	return w
}

func exhaustedGroupEntry(t *testing.T, status string) []byte {
	t.Helper()
	group := legacyFixtureEntry(t, "legacy-165-group-nil-relays.json")
	group = bytes.Replace(group, []byte(`"retry_count": 0`), []byte(`"retry_count": 10`), 1)
	return bytes.Replace(group, []byte(`"group_pending"`), []byte(`"`+status+`"`), 1)
}

func readD6Entry(t *testing.T, path string) (*RawGroupEntry, []byte) {
	t.Helper()
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return scanO3Target(t, after, d6NewQueue), after
}

func TestCommitOutboxD6Missing(t *testing.T) {
	want, w := fixtureMissingWitness(t)
	dm := legacyFixtureEntry(t, "legacy-dm-nil-relays.json")
	for name, before := range map[string][]byte{
		"append":      rawOutboxDoc(dm),
		"empty array": []byte(`{"entries":[]}`),
	} {
		path := writeOutboxFile(t, before)
		if err := commitOutboxD6(path, outboxD6{Witness: w, EventJSON: want.Entry.EventJSON}); err != nil {
			t.Fatalf("%s: D6 missing: %v", name, err)
		}
		got, after := readD6Entry(t, path)
		if !reflect.DeepEqual(got.Entry, want.Entry) || got.RecoveryWitness.RecoveryID != w.RecoveryID || got.FailureResult != nil {
			t.Fatalf("%s: new entry = %+v", name, got.Entry)
		}
		if name == "append" && !bytes.HasPrefix(after, before[:bytes.LastIndex(before, dm)+len(dm)]) {
			t.Fatalf("existing bytes before the new entry changed:\n%s", after)
		}
	}
}

func TestCommitOutboxD6MissingRefusals(t *testing.T) {
	want, w := fixtureMissingWitness(t)
	dm := legacyFixtureEntry(t, "legacy-dm-nil-relays.json")
	group := legacyFixtureEntry(t, "legacy-165-group-nil-relays.json")
	dmEvent, err := ParseRawGroupEntry(dm)
	if err != nil {
		t.Fatal(err)
	}
	collide := w
	collide.NewQueueID = "219bdbf9ed70b96893cc2382f365f24c"
	sameQueues := w
	sameQueues.NewQueueID = w.OldQueueID
	for name, c := range map[string]struct {
		doc []byte
		d6  outboxD6
	}{
		"event still present":   {rawOutboxDoc(dm, group), outboxD6{Witness: w, EventJSON: want.Entry.EventJSON}},
		"new queue collides":    {rawOutboxDoc(dm), outboxD6{Witness: collide, EventJSON: want.Entry.EventJSON}},
		"new equals old queue":  {rawOutboxDoc(dm), outboxD6{Witness: sameQueues, EventJSON: want.Entry.EventJSON}},
		"no frozen event":       {rawOutboxDoc(dm), outboxD6{Witness: w}},
		"wrong frozen event":    {rawOutboxDoc(dm), outboxD6{Witness: w, EventJSON: dmEvent.Entry.EventJSON}},
		"consumed digest given": {rawOutboxDoc(dm), outboxD6{Witness: w, EventJSON: want.Entry.EventJSON, ConsumedResultDigest: sha256Hex(nil)}},
	} {
		path := writeOutboxFile(t, c.doc)
		err := commitOutboxD6(path, c.d6)
		t.Logf("%s: %v", name, err)
		if err == nil {
			t.Errorf("%s: expected refusal", name)
		}
		requireOutboxUnchanged(t, path, c.doc)
	}
}

func TestCommitOutboxD6Exhausted(t *testing.T) {
	dm := legacyFixtureEntry(t, "legacy-dm-nil-relays.json")
	for reason, status := range map[string]string{
		witnessReasonExhaustedFailed:  OutboxStatusGroupFailed,
		witnessReasonExhaustedPending: OutboxStatusGroupPending,
	} {
		old := exhaustedGroupEntry(t, status)
		before := rawOutboxDoc(old, dm)
		path := writeOutboxFile(t, before)
		w := exhaustedWitness(t, old, reason, status, 10)
		if err := commitOutboxD6(path, outboxD6{Witness: w}); err != nil {
			t.Fatalf("%s: D6: %v", reason, err)
		}
		got, after := readD6Entry(t, path)
		oldParsed, _ := ParseRawGroupEntry(old)
		if got.Entry.Status != OutboxStatusGroupPending || got.Entry.RetryCount != 0 || got.Entry.LastAttempt != 0 ||
			got.Entry.CreatedAt != w.AuthorizedAt || string(got.Fields["relays"]) != "[]" || got.RecoveryWitness.OldEntryDigest != sha256Hex(old) {
			t.Fatalf("%s: new entry = %+v", reason, got.Entry)
		}
		if !bytes.Equal(bytes.TrimSpace(got.Fields["event_json"]), bytes.TrimSpace(oldParsed.Fields["event_json"])) {
			t.Fatalf("%s: frozen event_json token not carried verbatim", reason)
		}
		if bytes.Contains(after, []byte(d6OldQueue)) && !bytes.Contains(got.Fields["recovery_witness"], []byte(d6OldQueue)) {
			t.Fatalf("%s: old entry survived", reason)
		}
		if !bytes.HasSuffix(after, before[bytes.Index(before, dm):]) {
			t.Fatalf("%s: bytes after the replaced entry changed", reason)
		}
	}
}

func TestCommitOutboxD6ExhaustedRefusals(t *testing.T) {
	failed := exhaustedGroupEntry(t, OutboxStatusGroupFailed)
	before := rawOutboxDoc(failed)
	good := exhaustedWitness(t, failed, witnessReasonExhaustedFailed, OutboxStatusGroupFailed, 10)
	edit := func(change func(*RawRecoveryWitness)) RawRecoveryWitness {
		w := good
		change(&w)
		return w
	}
	_, missing := fixtureMissingWitness(t)
	for name, d6 := range map[string]outboxD6{
		"old digest":      {Witness: edit(func(w *RawRecoveryWitness) { w.OldEntryDigest = sha256Hex(nil) })},
		"old status":      {Witness: exhaustedWitness(t, failed, witnessReasonExhaustedPending, OutboxStatusGroupPending, 10)},
		"old count":       {Witness: edit(func(w *RawRecoveryWitness) { w.OldRetryCount = 11 })},
		"max retries":     {Witness: edit(func(w *RawRecoveryWitness) { w.MaxRetries, w.OldRetryCount = 9, 10 })},
		"old queue":       {Witness: edit(func(w *RawRecoveryWitness) { w.OldQueueID = "ffffffffffffffffffffffffffffffff" })},
		"event given":     {Witness: good, EventJSON: "{}"},
		"event digest":    {Witness: edit(func(w *RawRecoveryWitness) { w.EventJSONDigest = sha256Hex(nil) })},
		"recovery id":     {Witness: edit(func(w *RawRecoveryWitness) { w.RecoveryID = "XYZ" })},
		"missing present": {Witness: missing, EventJSON: "{}"},
	} {
		path := writeOutboxFile(t, before)
		err := commitOutboxD6(path, d6)
		t.Logf("%s: %v", name, err)
		if err == nil {
			t.Errorf("%s: expected refusal", name)
		}
		requireOutboxUnchanged(t, path, before)
	}
}

func TestCommitOutboxD6DropsConsumedResult(t *testing.T) {
	o3 := bytes.TrimSpace(readF2BFixture(t, "p-o3-entry.json"))
	old := bytes.Replace(o3, []byte(`"retry_count": 1`), []byte(`"retry_count": 10`), 1)
	old = bytes.Replace(old, []byte(`"group_pending"`), []byte(`"group_failed"`), 1)
	parsed, err := ParseRawGroupEntry(old)
	if err != nil {
		t.Fatalf("exhausted P-O3 entry: %v", err)
	}
	before := rawOutboxDoc(old)
	w := exhaustedWitness(t, old, witnessReasonExhaustedFailed, OutboxStatusGroupFailed, 10)

	path := writeOutboxFile(t, before)
	if err := commitOutboxD6(path, outboxD6{Witness: w}); err == nil {
		t.Fatal("D6 accepted over an unattested failure_result")
	}
	requireOutboxUnchanged(t, path, before)

	if err := commitOutboxD6(path, outboxD6{Witness: w, ConsumedResultDigest: sha256Hex(parsed.Fields["failure_result"])}); err != nil {
		t.Fatalf("D6 over consumed result: %v", err)
	}
	got, _ := readD6Entry(t, path)
	if got.FailureResult != nil || got.RecoveryWitness == nil {
		t.Fatalf("new epoch copied the old result: %+v", got)
	}
}

package messaging

import (
	"bytes"
	"errors"
	"os"
	"testing"
)

func TestCommitGroupCleanupLockedRemovesOnlyTheEntry(t *testing.T) {
	dm := legacyFixtureEntry(t, "legacy-dm-nil-relays.json")
	group := legacyFixtureEntry(t, "legacy-165-group-nil-relays.json")
	parsed, err := ParseRawGroupEntry(group)
	if err != nil {
		t.Fatal(err)
	}
	rec := GroupCleanupRecord{EventID: parsed.Entry.ID, QueueID: parsed.QueueID, FrozenEventJSON: parsed.Entry.EventJSON, MaxRetries: 10}
	for name, c := range map[string]struct{ before, after []byte }{
		"last":   {rawOutboxDoc(dm, group), rawOutboxDoc(dm)},
		"first":  {rawOutboxDoc(group, dm), rawOutboxDoc(dm)},
		"only":   {rawOutboxDoc(group), []byte("{ \"entries\" :\t[\n ] }\n")},
		"middle": {rawOutboxDoc(dm, group, dm), nil},
	} {
		if name == "middle" {
			continue // duplicate DM ids fail the scan; covered by the scan tests
		}
		path := writeOutboxFile(t, c.before)
		if err := CommitGroupCleanupLocked(path, rec); err != nil {
			t.Fatalf("%s: O2: %v", name, err)
		}
		got, _ := os.ReadFile(path)
		if !bytes.Equal(got, c.after) {
			t.Fatalf("%s: file =\n%s\nwant\n%s", name, got, c.after)
		}
		info, _ := os.Stat(path)
		if err := CommitGroupCleanupLocked(path, rec); err != nil {
			t.Fatalf("%s: second O2 must be a no-op: %v", name, err)
		}
		again, _ := os.ReadFile(path)
		if after, _ := os.Stat(path); !bytes.Equal(again, c.after) || !after.ModTime().Equal(info.ModTime()) {
			t.Fatalf("%s: no-op O2 touched the file", name)
		}
	}
}

func TestCommitGroupCleanupLockedRefusals(t *testing.T) {
	group := legacyFixtureEntry(t, "legacy-165-group-nil-relays.json")
	o3 := bytes.TrimSpace(readF2BFixture(t, "p-o3-entry.json"))
	parsed, err := ParseRawGroupEntry(group)
	if err != nil {
		t.Fatal(err)
	}
	o3Parsed, err := ParseRawGroupEntry(o3)
	if err != nil {
		t.Fatal(err)
	}
	base := GroupCleanupRecord{EventID: parsed.Entry.ID, QueueID: parsed.QueueID, FrozenEventJSON: parsed.Entry.EventJSON, MaxRetries: 10}
	edit := func(change func(*GroupCleanupRecord)) GroupCleanupRecord {
		r := base
		change(&r)
		return r
	}
	for name, c := range map[string]struct {
		doc  []byte
		rec  GroupCleanupRecord
		held bool
	}{
		"queue mismatch":      {rawOutboxDoc(group), edit(func(r *GroupCleanupRecord) { r.QueueID = "ffffffffffffffffffffffffffffffff" }), false},
		"frozen event":        {rawOutboxDoc(group), edit(func(r *GroupCleanupRecord) { r.FrozenEventJSON = "{}" }), false},
		"max retries":         {rawOutboxDoc(group), edit(func(r *GroupCleanupRecord) { r.MaxRetries = 3 }), false},
		"unconsumed result":   {rawOutboxDoc(o3), base, true},
		"wrong result digest": {rawOutboxDoc(o3), edit(func(r *GroupCleanupRecord) { r.LastResultDigest = sha256Hex(nil) }), true},
	} {
		path := writeOutboxFile(t, c.doc)
		err := CommitGroupCleanupLocked(path, c.rec)
		if err == nil || errors.Is(err, ErrOutboxCleanupHeld) != c.held {
			t.Errorf("%s: err = %v (held=%v)", name, err, c.held)
		}
		requireOutboxUnchanged(t, path, c.doc)
	}

	// A consumed result (digest attested) lets O2 proceed.
	path := writeOutboxFile(t, rawOutboxDoc(o3))
	consumed := edit(func(r *GroupCleanupRecord) { r.LastResultDigest = sha256Hex(o3Parsed.Fields["failure_result"]) })
	if err := CommitGroupCleanupLocked(path, consumed); err != nil {
		t.Fatalf("consumed result: %v", err)
	}
	if got, _ := os.ReadFile(path); bytes.Contains(got, []byte("failure_result")) {
		t.Fatalf("entry not removed:\n%s", got)
	}
}

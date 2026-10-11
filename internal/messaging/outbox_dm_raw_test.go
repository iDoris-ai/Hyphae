package messaging

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"fiatjaf.com/nostr"
	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/pkg/types"
)

// newDMEntry signs a fresh agent message for a random recipient and returns
// the typed entry enqueueOutboxEntry would build for it.
func newDMEntry(t *testing.T, queueID string) types.OutboxEntry {
	t.Helper()
	sender, recipient := nostr.Generate(), nostr.Generate()
	event := makeInboxEvent(t, recipient.Public(), sender, nostr.Now(), "raw dm "+queueID, nil)
	raw, err := event.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	return types.OutboxEntry{
		QueueID: queueID, ID: event.ID.Hex(), EventJSON: string(raw), RecipientNpub: common.EncodeNpub(recipient.Public()),
		Relays: nil, RetryCount: 0, MaxRetries: 10, LastAttempt: 0, CreatedAt: 1_791_539_064, Status: "pending",
	}
}

func TestAppendOutboxEntryLockedCreatesAndAppends(t *testing.T) {
	path := filepath.Join(t.TempDir(), "outbox.json")
	first := newDMEntry(t, "00000000000000000000000000000001")
	if err := appendOutboxEntryLocked(path, first); err != nil {
		t.Fatalf("append to missing file: %v", err)
	}
	ob, err := readOutbox(path)
	if err != nil || len(ob.Entries) != 1 || ob.Entries[0].ID != first.ID || ob.Entries[0].Relays == nil || len(ob.Entries[0].Relays) != 0 {
		t.Fatalf("typed read after raw append: %+v %v", ob, err)
	}
	second := newDMEntry(t, "00000000000000000000000000000002")
	second.Relays = []string{"wss://a", "wss://b\"c"}
	before, _ := os.ReadFile(path)
	if err := appendOutboxEntryLocked(path, second); err != nil {
		t.Fatalf("append second: %v", err)
	}
	after, _ := os.ReadFile(path)
	prefix := bytes.TrimSuffix(before, []byte("]\n}\n"))
	if len(prefix) == len(before) || !bytes.HasPrefix(after, prefix) {
		t.Fatalf("earlier bytes rewritten:\n%s\n---\n%s", before, after)
	}
	ob, err = readOutbox(path)
	if err != nil || len(ob.Entries) != 2 || ob.Entries[1].Relays[1] != `wss://b"c` {
		t.Fatalf("typed read: %+v %v", ob, err)
	}
	for name, dup := range map[string]types.OutboxEntry{
		"duplicate event": func() types.OutboxEntry { e := first; e.QueueID = "00000000000000000000000000000003"; return e }(),
		"duplicate queue": func() types.OutboxEntry { e := newDMEntry(t, first.QueueID); return e }(),
		"group route": func() types.OutboxEntry {
			e := newDMEntry(t, "00000000000000000000000000000004")
			e.Route = OutboxRouteGroup
			return e
		}(),
		"no queue id": func() types.OutboxEntry { e := newDMEntry(t, ""); return e }(),
	} {
		snapshot, _ := os.ReadFile(path)
		if err := appendOutboxEntryLocked(path, dup); err == nil {
			t.Errorf("%s: accepted", name)
		}
		if now, _ := os.ReadFile(path); !bytes.Equal(now, snapshot) {
			t.Errorf("%s: file changed", name)
		}
	}
}

func TestPatchAndRemoveOutboxEntryLockedLeaveGroupRecordsIntact(t *testing.T) {
	o3 := bytes.TrimSpace(readF2BFixture(t, "p-o3-entry.json"))
	dm := newDMEntry(t, "00000000000000000000000000000011")
	path := writeOutboxFile(t, rawOutboxDoc(o3, encodeLegacyOutboxEntry(dm)))
	groupBefore, _ := ParseRawGroupEntry(o3)

	sent, one, at := "sent", int64(1), int64(1_791_539_999)
	if err := patchOutboxEntryLocked(path, dm.ID, outboxDMPatch{Status: &sent, RetryCount: &one, LastAttempt: &at}); err != nil {
		t.Fatalf("patch: %v", err)
	}
	ob, err := readOutbox(path)
	if err != nil || len(ob.Entries) != 2 || ob.Entries[1].Status != "sent" || ob.Entries[1].RetryCount != 1 || ob.Entries[1].LastAttempt != at {
		t.Fatalf("patched entry: %+v %v", ob.Entries, err)
	}
	after, _ := os.ReadFile(path)
	entry, _, err := ReadGroupEntryLocked(path, groupBefore.Entry.ID)
	if err != nil || !bytes.Equal(entry.Raw, groupBefore.Raw) {
		t.Fatalf("group entry bytes changed: %v", err)
	}
	if !bytes.Contains(after, groupBefore.Fields["failure_result"]) {
		t.Fatal("failure_result fragment changed")
	}

	// Refusals write nothing.
	before, _ := os.ReadFile(path)
	bad := "bogus"
	for name, err := range map[string]error{
		"invalid status":      patchOutboxEntryLocked(path, dm.ID, outboxDMPatch{Status: &bad}),
		"group entry patched": patchOutboxEntryLocked(path, groupBefore.Entry.ID, outboxDMPatch{Status: &sent}),
		"group entry removed": removeOutboxEntryLocked(path, groupBefore.Entry.ID),
	} {
		if err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if err := removeOutboxEntryLocked(path, "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"); !errors.Is(err, errOutboxEntrySuperseded) {
		t.Fatalf("missing entry: %v", err)
	}
	if err := patchOutboxEntryLocked(path, groupBefore.Entry.ID, outboxDMPatch{Status: &sent}); !errors.Is(err, errOutboxGroupEntryViaRawDM) {
		t.Fatalf("group patch error class: %v", err)
	}
	if now, _ := os.ReadFile(path); !bytes.Equal(now, before) {
		t.Fatal("refusals changed the file")
	}
	if err := patchOutboxEntryLocked(path, dm.ID, outboxDMPatch{}); err != nil {
		t.Fatalf("empty patch must be a no-op: %v", err)
	}

	if err := removeOutboxEntryLocked(path, dm.ID); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if ob, err = readOutbox(path); err != nil || len(ob.Entries) != 1 || ob.Entries[0].ID != groupBefore.Entry.ID {
		t.Fatalf("after remove: %+v %v", ob.Entries, err)
	}
	entry, _, err = ReadGroupEntryLocked(path, groupBefore.Entry.ID)
	if err != nil || !bytes.Equal(entry.Raw, groupBefore.Raw) {
		t.Fatalf("group entry bytes changed by DM removal: %v", err)
	}
	if err := removeOutboxEntryLocked(path, dm.ID); !errors.Is(err, errOutboxEntrySuperseded) {
		t.Fatalf("second removal: %v", err)
	}
}

func TestRemoveOutboxEntryLockedPositions(t *testing.T) {
	a, b, c := newDMEntry(t, "0000000000000000000000000000000a"), newDMEntry(t, "0000000000000000000000000000000b"), newDMEntry(t, "0000000000000000000000000000000c")
	doc := func(entries ...types.OutboxEntry) []byte {
		var raw [][]byte
		for _, e := range entries {
			raw = append(raw, encodeLegacyOutboxEntry(e))
		}
		return rawOutboxDoc(raw...)
	}
	for name, tc := range map[string]struct {
		remove types.OutboxEntry
		want   []byte
	}{
		"first":  {a, doc(b, c)},
		"middle": {b, doc(a, c)},
		"last":   {c, doc(a, b)},
	} {
		path := writeOutboxFile(t, doc(a, b, c))
		if err := removeOutboxEntryLocked(path, tc.remove.ID); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got, _ := os.ReadFile(path)
		if !bytes.Equal(got, tc.want) {
			t.Fatalf("%s: got\n%s\nwant\n%s", name, got, tc.want)
		}
	}
	path := writeOutboxFile(t, doc(a))
	if err := removeOutboxEntryLocked(path, a.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, []byte("{ \"entries\" :\t[\n ] }\n")) {
		t.Fatalf("only entry removal: %q", got)
	}
}

package messaging

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/pkg/types"
)

// stageHomeOutbox writes raw bytes to the temp-HOME outbox with a fixed mtime.
func stageHomeOutbox(t *testing.T, raw []byte) string {
	t.Helper()
	setupTempOutbox(t)
	path, err := GetOutboxPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	old := time.Unix(1_000_000_000, 0)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	return path
}

func requireTypedWriteHeld(t *testing.T, name string, err error) {
	t.Helper()
	if !errors.Is(err, ErrOutboxTypedWriteHeld) {
		t.Errorf("%s: err = %v, want ErrOutboxTypedWriteHeld", name, err)
	}
}

// With protocol records in the file, typed transactions are committed as
// DM-only raw patches: DM traffic keeps flowing, group entries are immutable
// through the typed path, and SaveOutbox (whole-file replacement) is refused.
func TestTypedOutboxWritersWithProtocolRecords(t *testing.T) {
	dm := legacyFixtureEntry(t, "legacy-dm-nil-relays.json")
	o3 := bytes.TrimSpace(readF2BFixture(t, "p-o3-entry.json"))
	path := stageHomeOutbox(t, rawOutboxDoc(dm, o3))
	dmParsed, err := ParseRawGroupEntry(dm)
	if err != nil {
		t.Fatal(err)
	}
	groupParsed, err := ParseRawGroupEntry(o3)
	if err != nil {
		t.Fatal(err)
	}
	legacyGroup, err := ParseRawGroupEntry(legacyFixtureEntry(t, "legacy-165-group-nil-relays.json"))
	if err != nil {
		t.Fatal(err)
	}
	groupIntact := func(step string) {
		t.Helper()
		entry, _, err := ReadGroupEntryLocked(path, groupParsed.Entry.ID)
		if err != nil || entry == nil || !bytes.Equal(entry.Raw, groupParsed.Raw) {
			t.Fatalf("%s: group entry bytes changed (%v)", step, err)
		}
	}

	// Group-touching typed operations are refused with zero writes.
	raw, _ := os.ReadFile(path)
	_, err = UpdateOutbox(func(ob *types.Outbox) error { ob.Entries = nil; return nil })
	requireTypedWriteHeld(t, "typed removal of a group entry", err)
	requireTypedWriteHeld(t, "SaveOutbox", SaveOutbox(&types.Outbox{Entries: []types.OutboxEntry{}}))
	requireTypedWriteHeld(t, "UpdateOutboxStatus on group", UpdateOutboxStatus(&types.Outbox{}, groupParsed.Entry.ID, "group_failed"))
	freshGroup := makeInboxEvent(t, nostr.Generate().Public(), nostr.Generate(), nostr.Now(), "new group event", nil)
	freshJSON, _ := freshGroup.MarshalJSON()
	_, _, err = EnqueueGroupOutboxEntry(string(freshJSON), legacyGroup.Entry.RecipientNpub, nil, 10)
	requireTypedWriteHeld(t, "EnqueueGroupOutboxEntry", err)
	_, err = UpdateOutbox(func(ob *types.Outbox) error { ob.Entries[0], ob.Entries[1] = ob.Entries[1], ob.Entries[0]; return nil })
	requireTypedWriteHeld(t, "reorder", err)
	_, err = UpdateOutbox(func(ob *types.Outbox) error { ob.Entries[0].MaxRetries = 3; return nil })
	requireTypedWriteHeld(t, "immutable DM field", err)
	requireOutboxUnchanged(t, path, raw)

	// DM operations succeed and the group entry stays byte-identical.
	if _, err := UpdateOutbox(func(*types.Outbox) error { return nil }); err != nil {
		t.Fatalf("no-op: %v", err)
	}
	requireOutboxUnchanged(t, path, raw)
	if err := UpdateOutboxStatus(&types.Outbox{}, dmParsed.Entry.ID, "failed"); err != nil {
		t.Fatalf("UpdateOutboxStatus DM: %v", err)
	}
	groupIntact("status")
	if err := IncrementOutboxRetry(&types.Outbox{}, dmParsed.Entry.ID); err != nil {
		t.Fatalf("IncrementOutboxRetry: %v", err)
	}
	ob, err := LoadOutbox()
	if err != nil || len(ob.Entries) != 2 || ob.Entries[0].Status != "failed" || ob.Entries[0].RetryCount != 1 {
		t.Fatalf("DM patches not visible: %+v %v", ob.Entries, err)
	}
	groupIntact("retry")

	sender, recipient := nostr.Generate(), nostr.Generate()
	event := makeInboxEvent(t, recipient.Public(), sender, nostr.Now(), "dm beside group records", nil)
	if err := AddToOutbox(&types.Outbox{}, &event, common.EncodeNpub(recipient.Public()), nil); err != nil {
		t.Fatalf("AddToOutbox: %v", err)
	}
	groupIntact("append")
	published := false
	publish := func(context.Context, []string, nostr.Event, time.Duration) bool { published = true; return true }
	store := func(*nostr.Event, string, string, bool) error { return nil }
	ob, _ = LoadOutbox()
	result, err := attemptSend(context.Background(), ob, ob.Entries[2], []string{"wss://relay.invalid"}, time.Second, publish, store)
	if err != nil || !published || !result.Sent || result.Superseded {
		t.Fatalf("DM send beside group records: %+v %v", result, err)
	}
	groupIntact("send")
	if err := RemoveFromOutbox(&types.Outbox{}, dmParsed.Entry.ID); err != nil {
		t.Fatalf("RemoveFromOutbox: %v", err)
	}
	if err := CleanupOutbox(&types.Outbox{}, 0); err != nil {
		t.Fatalf("CleanupOutbox: %v", err)
	}
	groupIntact("remove+cleanup")
	ob, _ = LoadOutbox()
	if len(ob.Entries) != 1 || ob.Entries[0].ID != groupParsed.Entry.ID {
		t.Fatalf("expected only the group entry to remain: %+v", ob.Entries)
	}
}

func TestTypedDiffAssignsQueueIDToLegacyEntry(t *testing.T) {
	dm := legacyFixtureEntry(t, "legacy-dm-nil-relays.json")
	legacy := bytes.Replace(dm, []byte(`"queue_id": "219bdbf9ed70b96893cc2382f365f24c",`), nil, 1)
	if bytes.Equal(legacy, dm) {
		t.Fatal("fixture edit did not apply")
	}
	o3 := bytes.TrimSpace(readF2BFixture(t, "p-o3-entry.json"))
	path := stageHomeOutbox(t, rawOutboxDoc(legacy, o3))
	ob, err := LoadOutbox()
	if err != nil || ob.Entries[0].QueueID != "" {
		t.Fatalf("legacy entry: %+v %v", ob.Entries, err)
	}
	current, err := currentOutboxAttempt(ob.Entries[0])
	if err != nil || current.entry.QueueID == "" {
		t.Fatalf("currentOutboxAttempt: %+v %v", current.entry, err)
	}
	after, _ := LoadOutbox()
	if after.Entries[0].QueueID != current.entry.QueueID || after.Entries[0].Status != "pending" {
		t.Fatalf("queue_id not persisted: %+v", after.Entries[0])
	}
	if _, err := scanRawOutboxFile(mustRead(t, path)); err != nil {
		t.Fatalf("file after queue_id assignment fails the strict scan: %v", err)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestTypedOutboxGuardMembers(t *testing.T) {
	dm := legacyFixtureEntry(t, "legacy-dm-nil-relays.json")
	escaped := bytes.Replace(dm, []byte(`"status": "pending"`), []byte(`"status": "pending", "fanout_protocol": 1`), 1)
	extra := bytes.Replace(dm, []byte(`"status": "pending"`), []byte(`"status": "pending", "note": "x"`), 1)
	for name, raw := range map[string][]byte{
		"escaped protocol key": rawOutboxDoc(escaped),
		"unknown entry member": rawOutboxDoc(extra),
		"top-level member":     []byte(`{"entries":[],"version":2}`),
	} {
		if bytes.Equal(raw, rawOutboxDoc(dm)) {
			t.Fatalf("%s: mutation did not apply", name)
		}
		path := stageHomeOutbox(t, raw)
		_, err := UpdateOutbox(func(ob *types.Outbox) error {
			ob.Entries = append(ob.Entries, types.OutboxEntry{QueueID: "ffffffffffffffffffffffffffffffff", ID: "x", Status: "pending"})
			return nil
		})
		requireTypedWriteHeld(t, name, err)
		requireOutboxUnchanged(t, path, raw)
	}

	stageHomeOutbox(t, rawOutboxDoc(dm))
	if _, err := UpdateOutbox(func(ob *types.Outbox) error { ob.Entries = nil; return nil }); err != nil {
		t.Fatalf("legacy-only outbox must stay writable: %v", err)
	}

	path := stageHomeOutbox(t, []byte(`{"entries":[`))
	if _, err := UpdateOutbox(func(*types.Outbox) error { return nil }); err == nil || errors.Is(err, ErrOutboxTypedWriteHeld) {
		t.Fatalf("malformed outbox: err = %v", err)
	}
	requireOutboxUnchanged(t, path, []byte(`{"entries":[`))
}

// TestUpdateOutboxHeldNoopGuard guards #224: a held file the strict scan
// rejects (here, a duplicate queue_id) must stay refused even when the
// transaction itself changes nothing, instead of falling through to a send
// path that would publish a DM from it.
func TestUpdateOutboxHeldNoopGuard(t *testing.T) {
	dm := legacyFixtureEntry(t, "legacy-dm-nil-relays.json")
	o3 := bytes.TrimSpace(readF2BFixture(t, "p-o3-entry.json"))
	dupQueue := bytes.Replace(dm, []byte(`"queue_id": "219bdbf9ed70b96893cc2382f365f24c"`), []byte(`"queue_id": "2fc72e7f8539e9eff9120c979198790b"`), 1)
	if bytes.Equal(dupQueue, dm) {
		t.Fatal("fixture mutation did not apply")
	}
	raw := rawOutboxDoc(dupQueue, o3)
	path := stageHomeOutbox(t, raw)

	if _, err := UpdateOutbox(func(*types.Outbox) error { return nil }); err == nil {
		t.Fatal("no-op UpdateOutbox on a strict-invalid held file: want error, got nil")
	} else if !errors.Is(err, ErrOutboxTypedWriteHeld) {
		t.Fatalf("no-op UpdateOutbox err = %v, want ErrOutboxTypedWriteHeld", err)
	}
	requireOutboxUnchanged(t, path, raw)

	// The DM already carries a durable queue_id, so currentOutboxAttempt
	// takes the no-op branch -- this proves the guard, not an unrelated
	// signature/parse failure, is what stops the send.
	ob, err := LoadOutbox()
	if err != nil || len(ob.Entries) == 0 || ob.Entries[0].QueueID == "" {
		t.Fatalf("LoadOutbox: %+v %v", ob, err)
	}
	calls := 0
	publish := func(context.Context, []string, nostr.Event, time.Duration) bool { calls++; return true }
	store := func(*nostr.Event, string, string, bool) error { return nil }
	if _, err := attemptSend(context.Background(), ob, ob.Entries[0], []string{"wss://relay.invalid"}, time.Second, publish, store); err == nil {
		t.Fatal("attemptSend on a strict-invalid held file: want error, got nil")
	}
	if calls != 0 {
		t.Fatalf("publish called %d times on a strict-invalid held file", calls)
	}
	requireOutboxUnchanged(t, path, raw)
}

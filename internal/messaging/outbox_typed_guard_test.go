package messaging

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"fiatjaf.com/nostr"
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

func TestTypedOutboxWritersRefuseProtocolRecords(t *testing.T) {
	dm := legacyFixtureEntry(t, "legacy-dm-nil-relays.json")
	o3 := bytes.TrimSpace(readF2BFixture(t, "p-o3-entry.json"))
	raw := rawOutboxDoc(dm, o3)
	path := stageHomeOutbox(t, raw)
	dmParsed, err := ParseRawGroupEntry(dm)
	if err != nil {
		t.Fatal(err)
	}
	legacyGroup, err := ParseRawGroupEntry(legacyFixtureEntry(t, "legacy-165-group-nil-relays.json"))
	if err != nil {
		t.Fatal(err)
	}

	_, err = UpdateOutbox(func(*types.Outbox) error { return nil })
	requireTypedWriteHeld(t, "no-op UpdateOutbox", err)
	_, err = UpdateOutbox(func(ob *types.Outbox) error { ob.Entries = nil; return nil })
	requireTypedWriteHeld(t, "UpdateOutbox", err)
	requireTypedWriteHeld(t, "SaveOutbox", SaveOutbox(&types.Outbox{}))
	requireTypedWriteHeld(t, "CleanupOutbox", CleanupOutbox(&types.Outbox{}, 0))
	requireTypedWriteHeld(t, "RemoveFromOutbox", RemoveFromOutbox(&types.Outbox{}, dmParsed.Entry.ID))
	requireTypedWriteHeld(t, "UpdateOutboxStatus", UpdateOutboxStatus(&types.Outbox{}, dmParsed.Entry.ID, "sent"))
	_, _, err = EnqueueGroupOutboxEntry(legacyGroup.Entry.EventJSON, legacyGroup.Entry.RecipientNpub, nil, 10)
	requireTypedWriteHeld(t, "EnqueueGroupOutboxEntry", err)
	_, _, err = RequeueGroupOutboxEntry(legacyGroup.Entry.EventJSON, legacyGroup.Entry.RecipientNpub, nil, 10)
	requireTypedWriteHeld(t, "RequeueGroupOutboxEntry", err)

	published := false
	publish := func(context.Context, []string, nostr.Event, time.Duration) bool { published = true; return true }
	store := func(*nostr.Event, string, string, bool) error { return nil }
	ob, err := LoadOutbox()
	if err != nil {
		t.Fatalf("read-only LoadOutbox must still work: %v", err)
	}
	_, err = attemptSend(context.Background(), ob, dmParsed.Entry, []string{"wss://relay.invalid"}, time.Second, publish, store)
	requireTypedWriteHeld(t, "attemptSend", err)
	if published {
		t.Fatal("legacy send path published while protocol records were present")
	}
	requireOutboxUnchanged(t, path, raw)
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
		_, err := UpdateOutbox(func(*types.Outbox) error { return nil })
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

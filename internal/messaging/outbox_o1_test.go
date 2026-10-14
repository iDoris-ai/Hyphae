package messaging

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCommitGroupEnqueueLockedAppendsProtocolEntry(t *testing.T) {
	group, err := ParseRawGroupEntry(legacyFixtureEntry(t, "legacy-165-group-nil-relays.json"))
	if err != nil {
		t.Fatal(err)
	}
	rec := GroupEnqueueRecord{EventJSON: group.Entry.EventJSON, RecipientNpub: group.Entry.RecipientNpub, MaxRetries: 10, CreatedAt: 1_791_539_064}
	path := filepath.Join(t.TempDir(), "outbox.json")

	queueID, existed, err := CommitGroupEnqueueLocked(path, rec)
	if err != nil || existed || len(queueID) != 32 {
		t.Fatalf("O1 on missing file: %q %v %v", queueID, existed, err)
	}
	entry, _, err := ReadGroupEntryLocked(path, group.Entry.ID)
	if err != nil || entry == nil || entry.QueueID != queueID || entry.FanoutProtocol == nil || *entry.FanoutProtocol != 1 {
		t.Fatalf("O1 entry: %+v %v", entry, err)
	}
	if entry.Entry.Status != OutboxStatusGroupPending || entry.Entry.RetryCount != 0 || entry.Entry.LastAttempt != 0 || entry.Entry.MaxRetries != 10 ||
		entry.Entry.CreatedAt != rec.CreatedAt || string(entry.Fields["relays"]) != "[]" {
		t.Fatalf("O1 entry fields: %+v relays=%s", entry.Entry, entry.Fields["relays"])
	}
	ob, err := readOutbox(path)
	if err != nil || len(ob.Entries) != 1 || ob.Entries[0].Route != OutboxRouteGroup || ob.Entries[0].QueueID != queueID {
		t.Fatalf("typed read of the O1 entry: %+v %v", ob, err)
	}

	// Idempotent: the same frozen event for the same recipient is "existed".
	before, _ := os.ReadFile(path)
	again, existed, err := CommitGroupEnqueueLocked(path, rec)
	if err != nil || !existed || again != queueID {
		t.Fatalf("re-enqueue: %q %v %v", again, existed, err)
	}
	requireOutboxBytes(t, path, before)

	// Conflicts write nothing.
	other := rec
	other.RecipientNpub = "npub1qs84sd0arvhxqn33mx7rlc9sncrjwcsgzdgyzxszcu7y4qh7w3dqw4y824"
	if _, _, err := CommitGroupEnqueueLocked(path, other); !errors.Is(err, ErrOutboxEnqueueConflict) {
		t.Fatalf("different recipient: %v", err)
	}
	requireOutboxBytes(t, path, before)
	for name, bad := range map[string]GroupEnqueueRecord{
		"no recipient": {EventJSON: rec.EventJSON, MaxRetries: 10},
		"zero budget":  {EventJSON: rec.EventJSON, RecipientNpub: rec.RecipientNpub},
		"bad event":    {EventJSON: "{", RecipientNpub: rec.RecipientNpub, MaxRetries: 10},
	} {
		if _, _, err := CommitGroupEnqueueLocked(path, bad); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	requireOutboxBytes(t, path, before)

	// Once protocol records exist, typed DM traffic still flows (F2e-2b) and
	// a typed group enqueue is refused.
	t.Setenv("HOME", filepath.Dir(path))
	dm := newDMEntry(t, "00000000000000000000000000000021")
	if err := appendOutboxEntryLocked(path, dm); err != nil {
		t.Fatalf("DM beside O1 entry: %v", err)
	}
	entry, _, err = ReadGroupEntryLocked(path, group.Entry.ID)
	if err != nil || entry.FanoutProtocol == nil {
		t.Fatalf("O1 entry lost its protocol tag: %v", err)
	}
	withRelays := GroupEnqueueRecord{EventJSON: dm.EventJSON, RecipientNpub: dm.RecipientNpub, MaxRetries: 3, CreatedAt: 1, Relays: []string{"wss://x"}}
	if _, _, err := CommitGroupEnqueueLocked(path, withRelays); !errors.Is(err, ErrOutboxEnqueueConflict) {
		t.Fatalf("DM entry already holds the event: %v", err)
	}
}

func TestCommitGroupEnqueueLockedExhaustedEntryConflicts(t *testing.T) {
	failed := exhaustedGroupEntry(t, OutboxStatusGroupFailed)
	parsed, err := ParseRawGroupEntry(failed)
	if err != nil {
		t.Fatal(err)
	}
	path := writeOutboxFile(t, rawOutboxDoc(failed))
	rec := GroupEnqueueRecord{EventJSON: parsed.Entry.EventJSON, RecipientNpub: parsed.Entry.RecipientNpub, MaxRetries: 10, CreatedAt: 1}
	if _, _, err := CommitGroupEnqueueLocked(path, rec); !errors.Is(err, ErrOutboxEnqueueConflict) {
		t.Fatalf("exhausted entry must not be re-enqueued by O1 (that is D6): %v", err)
	}
	requireOutboxUnchanged(t, path, rawOutboxDoc(failed))
}

func requireOutboxBytes(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("outbox bytes changed (%v):\n%s", err, got)
	}
}

// An active entry whose max_retries differs from the frozen record is not
// reusable: O1 refuses without adopting or rewriting the budget.
func TestCommitGroupEnqueueLockedMaxRetriesMismatchConflicts(t *testing.T) {
	group, err := ParseRawGroupEntry(legacyFixtureEntry(t, "legacy-165-group-nil-relays.json"))
	if err != nil {
		t.Fatal(err)
	}
	rec := GroupEnqueueRecord{EventJSON: group.Entry.EventJSON, RecipientNpub: group.Entry.RecipientNpub, MaxRetries: 3, CreatedAt: 7}
	path := filepath.Join(t.TempDir(), "outbox.json")
	if _, _, err := CommitGroupEnqueueLocked(path, rec); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	rec.MaxRetries++
	if _, _, err := CommitGroupEnqueueLocked(path, rec); !errors.Is(err, ErrOutboxEnqueueConflict) {
		t.Fatalf("max_retries mismatch must conflict, got %v", err)
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Fatal("conflict must not rewrite the outbox")
	}
}

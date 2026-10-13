package messaging

import (
	"bytes"
	"errors"
	"os"
	"testing"
)

// Cloud ABC pilot (run_77c53b02dd26 task B): O1 must refuse adoption of an
// existing group_pending entry whose last_attempt is nonzero, leaving raw
// bytes and mtime untouched. Baseline d316a98 lacks that guard; this file
// records the expected rejection contract without changing production code.
func TestCommitGroupEnqueueLockedRefusesNonzeroLastAttempt(t *testing.T) {
	group, err := ParseRawGroupEntry(legacyFixtureEntry(t, "legacy-165-group-nil-relays.json"))
	if err != nil {
		t.Fatal(err)
	}
	rec := GroupEnqueueRecord{
		EventJSON:     group.Entry.EventJSON,
		RecipientNpub: group.Entry.RecipientNpub,
		MaxRetries:    int64(group.Entry.MaxRetries),
		CreatedAt:     group.Entry.CreatedAt,
	}

	nonzeroAttempt := bytes.Replace(
		legacyFixtureEntry(t, "legacy-165-group-nil-relays.json"),
		[]byte(`"last_attempt": 0`),
		[]byte(`"last_attempt": 1`),
		1,
	)
	if bytes.Equal(nonzeroAttempt, legacyFixtureEntry(t, "legacy-165-group-nil-relays.json")) {
		t.Fatal("fixture mutation did not change last_attempt")
	}
	doc := rawOutboxDoc(nonzeroAttempt)
	path := writeOutboxFile(t, doc)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	queueID, existed, err := CommitGroupEnqueueLocked(path, rec)
	if !errors.Is(err, ErrOutboxEnqueueConflict) {
		t.Fatalf("nonzero last_attempt must conflict (want ErrOutboxEnqueueConflict); got queueID=%q existed=%v err=%v", queueID, existed, err)
	}
	if existed || queueID != "" {
		t.Fatalf("conflict must not return an adopted queue id: queueID=%q existed=%v", queueID, existed)
	}
	requireOutboxUnchanged(t, path, before)
}

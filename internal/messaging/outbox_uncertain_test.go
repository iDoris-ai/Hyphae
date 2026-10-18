package messaging

import (
	"bytes"
	"errors"
	"os"
	"testing"
)

// injectDirSyncFailure makes the post-rename directory fsync fail for the
// duration of the test.
func injectDirSyncFailure(t *testing.T) {
	t.Helper()
	original := syncOutboxDir
	syncOutboxDir = func(*os.File) error { return errors.New("injected EIO on directory fsync") }
	t.Cleanup(func() { syncOutboxDir = original })
}

func TestWriteRawOutboxAtomicDirSyncFailureIsUncertainButVisible(t *testing.T) {
	path := writeOutboxFile(t, []byte(`{"entries":[]}`))
	injectDirSyncFailure(t)
	err := writeRawOutboxAtomic(path, []byte(`{"entries":[ ]}`))
	if !IsOutboxCommitUncertain(err) {
		t.Fatalf("err = %v, want commit-uncertain", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != `{"entries":[ ]}` {
		t.Fatalf("rename already happened; file = %s", got)
	}
	if err := ConfirmOutboxDurable(path); !IsOutboxCommitUncertain(err) {
		t.Fatalf("confirm must stay uncertain while fsync fails: %v", err)
	}
	if !IsOutboxCommitUncertain(MarkOutboxCommitUncertain(errors.New("x"))) {
		t.Fatal("MarkOutboxCommitUncertain must classify as uncertain")
	}
}

func TestCommitGroupFailureLockedUncertainLeavesRecordForConfirm(t *testing.T) {
	group := legacyFixtureEntry(t, "legacy-165-group-nil-relays.json")
	parsed, err := ParseRawGroupEntry(group)
	if err != nil {
		t.Fatal(err)
	}
	path := writeOutboxFile(t, rawOutboxDoc(group))
	injectDirSyncFailure(t)
	receipt, err := CommitGroupFailureLocked(path, GroupFailureRecord{Result: fixtureO3Result(t), FrozenEventJSON: parsed.Entry.EventJSON, MaxRetries: 10})
	if !IsOutboxCommitUncertain(err) || receipt != "" {
		t.Fatalf("receipt=%q err=%v", receipt, err)
	}
	// The record is on disk: a locked reread (what confirm does) finds it.
	entry, found, err := ReadGroupEntryLocked(path, parsed.Entry.ID)
	if err != nil || entry == nil || entry.FailureResult == nil || found == "" {
		t.Fatalf("record not visible after uncertain commit: %v %v", entry, err)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Contains(after, []byte(`"failure_result"`)) {
		t.Fatal("file lacks the record")
	}
}

package messaging

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestWithGroupOutboxLockPassesOutboxPath(t *testing.T) {
	setupTempOutbox(t)
	want, err := GetOutboxPath()
	if err != nil {
		t.Fatal(err)
	}
	called := false
	err = WithGroupOutboxLock(func(path string) error {
		called = path == want
		return errors.New("inner")
	})
	if !called || err == nil || err.Error() != "inner" {
		t.Fatalf("called=%v err=%v", called, err)
	}
}

func TestCommitGroupFailureLocked(t *testing.T) {
	group := legacyFixtureEntry(t, "legacy-165-group-nil-relays.json")
	parsed, err := ParseRawGroupEntry(group)
	if err != nil {
		t.Fatal(err)
	}
	before := rawOutboxDoc(group)
	record := GroupFailureRecord{Result: fixtureO3Result(t), FrozenEventJSON: parsed.Entry.EventJSON, MaxRetries: 10}

	for name, change := range map[string]func(*GroupFailureRecord){
		"frozen event": func(r *GroupFailureRecord) { r.FrozenEventJSON = "{}" },
		"max retries":  func(r *GroupFailureRecord) { r.MaxRetries = 3 },
		"queue":        func(r *GroupFailureRecord) { r.Result.Token.QueueID = "missing" },
	} {
		path := writeOutboxFile(t, before)
		bad := record
		change(&bad)
		if receipt, err := CommitGroupFailureLocked(path, bad); err == nil || receipt != "" {
			t.Errorf("%s: receipt=%q err=%v", name, receipt, err)
		}
		requireOutboxUnchanged(t, path, before)
	}

	path := writeOutboxFile(t, before)
	receipt, err := CommitGroupFailureLocked(path, record)
	if err != nil {
		t.Fatalf("O3: %v", err)
	}
	after, _ := os.ReadFile(path)
	got := scanO3Target(t, after, record.Result.Token.QueueID)
	if want := FailureReceipt(got.FailureResult.ResultID, sha256Hex(got.Fields["failure_result"])); receipt != want {
		t.Fatalf("receipt = %q, want %q", receipt, want)
	}
}

func TestIsOutboxCommitUncertain(t *testing.T) {
	uncertain := fmt.Errorf("wrapped: %w", &outboxCommitUncertainError{errors.New("dir fsync")})
	if !IsOutboxCommitUncertain(uncertain) || IsOutboxCommitUncertain(errors.New("plain")) || IsOutboxCommitUncertain(nil) {
		t.Fatal("IsOutboxCommitUncertain misclassified an error")
	}
	if FailureReceipt("a", "b") != "a:b" {
		t.Fatal("receipt format changed")
	}
}

func TestReadGroupFailureLocked(t *testing.T) {
	group := legacyFixtureEntry(t, "legacy-165-group-nil-relays.json")
	o3 := bytes.TrimSpace(readF2BFixture(t, "p-o3-entry.json"))
	path := writeOutboxFile(t, rawOutboxDoc(group))
	entry, receipt, err := ReadGroupFailureLocked(path, "2fc72e7f8539e9eff9120c979198790b")
	if err != nil || receipt != "" || entry == nil || entry.FailureResult != nil {
		t.Fatalf("legacy entry: entry=%v receipt=%q err=%v", entry, receipt, err)
	}
	if _, _, err := ReadGroupFailureLocked(path, "missing"); err == nil {
		t.Fatal("missing queue must error")
	}
	if _, _, err := ReadGroupFailureLocked(writeOutboxFile(t, []byte(`{"entries":[{}]}`)), "x"); err == nil {
		t.Fatal("corrupt outbox must error")
	}
	path = writeOutboxFile(t, rawOutboxDoc(o3))
	entry, receipt, err = ReadGroupFailureLocked(path, "2fc72e7f8539e9eff9120c979198790b")
	if err != nil || entry.FailureResult == nil {
		t.Fatalf("P-O3 entry: %v", err)
	}
	if want := FailureReceipt(entry.FailureResult.ResultID, sha256Hex(entry.Fields["failure_result"])); receipt != want {
		t.Fatalf("receipt = %q, want %q", receipt, want)
	}
}

func TestReadGroupEntryLockedAndConfirmDurable(t *testing.T) {
	group := legacyFixtureEntry(t, "legacy-165-group-nil-relays.json")
	o3 := bytes.TrimSpace(readF2BFixture(t, "p-o3-entry.json"))
	const eventID = "3ab652f4ab22a053e7c17b8b35ab99f903f5086a2b44c459ace8d81844662906"

	missing := filepath.Join(t.TempDir(), "outbox.json")
	if entry, receipt, err := ReadGroupEntryLocked(missing, eventID); entry != nil || receipt != "" || err != nil {
		t.Fatalf("missing file: %v %q %v", entry, receipt, err)
	}
	path := writeOutboxFile(t, rawOutboxDoc(group))
	if entry, receipt, err := ReadGroupEntryLocked(path, eventID); err != nil || receipt != "" || entry == nil || entry.QueueID != "2fc72e7f8539e9eff9120c979198790b" {
		t.Fatalf("legacy entry: %v %q %v", entry, receipt, err)
	}
	if entry, _, err := ReadGroupEntryLocked(path, "0000"); entry != nil || err != nil {
		t.Fatalf("absent event: %v %v", entry, err)
	}
	if _, _, err := ReadGroupEntryLocked(writeOutboxFile(t, []byte(`{"entries":[{}]}`)), eventID); err == nil {
		t.Fatal("corrupt outbox must error")
	}
	path = writeOutboxFile(t, rawOutboxDoc(o3))
	entry, receipt, err := ReadGroupEntryLocked(path, eventID)
	if err != nil || entry.FailureResult == nil || receipt != FailureReceipt(entry.FailureResult.ResultID, sha256Hex(entry.Fields["failure_result"])) {
		t.Fatalf("P-O3 entry: %v %q", err, receipt)
	}
	if err := ConfirmOutboxDurable(path); err != nil {
		t.Fatalf("durable: %v", err)
	}
	if err := ConfirmOutboxDurable(filepath.Join(t.TempDir(), "nodir", "outbox.json")); !IsOutboxCommitUncertain(err) {
		t.Fatalf("missing directory must stay uncertain: %v", err)
	}
}

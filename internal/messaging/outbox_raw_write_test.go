package messaging

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestApplyRawOutboxPatchesReplacesOnlyTheRange(t *testing.T) {
	raw := []byte(`{"entries":[{"status":"pending"}]}`)
	start := len(`{"entries":[{"status":"`)
	end := start + len("pending")
	got, err := ApplyRawOutboxPatches(raw, []RawOutboxPatch{{Start: start, End: end, Replacement: []byte("failed")}})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if want := `{"entries":[{"status":"failed"}]}`; string(got) != want {
		t.Fatalf("patched = %s, want %s", got, want)
	}
	// Every byte outside the range is carried through unchanged.
	if string(got[:start]) != string(raw[:start]) || string(got[start+len("failed"):]) != string(raw[end:]) {
		t.Fatal("bytes outside the patched range changed")
	}
	if string(raw) != `{"entries":[{"status":"pending"}]}` {
		t.Fatal("input buffer was mutated")
	}
}

func TestApplyRawOutboxPatchesMultiplePreservesGaps(t *testing.T) {
	raw := []byte("0123456789")
	got, err := ApplyRawOutboxPatches(raw, []RawOutboxPatch{
		{Start: 1, End: 2, Replacement: []byte("A")},
		{Start: 5, End: 7, Replacement: []byte("BC")},
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if want := "0A234BC789"; string(got) != want {
		t.Fatalf("patched = %q, want %q", got, want)
	}
}

func TestApplyRawOutboxPatchesRejectsBadRanges(t *testing.T) {
	raw := []byte("abcdef")
	cases := map[string][]RawOutboxPatch{
		"overlap":      {{Start: 0, End: 3}, {Start: 2, End: 4}},
		"out of range": {{Start: 4, End: 7}},
		"reversed":     {{Start: 3, End: 1}},
	}
	for name, patches := range cases {
		if _, err := ApplyRawOutboxPatches(raw, patches); err == nil {
			t.Fatalf("%s: expected error", name)
		}
	}
	if got, err := ApplyRawOutboxPatches(raw, nil); err != nil || string(got) != "abcdef" {
		t.Fatalf("no-patch copy = %q, %v", got, err)
	}
}

func TestWriteRawOutboxAtomicReplacesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "outbox.json")
	if err := os.WriteFile(path, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := writeRawOutboxAtomic(path, []byte(`{"entries":[]}`)); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"entries":[]}` {
		t.Fatalf("file = %s", got)
	}
}

func TestWriteRawOutboxAtomicRenameFailureKeepsOldFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "outbox.json")
	if err := os.WriteFile(path, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	original := renameOutbox
	renameOutbox = func(string, string) error { return errors.New("injected rename failure") }
	defer func() { renameOutbox = original }()

	if err := writeRawOutboxAtomic(path, []byte("new")); err == nil {
		t.Fatal("expected the injected rename failure")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "old" {
		t.Fatalf("old file was not preserved: %s", got)
	}
}

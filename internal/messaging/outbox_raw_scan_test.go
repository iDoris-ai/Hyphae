package messaging

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// legacyFixtureEntry returns the verbatim bytes of the single entry in a
// captured raw-outbox fixture.
func legacyFixtureEntry(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "raw-outbox", name))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct{ Entries []json.RawMessage }
	if err := json.Unmarshal(data, &doc); err != nil || len(doc.Entries) != 1 {
		t.Fatalf("fixture %s: %v (%d entries)", name, err, len(doc.Entries))
	}
	return doc.Entries[0]
}

func rawOutboxDoc(entries ...[]byte) []byte {
	return []byte("{ \"entries\" :\t[\n" + string(bytes.Join(entries, []byte(" ,\n"))) + "\n ] }\n")
}

func writeOutboxFile(t *testing.T, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "outbox.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	old := time.Unix(1_000_000_000, 0)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	return path
}

func requireOutboxUnchanged(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("outbox bytes changed:\n%s", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(time.Unix(1_000_000_000, 0)) {
		t.Fatalf("outbox mtime changed to %v", info.ModTime())
	}
}

func TestScanRawOutboxFileSpans(t *testing.T) {
	dm := legacyFixtureEntry(t, "legacy-dm-nil-relays.json")
	group := readF2BFixture(t, "p-o3-entry.json")
	group = bytes.TrimSpace(group)
	raw := rawOutboxDoc(dm, group)
	file, err := scanRawOutboxFile(raw)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(file.Entries) != 2 || raw[file.ArrayOpen-1] != '[' {
		t.Fatalf("entries=%d arrayOpen=%d", len(file.Entries), file.ArrayOpen)
	}
	for i, want := range [][]byte{dm, group} {
		entry := file.Entries[i]
		if got := raw[entry.Span.Start:entry.Span.End]; !bytes.Equal(got, want) {
			t.Fatalf("entry %d span mismatch:\n%s", i, got)
		}
		if entry.Parsed == nil || !bytes.Equal(entry.Parsed.Raw, want) {
			t.Fatalf("entry %d parse mismatch", i)
		}
	}
	values := file.Entries[1].Values
	for key, want := range map[string]string{"retry_count": "1", "relays": "null", "status": `"group_pending"`, "fanout_protocol": "1"} {
		span, ok := values[key]
		if !ok || string(raw[span.Start:span.End]) != want {
			t.Fatalf("%s span = %+v (%q)", key, span, raw[span.Start:span.End])
		}
	}
	result := values["failure_result"]
	if raw[result.Start] != '{' || raw[result.End-1] != '}' {
		t.Fatalf("failure_result span is not the object token: %q", raw[result.Start:result.End])
	}
}

func TestScanRawOutboxFileEmpty(t *testing.T) {
	raw := []byte(`{"entries":[ ]}`)
	file, err := scanRawOutboxFile(raw)
	if err != nil || len(file.Entries) != 0 || file.ArrayOpen != len(`{"entries":[`) {
		t.Fatalf("file=%+v err=%v", file, err)
	}
}

func TestScanRawOutboxFileRejects(t *testing.T) {
	dm := legacyFixtureEntry(t, "legacy-dm-nil-relays.json")
	group := legacyFixtureEntry(t, "legacy-165-group-nil-relays.json")
	sameQueue := bytes.Replace(dm, []byte(`"219bdbf9ed70b96893cc2382f365f24c"`), []byte(`"2fc72e7f8539e9eff9120c979198790b"`), 1)
	unknown := bytes.Replace(group, []byte(`"route"`), []byte(`"extra": 1, "route"`), 1)
	dmWithProtocol := bytes.Replace(dm, []byte(`"status": "pending"`), []byte(`"status": "pending", "fanout_protocol": 1`), 1)
	o3 := bytes.TrimSpace(readF2BFixture(t, "p-o3-entry.json"))
	resultElsewhere := bytes.Replace(o3, []byte(`"queue_id": "2fc72e7f8539e9eff9120c979198790b",
  "route"`), []byte(`"queue_id": "ffffffffffffffffffffffffffffffff",
  "route"`), 1)
	missing := bytes.TrimSpace(readF2BFixture(t, "p-missing-entry.json"))
	witnessMaxMismatch := bytes.Replace(missing, []byte(`"max_retries": 10,
  "last_attempt"`), []byte(`"max_retries": 11,
  "last_attempt"`), 1)
	if _, err := scanRawOutboxFile(rawOutboxDoc(dm, missing)); err != nil {
		t.Fatalf("P-MISSING entry should scan: %v", err)
	}
	for _, mutated := range [][]byte{sameQueue, unknown, dmWithProtocol, resultElsewhere, witnessMaxMismatch} {
		if bytes.Equal(mutated, dm) || bytes.Equal(mutated, group) || bytes.Equal(mutated, o3) || bytes.Equal(mutated, missing) {
			t.Fatal("fixture mutation did not apply")
		}
	}
	for name, raw := range map[string][]byte{
		"duplicate id":               rawOutboxDoc(group, group),
		"duplicate queue_id":         rawOutboxDoc(sameQueue, group),
		"unknown entry member":       rawOutboxDoc(dm, unknown),
		"DM group metadata":          rawOutboxDoc(dmWithProtocol),
		"result binds another queue": rawOutboxDoc(resultElsewhere),
		"witness max mismatch":       rawOutboxDoc(witnessMaxMismatch),
		"outer extra member":         []byte(`{"entries":[],"x":1}`),
		"entries null":               []byte(`{"entries":null}`),
		"trailing token":             append(rawOutboxDoc(dm), []byte(`{}`)...),
		"truncated":                  rawOutboxDoc(dm)[:40],
		"entry is not object":        []byte(`{"entries":[1]}`),
		"duplicate outer entry":      []byte(`{"entries":[],"entries":[]}`),
	} {
		if _, err := scanRawOutboxFile(raw); err == nil {
			t.Errorf("%s: expected scan error", name)
		}
	}
}

func TestCommitRawOutboxEditWritesScannedBytes(t *testing.T) {
	dm := legacyFixtureEntry(t, "legacy-dm-nil-relays.json")
	group := legacyFixtureEntry(t, "legacy-165-group-nil-relays.json")
	before := rawOutboxDoc(dm, group)
	path := writeOutboxFile(t, before)
	after := rawOutboxDoc(dm)
	err := commitRawOutboxEdit(path, func(file *rawOutboxFile) ([]byte, error) {
		if !bytes.Equal(file.Raw, before) || len(file.Entries) != 2 {
			t.Fatalf("edit saw a different snapshot")
		}
		return after, nil
	})
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, after) {
		t.Fatalf("file = %s", got)
	}
}

func TestCommitRawOutboxEditZeroWrite(t *testing.T) {
	dm := legacyFixtureEntry(t, "legacy-dm-nil-relays.json")
	before := rawOutboxDoc(dm)
	editErr := errors.New("refused")
	for name, edit := range map[string]func(*rawOutboxFile) ([]byte, error){
		"edit error":       func(*rawOutboxFile) ([]byte, error) { return nil, editErr },
		"no-op":            func(file *rawOutboxFile) ([]byte, error) { return bytes.Clone(file.Raw), nil },
		"invalid result":   func(*rawOutboxFile) ([]byte, error) { return []byte(`{"entries":[{}]}`), nil },
		"duplicate result": func(*rawOutboxFile) ([]byte, error) { return rawOutboxDoc(dm, dm), nil },
	} {
		path := writeOutboxFile(t, before)
		err := commitRawOutboxEdit(path, edit)
		if (name == "no-op") != (err == nil) {
			t.Errorf("%s: err = %v", name, err)
		}
		requireOutboxUnchanged(t, path, before)
	}
}

// TestCommitRawOutboxEditInPlacePatchPersists mirrors the O3 span-patch style
// edit: a same-length retry_count bump written directly into file.Raw instead
// of into a fresh buffer. The expected "after" bytes are derived independently
// of what edit returns, so this fails if commitRawOutboxEdit silently drops
// the write because file.Raw aliases the snapshot it compares against.
func TestCommitRawOutboxEditInPlacePatchPersists(t *testing.T) {
	dm := legacyFixtureEntry(t, "legacy-dm-nil-relays.json")
	before := rawOutboxDoc(dm)
	path := writeOutboxFile(t, before)

	idx := bytes.Index(before, []byte(`"retry_count": 0`))
	if idx < 0 {
		t.Fatal("fixture missing retry_count: 0")
	}
	digitIdx := idx + len(`"retry_count": `)
	wantAfter := bytes.Clone(before)
	wantAfter[digitIdx] = '1'

	err := commitRawOutboxEdit(path, func(file *rawOutboxFile) ([]byte, error) {
		span, ok := file.Entries[0].Values["retry_count"]
		if !ok || span.End-span.Start != 1 || file.Raw[span.Start] != '0' {
			t.Fatalf("retry_count span = %+v (%q)", span, file.Raw)
		}
		file.Raw[span.Start] = '1'
		return file.Raw, nil
	})
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, wantAfter) {
		t.Fatalf("disk = %s\nwant  = %s", got, wantAfter)
	}
}

// TestCommitRawOutboxEditInPlaceInvalidPatchRejected mirrors an in-place edit
// that corrupts the entry without changing its length. commitRawOutboxEdit
// must still run the post-edit rescan and refuse to write: the no-op check
// must not short-circuit validation just because edit mutated file.Raw (and
// therefore the snapshot it was read from) directly.
func TestCommitRawOutboxEditInPlaceInvalidPatchRejected(t *testing.T) {
	dm := legacyFixtureEntry(t, "legacy-dm-nil-relays.json")
	before := rawOutboxDoc(dm)
	path := writeOutboxFile(t, before)

	err := commitRawOutboxEdit(path, func(file *rawOutboxFile) ([]byte, error) {
		span, ok := file.Entries[0].Values["status"]
		if !ok || !bytes.Equal(file.Raw[span.Start:span.End], []byte(`"pending"`)) {
			t.Fatalf("status span = %+v (%q)", span, file.Raw)
		}
		file.Raw[span.Start+1] = 'P' // "pending" -> "Pending": same length, no longer a valid DM status
		return file.Raw, nil
	})
	if err == nil {
		t.Fatal("expected validation error for invalid in-place patch, got nil")
	}
	requireOutboxUnchanged(t, path, before)
}

func TestCommitRawOutboxEditRefusesInvalidSnapshot(t *testing.T) {
	before := []byte(`{"entries":[{"id":"x"}]}`)
	path := writeOutboxFile(t, before)
	called := false
	err := commitRawOutboxEdit(path, func(*rawOutboxFile) ([]byte, error) {
		called = true
		return []byte(`{"entries":[]}`), nil
	})
	if err == nil || called {
		t.Fatalf("err=%v called=%v", err, called)
	}
	requireOutboxUnchanged(t, path, before)
}

func TestCommitRawOutboxEditMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "outbox.json")
	err := commitRawOutboxEdit(path, func(*rawOutboxFile) ([]byte, error) { return []byte(`{"entries":[]}`), nil })
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err = %v", err)
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("missing outbox was created: %v", statErr)
	}
}

func TestWalkRawObjectRejectsNonObject(t *testing.T) {
	if err := walkRawObject([]byte(`[1]`), 0, nil); err == nil || !strings.Contains(err.Error(), "object") {
		t.Fatalf("err = %v", err)
	}
	if _, err := rawArraySpans([]byte(`{}`), rawSpan{0, 2}); err == nil {
		t.Fatal("expected array error")
	}
}

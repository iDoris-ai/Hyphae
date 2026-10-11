package messaging

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// RawOutboxPatch replaces one exact byte range [Start,End) of a raw outbox
// snapshot with Replacement.
type RawOutboxPatch struct {
	Start       int
	End         int
	Replacement []byte
}

// ApplyRawOutboxPatches builds a new slice from raw by substituting each
// patch's Replacement for its [Start,End) range and appending the gap bytes
// from raw. An out-of-bounds or overlapping range returns an error and no
// slice. The caller's raw argument is not written to.
func ApplyRawOutboxPatches(raw []byte, patches []RawOutboxPatch) ([]byte, error) {
	ordered := append([]RawOutboxPatch(nil), patches...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Start < ordered[j].Start })
	out := make([]byte, 0, len(raw))
	last := 0
	for _, patch := range ordered {
		if patch.Start < last || patch.End < patch.Start || patch.End > len(raw) {
			return nil, fmt.Errorf("raw patch range [%d,%d) is out of bounds or overlapping", patch.Start, patch.End)
		}
		out = append(out, raw[last:patch.Start]...)
		out = append(out, patch.Replacement...)
		last = patch.End
	}
	out = append(out, raw[last:]...)
	return out, nil
}

// syncOutboxDir is the post-rename directory fsync; tests replace it to
// provoke the commit-uncertain path.
var syncOutboxDir = func(dir *os.File) error { return dir.Sync() }

// MarkOutboxCommitUncertain wraps err so IsOutboxCommitUncertain reports it;
// for callers that stand in for the raw writer (test seams).
func MarkOutboxCommitUncertain(err error) error { return &outboxCommitUncertainError{err} }

// writeRawOutboxAtomic writes data through a sibling temp file (write → fsync →
// close), renames it over path, then fsyncs the directory. A step that fails
// after the rename returns a *outboxCommitUncertainError because the
// replacement may already be visible.
func writeRawOutboxAtomic(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".outbox-*.tmp")
	if err != nil {
		return fmt.Errorf("open temp outbox: %w", err)
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err := f.Chmod(0600); err != nil {
		_ = f.Close()
		return fmt.Errorf("chmod temp outbox: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("write temp outbox: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("fsync temp outbox: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close temp outbox: %w", err)
	}
	if err := renameOutbox(tmp, path); err != nil {
		return fmt.Errorf("rename outbox: %w", err)
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return &outboxCommitUncertainError{fmt.Errorf("open outbox directory after rename: %w", err)}
	}
	if err := syncOutboxDir(dir); err != nil {
		_ = dir.Close()
		return &outboxCommitUncertainError{fmt.Errorf("fsync outbox directory after rename: %w", err)}
	}
	if err := dir.Close(); err != nil {
		return &outboxCommitUncertainError{fmt.Errorf("close outbox directory after rename: %w", err)}
	}
	return nil
}

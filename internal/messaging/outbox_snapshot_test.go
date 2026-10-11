package messaging

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func stageOutboxFixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "raw-outbox", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	path := filepath.Join(t.TempDir(), "outbox.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatalf("stage fixture: %v", err)
	}
	return path
}

func TestReadRawOutboxEvidenceSnapshotIsReadOnly(t *testing.T) {
	path := stageOutboxFixture(t, "legacy-dm-nil-relays.json")
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := ReadRawOutboxEvidenceSnapshot(path)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Fatalf("outbox mtime changed: %v -> %v", before.ModTime(), after.ModTime())
	}
	current, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(current) != string(evidence.Raw) {
		t.Fatal("snapshot bytes differ from the file (normalization?)")
	}
	if len(evidence.Entries) == 0 {
		t.Fatal("expected at least one parsed entry")
	}
}

func TestReadRawOutboxEvidenceSnapshotMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "outbox.json")
	if _, err := ReadRawOutboxEvidenceSnapshot(path); err == nil {
		t.Fatal("expected an error for a missing outbox")
	}
}

func TestReadRawOutboxEvidenceSnapshotRejectsCorrupt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "outbox.json")
	if err := os.WriteFile(path, []byte(`{"entries":[{"id":"x"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRawOutboxEvidenceSnapshot(path); err == nil {
		t.Fatal("expected an error for a corrupt outbox")
	}
}

func TestReadRawOutboxEvidenceSnapshotBlocksOnSiblingLock(t *testing.T) {
	path := stageOutboxFixture(t, "legacy-165-group-nil-relays.json")
	release := make(chan struct{})
	held := make(chan struct{})
	go func() {
		_ = withOutboxLock(path, func() error {
			close(held)
			<-release
			return nil
		})
	}()
	<-held

	done := make(chan error, 1)
	go func() {
		_, err := ReadRawOutboxEvidenceSnapshot(path)
		done <- err
	}()
	select {
	case <-done:
		t.Fatal("reader did not block while the sibling lock was held")
	case <-time.After(200 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("reader failed after lock release: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reader did not proceed after the lock was released")
	}
}

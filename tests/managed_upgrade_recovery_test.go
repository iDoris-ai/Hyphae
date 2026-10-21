//go:build integration

package integration_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/iDoris-ai/hyphae/internal/deployment"
)

func TestManagedUpgradeRecovery(t *testing.T) {
	t.Run("refused restart leaves store bytes", func(t *testing.T) {
		w := newInstall(t)
		before := w.snap(t)
		_, err := deployment.ColdStart(w.manifest)
		requireClosed(t, err, "DEPLOYMENT_CLOSED")
		w.same(t, before)
	})

	t.Run("close before backup", func(t *testing.T) {
		w := newInstall(t)
		before := w.snap(t)
		err := deployment.BackupInstall(w.manifest, w.backup, nil)
		requireClosed(t, err, "DEPLOYMENT_CLOSED")
		if _, statErr := os.Lstat(w.backup); !os.IsNotExist(statErr) {
			t.Fatalf("backup created early: %v", statErr)
		}
		w.same(t, before)
		if err := deployment.CloseEntries(w.manifest, nil); err != nil {
			t.Fatal(err)
		}
		if err := deployment.BackupInstall(w.manifest, w.backup, nil); err != nil {
			t.Fatal(err)
		}
		m := w.parse(t)
		if m.State != "closed" {
			t.Fatalf("state %s", m.State)
		}
	})

	t.Run("running writer blocks upgrade", func(t *testing.T) {
		w := newInstall(t)
		before := w.snap(t)
		err := deployment.CloseEntries(w.manifest, []deployment.RunningWriter{{EntryID: "daemon", Alive: true}})
		requireClosed(t, err, "DEPLOYMENT_CLOSED")
		err = deployment.BackupInstall(w.manifest, w.backup, []deployment.RunningWriter{{EntryID: "tui", Alive: true}})
		requireClosed(t, err, "DEPLOYMENT_CLOSED")
		m := w.parse(t)
		if m.State != "active" || m.BinarySHA256 != w.oldSHA {
			t.Fatalf("pin moved %#v", m)
		}
		w.same(t, before)
	})

	t.Run("missing backup refuses migrate", func(t *testing.T) {
		w := newInstall(t)
		w.closeAndBackup(t)
		before := w.snap(t)
		if err := os.RemoveAll(w.backup); err != nil {
			t.Fatal(err)
		}
		err := deployment.StageMigrate(w.manifest, w.next, w.nextSHA)
		requireClosed(t, err, "DEPLOYMENT_CLOSED")
		m := w.parse(t)
		if m.BinaryPath != w.binary || m.BinarySHA256 != w.oldSHA {
			t.Fatalf("pin %s %s", m.BinaryPath, m.BinarySHA256)
		}
		w.same(t, before)
	})

	t.Run("drifted hash refuses cold start", func(t *testing.T) {
		w := newInstall(t)
		w.upgrade(t)
		before := w.snap(t)
		writeMode(t, w.next, []byte("pinned-hyphae-drift"), 0700)
		fifo := w.m.Stores.MessagesDB
		if err := os.Remove(fifo); err != nil {
			t.Fatal(err)
		}
		if err := syscall.Mkfifo(fifo, 0600); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() {
			_, err := deployment.ColdStart(w.manifest)
			done <- err
		}()
		select {
		case err := <-done:
			requireClosed(t, err, "HASH_MISMATCH")
		case <-time.After(2 * time.Second):
			t.Fatal("cold start hung on a store fifo")
		}
		for path, body := range before {
			if path == fifo {
				continue
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != string(body) {
				t.Fatalf("store %s changed", path)
			}
		}
		m := w.parse(t)
		if m.State == "active" {
			t.Fatal("drifted start activated")
		}
	})

	t.Run("interrupted migrate keeps previous pin", func(t *testing.T) {
		w := newInstall(t)
		w.closeAndBackup(t)
		before := w.snap(t)
		if err := deployment.StageMigrate(w.manifest, w.next, w.nextSHA); err != nil {
			t.Fatal(err)
		}
		m := w.parse(t)
		if m.BinaryPath != w.binary || m.BinarySHA256 != w.oldSHA {
			t.Fatalf("pin moved to %s %s", m.BinaryPath, m.BinarySHA256)
		}
		_, err := deployment.ColdStart(w.manifest)
		requireClosed(t, err, "DEPLOYMENT_CLOSED")
		w.same(t, before)
	})

	t.Run("second start uses the new hash", func(t *testing.T) {
		w := newInstall(t)
		w.upgrade(t)
		first, err := deployment.ColdStart(w.manifest)
		if err != nil {
			t.Fatal(err)
		}
		second, err := deployment.ColdStart(w.manifest)
		if err != nil {
			t.Fatal(err)
		}
		if first != w.next || second != w.next {
			t.Fatalf("starts %s %s", first, second)
		}
		m := w.parse(t)
		if m.BinarySHA256 != w.nextSHA || m.State != "active" {
			t.Fatalf("manifest %s %s", m.State, m.BinarySHA256)
		}
	})

	t.Run("rollback refuses a mismatched backup", func(t *testing.T) {
		w := newInstall(t)
		w.closeAndBackup(t)
		before := w.snap(t)
		backupDB := filepath.Join(w.backup, "messages_db")
		writeMode(t, backupDB, []byte("tampered-backup"), 0600)
		err := deployment.RollbackInstall(w.manifest)
		requireClosed(t, err, "HASH_MISMATCH")
		w.same(t, before)
		m := w.parse(t)
		if m.BinarySHA256 != w.oldSHA {
			t.Fatalf("pin %s", m.BinarySHA256)
		}
	})
}

type install struct {
	root, home, binary, next, manifest, backup string
	oldSHA, nextSHA                            string
	m                                          deployment.Manifest
}

func newInstall(t *testing.T) *install {
	t.Helper()
	raw := t.TempDir()
	root, err := filepath.EvalSymlinks(raw)
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "home")
	binDir := filepath.Join(root, "bin")
	if err := os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(binDir, 0700); err != nil {
		t.Fatal(err)
	}
	w := &install{
		root:     root,
		home:     home,
		binary:   filepath.Join(binDir, "hyphae"),
		next:     filepath.Join(binDir, "hyphae-next"),
		manifest: filepath.Join(root, "install.json"),
		backup:   filepath.Join(root, "backup"),
	}
	writeMode(t, w.binary, []byte("pinned-hyphae-v1"), 0700)
	writeMode(t, w.next, []byte("pinned-hyphae-v2"), 0700)
	w.oldSHA = fileSHA(t, w.binary)
	w.nextSHA = fileSHA(t, w.next)
	launcher := filepath.Join(binDir, "hyphae-managed")
	writeMode(t, launcher, []byte("launcher-v1"), 0700)
	entries := deployment.ManagedEntryInventory()
	stores, err := deployment.CanonicalStores(home)
	if err != nil {
		t.Fatal(err)
	}
	w.m = deployment.Manifest{
		SchemaVersion:   deployment.SupportedSchemaVersion,
		State:           "active",
		CanonicalHome:   home,
		ControlOwnerUID: os.Geteuid(),
		ExpectedModes:   deployment.ModeConfig{DirMode: 0700, FileMode: 0700},
		Stores:          stores,
		BinaryPath:      w.binary,
		BinarySHA256:    w.oldSHA,
		LauncherPath:    launcher,
		LauncherSHA256:  fileSHA(t, launcher),
		AllowedEntries:  entries,
		InventoryDigest: deployment.EntryInventoryDigest(entries),
	}
	body, err := json.MarshalIndent(&w.m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeMode(t, w.manifest, append(body, '\n'), 0600)
	for _, path := range []string{
		stores.OutboxJSON, stores.OutboxLock, stores.OutboxTempPrefix,
		stores.MessagesDB, stores.MessagesDBWAL, stores.MessagesDBSHM, stores.DaemonLock,
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		writeMode(t, path, []byte("sentinel:"+path), 0600)
	}
	return w
}

func (w *install) parse(t *testing.T) *deployment.Manifest {
	t.Helper()
	body, err := os.ReadFile(w.manifest)
	if err != nil {
		t.Fatal(err)
	}
	m, err := deployment.ParseManifestJSON(body)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func (w *install) snap(t *testing.T) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	for _, path := range []string{
		w.m.Stores.OutboxJSON, w.m.Stores.OutboxLock, w.m.Stores.OutboxTempPrefix,
		w.m.Stores.MessagesDB, w.m.Stores.MessagesDBWAL, w.m.Stores.MessagesDBSHM, w.m.Stores.DaemonLock,
	} {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		out[path] = body
	}
	return out
}

func (w *install) same(t *testing.T, before map[string][]byte) {
	t.Helper()
	for path, want := range before {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want) {
			t.Fatalf("store %s changed", path)
		}
	}
}

func (w *install) closeAndBackup(t *testing.T) {
	t.Helper()
	if err := deployment.CloseEntries(w.manifest, nil); err != nil {
		t.Fatal(err)
	}
	if err := deployment.BackupInstall(w.manifest, w.backup, nil); err != nil {
		t.Fatal(err)
	}
}

func (w *install) upgrade(t *testing.T) {
	t.Helper()
	w.closeAndBackup(t)
	if err := deployment.StageMigrate(w.manifest, w.next, w.nextSHA); err != nil {
		t.Fatal(err)
	}
	if err := deployment.CommitMigrate(w.manifest); err != nil {
		t.Fatal(err)
	}
}

func fileSHA(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

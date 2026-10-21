//go:build integration

package integration_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/iDoris-ai/hyphae/internal/deployment"
)

func TestManagedUpgradeRecovery(t *testing.T) {
	if os.Getenv("HYPHAE_V7B_CHILD") == "1" {
		planChild()
		return
	}
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

	t.Run("stale journal cannot roll current data back", func(t *testing.T) {
		w := newInstall(t)
		w.upgrade(t)
		if _, err := deployment.ColdStart(w.manifest); err != nil {
			t.Fatal(err)
		}
		const fresh = "NEW-DATA"
		writeMode(t, w.m.Stores.MessagesDB, []byte(fresh), 0600)
		m := w.parse(t)
		m.State = "closed"
		body, err := json.MarshalIndent(m, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		writeMode(t, w.manifest, append(body, '\n'), 0700)
		err = deployment.RollbackInstall(w.manifest)
		requireClosed(t, err, "DEPLOYMENT_CLOSED")
		if got := readBytes(t, w.m.Stores.MessagesDB); string(got) != fresh {
			t.Fatalf("live store %s", got)
		}
		if err = deployment.CloseEntries(w.manifest, nil); err != nil {
			t.Fatal(err)
		}
		j := w.journal(t)
		if j["Phase"] != "closed" || j["PreviousSHA256"] != w.nextSHA || j["BackupDir"] != "" {
			t.Fatalf("journal %#v", j)
		}
		err = deployment.RollbackInstall(w.manifest)
		requireClosed(t, err, "DEPLOYMENT_CLOSED")
		if got := readBytes(t, w.m.Stores.MessagesDB); string(got) != fresh {
			t.Fatalf("live store after close %s", got)
		}
		if err = deployment.CloseEntries(w.manifest, nil); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("interrupted rollback stays closed", func(t *testing.T) {
		w := newInstall(t)
		w.upgrade(t)
		writeMode(t, w.m.Stores.MessagesDB, []byte("MIGRATED"), 0600)
		block := w.m.Stores.MessagesDB + ".tmp"
		if err := os.Mkdir(block, 0700); err != nil {
			t.Fatal(err)
		}
		writeMode(t, filepath.Join(block, "held"), []byte("x"), 0600)
		err := deployment.RollbackInstall(w.manifest)
		requireClosed(t, err, "DEPLOYMENT_CLOSED")
		if w.journal(t)["Phase"] != "rolling-back" {
			t.Fatalf("phase %v", w.journal(t)["Phase"])
		}
		_, err = deployment.ColdStart(w.manifest)
		requireClosed(t, err, "DEPLOYMENT_CLOSED")
		if got := readBytes(t, w.m.Stores.MessagesDB); string(got) != "MIGRATED" {
			t.Fatalf("db %s", got)
		}
		if err = os.RemoveAll(block); err != nil {
			t.Fatal(err)
		}
		if err = deployment.RollbackInstall(w.manifest); err != nil {
			t.Fatal(err)
		}
		if got := readBytes(t, w.m.Stores.MessagesDB); string(got) != "sentinel:"+w.m.Stores.MessagesDB {
			t.Fatalf("restored %s", got)
		}
		m := w.parse(t)
		if m.BinarySHA256 != w.oldSHA || m.State != "closed" {
			t.Fatalf("pin %s %s", m.State, m.BinarySHA256)
		}
		_, err = deployment.ColdStart(w.manifest)
		requireClosed(t, err, "DEPLOYMENT_CLOSED")
	})

	t.Run("rollback hashes the previous binary first", func(t *testing.T) {
		w := newInstall(t)
		w.upgrade(t)
		writeMode(t, w.m.Stores.MessagesDB, []byte("MIGRATED"), 0600)
		writeMode(t, w.binary, []byte("old-binary-drift"), 0700)
		err := deployment.RollbackInstall(w.manifest)
		requireClosed(t, err, "HASH_MISMATCH")
		if got := readBytes(t, w.m.Stores.MessagesDB); string(got) != "MIGRATED" {
			t.Fatalf("db %s", got)
		}
		m := w.parse(t)
		if m.BinarySHA256 != w.nextSHA {
			t.Fatalf("pin %s", m.BinarySHA256)
		}
		if w.journal(t)["Phase"] != "migrated" {
			t.Fatalf("phase %v", w.journal(t)["Phase"])
		}
	})

	t.Run("corrupt journal is left in place", func(t *testing.T) {
		w := newInstall(t)
		w.closeAndBackup(t)
		journal := w.manifest + ".maintain.json"
		writeMode(t, journal, []byte("{"), 0600)
		before := w.snap(t)
		err := deployment.CloseEntries(w.manifest, nil)
		requireClosed(t, err, "DEPLOYMENT_CLOSED")
		if got := readBytes(t, journal); string(got) != "{" {
			t.Fatalf("journal %s", got)
		}
		w.same(t, before)
	})

	t.Run("missing prefix and optional stores still back up", func(t *testing.T) {
		w := newInstall(t)
		optional := []string{
			w.m.Stores.OutboxTempPrefix, w.m.Stores.OutboxLock,
			w.m.Stores.MessagesDBWAL, w.m.Stores.MessagesDBSHM, w.m.Stores.DaemonLock,
		}
		for _, path := range optional {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
		}
		w.closeAndBackup(t)
		for _, role := range []string{"outbox_temp_prefix", "outbox_lock", "messages_db_wal", "messages_db_shm", "daemon_lock"} {
			if _, err := os.Lstat(filepath.Join(w.backup, role)); !os.IsNotExist(err) {
				t.Fatalf("backup role %s: %v", role, err)
			}
		}
		sums, _ := w.journal(t)["StoreSHA256"].(map[string]any)
		if sums["outbox_temp_prefix"] != "-" || sums["messages_db_wal"] != "-" || sums["messages_db"] == "-" {
			t.Fatalf("sums %#v", sums)
		}
		writeMode(t, w.m.Stores.OutboxTempPrefix, []byte("phantom-prefix"), 0600)
		writeMode(t, w.m.Stores.MessagesDBWAL, []byte("phantom-wal"), 0600)
		if err := deployment.RollbackInstall(w.manifest); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Lstat(w.m.Stores.OutboxTempPrefix); !os.IsNotExist(err) {
			t.Fatalf("prefix left behind: %v", err)
		}
		if _, err := os.Lstat(w.m.Stores.MessagesDBWAL); !os.IsNotExist(err) {
			t.Fatalf("wal left behind: %v", err)
		}
		if got := readBytes(t, w.m.Stores.MessagesDB); string(got) != "sentinel:"+w.m.Stores.MessagesDB {
			t.Fatalf("db %s", got)
		}
	})

	t.Run("manifest mode survives cold start", func(t *testing.T) {
		w := newInstall(t)
		exe, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		if err = os.Remove(w.m.LauncherPath); err != nil {
			t.Fatal(err)
		}
		copyMode(t, exe, w.m.LauncherPath, 0700)
		w.m.LauncherSHA256 = fileSHA(t, w.m.LauncherPath)
		body, err := json.MarshalIndent(&w.m, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		writeMode(t, w.manifest, append(body, '\n'), 0700)
		if got := w.plan(t); got != w.binary {
			t.Fatalf("before %s", got)
		}
		w.upgrade(t)
		started, err := deployment.ColdStart(w.manifest)
		if err != nil {
			t.Fatal(err)
		}
		if started != w.next {
			t.Fatalf("start %s", started)
		}
		info, err := os.Stat(w.manifest)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0700 {
			t.Fatalf("mode %o", info.Mode().Perm())
		}
		if got := w.plan(t); got != w.next {
			t.Fatalf("after %s", got)
		}
	})

	t.Run("reused backup leaves an older wal behind", func(t *testing.T) {
		w := newInstall(t)
		wal := w.m.Stores.MessagesDBWAL
		writeMode(t, wal, []byte("ROUND1-WAL"), 0600)
		w.upgrade(t)
		if _, err := deployment.ColdStart(w.manifest); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(wal); err != nil {
			t.Fatal(err)
		}
		writeMode(t, w.m.Stores.MessagesDB, []byte("ROUND2-DB"), 0600)
		if err := deployment.CloseEntries(w.manifest, nil); err != nil {
			t.Fatal(err)
		}
		if err := deployment.BackupInstall(w.manifest, w.backup, nil); err != nil {
			t.Fatal(err)
		}
		sums, _ := w.journal(t)["StoreSHA256"].(map[string]any)
		if sums["messages_db_wal"] != "-" {
			t.Fatalf("wal sum %#v", sums["messages_db_wal"])
		}
		backed := filepath.Join(w.backup, "messages_db_wal")
		if _, err := os.Lstat(backed); !os.IsNotExist(err) {
			t.Fatalf("stale backup wal %v", err)
		}
		writeMode(t, backed, []byte("ROUND1-WAL"), 0600)
		if err := deployment.RollbackInstall(w.manifest); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Lstat(wal); !os.IsNotExist(err) {
			t.Fatalf("live wal %v", err)
		}
		if got := readBytes(t, w.m.Stores.MessagesDB); string(got) != "ROUND2-DB" {
			t.Fatalf("db %s", got)
		}
	})

	t.Run("a blocked journal write can be retried", func(t *testing.T) {
		w := newInstall(t)
		before := w.snap(t)
		block := w.manifest + ".maintain.json.tmp"
		if err := os.Mkdir(block, 0700); err != nil {
			t.Fatal(err)
		}
		writeMode(t, filepath.Join(block, "held"), []byte("x"), 0600)
		err := deployment.CloseEntries(w.manifest, nil)
		requireClosed(t, err, "DEPLOYMENT_CLOSED")
		if _, statErr := os.Lstat(w.manifest + ".maintain.json"); !os.IsNotExist(statErr) {
			t.Fatalf("journal %v", statErr)
		}
		if w.parse(t).State != "closed" {
			t.Fatalf("state %s", w.parse(t).State)
		}
		w.same(t, before)
		if err = os.RemoveAll(block); err != nil {
			t.Fatal(err)
		}
		if err = deployment.CloseEntries(w.manifest, nil); err != nil {
			t.Fatal(err)
		}
		j := w.journal(t)
		if j["Phase"] != "closed" || j["PreviousSHA256"] != w.oldSHA || j["BackupDir"] != "" {
			t.Fatalf("journal %#v", j)
		}
		_, err = deployment.ColdStart(w.manifest)
		requireClosed(t, err, "DEPLOYMENT_CLOSED")
		if err = deployment.CloseEntries(w.manifest, nil); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("close leaves a migrated journal in place", func(t *testing.T) {
		w := newInstall(t)
		w.upgrade(t)
		before := readBytes(t, w.manifest+".maintain.json")
		err := deployment.CloseEntries(w.manifest, nil)
		requireClosed(t, err, "DEPLOYMENT_CLOSED")
		if got := readBytes(t, w.manifest+".maintain.json"); string(got) != string(before) {
			t.Fatal("journal rewritten")
		}
	})

	t.Run("backup refuses a missing messages database", func(t *testing.T) {
		w := newInstall(t)
		if err := deployment.CloseEntries(w.manifest, nil); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(w.m.Stores.MessagesDB); err != nil {
			t.Fatal(err)
		}
		err := deployment.BackupInstall(w.manifest, w.backup, nil)
		requireClosed(t, err, "DEPLOYMENT_CLOSED")
		if _, statErr := os.Lstat(w.m.Stores.MessagesDB); !os.IsNotExist(statErr) {
			t.Fatalf("db %v", statErr)
		}
		sums, _ := w.journal(t)["StoreSHA256"].(map[string]any)
		if sums["messages_db"] == "-" {
			t.Fatalf("db recorded absent %#v", sums)
		}
	})

	t.Run("rollback reports a directory left in place of an absent file", func(t *testing.T) {
		w := newInstall(t)
		if err := os.Remove(w.m.Stores.MessagesDBWAL); err != nil {
			t.Fatal(err)
		}
		w.closeAndBackup(t)
		block := w.m.Stores.MessagesDBWAL
		if err := os.Mkdir(block, 0700); err != nil {
			t.Fatal(err)
		}
		writeMode(t, filepath.Join(block, "held"), []byte("x"), 0600)
		err := deployment.RollbackInstall(w.manifest)
		requireClosed(t, err, "DEPLOYMENT_CLOSED")
		if _, statErr := os.Lstat(filepath.Join(block, "held")); statErr != nil {
			t.Fatal(statErr)
		}
		if w.journal(t)["Phase"] != "rolling-back" {
			t.Fatalf("phase %v", w.journal(t)["Phase"])
		}
	})

	t.Run("backup refuses a symlink where a store file belongs", func(t *testing.T) {
		w := newInstall(t)
		lock := w.m.Stores.DaemonLock
		if err := os.Remove(lock); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(w.binary, lock); err != nil {
			t.Fatal(err)
		}
		if err := deployment.CloseEntries(w.manifest, nil); err != nil {
			t.Fatal(err)
		}
		err := deployment.BackupInstall(w.manifest, w.backup, nil)
		requireClosed(t, err, "DEPLOYMENT_CLOSED")
		got, statErr := os.Lstat(lock)
		if statErr != nil || got.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("symlink %v", statErr)
		}
		if w.journal(t)["Phase"] != "closed" {
			t.Fatalf("phase %v", w.journal(t)["Phase"])
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

func (w *install) journal(t *testing.T) map[string]any {
	t.Helper()
	var j map[string]any
	if err := json.Unmarshal(readBytes(t, w.manifest+".maintain.json"), &j); err != nil {
		t.Fatal(err)
	}
	return j
}

func readBytes(t *testing.T, path string) []byte {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func (w *install) plan(t *testing.T) string {
	t.Helper()
	env := make([]string, 0, 16)
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "HYPHAE_V7B_") {
			env = append(env, kv)
		}
	}
	env = append(env,
		"HYPHAE_V7B_CHILD=1",
		"HYPHAE_V7B_MANIFEST="+w.manifest,
		"HYPHAE_V7B_ENTRY=cli",
		"HYPHAE_V7B_SESSION=runtime",
	)
	cmd := exec.Command(w.m.LauncherPath, "-test.run=^TestManagedUpgradeRecovery$")
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("plan %v %s", err, out)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if line == w.binary || line == w.next {
			return line
		}
	}
	t.Fatalf("planned %q", out)
	return ""
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

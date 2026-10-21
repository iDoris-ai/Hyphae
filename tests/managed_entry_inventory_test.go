//go:build integration

package integration_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/iDoris-ai/hyphae/internal/deployment"
)

func TestManagedEntryInventory(t *testing.T) {
	if os.Getenv("HYPHAE_V7B_CHILD") == "1" {
		planChild()
		return
	}

	t.Run("registered classes", func(t *testing.T) {
		got := map[string]string{}
		for _, entry := range deployment.ManagedEntries() {
			if _, dup := got[entry.CommandClass]; dup {
				t.Fatalf("duplicate class %s", entry.CommandClass)
			}
			got[entry.CommandClass] = entry.ID
		}
		for _, class := range []string{"dm", "group-store", "group-write", "maintenance", "probe"} {
			if got[class] == "" {
				t.Fatalf("missing class %s in %v", class, got)
			}
		}
		if len(got) != 5 {
			t.Fatalf("classes %v", got)
		}
	})

	t.Run("daemon and tui ids", func(t *testing.T) {
		byID := map[string]deployment.ManagedEntry{}
		for _, entry := range deployment.ManagedEntries() {
			byID[entry.ID] = entry
		}
		if byID["daemon"].CommandClass != "group-store" {
			t.Fatalf("daemon %+v", byID["daemon"])
		}
		if byID["tui"].CommandClass != "group-write" {
			t.Fatalf("tui %+v", byID["tui"])
		}
		if byID["cli"].CommandClass != "dm" || len(byID["cli"].Stores) == 0 {
			t.Fatalf("cli %+v", byID["cli"])
		}
		if len(byID["probe"].Stores) != 0 {
			t.Fatalf("probe stores %+v", byID["probe"].Stores)
		}
		covered := map[string]struct{}{}
		for _, entry := range byID {
			for _, role := range entry.Stores {
				covered[role] = struct{}{}
			}
		}
		for _, role := range []string{
			"outbox_json", "outbox_lock", "outbox_temp_prefix",
			"messages_db", "messages_db_wal", "messages_db_shm", "daemon_lock",
		} {
			if _, ok := covered[role]; !ok {
				t.Fatalf("unbound role %s", role)
			}
		}
	})

	t.Run("dm entry plans the pinned binary", func(t *testing.T) {
		w := newWorld(t)
		dm := findClass(t, "dm")
		stdout, stderr, code := w.execPlan(t, dm.ID, deployment.SessionModeRuntime)
		if code != 0 {
			t.Fatalf("exit %d stderr %s", code, stderr)
		}
		if !planLine(stdout, w.binary) {
			t.Fatalf("planned %q want %s", stdout, w.binary)
		}
		assertNoStores(t, w.home)
	})

	t.Run("unknown entry before store open", func(t *testing.T) {
		w := newWorld(t)
		store := filepath.Join(w.home, ".hyphae", "messages.db")
		_, err := deployment.PlanManagedEntry(deployment.LaunchRequest{
			ManifestPath: store,
			EntryID:      "nope",
			Session:      deployment.SessionModeRuntime,
			Env:          []string{},
		})
		requireClosed(t, err, "UNKNOWN_ENTRY")
		if _, statErr := os.Lstat(store); !os.IsNotExist(statErr) {
			t.Fatalf("store path stat %v", statErr)
		}
		assertNoStores(t, w.home)
	})

	t.Run("closed manifest before store open", func(t *testing.T) {
		w := newWorld(t)
		w.m.State = "closed"
		w.save(t)
		if err := os.Remove(w.binary); err != nil {
			t.Fatal(err)
		}
		_, err := w.plan(t, findClass(t, "dm").ID, deployment.SessionModeRuntime, nil)
		requireClosed(t, err, "DEPLOYMENT_CLOSED")
		if _, statErr := os.Lstat(w.binary); !os.IsNotExist(statErr) {
			t.Fatalf("binary reappeared: %v", statErr)
		}
		assertNoStores(t, w.home)
	})

	t.Run("home override before store open", func(t *testing.T) {
		w := newWorld(t)
		if err := os.Remove(w.binary); err != nil {
			t.Fatal(err)
		}
		_, err := w.plan(t, findClass(t, "dm").ID, deployment.SessionModeRuntime, []string{"HOME=/not/canonical"})
		requireClosed(t, err, "FOREIGN_HOME")
		if _, statErr := os.Lstat(w.binary); !os.IsNotExist(statErr) {
			t.Fatalf("binary reappeared: %v", statErr)
		}
		assertNoStores(t, w.home)
	})

	t.Run("hash mismatch before store open", func(t *testing.T) {
		w := newWorld(t)
		writeMode(t, w.binary, []byte("pinned-hyphae-v2"), 0700)
		_, stderr, code := w.execPlan(t, findClass(t, "dm").ID, deployment.SessionModeRuntime)
		if code != 4 || !strings.Contains(stderr, "(HASH_MISMATCH)") {
			t.Fatalf("exit %d stderr %s", code, stderr)
		}
		assertNoStores(t, w.home)
	})

	t.Run("digest comes from the entries", func(t *testing.T) {
		entries := deployment.ManagedEntryInventory()
		sum := deployment.EntryInventoryDigest(entries)
		entries["cli"] = deployment.EntryDef{ID: "cli", CommandClass: "probe"}
		if deployment.EntryInventoryDigest(entries) == sum {
			t.Fatal("digest ignored the command class")
		}
		w := newWorld(t)
		w.m.InventoryDigest = strings.Repeat("cd", 32)
		if w.m.InventoryDigest == sum {
			t.Fatal("replacement digest collided")
		}
		w.save(t)
		_, stderr, code := w.execPlan(t, findClass(t, "dm").ID, deployment.SessionModeRuntime)
		if code != 4 || !strings.Contains(stderr, "(DRIFT)") {
			t.Fatalf("exit %d stderr %s", code, stderr)
		}
		assertNoStores(t, w.home)
	})

	t.Run("unregistered ids are refused", func(t *testing.T) {
		w := newWorld(t)
		entries := map[string]deployment.EntryDef{}
		for _, class := range []string{"dm", "group-store", "group-write", "maintenance", "probe"} {
			entries[class] = deployment.EntryDef{ID: class, CommandClass: class}
		}
		w.m.AllowedEntries = entries
		w.m.InventoryDigest = deployment.EntryInventoryDigest(entries)
		w.save(t)
		if err := os.Remove(w.binary); err != nil {
			t.Fatal(err)
		}
		_, err := w.plan(t, findClass(t, "dm").ID, deployment.SessionModeRuntime, nil)
		requireClosed(t, err, "DRIFT")
		if _, statErr := os.Lstat(w.binary); !os.IsNotExist(statErr) {
			t.Fatalf("binary reappeared: %v", statErr)
		}
		assertNoStores(t, w.home)
	})

	t.Run("extra entry is refused", func(t *testing.T) {
		w := newWorld(t)
		entries := deployment.ManagedEntryInventory()
		entries["extra"] = deployment.EntryDef{ID: "extra", CommandClass: "dm"}
		w.m.AllowedEntries = entries
		w.m.InventoryDigest = deployment.EntryInventoryDigest(entries)
		w.save(t)
		if err := os.Remove(w.binary); err != nil {
			t.Fatal(err)
		}
		_, err := w.plan(t, findClass(t, "dm").ID, deployment.SessionModeRuntime, nil)
		requireClosed(t, err, "DRIFT")
		if _, statErr := os.Lstat(w.binary); !os.IsNotExist(statErr) {
			t.Fatalf("binary reappeared: %v", statErr)
		}
		assertNoStores(t, w.home)
	})

	t.Run("fifo manifest is refused", func(t *testing.T) {
		w := newWorld(t)
		fifo := filepath.Join(w.root, "manifest.fifo")
		if err := syscall.Mkfifo(fifo, 0600); err != nil {
			t.Fatal(err)
		}
		entry := findClass(t, "dm")
		done := make(chan error, 1)
		go func() {
			_, err := deployment.PlanManagedEntry(deployment.LaunchRequest{
				ManifestPath: fifo,
				EntryID:      entry.ID,
				Session:      deployment.SessionModeRuntime,
				Env:          []string{},
			})
			done <- err
		}()
		select {
		case err := <-done:
			requireClosed(t, err, "PATH_UNSAFE")
		case <-time.After(2 * time.Second):
			t.Fatal("fifo plan hung")
		}
		assertNoStores(t, w.home)
	})

	t.Run("non-canonical store is refused", func(t *testing.T) {
		w := newWorld(t)
		w.m.Stores.MessagesDB = filepath.Join(w.home, ".hyphae", "other.db")
		w.save(t)
		_, err := w.plan(t, "daemon", deployment.SessionModeRuntime, nil)
		requireClosed(t, err, "PATH_ESCAPED")
		assertNoStores(t, w.home)
	})
}

func planChild() {
	req := deployment.LaunchRequest{
		ManifestPath: os.Getenv("HYPHAE_V7B_MANIFEST"),
		EntryID:      os.Getenv("HYPHAE_V7B_ENTRY"),
		Session:      deployment.SessionMode(os.Getenv("HYPHAE_V7B_SESSION")),
		Env:          []string{},
	}
	path, err := deployment.PlanManagedEntry(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(planExit(err))
	}
	fmt.Println(path)
}

func planLine(stdout, path string) bool {
	for _, line := range strings.Split(stdout, "\n") {
		if line == path {
			return true
		}
	}
	return false
}

func planExit(err error) int {
	var closed *deployment.DeploymentClosedError
	if errors.As(err, &closed) && closed != nil {
		return closed.ExitCode()
	}
	return 1
}

type world struct {
	root, home, binary, launcher, manifest string
	m                                      deployment.Manifest
}

func newWorld(t *testing.T) *world {
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
	w := &world{
		root:     root,
		home:     home,
		binary:   filepath.Join(binDir, "hyphae"),
		launcher: filepath.Join(binDir, "hyphae-managed"),
		manifest: filepath.Join(root, "install.json"),
	}
	writeMode(t, w.binary, []byte("pinned-hyphae"), 0700)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	copyMode(t, exe, w.launcher, 0700)
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
		BinarySHA256:    fileSum(t, w.binary),
		LauncherPath:    w.launcher,
		LauncherSHA256:  fileSum(t, w.launcher),
		AllowedEntries:  entries,
		InventoryDigest: deployment.EntryInventoryDigest(entries),
	}
	w.save(t)
	return w
}

func (w *world) save(t *testing.T) {
	t.Helper()
	body, err := json.MarshalIndent(&w.m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeMode(t, w.manifest, append(body, '\n'), 0700)
	if err := os.Chmod(w.home, 0700); err != nil {
		t.Fatal(err)
	}
}

func (w *world) plan(t *testing.T, entry string, session deployment.SessionMode, env []string) (string, error) {
	t.Helper()
	if env == nil {
		env = []string{}
	}
	return deployment.PlanManagedEntry(deployment.LaunchRequest{
		ManifestPath: w.manifest,
		EntryID:      entry,
		Session:      session,
		Env:          env,
	})
}

func (w *world) execPlan(t *testing.T, entry string, session deployment.SessionMode) (string, string, int) {
	t.Helper()
	env := make([]string, 0, 8)
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "HYPHAE_V7B_") {
			continue
		}
		env = append(env, kv)
	}
	env = append(env,
		"HYPHAE_V7B_CHILD=1",
		"HYPHAE_V7B_MANIFEST="+w.manifest,
		"HYPHAE_V7B_ENTRY="+entry,
		"HYPHAE_V7B_SESSION="+string(session),
	)
	cmd := exec.Command(w.launcher, os.Args[1:]...)
	cmd.Dir = w.root
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatal(err)
		}
		code = exit.ExitCode()
	}
	return stdout.String(), stderr.String(), code
}

func findClass(t *testing.T, class string) deployment.ManagedEntry {
	t.Helper()
	var found deployment.ManagedEntry
	n := 0
	for _, entry := range deployment.ManagedEntries() {
		if entry.CommandClass == class {
			found = entry
			n++
		}
	}
	if n != 1 {
		t.Fatalf("class %s count %d", class, n)
	}
	return found
}

func requireClosed(t *testing.T, err error, code string) {
	t.Helper()
	var closed *deployment.DeploymentClosedError
	if !errors.As(err, &closed) || closed == nil || closed.Code != code {
		t.Fatalf("got %v, want %s", err, code)
	}
}

func assertNoStores(t *testing.T, home string) {
	t.Helper()
	_, err := os.Lstat(filepath.Join(home, ".hyphae"))
	if !os.IsNotExist(err) {
		t.Fatalf(".hyphae stat %v", err)
	}
}

func writeMode(t *testing.T, path string, body []byte, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, body, mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func copyMode(t *testing.T, src, dst string, mode os.FileMode) {
	t.Helper()
	in, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dst, mode); err != nil {
		t.Fatal(err)
	}
}

func fileSum(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

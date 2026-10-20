//go:build unix

package deployment

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManagedLauncher(t *testing.T) {
	t.Run("inventory digest is order independent", func(t *testing.T) {
		a := map[string]EntryDef{
			"dm":    {ID: "dm", CommandClass: "dm"},
			"probe": {ID: "probe", CommandClass: "probe"},
		}
		b := map[string]EntryDef{
			"probe": {ID: "probe", CommandClass: "probe"},
			"dm":    {ID: "dm", CommandClass: "dm"},
		}
		if EntryInventoryDigest(a) != EntryInventoryDigest(b) {
			t.Fatal("digest depends on map order")
		}
		a["dm"] = EntryDef{ID: "dm", CommandClass: "probe"}
		if EntryInventoryDigest(a) == EntryInventoryDigest(b) {
			t.Fatal("digest ignored the command class")
		}
	})

	t.Run("non-pinned exec", func(t *testing.T) {
		for _, tc := range []struct {
			argv0 string
			argv  []string
		}{
			{"hyphae", []string{"hyphae"}},
			{"./hyphae", []string{"./hyphae"}},
			{"/pinned/hyphae", []string{"other"}},
			{"/pinned/hyphae", nil},
		} {
			err := execBinary(tc.argv0, tc.argv, nil)
			requireCode(t, err, "PATH_UNSAFE", "non-pinned")
		}
	})

	f := newFixture(t)
	decoy := filepath.Join(f.root, "decoy")
	if err := os.Mkdir(decoy, 0700); err != nil {
		t.Fatal(err)
	}
	writeMode(t, filepath.Join(decoy, "hyphae"), []byte("decoy-binary"), 0700)

	t.Run("pinned exec ignores PATH", func(t *testing.T) {
		var opened []string
		argv, env, err := f.call(t, callOpt{
			args:   []string{"msg", "send", "/tmp/note"},
			env:    []string{"PATH=" + decoy, "HOME=" + f.home, "HYPHAE_ENTRY_ID=dm", "HYPHAE_SESSION_MODE=runtime", "OTHER=1"},
			opened: &opened,
		})
		if err != nil {
			t.Fatal(err)
		}
		want := []string{f.binary, "msg", "send", "/tmp/note"}
		if strings.Join(argv, "\x00") != strings.Join(want, "\x00") {
			t.Fatalf("argv %q", argv)
		}
		if argv[0] == filepath.Join(decoy, "hyphae") {
			t.Fatal("exec used PATH")
		}
		requireEnv(t, env, "HOME", f.home)
		requireEnv(t, env, "HYPHAE_ENTRY_ID", "dm")
		requireEnv(t, env, "HYPHAE_SESSION_MODE", "runtime")
		requireEnv(t, env, "PATH", decoy)
		requireEnv(t, env, "OTHER", "1")
		assertOpenedOnly(t, opened, f.manifest, f.launcher, f.binary)
		for _, path := range []string{f.manifest, f.launcher, f.binary} {
			if !containsPath(opened, path) {
				t.Fatalf("did not open %s in %v", path, opened)
			}
		}
	})

	t.Run("probe session", func(t *testing.T) {
		_, env, err := f.call(t, callOpt{entry: "probe", session: SessionModeProbe})
		if err != nil {
			t.Fatal(err)
		}
		requireEnv(t, env, "HYPHAE_ENTRY_ID", "probe")
		requireEnv(t, env, "HYPHAE_SESSION_MODE", "probe")
	})

	t.Run("binary hash mismatch", func(t *testing.T) {
		g := newFixture(t)
		writeMode(t, g.binary, []byte("binary-v2"), 0700)
		_, _, err := g.call(t, callOpt{})
		requireCode(t, err, "HASH_MISMATCH", "hash mismatch")
	})

	t.Run("launcher hash mismatch", func(t *testing.T) {
		g := newFixture(t)
		writeMode(t, g.launcher, []byte("launcher-v2"), 0700)
		_, _, err := g.call(t, callOpt{})
		requireCode(t, err, "HASH_MISMATCH", "hash mismatch")
	})

	t.Run("uppercase hash", func(t *testing.T) {
		g := newFixture(t)
		g.m.BinarySHA256 = strings.ToUpper(g.m.BinarySHA256)
		g.save(t)
		_, _, err := g.call(t, callOpt{})
		requireCode(t, err, "HASH_MISMATCH", "hash mismatch")
	})

	t.Run("closed before binary open", func(t *testing.T) {
		g := newFixture(t)
		g.m.State = "closed"
		g.save(t)
		if err := os.Remove(g.binary); err != nil {
			t.Fatal(err)
		}
		var opened []string
		_, _, err := g.call(t, callOpt{opened: &opened})
		requireCode(t, err, "DEPLOYMENT_CLOSED", "not active")
		assertOpenedOnly(t, opened, g.manifest)
	})

	t.Run("home override", func(t *testing.T) {
		var opened []string
		_, _, err := f.call(t, callOpt{env: []string{"HOME=/not/canonical"}, opened: &opened})
		requireCode(t, err, "FOREIGN_HOME", "HOME override")
		assertOpenedOnly(t, opened, f.manifest)
	})

	t.Run("duplicate home", func(t *testing.T) {
		_, _, err := f.call(t, callOpt{env: []string{"HOME=" + f.home, "HOME=" + f.home}})
		requireCode(t, err, "FOREIGN_HOME", "HOME override")
	})

	t.Run("nil env uses process HOME", func(t *testing.T) {
		t.Setenv("HOME", "/not/canonical")
		_, _, err := f.call(t, callOpt{envNil: true})
		requireCode(t, err, "FOREIGN_HOME", "HOME override")
	})

	t.Run("inherited legacy override", func(t *testing.T) {
		t.Setenv("HYPHAE_BINARY", "/old/hyphae")
		var opened []string
		_, _, err := f.call(t, callOpt{envNil: true, opened: &opened})
		requireCode(t, err, "DEPLOYMENT_CLOSED", "legacy version request")
		if len(opened) != 0 {
			t.Fatalf("opened %v", opened)
		}
	})

	t.Run("legacy args", func(t *testing.T) {
		for _, arg := range []string{"--binary", "--binary=/old", "--exec", "--exec=/old", "--legacy", "--legacy=165"} {
			var opened []string
			_, _, err := f.call(t, callOpt{args: []string{arg}, opened: &opened})
			requireCode(t, err, "DEPLOYMENT_CLOSED", "legacy version request")
			if len(opened) != 0 {
				t.Fatalf("arg %s opened %v", arg, opened)
			}
		}
	})

	t.Run("entry override", func(t *testing.T) {
		_, _, err := f.call(t, callOpt{env: []string{"HYPHAE_ENTRY_ID=probe"}})
		requireCode(t, err, "DEPLOYMENT_CLOSED", "entry override")
	})

	t.Run("session mismatch", func(t *testing.T) {
		_, _, err := f.call(t, callOpt{entry: "maintenance", session: SessionModeRuntime})
		requireCode(t, err, "MODE_MISMATCH", "incompatible")
	})

	t.Run("unknown entry", func(t *testing.T) {
		_, _, err := f.call(t, callOpt{entry: "nope"})
		requireCode(t, err, "UNKNOWN_ENTRY", "unknown")
	})

	t.Run("digest drift", func(t *testing.T) {
		g := newFixture(t)
		g.m.InventoryDigest = strings.Repeat("ab", 32)
		g.save(t)
		_, _, err := g.call(t, callOpt{})
		requireCode(t, err, "DRIFT", "digest")
	})

	t.Run("symlink binary", func(t *testing.T) {
		g := newFixture(t)
		moved := filepath.Join(g.root, "moved-hyphae")
		if err := os.Rename(g.binary, moved); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(moved, g.binary); err != nil {
			t.Fatal(err)
		}
		_, _, err := g.call(t, callOpt{})
		requireCode(t, err, "PATH_UNSAFE", "alias")
	})

	t.Run("hard link", func(t *testing.T) {
		g := newFixture(t)
		if err := os.Link(g.binary, filepath.Join(g.root, "hyphae-link")); err != nil {
			t.Fatal(err)
		}
		_, _, err := g.call(t, callOpt{})
		requireCode(t, err, "PATH_UNSAFE", "hard link")
	})

	t.Run("wrong owner", func(t *testing.T) {
		g := newFixture(t)
		g.m.ControlOwnerUID = g.m.ControlOwnerUID + 1
		g.save(t)
		_, _, err := g.call(t, callOpt{})
		requireCode(t, err, "WRONG_OWNER", "process uid")
	})

	t.Run("binary mode", func(t *testing.T) {
		g := newFixture(t)
		if err := os.Chmod(g.binary, 0755); err != nil {
			t.Fatal(err)
		}
		_, _, err := g.call(t, callOpt{})
		requireCode(t, err, "WRONG_MODE", "permission mode")
	})

	t.Run("manifest mode", func(t *testing.T) {
		g := newFixture(t)
		if err := os.Chmod(g.manifest, 0644); err != nil {
			t.Fatal(err)
		}
		_, _, err := g.call(t, callOpt{})
		requireCode(t, err, "WRONG_MODE", "permission mode")
	})

	t.Run("launcher mode", func(t *testing.T) {
		g := newFixture(t)
		if err := os.Chmod(g.launcher, 0755); err != nil {
			t.Fatal(err)
		}
		_, _, err := g.call(t, callOpt{})
		requireCode(t, err, "WRONG_MODE", "permission mode")
	})

	t.Run("home mode", func(t *testing.T) {
		g := newFixture(t)
		if err := os.Chmod(g.home, 0755); err != nil {
			t.Fatal(err)
		}
		_, _, err := g.call(t, callOpt{})
		requireCode(t, err, "WRONG_MODE", "permission mode")
	})

	t.Run("setuid binary", func(t *testing.T) {
		g := newFixture(t)
		if err := os.Chmod(g.binary, os.ModeSetuid|0700); err != nil {
			t.Fatal(err)
		}
		info, err := os.Lstat(g.binary)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode()&os.ModeSetuid == 0 {
			t.Skip("host did not keep setuid")
		}
		_, _, err = g.call(t, callOpt{})
		requireCode(t, err, "WRONG_MODE", "special mode")
	})

	t.Run("store path is not opened", func(t *testing.T) {
		g := newFixture(t)
		g.m.BinaryPath = g.m.Stores.MessagesDB
		g.save(t)
		var opened []string
		_, _, err := g.call(t, callOpt{opened: &opened})
		requireCode(t, err, "PATH_UNSAFE", "store path")
		assertOpenedOnly(t, opened, g.manifest, g.launcher)
		if containsPath(opened, g.m.Stores.MessagesDB) {
			t.Fatal("opened a store path")
		}
	})

	t.Run("relative manifest", func(t *testing.T) {
		err := launch(LaunchRequest{ManifestPath: "install.json", EntryID: "dm", Session: SessionModeRuntime, Env: []string{}}, launchEnv{
			executable: func() (string, error) { return f.launcher, nil },
			execFn: func(string, []string, []string) error {
				t.Fatal("exec called")
				return nil
			},
			open: func(string) (*os.File, error) {
				t.Fatal("open called")
				return nil, errors.New("open")
			},
		})
		requireCode(t, err, "PATH_UNSAFE", "unsafe path")
	})

	t.Run("exec failure stays closed", func(t *testing.T) {
		_, _, err := f.call(t, callOpt{execFn: func(string, []string, []string) error {
			return errors.New("boom")
		}})
		requireCode(t, err, "DEPLOYMENT_CLOSED", "exec failed")
	})

	t.Run("unregistered launcher", func(t *testing.T) {
		var opened []string
		_, _, err := f.call(t, callOpt{self: f.binary, opened: &opened})
		requireCode(t, err, "PATH_UNSAFE", "registered launcher")
		assertOpenedOnly(t, opened, f.manifest)
	})
}

type fixture struct {
	root, home, binary, launcher, manifest string
	m                                      Manifest
}

type callOpt struct {
	entry   string
	session SessionMode
	args    []string
	env     []string
	envNil  bool
	self    string
	execFn  func(string, []string, []string) error
	opened  *[]string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	rawRoot := t.TempDir()
	root, err := filepath.EvalSymlinks(rawRoot)
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "home")
	if err := os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	binDir := filepath.Join(root, "bin")
	if err := os.Mkdir(binDir, 0700); err != nil {
		t.Fatal(err)
	}
	f := &fixture{
		root:     root,
		home:     home,
		binary:   filepath.Join(binDir, "hyphae"),
		launcher: filepath.Join(binDir, "hyphae-managed"),
		manifest: filepath.Join(root, "install.json"),
	}
	writeMode(t, f.binary, []byte("binary-v1"), 0700)
	writeMode(t, f.launcher, []byte("launcher-v1"), 0700)
	entries := map[string]EntryDef{}
	for _, class := range RequiredClasses {
		entries[class] = EntryDef{ID: class, CommandClass: class}
	}
	f.m = Manifest{
		SchemaVersion:   SupportedSchemaVersion,
		State:           "active",
		CanonicalHome:   home,
		ControlOwnerUID: os.Geteuid(),
		ExpectedModes:   ModeConfig{DirMode: 0700, FileMode: 0700},
		Stores:          storePaths(home),
		BinaryPath:      f.binary,
		BinarySHA256:    sumFile(t, f.binary),
		LauncherPath:    f.launcher,
		LauncherSHA256:  sumFile(t, f.launcher),
		AllowedEntries:  entries,
		InventoryDigest: EntryInventoryDigest(entries),
	}
	f.save(t)
	f.seedStores(t)
	return f
}

func storePaths(home string) StorePaths {
	dir := filepath.Join(home, ".hyphae")
	return StorePaths{
		OutboxJSON:       filepath.Join(dir, "outbox.json"),
		OutboxLock:       filepath.Join(dir, "outbox.json.lock"),
		OutboxTempPrefix: filepath.Join(dir, ".outbox-"),
		MessagesDB:       filepath.Join(dir, "messages.db"),
		MessagesDBWAL:    filepath.Join(dir, "messages.db-wal"),
		MessagesDBSHM:    filepath.Join(dir, "messages.db-shm"),
		DaemonLock:       filepath.Join(dir, "daemon.lock"),
	}
}

func (f *fixture) save(t *testing.T) {
	t.Helper()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(&f.m); err != nil {
		t.Fatal(err)
	}
	writeMode(t, f.manifest, buf.Bytes(), 0700)
}

func (f *fixture) seedStores(t *testing.T) {
	t.Helper()
	for _, path := range f.m.Stores.all() {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		writeMode(t, path, []byte("sentinel:"+path), 0600)
	}
	if err := os.Chmod(f.home, 0700); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) assertStores(t *testing.T) {
	t.Helper()
	for _, path := range f.m.Stores.all() {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != "sentinel:"+path {
			t.Fatalf("store %s changed", path)
		}
	}
}

func (f *fixture) call(t *testing.T, opt callOpt) ([]string, []string, error) {
	t.Helper()
	if opt.entry == "" {
		opt.entry = "dm"
	}
	if opt.session == "" {
		opt.session = SessionModeRuntime
	}
	if opt.self == "" {
		opt.self = f.launcher
	}
	var argv, envv []string
	execFn := opt.execFn
	if execFn == nil {
		execFn = func(argv0 string, got []string, envIn []string) error {
			if argv0 != f.m.BinaryPath || len(got) == 0 || got[0] != argv0 {
				t.Fatalf("exec %s %v", argv0, got)
			}
			argv = append([]string(nil), got...)
			envv = append([]string(nil), envIn...)
			return nil
		}
	}
	open := openNoFollowOS
	if opt.opened != nil {
		open = func(path string) (*os.File, error) {
			*opt.opened = append(*opt.opened, path)
			return openNoFollowOS(path)
		}
	}
	env := opt.env
	if !opt.envNil && env == nil {
		env = []string{}
	}
	defer f.assertStores(t)
	err := launch(LaunchRequest{
		ManifestPath: f.manifest,
		EntryID:      opt.entry,
		Session:      opt.session,
		Args:         opt.args,
		Env:          env,
	}, launchEnv{
		executable: func() (string, error) { return opt.self, nil },
		execFn:     execFn,
		open:       open,
	})
	return argv, envv, err
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

func sumFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func requireCode(t *testing.T, err error, code, sub string) {
	t.Helper()
	var closed *DeploymentClosedError
	if !errors.As(err, &closed) {
		t.Fatalf("got %v", err)
	}
	if closed.Code != code || !strings.Contains(closed.Reason, sub) {
		t.Fatalf("got %s: %s", closed.Code, closed.Reason)
	}
}

func requireEnv(t *testing.T, env []string, key, val string) {
	t.Helper()
	var got []string
	for _, kv := range env {
		k, v, ok := strings.Cut(kv, "=")
		if ok && k == key {
			got = append(got, v)
		}
	}
	if len(got) != 1 || got[0] != val {
		t.Fatalf("%s = %q, env %q", key, got, env)
	}
}

func assertOpenedOnly(t *testing.T, opened []string, allow ...string) {
	t.Helper()
	ok := map[string]bool{}
	for _, path := range allow {
		ok[path] = true
	}
	for _, path := range opened {
		if !ok[path] {
			t.Fatalf("opened %s in %v", path, opened)
		}
	}
}

func containsPath(paths []string, want string) bool {
	for _, path := range paths {
		if path == want {
			return true
		}
	}
	return false
}

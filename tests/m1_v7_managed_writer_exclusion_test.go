//go:build integration

package integration_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/iDoris-ai/hyphae/internal/deployment"
)

func TestM1V7ManagedWriterExclusion(t *testing.T) {
	if os.Getenv("HYPHAE_V7E_CHILD") == "1" {
		v7eChild()
		return
	}
	t.Log("linux strace not collected on this darwin host")

	w := newWorld(t)
	snap := seedLiveStores(t, w.m.Stores)
	seen := map[string]struct{}{}
	for _, entry := range deployment.ManagedEntries() {
		seen[entry.CommandClass] = struct{}{}
		stdout, stderr, code := w.execExclusion(t, entry.ID, sessionForClass(entry.CommandClass), "--legacy")
		if code != 0 {
			t.Fatalf("entry %s exit %d stderr %s stdout %s", entry.ID, code, stderr, stdout)
		}
		if v7eField(stdout, "code") != "DEPLOYMENT_CLOSED" {
			t.Fatalf("entry %s stdout %s stderr %s", entry.ID, stdout, stderr)
		}
		if v7eField(stdout, "exe") != w.launcher || v7eField(stdout, "exe_sha256") != w.m.LauncherSHA256 {
			t.Fatalf("entry %s exe record %s", entry.ID, stdout)
		}
		if v7eField(stdout, "path") != "" {
			t.Fatalf("entry %s planned %s", entry.ID, v7eField(stdout, "path"))
		}
		sameLive(t, snap)
		t.Logf("entry %s class %s refusal DEPLOYMENT_CLOSED exe %s sha256 %s", entry.ID, entry.CommandClass, w.launcher, w.m.LauncherSHA256)
	}
	for _, class := range []string{"dm", "group-store", "group-write", "maintenance", "probe"} {
		if _, ok := seen[class]; !ok {
			t.Fatalf("missing class %s", class)
		}
	}

	stdout, stderr, code := w.execExclusion(t, "cli", deployment.SessionModeRuntime, "")
	if code != 0 {
		t.Fatalf("dm plan exit %d stderr %s stdout %s", code, stderr, stdout)
	}
	pinned := v7eField(stdout, "path")
	sum := v7eField(stdout, "path_sha256")
	if pinned != w.binary || sum != w.m.BinarySHA256 || sum != fileSum(t, w.binary) {
		t.Fatalf("dm pinned %s hash %s stdout %s stderr %s", pinned, sum, stdout, stderr)
	}
	if v7eField(stdout, "exe") != w.launcher || v7eField(stdout, "exe_sha256") != w.m.LauncherSHA256 {
		t.Fatalf("dm exe record %s", stdout)
	}
	sameLive(t, snap)
	t.Logf("dm pinned %s sha256 %s", pinned, sum)

	copyPath := filepath.Join(w.root, "hyphae-same-uid-copy")
	copyMode(t, w.binary, copyPath, 0700)
	info, err := os.Lstat(copyPath)
	if err != nil {
		t.Fatal(err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Geteuid() || fileSum(t, copyPath) != w.m.BinarySHA256 || copyPath == w.binary {
		t.Fatalf("same-uid copy %s", copyPath)
	}
	stdout, stderr, code = w.execExclusion(t, "cli", deployment.SessionModeRuntime, "")
	if code != 0 || v7eField(stdout, "path") != w.binary {
		t.Fatalf("copy altered plan exit %d stdout %s stderr %s", code, stdout, stderr)
	}
	sameLive(t, snap)
	t.Logf("same-uid copy %s is outside the supported model", copyPath)

	stdout, stderr, code = w.execExclusion(t, "cli", deployment.SessionModeRuntime, "--binary")
	if code != 0 || v7eField(stdout, "code") != "DEPLOYMENT_CLOSED" {
		t.Fatalf("--binary exit %d stdout %s stderr %s", code, stdout, stderr)
	}
	sameLive(t, snap)

	store := w.m.Stores.MessagesDB
	before := append([]byte(nil), snap[store]...)
	_, err = deployment.PlanManagedEntry(deployment.LaunchRequest{
		ManifestPath: store,
		EntryID:      "no-such-entry",
		Session:      deployment.SessionModeRuntime,
		Env:          []string{},
	})
	requireClosed(t, err, "UNKNOWN_ENTRY")
	if !bytes.Equal(readBytes(t, store), before) {
		t.Fatal("unknown entry changed the store")
	}
	sameLive(t, snap)
	t.Log("managed launch record is not arbitrary-writer exclusion")
}

func v7eChild() {
	arg := os.Getenv("HYPHAE_V7E_ARG")
	var args []string
	if arg != "" {
		args = []string{arg}
	}
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "executable: %v\n", err)
		fmt.Printf("V7E code=PATH_UNSAFE\n")
		return
	}
	fmt.Printf("V7E exe=%s\n", exe)
	fmt.Printf("V7E exe_sha256=%s\n", shaOf(exe))
	path, err := deployment.PlanManagedEntry(deployment.LaunchRequest{
		ManifestPath: os.Getenv("HYPHAE_V7E_MANIFEST"),
		EntryID:      os.Getenv("HYPHAE_V7E_ENTRY"),
		Session:      deployment.SessionMode(os.Getenv("HYPHAE_V7E_SESSION")),
		Args:         args,
		Env:          []string{},
	})
	if err != nil {
		var closed *deployment.DeploymentClosedError
		if errors.As(err, &closed) && closed != nil {
			fmt.Printf("V7E code=%s\n", closed.Code)
			return
		}
		fmt.Fprintf(os.Stderr, "plan: %v\n", err)
		fmt.Printf("V7E code=UNCLASSIFIED\n")
		return
	}
	fmt.Printf("V7E path=%s\n", path)
	fmt.Printf("V7E path_sha256=%s\n", shaOf(path))
}

func (w *world) execExclusion(t *testing.T, entry string, session deployment.SessionMode, arg string) (string, string, int) {
	t.Helper()
	env := make([]string, 0, 16)
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "HYPHAE_V7E_") {
			continue
		}
		env = append(env, kv)
	}
	env = append(env,
		"HYPHAE_V7E_CHILD=1",
		"HYPHAE_V7E_MANIFEST="+w.manifest,
		"HYPHAE_V7E_ENTRY="+entry,
		"HYPHAE_V7E_SESSION="+string(session),
	)
	if arg != "" {
		env = append(env, "HYPHAE_V7E_ARG="+arg)
	}
	cmd := exec.Command(w.launcher, "-test.run=^TestM1V7ManagedWriterExclusion$")
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

func sessionForClass(class string) deployment.SessionMode {
	switch class {
	case "maintenance":
		return deployment.SessionModeMaintenance
	case "probe":
		return deployment.SessionModeProbe
	default:
		return deployment.SessionModeRuntime
	}
}

func seedLiveStores(t *testing.T, stores deployment.StorePaths) map[string][]byte {
	t.Helper()
	dir := filepath.Dir(stores.OutboxJSON)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	paths := []string{
		stores.OutboxJSON, stores.OutboxLock, stores.OutboxTempPrefix,
		stores.MessagesDB, stores.MessagesDBWAL, stores.MessagesDBSHM, stores.DaemonLock,
	}
	snap := make(map[string][]byte, len(paths))
	for _, path := range paths {
		body := []byte("live:" + filepath.Base(path))
		writeMode(t, path, body, 0600)
		snap[path] = body
	}
	return snap
}

func sameLive(t *testing.T, snap map[string][]byte) {
	t.Helper()
	for path, want := range snap {
		if !bytes.Equal(readBytes(t, path), want) {
			t.Fatalf("store changed %s", path)
		}
	}
}

func v7eField(out, key string) string {
	prefix := "V7E " + key + "="
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimPrefix(line, prefix)
		}
	}
	return ""
}

func shaOf(path string) string {
	body, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

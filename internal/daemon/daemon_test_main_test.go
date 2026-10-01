package daemon

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const daemonLockHelperEnv = "HYPHAE_DAEMON_LOCK_HELPER"

var daemonLockCLI string
var subprocessGoEnv []string

func TestMain(m *testing.M) {
	if os.Getenv(daemonLockHelperEnv) != "" {
		os.Exit(m.Run())
	}
	subprocessGoEnv = resolveSubprocessGoEnv()
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		os.Exit(m.Run())
	}
	_, source, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
	buildDir, err := os.MkdirTemp("", "hyphae-daemon-cli-")
	if err != nil {
		panic(err)
	}
	daemonLockCLI = filepath.Join(buildDir, "hyphae")
	build := exec.Command("go", "build", "-o", daemonLockCLI, "./cmd/hyphae")
	build.Dir = root
	build.Env = withSubprocessGoEnv(os.Environ())
	if output, err := build.CombinedOutput(); err != nil {
		_ = os.RemoveAll(buildDir)
		panic("build CLI: " + err.Error() + ": " + string(output))
	}
	code := m.Run()
	_ = os.RemoveAll(buildDir)
	os.Exit(code)
}

func resolveSubprocessGoEnv() []string {
	cmd := exec.Command("go", "env", "-json", "GOPATH", "GOMODCACHE", "GOCACHE")
	output, err := cmd.Output()
	if err != nil {
		panic("resolve Go subprocess cache paths: " + err.Error())
	}
	var paths struct {
		GoPath     string `json:"GOPATH"`
		GoModCache string `json:"GOMODCACHE"`
		GoCache    string `json:"GOCACHE"`
	}
	if err := json.Unmarshal(output, &paths); err != nil {
		panic("decode Go subprocess cache paths: " + err.Error())
	}
	if paths.GoPath == "" || paths.GoModCache == "" || paths.GoCache == "" {
		panic("go env returned an empty GOPATH, GOMODCACHE, or GOCACHE")
	}
	return []string{"GOPATH=" + paths.GoPath, "GOMODCACHE=" + paths.GoModCache, "GOCACHE=" + paths.GoCache}
}

func withSubprocessGoEnv(env []string) []string {
	filtered := make([]string, 0, len(env)+len(subprocessGoEnv))
	for _, item := range env {
		if strings.HasPrefix(item, "GOPATH=") || strings.HasPrefix(item, "GOMODCACHE=") || strings.HasPrefix(item, "GOCACHE=") {
			continue
		}
		filtered = append(filtered, item)
	}
	return append(filtered, subprocessGoEnv...)
}

func requireSubprocessGoEnv(t *testing.T) {
	t.Helper()
	home := os.Getenv("HOME")
	if home == "" {
		t.Fatal("child process is missing isolated HOME")
	}
	for _, key := range []string{"GOPATH", "GOMODCACHE", "GOCACHE"} {
		path := os.Getenv(key)
		if path == "" {
			t.Fatalf("child process is missing %s", key)
		}
		rel, err := filepath.Rel(home, path)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			t.Fatalf("child %s points inside isolated HOME: %q", key, path)
		}
	}
}

func daemonLockCLIEnv(current []string, home string) []string {
	env := make([]string, 0, len(current)+1)
	for _, item := range current {
		if strings.HasPrefix(item, "HOME=") || strings.HasPrefix(item, "HYPHAE_OUTPUT=") || strings.HasPrefix(item, "AGENT_SPEAKER_OUTPUT=") || strings.HasPrefix(item, daemonLockHelperEnv+"=") {
			continue
		}
		env = append(env, item)
	}
	return withSubprocessGoEnv(append(env, "HOME="+home))
}

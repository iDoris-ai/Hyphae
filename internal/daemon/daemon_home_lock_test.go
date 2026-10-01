//go:build linux || darwin

package daemon

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/internal/identity"
	"github.com/iDoris-ai/hyphae/internal/messaging"
	"github.com/iDoris-ai/hyphae/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

const daemonLockHelperEnv = "HYPHAE_DAEMON_LOCK_HELPER"

var daemonLockCLI string

func TestMain(m *testing.M) {
	if os.Getenv(daemonLockHelperEnv) == "1" {
		os.Exit(m.Run())
	}
	_, source, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
	buildDir, err := os.MkdirTemp("", "hyphae-daemon-lock-cli-")
	if err != nil {
		panic(err)
	}
	daemonLockCLI = filepath.Join(buildDir, "hyphae")
	build := exec.Command("go", "build", "-o", daemonLockCLI, "./cmd/hyphae")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		_ = os.RemoveAll(buildDir)
		panic("build CLI: " + err.Error() + ": " + string(output))
	}
	code := m.Run()
	_ = os.RemoveAll(buildDir)
	os.Exit(code)
}

func TestDaemonHomeLockCreatesPrivateStableFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	lock, err := acquireDaemonHomeLock()
	require.NoError(t, err)
	path := filepath.Join(home, identity.KeyStoreDirName, "daemon.lock")
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), info.Mode().Perm())
	dirInfo, err := os.Stat(filepath.Dir(path))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0700), dirInfo.Mode().Perm())
	first := info.Sys().(*syscall.Stat_t)
	firstDev, firstIno := first.Dev, first.Ino
	require.NoError(t, lock.Close())

	lock, err = acquireDaemonHomeLock()
	require.NoError(t, err)
	defer lock.Close()
	info, err = os.Stat(path)
	require.NoError(t, err)
	second := info.Sys().(*syscall.Stat_t)
	assert.Equal(t, firstDev, second.Dev)
	assert.Equal(t, firstIno, second.Ino, "lock path must keep the same inode across releases")
}

func TestDaemonHomeLockIsExclusivePerHome(t *testing.T) {
	homeA, homeB := t.TempDir(), t.TempDir()
	t.Setenv("HOME", homeA)
	first, err := acquireDaemonHomeLock()
	require.NoError(t, err)
	defer first.Close()

	second, err := acquireDaemonHomeLock()
	require.ErrorIs(t, err, errDaemonHomeLocked)
	assert.Nil(t, second)

	t.Setenv("HOME", homeB)
	independent, err := acquireDaemonHomeLock()
	require.NoError(t, err)
	require.NoError(t, independent.Close())
}

func TestDaemonHomeLockChild(t *testing.T) {
	if os.Getenv(daemonLockHelperEnv) != "1" {
		return
	}
	lock, err := acquireDaemonHomeLock()
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "acquire lock: %v\n", err)
		os.Exit(2)
	}
	_, _ = fmt.Fprintln(os.Stdout, "LOCK_HELD")
	_, _ = io.Copy(io.Discard, os.Stdin)
	if err := lock.Close(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "release lock: %v\n", err)
		os.Exit(3)
	}
}

func TestDaemonHomeLockReleasesAfterHelperExitAndSIGKILL(t *testing.T) {
	for _, kill := range []bool{false, true} {
		name := "normal exit"
		if kill {
			name = "SIGKILL"
		}
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			cmd, stdin := startDaemonLockChild(t, home)
			if kill {
				require.NoError(t, cmd.Process.Kill())
				_ = stdin.Close()
				_ = cmd.Wait()
			} else {
				require.NoError(t, stdin.Close())
				require.NoError(t, cmd.Wait())
			}
			t.Setenv("HOME", home)
			lock, err := acquireDaemonHomeLock()
			require.NoError(t, err, "kernel must release the lock when the process exits")
			require.NoError(t, lock.Close())
		})
	}
}

func TestDaemonCLIConflictPrecedesPasswordRead(t *testing.T) {
	home := t.TempDir()
	createEncryptedIdentity(t, home)
	child, childStdin := startDaemonLockChild(t, home)
	defer func() {
		_ = childStdin.Close()
		_ = child.Process.Kill()
		_ = child.Wait()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, daemonLockCLI, "--json", "daemon", "--identity", "bob", "--password-stdin")
	stdin, err := cmd.StdinPipe()
	require.NoError(t, err)
	cmd.Env = daemonLockCLIEnv(os.Environ(), home)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run()
	_ = stdin.Close()
	if ctx.Err() != nil {
		t.Fatalf("conflicting daemon tried to read password stdin: %v; stdout=%q stderr=%q", ctx.Err(), stdout.String(), stderr.String())
	}
	exit, ok := err.(*exec.ExitError)
	require.True(t, ok, "expected conflict exit, got err=%v stdout=%q stderr=%q", err, stdout.String(), stderr.String())
	assert.Equal(t, common.ExitWriteConflict, exit.ExitCode())
	assert.Empty(t, stdout.String())
	var result common.Result
	require.NoError(t, json.Unmarshal(stderr.Bytes(), &result), "stderr should contain one machine error envelope")
	assert.False(t, result.OK)
	assert.Equal(t, common.ErrCodeWriteConflict, result.Error)
}

func TestDaemonParentCancellationReleasesHomeLock(t *testing.T) {
	home := t.TempDir()
	createPlainIdentity(t, home)
	messaging.ResetStoreForTest()
	t.Cleanup(messaging.ResetStoreForTest)

	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() {
		app := &cli.Command{Name: "hyphae", Commands: []*cli.Command{DaemonCmd}}
		finished <- app.Run(ctx, []string{"hyphae", "daemon", "--relay", "ws://127.0.0.1:1", "--notify=false"})
	}()
	deadline := time.After(3 * time.Second)
	for {
		lock, err := acquireDaemonHomeLock()
		if errors.Is(err, errDaemonHomeLocked) {
			break
		}
		if err == nil {
			_ = lock.Close()
		}
		select {
		case <-deadline:
			cancel()
			t.Fatal("daemon action did not acquire the home lock")
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()
	select {
	case err := <-finished:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("daemon did not return after parent context cancellation")
	}
	lock, err := acquireDaemonHomeLock()
	require.NoError(t, err)
	require.NoError(t, lock.Close())
}

func startDaemonLockChild(t *testing.T, home string) (*exec.Cmd, io.WriteCloser) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestDaemonHomeLockChild$")
	cmd.Env = daemonLockCLIEnv(os.Environ(), home)
	cmd.Env = append(cmd.Env, daemonLockHelperEnv+"=1")
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	stdin, err := cmd.StdinPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())
	ready := make(chan string, 1)
	go func() { line, _ := bufio.NewReader(stdout).ReadString('\n'); ready <- line }()
	select {
	case line := <-ready:
		require.Equal(t, "LOCK_HELD\n", line)
	case <-time.After(3 * time.Second):
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatal("lock helper did not report that it holds the lock")
	}
	return cmd, stdin
}

func daemonLockCLIEnv(current []string, home string) []string {
	env := make([]string, 0, len(current)+1)
	for _, item := range current {
		if strings.HasPrefix(item, "HOME=") || strings.HasPrefix(item, "HYPHAE_OUTPUT=") || strings.HasPrefix(item, "AGENT_SPEAKER_OUTPUT=") || strings.HasPrefix(item, daemonLockHelperEnv+"=") {
			continue
		}
		env = append(env, item)
	}
	return append(env, "HOME="+home)
}

func createEncryptedIdentity(t *testing.T, home string) {
	t.Helper()
	t.Setenv("HOME", home)
	ks := &types.KeyStore{Identities: map[string]*types.Identity{}, Contacts: map[string]*types.Contact{}}
	_, err := identity.CreateIdentityWithPassword(ks, "alice", "daemon-lock-test-password")
	require.NoError(t, err)
	_, err = identity.CreateIdentityWithPassword(ks, "bob", "daemon-lock-test-password")
	require.NoError(t, err)
	ks.MasterKey = nil
	require.NoError(t, identity.SaveKeyStore(ks))
}

func createPlainIdentity(t *testing.T, home string) {
	t.Helper()
	t.Setenv("HOME", home)
	ks := &types.KeyStore{Identities: map[string]*types.Identity{}, Contacts: map[string]*types.Contact{}}
	_, err := identity.CreateIdentity(ks, "alice")
	require.NoError(t, err)
	require.NoError(t, identity.SaveKeyStore(ks))
}

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

func TestDaemonHomeLockInvalidPathChild(t *testing.T) {
	if os.Getenv(daemonLockHelperEnv) != "invalid" {
		return
	}
	lock, err := acquireDaemonHomeLock()
	if err == nil {
		_ = lock.Close()
		_, _ = fmt.Fprintln(os.Stdout, "UNEXPECTED_LOCK_ACQUIRED")
		os.Exit(0)
	}
	if errors.Is(err, errDaemonHomeLocked) {
		_, _ = fmt.Fprintln(os.Stdout, "WRITE_CONFLICT")
		os.Exit(common.ExitWriteConflict)
	}
	_, _ = fmt.Fprintf(os.Stdout, "ORDINARY_ERROR: %v\n", err)
	os.Exit(common.ExitOtherError)
}

func TestDaemonHomeLockMalformedPathsFailWithoutConflict(t *testing.T) {
	for _, kind := range []string{"fifo", "symlink", "directory"} {
		t.Run(kind, func(t *testing.T) {
			home := t.TempDir()
			dir := filepath.Join(home, identity.KeyStoreDirName)
			require.NoError(t, os.Mkdir(dir, 0700))
			path := filepath.Join(dir, "daemon.lock")
			var checkedPath string
			switch kind {
			case "fifo":
				require.NoError(t, syscall.Mkfifo(path, 0644))
				require.NoError(t, os.Chmod(path, 0644))
				checkedPath = path
			case "symlink":
				checkedPath = filepath.Join(home, "sentinel")
				require.NoError(t, os.WriteFile(checkedPath, []byte("keep mode"), 0644))
				require.NoError(t, os.Symlink(checkedPath, path))
			case "directory":
				require.NoError(t, os.Mkdir(path, 0755))
				require.NoError(t, os.Chmod(path, 0755))
				checkedPath = path
			}

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestDaemonHomeLockInvalidPathChild$")
			cmd.Env = append(daemonLockCLIEnv(os.Environ(), home), daemonLockHelperEnv+"=invalid")
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			err := cmd.Run()
			if ctx.Err() != nil {
				t.Fatalf("malformed lock path blocked startup: %v; stdout=%q stderr=%q", ctx.Err(), stdout.String(), stderr.String())
			}
			exit, ok := err.(*exec.ExitError)
			require.True(t, ok, "expected ordinary helper error, got err=%v stdout=%q stderr=%q", err, stdout.String(), stderr.String())
			assert.Equal(t, common.ExitOtherError, exit.ExitCode())
			assert.Contains(t, stdout.String(), "ORDINARY_ERROR:")
			assert.NotContains(t, stdout.String(), "WRITE_CONFLICT")

			info, statErr := os.Stat(checkedPath)
			require.NoError(t, statErr)
			wantMode := os.FileMode(0644)
			if kind == "directory" {
				wantMode = 0755
			}
			assert.Equal(t, wantMode, info.Mode().Perm(), "malformed path permissions must remain untouched")
		})
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
	t.Setenv("HOME", home)
	lock, lockErr := acquireDaemonHomeLock()
	require.ErrorIs(t, lockErr, errDaemonHomeLocked, "the helper must hold this exact home lock before the CLI conflict check")
	assert.Nil(t, lock)

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

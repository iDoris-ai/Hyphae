//go:build linux || darwin

package daemon

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDaemonCLIConflictColdStartupInitTrace is diagnostic-only. It measures
// --version before any CLI child runs, then compares a held-lock daemon
// conflict against that same binary. The password stdin pipe stays open and
// receives no bytes. GODEBUG is scoped to these subprocesses only.
func TestDaemonCLIConflictColdStartupInitTrace(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	initTraceEnv := func() []string {
		env := daemonLockCLIEnv(os.Environ(), home)
		filtered := make([]string, 0, len(env)+1)
		for _, item := range env {
			if strings.HasPrefix(item, "GODEBUG=") {
				continue
			}
			filtered = append(filtered, item)
		}
		return append(filtered, "GODEBUG=inittrace=1")
	}

	versionCtx, cancelVersion := context.WithTimeout(context.Background(), 3*time.Second)
	versionCommand := exec.CommandContext(versionCtx, daemonLockCLI, "--version")
	versionCommand.Env = initTraceEnv()
	versionStdout, err := versionCommand.StdoutPipe()
	require.NoError(t, err)
	versionStderr, err := versionCommand.StderrPipe()
	require.NoError(t, err)
	versionStarted := time.Now()
	require.NoError(t, versionCommand.Start())
	type initLineResult struct {
		line    string
		err     error
		elapsed time.Duration
	}
	firstTraceCh := make(chan initLineResult, 1)
	var versionOut, versionTrace bytes.Buffer
	var drains sync.WaitGroup
	drains.Add(2)
	go func() { defer drains.Done(); _, _ = io.Copy(&versionOut, versionStdout) }()
	go func() {
		defer drains.Done()
		reader := bufio.NewReader(versionStderr)
		first, readErr := reader.ReadString('\n')
		versionTrace.WriteString(first)
		firstTraceCh <- initLineResult{line: first, err: readErr, elapsed: time.Since(versionStarted)}
		_, _ = io.Copy(&versionTrace, reader)
	}()
	var first initLineResult
	firstTraceReceived := false
	var versionProcessSample, versionProcessStacks []byte
	select {
	case first = <-firstTraceCh:
		firstTraceReceived = true
	case <-time.After(2500 * time.Millisecond):
		versionProcessSample, _ = exec.Command("ps", "-o", "pid=,ppid=,stat=,etime=,command=", "-p", fmt.Sprint(versionCommand.Process.Pid)).CombinedOutput()
		if runtime.GOOS == "darwin" {
			if sample, lookErr := exec.LookPath("sample"); lookErr == nil {
				sampleCtx, stopSample := context.WithTimeout(versionCtx, 200*time.Millisecond)
				versionProcessStacks, _ = exec.CommandContext(sampleCtx, sample, fmt.Sprint(versionCommand.Process.Pid), "0.15", "10").CombinedOutput()
				stopSample()
			}
		}
		select {
		case first = <-firstTraceCh:
			firstTraceReceived = true
		case <-time.After(250 * time.Millisecond):
			_ = versionCommand.Process.Signal(syscall.SIGQUIT)
			select {
			case first = <-firstTraceCh:
				firstTraceReceived = true
			case <-time.After(25 * time.Millisecond):
				_ = versionCommand.Process.Kill()
			}
		}
	}
	drains.Wait()
	if !firstTraceReceived {
		select {
		case first = <-firstTraceCh:
			firstTraceReceived = true
		default:
		}
	}
	versionErr := versionCommand.Wait()
	versionElapsed := time.Since(versionStarted)
	cancelVersion()

	binary, err := os.Open(daemonLockCLI)
	require.NoError(t, err)
	hash := sha256.New()
	_, err = io.Copy(hash, binary)
	require.NoError(t, err)
	require.NoError(t, binary.Close())
	build, err := buildinfo.ReadFile(daemonLockCLI)
	require.NoError(t, err)
	firstTraceElapsed := "none before exit"
	if firstTraceReceived {
		firstTraceElapsed = first.elapsed.String()
	}
	t.Logf("cold --version binary=%s sha256=%s build=%s@%s go=%s spawn-to-first-inittrace=%s exit=%s err=%v process=%q sample=%q", daemonLockCLI, hex.EncodeToString(hash.Sum(nil)), build.Main.Path, build.Main.Version, build.GoVersion, firstTraceElapsed, versionElapsed, versionErr, versionProcessSample, versionProcessStacks)
	t.Logf("cold --version inittrace=%q stdout=%q", versionTrace.String(), versionOut.String())
	if !firstTraceReceived || first.err != nil || !strings.HasPrefix(strings.TrimSpace(first.line), "init ") {
		t.Skipf("cold --version did not emit an inittrace line before exit; lock-phase comparison skipped; read=%v", first.err)
	}
	require.NoError(t, versionErr, "cold --version startup: stderr=%s", versionTrace.String())
	require.Contains(t, versionOut.String(), "version dev")

	createEncryptedIdentity(t, home)
	child, childStdin := startDaemonLockChild(t, home)
	defer func() {
		_ = childStdin.Close()
		_ = child.Process.Kill()
		_ = child.Wait()
	}()
	lock, lockErr := acquireDaemonHomeLock()
	require.ErrorIs(t, lockErr, errDaemonHomeLocked, "the helper must hold the same home lock checked by the CLI")
	assert.Nil(t, lock)

	conflictCtx, cancelConflict := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelConflict()
	conflictCommand := exec.CommandContext(conflictCtx, daemonLockCLI, "--json", "daemon", "--identity", "bob", "--password-stdin")
	stdin, err := conflictCommand.StdinPipe()
	require.NoError(t, err)
	defer stdin.Close()
	conflictCommand.Env = initTraceEnv()
	var stdout, stderr bytes.Buffer
	conflictCommand.Stdout, conflictCommand.Stderr = &stdout, &stderr
	conflictStarted := time.Now()
	require.NoError(t, conflictCommand.Start())
	waited := make(chan error, 1)
	go func() { waited <- conflictCommand.Wait() }()
	var conflictProcessSample, conflictProcessStacks []byte
	var conflictErr error
	select {
	case conflictErr = <-waited:
	case <-time.After(2500 * time.Millisecond):
		conflictProcessSample, _ = exec.Command("ps", "-o", "pid=,ppid=,stat=,etime=,command=", "-p", fmt.Sprint(conflictCommand.Process.Pid)).CombinedOutput()
		if runtime.GOOS == "darwin" {
			if sample, lookErr := exec.LookPath("sample"); lookErr == nil {
				sampleCtx, stopSample := context.WithTimeout(conflictCtx, 200*time.Millisecond)
				conflictProcessStacks, _ = exec.CommandContext(sampleCtx, sample, fmt.Sprint(conflictCommand.Process.Pid), "0.15", "10").CombinedOutput()
				stopSample()
			}
		}
		select {
		case conflictErr = <-waited:
		case <-time.After(250 * time.Millisecond):
			_ = conflictCommand.Process.Signal(syscall.SIGQUIT)
			select {
			case conflictErr = <-waited:
			case <-time.After(25 * time.Millisecond):
				_ = conflictCommand.Process.Kill()
				conflictErr = <-waited
			}
			t.Fatalf("locked conflict CLI remained live near its three-second bound; process=%q stacks=%q inittrace/stderr=%q wait=%v", conflictProcessSample, conflictProcessStacks, stderr.String(), conflictErr)
		}
	}
	conflictElapsed := time.Since(conflictStarted)
	require.NoError(t, conflictCtx.Err(), "conflict CLI exceeded its existing three-second bound; inittrace=%s", stderr.String())
	exit, ok := conflictErr.(*exec.ExitError)
	require.True(t, ok, "expected write conflict, got err=%v stdout=%q stderr=%q", conflictErr, stdout.String(), stderr.String())
	assert.Equal(t, common.ExitWriteConflict, exit.ExitCode())
	assert.Empty(t, stdout.String())
	conflictResultFound := false
	for _, line := range strings.Split(stderr.String(), "\n") {
		var result common.Result
		if json.Unmarshal([]byte(line), &result) == nil && result.Error == common.ErrCodeWriteConflict {
			conflictResultFound = true
		}
	}
	require.True(t, conflictResultFound, "stderr must include the conflict error after inittrace")
	t.Logf("same-binary held-lock conflict spawn-to-exit=%s inittrace/stderr=%q", conflictElapsed, stderr.String())
}

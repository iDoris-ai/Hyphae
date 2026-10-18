package groupchat

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/iDoris-ai/hyphae/internal/messaging"
	"github.com/stretchr/testify/require"
)

type crashKey struct {
	Key       FanoutKey
	Recipient string
	DBPath    string
}

// TestFanoutCrashHelper is the child half of the V3/V4 crash evidence. It
// runs the real R/S/O3 (or D6) path against the parent's stores, and stops
// inside fanoutCrashGate -- after the JSON commit, before the SQLite
// projection, while holding the outbox flock and the SQLite write
// transaction. It prints one line and blocks on stdin; the parent SIGKILLs it.
func TestFanoutCrashHelper(t *testing.T) {
	stage := os.Getenv("HYPHAE_FANOUT_CRASH_STAGE")
	if stage == "" {
		return
	}
	var k crashKey
	require.NoError(t, json.Unmarshal([]byte(os.Getenv("HYPHAE_FANOUT_CRASH_KEY")), &k))
	db, err := sql.Open("sqlite", k.DBPath)
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	store, err := NewStore(db)
	require.NoError(t, err)
	fanoutCrashGate = func(got string) {
		if got != stage {
			return
		}
		fmt.Println("gate:" + got)
		var b [1]byte
		_, _ = os.Stdin.Read(b[:])
		panic("parent closed the crash gate without killing the child")
	}
	switch stage {
	case "o3":
		token, err := store.reserveFanoutAttempt(k.Key, k.Recipient)
		require.NoError(t, err)
		require.NoError(t, store.startFanoutAttempt(token, 1, time.Now().Unix()))
		err = store.commitFanoutFailure(token)
		t.Fatalf("O3 gate unexpectedly returned: %v", err)
	case "d6":
		_, err = store.authorizeFanoutRecovery(k.Key, k.Recipient, 1_800_000_000)
		t.Fatalf("D6 gate unexpectedly returned: %v", err)
	default:
		t.Fatalf("unknown crash stage %q", stage)
	}
}

// killAtGate starts the helper for stage, waits for it to reach the gate,
// SIGKILLs it, and returns once the process is gone.
func (f failureFixture) killAtGate(t *testing.T, stage string) {
	t.Helper()
	key, err := json.Marshal(crashKey{Key: f.key, Recipient: f.recipient, DBPath: f.member.dbPath})
	require.NoError(t, err)
	cmd := exec.Command(os.Args[0], "-test.run=^TestFanoutCrashHelper$")
	cmd.Env = append(os.Environ(), "HYPHAE_FANOUT_CRASH_STAGE="+stage, "HYPHAE_FANOUT_CRASH_KEY="+string(key))
	out, err := cmd.StdoutPipe()
	require.NoError(t, err)
	in, err := cmd.StdinPipe()
	require.NoError(t, err)
	defer in.Close()
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	ready := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(out)
		for scanner.Scan() {
			if line := scanner.Text(); len(line) > 5 && line[:5] == "gate:" {
				ready <- line
				return
			}
		}
		ready <- ""
	}()
	select {
	case line := <-ready:
		require.Equal(t, "gate:"+stage, line, "child must reach the crash gate")
	case <-time.After(30 * time.Second):
		t.Fatal("child did not reach the crash gate")
	}
	// The child now holds the outbox flock and the SQLite write transaction.
	require.NoError(t, cmd.Process.Kill())
	err = cmd.Wait()
	require.Error(t, err)
	status, ok := cmd.ProcessState.Sys().(syscall.WaitStatus)
	require.True(t, ok && status.Signaled() && status.Signal() == syscall.SIGKILL, "child must die by SIGKILL, not exit: %v", err)
}

func TestCrashAfterO3BeforeT4ReplaysExactlyOnce(t *testing.T) {
	f := newFailureFixture(t)
	f.killAtGate(t, "o3")

	// Durable: R and S committed, O3 record in the outbox. Not durable: T4.
	entry := f.outboxEntry(t)
	require.NotNil(t, entry.FailureResult, "O3 record must survive the kill")
	require.Equal(t, 1, entry.Entry.RetryCount)
	require.EqualValues(t, 1, entry.FailureResult.Token.AttemptGeneration)
	row := f.recovery(t)
	require.Equal(t, "started", row.Phase, "T4 must not be visible; the SQLite transaction died with the child")
	require.EqualValues(t, 0, row.RetryCount)
	require.EqualValues(t, 1, row.Generation)
	require.Equal(t, "", row.Receipt)

	// The kill released both locks; the barrier replays T4 exactly once.
	replayed, err := f.member.store.recoverFanoutFailure(f.key, f.recipient)
	require.NoError(t, err)
	require.True(t, replayed)
	after := f.recovery(t)
	require.Equal(t, row.Attempts, after.Attempts, "replay never adds a publish start")
	require.EqualValues(t, 1, after.RetryCount)
	require.Equal(t, "failed", after.Phase)
	require.Equal(t, messaging.FailureReceipt(entry.FailureResult.ResultID, sha256Hex(entry.Fields["failure_result"])), after.Receipt)
	replayed, err = f.member.store.recoverFanoutFailure(f.key, f.recipient)
	require.NoError(t, err)
	require.False(t, replayed, "second recovery is a no-op")
	token, err := f.member.store.reserveFanoutAttempt(f.key, f.recipient)
	require.NoError(t, err)
	require.EqualValues(t, 2, token.generation)
	require.Equal(t, after.Attempts, f.recovery(t).Attempts, "replay never adds a publish start")
}

func TestCrashAfterD6BeforeT2PrimeReplaysExactlyOnce(t *testing.T) {
	f := newFailureFixture(t)
	f.exhaust(t)
	before := f.recovery(t)
	f.killAtGate(t, "d6")

	entry := f.outboxEntry(t)
	require.NotNil(t, entry.RecoveryWitness, "D6 epoch must survive the kill")
	require.NotEqual(t, f.queueID, entry.QueueID)
	require.Equal(t, f.queueID, entry.RecoveryWitness.OldQueueID)
	row := f.recovery(t)
	require.Equal(t, string(RecipientFailed), row.State, "T2' must not be visible")
	require.Equal(t, f.queueID, row.QueueID)
	require.Equal(t, before.Revision, row.Revision)

	replayed, err := f.member.store.recoverFanoutRecovery(f.key, f.recipient)
	require.NoError(t, err)
	require.True(t, replayed)
	after := f.recovery(t)
	require.Equal(t, before.Attempts, after.Attempts, "replay never adds a publish start")
	require.Equal(t, string(RecipientQueued), after.State)
	require.Equal(t, entry.QueueID, after.QueueID)
	require.Equal(t, entry.RecoveryWitness.RecoveryID, after.RecoveryID)
	require.EqualValues(t, 0, after.RetryCount)
	require.Equal(t, before.Generation, after.Generation)
	replayed, err = f.member.store.recoverFanoutRecovery(f.key, f.recipient)
	require.NoError(t, err)
	require.False(t, replayed)
	token, err := f.member.store.reserveFanoutAttempt(f.key, f.recipient)
	require.NoError(t, err)
	require.Equal(t, entry.QueueID, token.queueID)
	require.Equal(t, before.Generation+1, token.generation)
}

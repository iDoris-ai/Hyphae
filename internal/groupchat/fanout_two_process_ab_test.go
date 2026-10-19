package groupchat

import (
	"bufio"
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Two OS processes share one queue. A starts, then B starts and may settle.
// A's later failure and ACK return stale. Outbox bytes and the fanout row
// stay at the values B left behind. An in-process interleave is a different test.
func TestTwoProcessLateAttemptAIsStale(t *testing.T) {
	for _, outcome := range []string{"unreturned", "accepted", "failed"} {
		t.Run(outcome, func(t *testing.T) {
			f := newFailureFixture(t)
			key, err := json.Marshal(abProcessKey{Key: f.key, Recipient: f.recipient, DBPath: f.member.dbPath})
			require.NoError(t, err)

			procA := startABProcess(t, "a", "", key)
			lineA := procA.readLine(t)
			genA, ok := parseABLine(lineA, "ready")
			require.True(t, ok, "A ready line %q stderr %s", lineA, procA.stderr.String())

			procB := startABProcess(t, "b", outcome, key)
			lineB := procB.readLine(t)
			genB, ok := parseABLine(lineB, "done")
			require.True(t, ok, "B done line %q stderr %s", lineB, procB.stderr.String())
			require.NoError(t, procB.cmd.Wait(), "B stderr %s", procB.stderr.String())
			require.Equal(t, genA+1, genB, "B supersedes A's unfinished generation")

			unchanged := f.snapshot(t)
			_, err = io.WriteString(procA.stdin, "commit\n")
			require.NoError(t, err)
			require.NoError(t, procA.stdin.Close())
			lineLate := procA.readLine(t)
			require.Equal(t, "stale", lineLate, "A stderr %s", procA.stderr.String())
			require.NoError(t, procA.cmd.Wait(), "A stderr %s", procA.stderr.String())
			unchanged()
		})
	}
}

// TestFanoutTwoProcessABHelper is the child half of TestTwoProcessLateAttemptAIsStale.
// With HYPHAE_AB_ROLE unset it returns so a package run does not execute the child path.
func TestFanoutTwoProcessABHelper(t *testing.T) {
	role := os.Getenv("HYPHAE_AB_ROLE")
	if role == "" {
		return
	}
	var k abProcessKey
	require.NoError(t, json.Unmarshal([]byte(os.Getenv("HYPHAE_AB_KEY")), &k))
	store := openABStore(t, k.DBPath)
	switch role {
	case "a":
		token := abStart(t, store, k.Key, k.Recipient)
		fmt.Printf("ready %d\n", token.generation)
		scanner := bufio.NewScanner(os.Stdin)
		if !scanner.Scan() || scanner.Text() != "commit" {
			t.Fatalf("A expected commit, got %q", scanner.Text())
		}
		if err := store.commitFanoutFailure(token); !errors.Is(err, errFanoutStaleAttempt) {
			t.Fatalf("late failure: %v", err)
		}
		if err := store.commitFanoutAccepted(token, time.Now().Unix()); !errors.Is(err, errFanoutStaleAttempt) {
			t.Fatalf("late ack: %v", err)
		}
		if err := store.startFanoutAttempt(token, 1, time.Now().Unix()); err == nil {
			t.Fatal("late start was accepted")
		}
		fmt.Println("stale")
	case "b":
		token := abStart(t, store, k.Key, k.Recipient)
		switch os.Getenv("HYPHAE_AB_OUTCOME") {
		case "unreturned":
		case "accepted":
			require.NoError(t, store.commitFanoutAccepted(token, time.Now().Unix()))
		case "failed":
			require.NoError(t, store.commitFanoutFailure(token))
		default:
			t.Fatalf("unknown B outcome %q", os.Getenv("HYPHAE_AB_OUTCOME"))
		}
		fmt.Printf("done %d\n", token.generation)
	default:
		t.Fatalf("unknown role %q", role)
	}
}

type abProcessKey struct {
	Key       FanoutKey
	Recipient string
	DBPath    string
}

type abProcess struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	lines  chan string
	stderr *lockedBuffer
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func startABProcess(t *testing.T, role, outcome string, key []byte) *abProcess {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestFanoutTwoProcessABHelper$")
	cmd.Env = append(os.Environ(), "HYPHAE_AB_ROLE="+role, "HYPHAE_AB_OUTCOME="+outcome, "HYPHAE_AB_KEY="+string(key))
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	stdin, err := cmd.StdinPipe()
	require.NoError(t, err)
	stderr := &lockedBuffer{}
	cmd.Stderr = stderr
	require.NoError(t, cmd.Start())
	proc := &abProcess{cmd: cmd, stdin: stdin, lines: make(chan string, 4), stderr: stderr}
	t.Cleanup(func() {
		if proc.cmd.Process != nil && proc.cmd.ProcessState == nil {
			_ = proc.cmd.Process.Kill()
		}
	})
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			proc.lines <- scanner.Text()
		}
		close(proc.lines)
	}()
	return proc
}

func (p *abProcess) readLine(t *testing.T) string {
	t.Helper()
	select {
	case line, ok := <-p.lines:
		if !ok {
			t.Fatalf("child closed stdout; stderr %s", p.stderr.String())
		}
		return line
	case <-time.After(30 * time.Second):
		t.Fatalf("child produced no line; stderr %s", p.stderr.String())
		return ""
	}
}

func parseABLine(line, prefix string) (int64, bool) {
	var gen int64
	_, err := fmt.Sscanf(line, prefix+" %d", &gen)
	return gen, err == nil
}

func openABStore(t *testing.T, dbPath string) *Store {
	t.Helper()
	u := &url.URL{Scheme: "file", Path: filepath.ToSlash(dbPath)}
	q := url.Values{}
	q.Add("_pragma", "busy_timeout(5000)")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	store, err := NewStore(db)
	require.NoError(t, err)
	return store
}

func abStart(t *testing.T, store *Store, key FanoutKey, recipient string) fanoutAttemptToken {
	t.Helper()
	tx, err := store.beginImmediate()
	require.NoError(t, err)
	token, err := reserveFanoutAttemptTx(tx, key, recipient)
	require.NoError(t, err)
	require.NoError(t, startFanoutAttemptTx(tx, token, 2, time.Now().Unix()))
	require.NoError(t, tx.Commit())
	return token
}

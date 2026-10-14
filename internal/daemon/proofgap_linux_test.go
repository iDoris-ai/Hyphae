package daemon

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/internal/storage"
	"github.com/stretchr/testify/require"
)

// pgMembersDDL is the EXACT 47d8daf setupMembersTableMissingInviteIDDB DDL;
// withInviteID adds a nullable TEXT invite_id for the healthy positive control.
func pgPlantMembersDB(t *testing.T, home string, withInviteID bool) {
	t.Helper()
	dbPath := filepath.Join(home, ".hyphae", "messages.db")
	require.NoError(t, os.MkdirAll(filepath.Dir(dbPath), 0700))
	db, err := sql.Open("sqlite", dbPath)
	require.NoError(t, err)
	defer db.Close()
	invite := ""
	if withInviteID {
		invite = "invite_id TEXT,"
	}
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS messages (
			id TEXT PRIMARY KEY,
			sender_npub TEXT NOT NULL,
			recipient_npub TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			received_at INTEGER NOT NULL,
			plaintext TEXT,
			is_outgoing INTEGER DEFAULT 0,
			is_encrypted INTEGER DEFAULT 1
		);
		CREATE TABLE IF NOT EXISTS contacts (
			npub TEXT PRIMARY KEY,
			nickname TEXT NOT NULL,
			created_at INTEGER NOT NULL
		);
		CREATE TABLE groupchat_members (
			local_npub TEXT NOT NULL,
			group_id TEXT NOT NULL,
			npub TEXT NOT NULL,
			` + invite + `
			state TEXT NOT NULL,
			is_creator INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (local_npub, group_id, npub)
		);
	`)
	require.NoError(t, err)
}

type pgDaemonRun struct {
	exitCode        int
	killedAtBound   bool
	dials           int32
	firstDialMs     int64
	statusSnapshots int
	stdout, stderr  string
}

// pgRunCompiledDaemon runs the compiled daemon against a LOCAL counting WS
// relay (127.0.0.1 httptest; never a public relay) with a bounded deadline.
// If a dial is observed the process is stopped shortly after (baseline red).
func pgRunCompiledDaemon(t *testing.T, home string, bound time.Duration) pgDaemonRun {
	t.Helper()
	var dials atomic.Int32
	var firstDial atomic.Int64
	start := time.Now()
	dialed := make(chan struct{}, 1)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if dials.Add(1) == 1 {
			firstDial.Store(time.Since(start).Milliseconds())
			select {
			case dialed <- struct{}{}:
			default:
			}
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer server.Close()
	relayURL := "ws" + strings.TrimPrefix(server.URL, "http")

	var stdout, stderr bytes.Buffer
	cmd := exec.Command(daemonLockCLI, "--json", "daemon", "--identity", "alice", "--relay", relayURL, "--password-stdin")
	cmd.Stdin = strings.NewReader("startup-secret-123\n")
	cmd.Env = append(withoutDaemonOutputEnv(os.Environ()), "HOME="+home)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	require.NoError(t, cmd.Start())
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	run := pgDaemonRun{exitCode: -1, firstDialMs: -1}
	var waitErr error
	select {
	case waitErr = <-done:
	case <-dialed:
		time.Sleep(300 * time.Millisecond)
		run.killedAtBound = true
		_ = cmd.Process.Kill()
		waitErr = <-done
	case <-time.After(bound):
		run.killedAtBound = true
		_ = cmd.Process.Kill()
		waitErr = <-done
	}
	var exitErr *exec.ExitError
	if waitErr == nil {
		run.exitCode = 0
	} else if errors.As(waitErr, &exitErr) {
		run.exitCode = exitErr.ExitCode()
	}
	run.dials = dials.Load()
	if run.dials > 0 {
		run.firstDialMs = firstDial.Load()
	}
	run.stdout, run.stderr = stdout.String(), stderr.String()
	for _, line := range strings.Split(run.stdout, "\n") {
		if strings.Contains(line, `"event_sequence"`) {
			run.statusSnapshots++
		}
	}
	return run
}

func pgSHA(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

// Proof-gap (test-only, paired). EXACT 47d8daf missing-invite_id fixture.
// Baseline 127f460: compiled daemon dials the LOCAL relay before the bound (RED).
// Candidate 47d8daf: literal exit 4, zero dials, zero status snapshots,
// outbox bytes.Equal + sha256 identical (GREEN).
func TestProofGapDaemonMembersMissingInviteIDExit4ZeroDial(t *testing.T) {
	home, id := setupTestHomeAndIdentity(t)
	pgPlantMembersDB(t, home, false)
	setupInitialOutbox(t, home)
	outboxPath := filepath.Join(home, ".hyphae", "outbox.json")
	before, err := os.ReadFile(outboxPath)
	require.NoError(t, err)
	beforeSHA := pgSHA(before)

	run := pgRunCompiledDaemon(t, home, 8*time.Second)

	after, err := os.ReadFile(outboxPath)
	require.NoError(t, err)
	afterSHA := pgSHA(after)
	t.Logf("PROOFGAP daemon exit=%d killedAtBound=%v dials=%d firstDialMs=%d statusSnapshots=%d outboxBytesEqual=%v sha256Before=%s sha256After=%s stderr=%q",
		run.exitCode, run.killedAtBound, run.dials, run.firstDialMs, run.statusSnapshots, bytes.Equal(before, after), beforeSHA, afterSHA, pgFirstN(run.stderr, 300))

	require.False(t, run.killedAtBound, "daemon must refuse on its own before the bound")
	require.Equal(t, common.ExitOtherError, run.exitCode, "literal exit 4")
	require.Zero(t, run.dials, "zero relay dials")
	require.Zero(t, run.statusSnapshots, "no daemon status snapshot (work loop never reached)")
	require.True(t, bytes.Equal(before, after), "outbox bytes must be byte-identical")
	require.Equal(t, beforeSHA, afterSHA, "outbox sha256 must be identical")
	require.Contains(t, run.stderr+run.stdout, "group storage init failed")

	// DM positive control on the same broken file.
	db, err := storage.OpenDB()
	require.NoError(t, err)
	defer db.Close()
	_, err = db.Exec(`INSERT INTO messages (id, sender_npub, recipient_npub, created_at, received_at, plaintext)
		VALUES ('pg-dm', ?, 'npub1recipient', 1, 1, 'dm')`, id.Npub)
	require.NoError(t, err, "DM messages table must stay writable")
}

// Positive control (both SHAs): same DDL with nullable invite_id is healthy;
// the compiled daemon reaches the LOCAL relay, proving the zero-dial counter
// above is not vacuous.
func TestProofGapDaemonNullableInviteIDHealthyDialsControl(t *testing.T) {
	home, _ := setupTestHomeAndIdentity(t)
	pgPlantMembersDB(t, home, true)
	setupInitialOutbox(t, home)

	run := pgRunCompiledDaemon(t, home, 8*time.Second)
	t.Logf("PROOFGAP daemon-control exit=%d killedAtBound=%v dials=%d firstDialMs=%d statusSnapshots=%d stderr=%q",
		run.exitCode, run.killedAtBound, run.dials, run.firstDialMs, run.statusSnapshots, pgFirstN(run.stderr, 300))
	require.Greater(t, run.dials, int32(0), "healthy nullable invite_id must reach the local relay")
	require.False(t, strings.Contains(run.stderr+run.stdout, "group storage init failed"))
}

func pgFirstN(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

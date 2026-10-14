package daemon

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
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

	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Independent Linux proof (test-only): a view standing in for the unindexed
// groupchat_groups table must refuse daemon startup before any relay dial and
// leave the outbox untouched. Compiled daemon must exit with ExitOtherError (4).
func TestProofDaemonRefusesGroupsViewBeforeRelayDial(t *testing.T) {
	home, _ := setupTestHomeAndIdentity(t)
	db, err := storage.OpenDB()
	require.NoError(t, err)
	require.NoError(t, db.Close())
	raw, err := sql.Open("sqlite", filepath.Join(home, ".hyphae", "messages.db"))
	require.NoError(t, err)
	_, err = raw.Exec(`CREATE VIEW groupchat_groups AS SELECT 'n' AS local_npub, 'g' AS group_id`)
	require.NoError(t, err)
	require.NoError(t, raw.Close())
	outboxSize, outboxModTime := setupInitialOutbox(t, home)
	outboxBefore, err := os.ReadFile(filepath.Join(home, ".hyphae", "outbox.json"))
	require.NoError(t, err)

	var relayConnections atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		relayConnections.Add(1)
	}))
	defer server.Close()
	relayURL := "ws" + strings.TrimPrefix(server.URL, "http")

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, daemonLockCLI, "--json", "daemon", "--identity", "alice", "--relay", relayURL, "--password-stdin")
	command.Stdin = strings.NewReader("startup-secret-123\n")
	command.Env = append(withoutDaemonOutputEnv(os.Environ()), "HOME="+home)
	output, runErr := command.CombinedOutput()
	timedOut := ctx.Err() != nil

	fi, statErr := os.Stat(filepath.Join(home, ".hyphae", "outbox.json"))
	require.NoError(t, statErr)
	outboxAfter, readErr := os.ReadFile(filepath.Join(home, ".hyphae", "outbox.json"))
	require.NoError(t, readErr)
	assert.True(t, bytes.Equal(outboxBefore, outboxAfter), "outbox bytes must be byte-identical (bytes.Equal)")
	assert.Equal(t, pgSHA(outboxBefore), pgSHA(outboxAfter), "outbox sha256 must be identical")
	t.Logf("daemon err=%v timedOut=%v relayConnections=%d outboxSizeSame=%v outboxMtimeSame=%v output=%q",
		runErr, timedOut, relayConnections.Load(), fi.Size() == outboxSize, fi.ModTime().Equal(outboxModTime), firstN(string(output), 400))
	assert.False(t, timedOut, "daemon must exit on its own (refuse), not run until killed")
	assert.Zero(t, relayConnections.Load(), "relay dial count must be 0 on non-physical groupchat_groups")
	assert.True(t, fi.Size() == outboxSize && fi.ModTime().Equal(outboxModTime), "outbox must be unchanged")
	assert.True(t, strings.Contains(string(output), "group storage init failed"), "output must carry group storage init failed refusal")

	var exitErr *exec.ExitError
	require.True(t, errors.As(runErr, &exitErr), "daemon must exit nonzero: %v", runErr)
	assert.Equal(t, common.ExitOtherError, exitErr.ExitCode(), "compiled daemon must exit 4 (ExitOtherError) on group schema refusal")

	var result common.Result
	require.NoError(t, json.Unmarshal(output, &result))
	assert.False(t, result.OK)
	assert.Equal(t, common.ErrCodeOther, result.Error)
}

// TestProofDaemonRefusesMembersMissingInviteIDExit4: baseline-127 red fixture
// (members shape without invite_id). On 127f460 NewStore accepted and daemon
// dialed; on 47d8daf compiled daemon must refuse with exit 4, zero RelayDial,
// no outbox mutation.
func TestProofDaemonRefusesMembersMissingInviteIDExit4(t *testing.T) {
	home, _ := setupTestHomeAndIdentity(t)
	setupMembersTableMissingInviteIDDB(t, home)
	outboxSize, outboxModTime := setupInitialOutbox(t, home)
	outboxBefore, err := os.ReadFile(filepath.Join(home, ".hyphae", "outbox.json"))
	require.NoError(t, err)

	var relayConnections atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		relayConnections.Add(1)
	}))
	defer server.Close()
	relayURL := "ws" + strings.TrimPrefix(server.URL, "http")

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, daemonLockCLI, "--json", "daemon", "--identity", "alice", "--relay", relayURL, "--password-stdin")
	command.Stdin = strings.NewReader("startup-secret-123\n")
	command.Env = append(withoutDaemonOutputEnv(os.Environ()), "HOME="+home)
	output, runErr := command.CombinedOutput()
	timedOut := ctx.Err() != nil

	fi, statErr := os.Stat(filepath.Join(home, ".hyphae", "outbox.json"))
	require.NoError(t, statErr)
	outboxAfter, readErr := os.ReadFile(filepath.Join(home, ".hyphae", "outbox.json"))
	require.NoError(t, readErr)
	assert.True(t, bytes.Equal(outboxBefore, outboxAfter), "outbox bytes must be byte-identical (bytes.Equal)")
	assert.Equal(t, pgSHA(outboxBefore), pgSHA(outboxAfter), "outbox sha256 must be identical")
	t.Logf("invite_id-missing daemon err=%v timedOut=%v relay=%d outboxSame=%v output=%q",
		runErr, timedOut, relayConnections.Load(),
		fi.Size() == outboxSize && fi.ModTime().Equal(outboxModTime), firstN(string(output), 400))

	assert.False(t, timedOut, "daemon must refuse on its own")
	assert.Zero(t, relayConnections.Load(), "RelayDial count must be 0 (vs 127 real red)")
	assert.Equal(t, outboxSize, fi.Size(), "outbox bytes unchanged")
	assert.True(t, fi.ModTime().Equal(outboxModTime), "outbox mtime unchanged")

	var exitErr *exec.ExitError
	require.True(t, errors.As(runErr, &exitErr), "daemon must exit nonzero: %v", runErr)
	assert.Equal(t, common.ExitOtherError, exitErr.ExitCode(), "actual exit must be 4")
	assert.Contains(t, string(output), "group storage init failed")
}

func firstN(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

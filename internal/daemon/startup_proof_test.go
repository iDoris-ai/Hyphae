package daemon

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/iDoris-ai/hyphae/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Independent Linux proof (test-only): a view standing in for the unindexed
// groupchat_groups table must refuse daemon startup before any relay dial and
// leave the outbox untouched. The candidate's own view test only covers the
// indexed groupchat_invites table.
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
	t.Logf("daemon err=%v timedOut=%v relayConnections=%d outboxSizeSame=%v outboxMtimeSame=%v output=%q",
		runErr, timedOut, relayConnections.Load(), fi.Size() == outboxSize, fi.ModTime().Equal(outboxModTime), firstN(string(output), 400))
	assert.False(t, timedOut, "daemon must exit on its own (refuse), not run until killed")
	assert.Zero(t, relayConnections.Load(), "relay dial count must be 0 on non-physical groupchat_groups")
	assert.True(t, strings.Contains(string(output), "group storage init failed"), "output must carry group storage init failed refusal")
}

func firstN(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

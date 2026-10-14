package tui

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/iDoris-ai/hyphae/internal/storage"
	"github.com/stretchr/testify/require"
)

// Proof-gap (test-only, paired): members table missing invite_id.
// Baseline 127f460: NewChatModel constructs (RED). Candidate 47d8daf: nil model,
// group storage init refusal, zero relay dials (GREEN). Work-started is observed
// only through the pre-existing unexported inboxStarted/outboxStarted fields.
func TestProofGapNewChatModelMembersMissingInviteID(t *testing.T) {
	home, _, _ := setupTUIStartupEnv(t)
	db, err := storage.OpenDB()
	require.NoError(t, err)
	require.NoError(t, db.Close())
	raw, err := sql.Open("sqlite", filepath.Join(home, ".hyphae", "messages.db"))
	require.NoError(t, err)
	_, err = raw.Exec(`CREATE TABLE groupchat_members (
		local_npub TEXT NOT NULL,
		group_id TEXT NOT NULL,
		npub TEXT NOT NULL,
		state TEXT NOT NULL,
		is_creator INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY(local_npub, group_id, npub)
	)`)
	require.NoError(t, err)
	require.NoError(t, raw.Close())

	var dials atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { dials.Add(1) }))
	defer server.Close()

	model, err := NewChatModel("bob", "ws"+strings.TrimPrefix(server.URL, "http"))
	time.Sleep(100 * time.Millisecond)
	modelNil := model == nil
	inboxStarted, outboxStarted := false, false
	if model != nil {
		inboxStarted, outboxStarted = model.inboxStarted, model.outboxStarted
		_ = model.Close()
	}
	t.Logf("PROOFGAP tui err=%v modelNil=%v inboxStarted=%v outboxStarted=%v dials=%d", err, modelNil, inboxStarted, outboxStarted, dials.Load())

	require.False(t, inboxStarted || outboxStarted, "no worker may start during construction")
	require.Error(t, err, "NewChatModel must refuse (127f460 constructs a model)")
	require.True(t, modelNil, "refused NewChatModel must return a nil model")
	require.Contains(t, err.Error(), "group storage init failed")
	require.Zero(t, dials.Load(), "zero relay dials on refusal")
}

package tui

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/iDoris-ai/hyphae/internal/storage"
	"github.com/stretchr/testify/require"
)

// Independent Linux proof (test-only): NewChatModel must refuse when the
// unindexed groupchat_groups name is a view rather than a physical table.
func TestProofNewChatModelRefusesGroupsView(t *testing.T) {
	home, _, _ := setupTUIStartupEnv(t)
	db, err := storage.OpenDB()
	require.NoError(t, err)
	require.NoError(t, db.Close())
	raw, err := sql.Open("sqlite", filepath.Join(home, ".hyphae", "messages.db"))
	require.NoError(t, err)
	_, err = raw.Exec(`CREATE VIEW groupchat_groups AS SELECT 'n' AS local_npub, 'g' AS group_id`)
	require.NoError(t, err)
	require.NoError(t, raw.Close())

	var relayConnections atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		relayConnections.Add(1)
	}))
	defer server.Close()
	relayURL := "ws" + strings.TrimPrefix(server.URL, "http")

	model, err := NewChatModel("bob", relayURL)
	if err == nil {
		groupStoreRetained := model.GroupStore() != nil
		_ = model.Close()
		t.Fatalf("UNSAFE HEALTHY FALSE POSITIVE: NewChatModel succeeded on view groupchat_groups (groupStore retained=%v, dials at construct=%d)", groupStoreRetained, relayConnections.Load())
	}
	require.Contains(t, err.Error(), "group storage init failed")
}

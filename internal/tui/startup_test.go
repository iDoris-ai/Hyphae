package tui

import (
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/internal/identity"
	"github.com/iDoris-ai/hyphae/internal/storage"
	"github.com/iDoris-ai/hyphae/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTUIStartupEnv(t *testing.T) (string, *types.Identity, *types.Identity) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)

	ks, err := identity.LoadKeyStore()
	require.NoError(t, err)

	myID, err := identity.CreateIdentity(ks, "alice")
	require.NoError(t, err)

	contactID, err := identity.CreateIdentity(ks, "bob")
	require.NoError(t, err)

	ks, err = identity.LoadKeyStore()
	require.NoError(t, err)
	require.NoError(t, identity.AddContact(ks, "bob", contactID.Npub))

	return home, myID, contactID
}

func setupTUIInvalidLegacyFanoutDB(t *testing.T, home string) {
	t.Helper()
	dbPath := filepath.Join(home, ".hyphae", "messages.db")
	require.NoError(t, os.MkdirAll(filepath.Dir(dbPath), 0700))
	db, err := sql.Open("sqlite", dbPath)
	require.NoError(t, err)
	defer db.Close()

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
		CREATE TABLE groupchat_fanout (
			local_npub TEXT NOT NULL,
			group_id TEXT NOT NULL,
			envelope_type TEXT NOT NULL,
			send_key TEXT NOT NULL,
			recipient_npub TEXT NOT NULL,
			event_id TEXT NOT NULL,
			event_json TEXT NOT NULL,
			queue_id TEXT NOT NULL DEFAULT '',
			state TEXT NOT NULL,
			issue TEXT NOT NULL DEFAULT '',
			relay_acks INTEGER NOT NULL DEFAULT 0,
			relay_count INTEGER NOT NULL DEFAULT 0,
			attempts INTEGER NOT NULL DEFAULT 0,
			max_retries INTEGER NOT NULL,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			last_attempt_at INTEGER NOT NULL DEFAULT 0,
			accepted_at INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (local_npub, group_id, envelope_type, send_key, recipient_npub),
			UNIQUE (local_npub, event_id)
		);
		INSERT INTO groupchat_fanout (
			local_npub, group_id, envelope_type, send_key, recipient_npub, event_id, event_json, state, attempts, max_retries, created_at, updated_at
		) VALUES (
			'npub1alice', 'g1', 'message', 'k1', 'npub1bob', 'e1', '{}', 'prepared', -1, 3, 1000, 1000
		);
	`)
	require.NoError(t, err)
}

func setupTUIConflictingViewDB(t *testing.T, home string) {
	t.Helper()
	dbPath := filepath.Join(home, ".hyphae", "messages.db")
	require.NoError(t, os.MkdirAll(filepath.Dir(dbPath), 0700))
	db, err := sql.Open("sqlite", dbPath)
	require.NoError(t, err)
	defer db.Close()

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
		CREATE VIEW groupchat_invites AS SELECT 1 AS dummy;
	`)
	require.NoError(t, err)
}

func setupTUIConflictingTableDB(t *testing.T, home string) {
	t.Helper()
	dbPath := filepath.Join(home, ".hyphae", "messages.db")
	require.NoError(t, os.MkdirAll(filepath.Dir(dbPath), 0700))
	db, err := sql.Open("sqlite", dbPath)
	require.NoError(t, err)
	defer db.Close()

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
		CREATE TABLE groupchat_invites (dummy INT);
	`)
	require.NoError(t, err)
}

func TestTUIFailClosedOnInvalidLegacyFanoutCounters(t *testing.T) {
	home, _, _ := setupTUIStartupEnv(t)
	setupTUIInvalidLegacyFanoutDB(t, home)

	var relayConnections atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		relayConnections.Add(1)
	}))
	defer server.Close()
	relayURL := "ws" + strings.TrimPrefix(server.URL, "http")

	sentinel, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	defer sentinel.Close()
	prevDB := storage.DB
	storage.DB = sentinel
	t.Cleanup(func() { storage.DB = prevDB })

	model, err := NewChatModel("bob", relayURL)
	assert.Nil(t, model, "model must be nil on startup failure")
	require.Error(t, err)

	var exitErr *common.ExitError
	require.True(t, errors.As(err, &exitErr), "error must wrap *common.ExitError")
	assert.Equal(t, common.ErrCodeOther, exitErr.Code)
	assert.Contains(t, err.Error(), "group storage init failed")

	assert.Zero(t, relayConnections.Load(), "no network connection should be attempted on startup failure")
	assert.Same(t, sentinel, storage.DB, "sentinel global DB must remain untouched")
	require.NoError(t, sentinel.Ping(), "sentinel global DB must remain usable")
}

func TestTUIFailClosedOnConflictingView(t *testing.T) {
	home, _, _ := setupTUIStartupEnv(t)
	setupTUIConflictingViewDB(t, home)

	var relayConnections atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		relayConnections.Add(1)
	}))
	defer server.Close()
	relayURL := "ws" + strings.TrimPrefix(server.URL, "http")

	sentinel, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	defer sentinel.Close()
	prevDB := storage.DB
	storage.DB = sentinel
	t.Cleanup(func() { storage.DB = prevDB })

	model, err := NewChatModel("bob", relayURL)
	assert.Nil(t, model)
	require.Error(t, err)

	var exitErr *common.ExitError
	require.True(t, errors.As(err, &exitErr))
	assert.Equal(t, common.ErrCodeOther, exitErr.Code)
	assert.Contains(t, err.Error(), "group storage init failed")

	assert.Zero(t, relayConnections.Load())
	assert.Same(t, sentinel, storage.DB)
	require.NoError(t, sentinel.Ping())
}

func TestTUIFailClosedOnConflictingTable(t *testing.T) {
	home, _, _ := setupTUIStartupEnv(t)
	setupTUIConflictingTableDB(t, home)

	var relayConnections atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		relayConnections.Add(1)
	}))
	defer server.Close()
	relayURL := "ws" + strings.TrimPrefix(server.URL, "http")

	sentinel, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	defer sentinel.Close()
	prevDB := storage.DB
	storage.DB = sentinel
	t.Cleanup(func() { storage.DB = prevDB })

	model, err := NewChatModel("bob", relayURL)
	assert.Nil(t, model)
	require.Error(t, err)

	var exitErr *common.ExitError
	require.True(t, errors.As(err, &exitErr))
	assert.Equal(t, common.ErrCodeOther, exitErr.Code)
	assert.Contains(t, err.Error(), "group storage init failed")

	assert.Zero(t, relayConnections.Load())
	assert.Same(t, sentinel, storage.DB)
	require.NoError(t, sentinel.Ping())
}

func TestTUIHealthyStartupCreatesGroupIndexesAndRetainsStore(t *testing.T) {
	home, _, _ := setupTUIStartupEnv(t)

	sentinel, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	defer sentinel.Close()
	prevDB := storage.DB
	storage.DB = sentinel
	t.Cleanup(func() { storage.DB = prevDB })

	model, err := NewChatModel("bob")
	require.NoError(t, err)
	require.NotNil(t, model)
	assert.NotNil(t, model.GroupStore(), "ChatModel must retain borrowed groupchat.Store handle")
	assert.Same(t, sentinel, storage.DB, "NewChatModel must not overwrite global storage.DB")

	require.NoError(t, model.Close())
	assert.Same(t, sentinel, storage.DB, "closing model must not overwrite global storage.DB")
	require.NoError(t, sentinel.Ping(), "closing model must not close global storage.DB")

	// Verify group indexes were created on healthy startup
	dbPath := filepath.Join(home, ".hyphae", "messages.db")
	db, err := sql.Open("sqlite", dbPath)
	require.NoError(t, err)
	defer db.Close()

	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='index'`)
	require.NoError(t, err)
	defer rows.Close()

	indexes := make(map[string]bool)
	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name))
		indexes[name] = true
	}
	assert.True(t, indexes["idx_groupchat_invites_state"])
	assert.True(t, indexes["idx_groupchat_messages_order"])
	assert.True(t, indexes["idx_groupchat_fanout_pending"])
}

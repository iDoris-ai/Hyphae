package daemon

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"github.com/gorilla/websocket"
	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/internal/groupchat"
	"github.com/iDoris-ai/hyphae/internal/identity"
	"github.com/iDoris-ai/hyphae/internal/messaging"
	"github.com/iDoris-ai/hyphae/internal/storage"
	"github.com/iDoris-ai/hyphae/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type groupSelectionFixture struct {
	home       string
	identity   *types.Identity
	sk         nostr.SecretKey
	store      *groupchat.Store
	db         *sql.DB
	entry      types.OutboxEntry
	recipient  string
	outboxPath string
}

func setupGroupSelectionHome(t *testing.T) (string, *types.Identity, nostr.SecretKey) {
	t.Helper()
	messaging.ResetStoreForTest()
	home := t.TempDir()
	t.Setenv("HOME", home)
	ks := &types.KeyStore{Identities: make(map[string]*types.Identity), Contacts: make(map[string]*types.Contact)}
	id, err := identity.CreateIdentity(ks, "selector-alice")
	require.NoError(t, err)
	require.NoError(t, identity.SaveKeyStore(ks))
	sk, err := identity.GetSecretKey(ks, id.Nickname)
	require.NoError(t, err)
	t.Cleanup(func() { messaging.ResetStoreForTest() })
	return home, id, sk
}

func openOwnedGroupStore(t *testing.T) (*sql.DB, *groupchat.Store) {
	t.Helper()
	db, err := storage.OpenDB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	store, err := groupchat.NewStore(db)
	require.NoError(t, err)
	return db, store
}

func opaqueHex(t *testing.T) string {
	t.Helper()
	var b [16]byte
	_, err := rand.Read(b[:])
	require.NoError(t, err)
	return hex.EncodeToString(b[:])
}

func seedQueuedGroupFanout(t *testing.T, db *sql.DB, localNpub, recipient, eventID, eventJSON, queueID string, maxRetries int) {
	t.Helper()
	now := time.Now().Unix()
	_, err := db.Exec(`INSERT INTO groupchat_fanout
		(local_npub,group_id,envelope_type,send_key,recipient_npub,event_id,event_json,queue_id,state,max_retries,retry_count,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?,?, ?,0,?,?)`,
		localNpub, opaqueHex(t), "message", opaqueHex(t), recipient, eventID, eventJSON, queueID,
		string(groupchat.RecipientQueued), maxRetries, now, now)
	require.NoError(t, err)
}

func newGroupSelectionFixture(t *testing.T, relays []string, maxRetries int) groupSelectionFixture {
	t.Helper()
	home, id, sk := setupGroupSelectionHome(t)
	db, store := openOwnedGroupStore(t)
	recipientSK := nostr.Generate()
	recipient := common.EncodeNpub(recipientSK.Public())
	event, err := messaging.BuildAgentMessageEvent(sk, recipientSK.Public(), "group retry body", nostr.Now())
	require.NoError(t, err)
	eventJSON, err := json.Marshal(event)
	require.NoError(t, err)
	entry, _, err := messaging.EnqueueGroupOutboxEntry(string(eventJSON), recipient, append([]string(nil), relays...), maxRetries)
	require.NoError(t, err)
	seedQueuedGroupFanout(t, db, id.Npub, recipient, entry.ID, string(eventJSON), entry.QueueID, maxRetries)
	path, err := messaging.GetOutboxPath()
	require.NoError(t, err)
	return groupSelectionFixture{
		home: home, identity: id, sk: sk, store: store, db: db,
		entry: entry, recipient: recipient, outboxPath: path,
	}
}

func startGroupAcceptRelay(t *testing.T) (string, <-chan string, *atomic.Int32) {
	t.Helper()
	var accepts atomic.Int32
	eventIDs := make(chan string, 8)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			_, payload, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var envelope []json.RawMessage
			if json.Unmarshal(payload, &envelope) != nil || len(envelope) < 2 {
				continue
			}
			var command string
			if json.Unmarshal(envelope[0], &command) != nil || command != "EVENT" {
				continue
			}
			var event nostr.Event
			if json.Unmarshal(envelope[1], &event) != nil {
				continue
			}
			select {
			case eventIDs <- event.ID.Hex():
			default:
			}
			accepts.Add(1)
			_ = conn.WriteJSON([]any{"OK", event.ID.Hex(), true, "group selection test relay"})
		}
	}))
	t.Cleanup(server.Close)
	return "ws" + strings.TrimPrefix(server.URL, "http"), eventIDs, &accepts
}

func bindAttempt(t *testing.T, f groupSelectionFixture) messaging.GroupAttemptHandler {
	t.Helper()
	h, err := groupchat.NewFanoutAttemptHandlerFor(f.store, f.identity.Npub)
	require.NoError(t, err)
	return h
}

func TestProcessOutboxSelectsGroupPendingAndPublishesThroughBoundHandler(t *testing.T) {
	relayURL, eventIDs, accepts := startGroupAcceptRelay(t)
	f := newGroupSelectionFixture(t, []string{relayURL}, 3)
	handler := bindAttempt(t, f)

	captureStdout(t, func() {
		processOutbox(context.Background(), f.identity, []string{relayURL}, handler)
	})

	require.EqualValues(t, 1, accepts.Load(), "group_pending must be selected and published")
	gotID := receiveGroupEventID(t, eventIDs)
	require.Equal(t, f.entry.ID, gotID)

	outbox, err := messaging.LoadOutbox()
	require.NoError(t, err)
	require.Empty(t, messaging.GetPendingGroupOutbox(outbox), "accepted group entry must leave pending selection")
	for _, entry := range outbox.Entries {
		require.NotEqual(t, f.entry.ID, entry.ID, "accepted group outbox entry must be cleaned by O2")
	}
	var state string
	require.NoError(t, f.db.QueryRow(`SELECT state FROM groupchat_fanout WHERE event_id=?`, f.entry.ID).Scan(&state))
	assert.Equal(t, string(groupchat.RecipientRelayAccepted), state)
}

func TestProcessOutboxGroupMissingHandlerFailClosed(t *testing.T) {
	relayURL, _, accepts := startGroupAcceptRelay(t)
	f := newGroupSelectionFixture(t, []string{relayURL}, 3)
	before, err := os.ReadFile(f.outboxPath)
	require.NoError(t, err)

	captureStdout(t, func() {
		processOutbox(context.Background(), f.identity, []string{relayURL}, nil)
	})

	require.Zero(t, accepts.Load(), "missing Attempt handler must not publish")
	after, err := os.ReadFile(f.outboxPath)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after), "missing handler must not mutate outbox")
	require.Len(t, messaging.GetPendingGroupOutbox(mustLoadOutbox(t)), 1)
}

func TestProcessOutboxWrongIdentityGroupRefusesWithoutMutation(t *testing.T) {
	relayURL, _, accepts := startGroupAcceptRelay(t)
	f := newGroupSelectionFixture(t, []string{relayURL}, 3)
	before, err := os.ReadFile(f.outboxPath)
	require.NoError(t, err)
	rowBefore := fanoutRowBytes(t, f.db, f.entry.ID)

	foreignSK := nostr.Generate()
	foreignNpub := common.EncodeNpub(foreignSK.Public())
	foreignHandler, err := groupchat.NewFanoutAttemptHandlerFor(f.store, foreignNpub)
	require.NoError(t, err)

	captureStdout(t, func() {
		processOutbox(context.Background(), &types.Identity{Nickname: "foreign", Npub: foreignNpub}, []string{relayURL}, foreignHandler)
	})

	require.Zero(t, accepts.Load())
	after, err := os.ReadFile(f.outboxPath)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after))
	assert.Equal(t, rowBefore, fanoutRowBytes(t, f.db, f.entry.ID))
}

func TestProcessOutboxGroupBackoffSkipsRecentAttempt(t *testing.T) {
	relayURL, _, accepts := startGroupAcceptRelay(t)
	f := newGroupSelectionFixture(t, []string{relayURL}, 5)
	patchGroupOutboxEntryRaw(t, f.entry.ID, func(entry map[string]any) {
		entry["retry_count"] = 2
		entry["last_attempt"] = time.Now().Unix() - 1
	})
	before, err := os.ReadFile(f.outboxPath)
	require.NoError(t, err)

	captureStdout(t, func() {
		processOutbox(context.Background(), f.identity, []string{relayURL}, bindAttempt(t, f))
	})

	require.Zero(t, accepts.Load(), "RetryCount=2 LastAttempt=now-1 must skip under backoff")
	after, err := os.ReadFile(f.outboxPath)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after))
}

func TestProcessOutboxGroupFailedNotAutomaticallyRequeued(t *testing.T) {
	relayURL, _, accepts := startGroupAcceptRelay(t)
	f := newGroupSelectionFixture(t, []string{relayURL}, 3)
	patchGroupOutboxEntryRaw(t, f.entry.ID, func(entry map[string]any) {
		entry["status"] = messaging.OutboxStatusGroupFailed
		entry["retry_count"] = entry["max_retries"]
	})
	require.Empty(t, messaging.GetPendingGroupOutbox(mustLoadOutbox(t)))

	captureStdout(t, func() {
		processOutbox(context.Background(), f.identity, []string{relayURL}, bindAttempt(t, f))
	})

	require.Zero(t, accepts.Load())
	outbox := mustLoadOutbox(t)
	require.Len(t, outbox.Entries, 1)
	assert.Equal(t, messaging.OutboxStatusGroupFailed, outbox.Entries[0].Status)
}

func TestProcessOutboxGroupSuccessLeavesConcurrentDMBytesUnchanged(t *testing.T) {
	relayURL, eventIDs, accepts := startGroupAcceptRelay(t)
	home, id, sk := setupGroupSelectionHome(t)
	_ = home
	db, store := openOwnedGroupStore(t)

	// Signed DM fixture retained under backoff so strict outbox scan accepts it,
	// while daemonOutboxRetryDue skips the publish attempt.
	dmPeerSK := nostr.Generate()
	dmEvent, err := messaging.BuildAgentMessageEvent(sk, dmPeerSK.Public(), "dm retained", nostr.Now())
	require.NoError(t, err)
	dmJSON, err := json.Marshal(dmEvent)
	require.NoError(t, err)
	dmEntry := types.OutboxEntry{
		ID: dmEvent.ID.Hex(), Status: "pending", EventJSON: string(dmJSON),
		RecipientNpub: common.EncodeNpub(dmPeerSK.Public()), RetryCount: 2, MaxRetries: 10,
		LastAttempt: time.Now().Unix() - 1, CreatedAt: time.Now().Unix(),
	}
	require.NoError(t, messaging.SaveOutbox(&types.Outbox{Entries: []types.OutboxEntry{dmEntry}}))

	recipientSK := nostr.Generate()
	recipient := common.EncodeNpub(recipientSK.Public())
	event, err := messaging.BuildAgentMessageEvent(sk, recipientSK.Public(), "group retry body", nostr.Now())
	require.NoError(t, err)
	eventJSON, err := json.Marshal(event)
	require.NoError(t, err)
	entry, _, err := messaging.EnqueueGroupOutboxEntry(string(eventJSON), recipient, []string{relayURL}, 3)
	require.NoError(t, err)
	seedQueuedGroupFanout(t, db, id.Npub, recipient, entry.ID, string(eventJSON), entry.QueueID, 3)
	f := groupSelectionFixture{identity: id, sk: sk, store: store, db: db, entry: entry, recipient: recipient}
	handler := bindAttempt(t, f)

	before := mustLoadOutbox(t)
	var dmBefore types.OutboxEntry
	for _, e := range before.Entries {
		if e.ID == dmEntry.ID {
			dmBefore = e
		}
	}
	dmBeforeJSON, err := json.Marshal(dmBefore)
	require.NoError(t, err)

	captureStdout(t, func() {
		processOutbox(context.Background(), f.identity, []string{relayURL}, handler)
	})
	require.Equal(t, f.entry.ID, receiveGroupEventID(t, eventIDs))
	require.EqualValues(t, 1, accepts.Load())

	after := mustLoadOutbox(t)
	var dmAfter types.OutboxEntry
	found := false
	for _, e := range after.Entries {
		if e.ID == dmEntry.ID {
			dmAfter = e
			found = true
		}
	}
	require.True(t, found, "DM pending entry must be retained")
	dmAfterJSON, err := json.Marshal(dmAfter)
	require.NoError(t, err)
	assert.JSONEq(t, string(dmBeforeJSON), string(dmAfterJSON), "group retry must not mutate concurrent DM entry bytes")

	msgStore := storage.NewMessageStore(f.db)
	stored, err := msgStore.GetMessage(f.entry.ID)
	require.NoError(t, err)
	assert.Nil(t, stored, "group retry must not insert into messages history")
}

func TestProcessOutboxGroupOnlyPendingIsSelectedWhenDMEmpty(t *testing.T) {
	// Regression: baseline returned early when GetPendingOutbox was empty,
	// skipping group_pending entirely. This must select group-only queues.
	relayURL, eventIDs, accepts := startGroupAcceptRelay(t)
	f := newGroupSelectionFixture(t, []string{relayURL}, 3)
	require.Empty(t, messaging.GetPendingOutbox(mustLoadOutbox(t)))
	require.Len(t, messaging.GetPendingGroupOutbox(mustLoadOutbox(t)), 1)

	captureStdout(t, func() {
		processOutbox(context.Background(), f.identity, []string{relayURL}, bindAttempt(t, f))
	})
	require.EqualValues(t, 1, accepts.Load())
	require.Equal(t, f.entry.ID, receiveGroupEventID(t, eventIDs))
}

func patchGroupOutboxEntryRaw(t *testing.T, eventID string, mutate func(map[string]any)) {
	t.Helper()
	path, err := messaging.GetOutboxPath()
	require.NoError(t, err)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var doc map[string]any
	require.NoError(t, json.Unmarshal(data, &doc))
	entries, ok := doc["entries"].([]any)
	require.True(t, ok)
	found := false
	for i, raw := range entries {
		entry, ok := raw.(map[string]any)
		require.True(t, ok)
		if entry["id"] != eventID {
			continue
		}
		mutate(entry)
		entries[i] = entry
		found = true
	}
	require.True(t, found)
	doc["entries"] = entries
	out, err := json.Marshal(doc)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, out, 0o600))
}

func mustLoadOutbox(t *testing.T) *types.Outbox {
	t.Helper()
	ob, err := messaging.LoadOutbox()
	require.NoError(t, err)
	return ob
}

func fanoutRowBytes(t *testing.T, db *sql.DB, eventID string) string {
	t.Helper()
	var local, groupID, state, queueID, eventJSON string
	var retryCount int
	require.NoError(t, db.QueryRow(`SELECT local_npub,group_id,state,queue_id,event_json,retry_count FROM groupchat_fanout WHERE event_id=?`, eventID).
		Scan(&local, &groupID, &state, &queueID, &eventJSON, &retryCount))
	raw, err := json.Marshal(map[string]any{
		"local": local, "group": groupID, "state": state, "queue": queueID, "event": eventJSON, "retry": retryCount,
	})
	require.NoError(t, err)
	return string(raw)
}

func receiveGroupEventID(t *testing.T, eventIDs <-chan string) string {
	t.Helper()
	select {
	case id := <-eventIDs:
		return id
	case <-time.After(8 * time.Second):
		t.Fatal("timed out waiting for group relay EVENT")
		return ""
	}
}

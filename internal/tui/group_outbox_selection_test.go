package tui

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/internal/groupchat"
	"github.com/iDoris-ai/hyphae/internal/identity"
	"github.com/iDoris-ai/hyphae/internal/messaging"
	"github.com/iDoris-ai/hyphae/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func opaqueHexTUI(t *testing.T) string {
	t.Helper()
	var b [16]byte
	_, err := rand.Read(b[:])
	require.NoError(t, err)
	return hex.EncodeToString(b[:])
}

func seedTUIGroupFanout(t *testing.T, db *sql.DB, localNpub, recipient, eventID, eventJSON, queueID string, maxRetries int) {
	t.Helper()
	now := time.Now().Unix()
	_, err := db.Exec(`INSERT INTO groupchat_fanout
		(local_npub,group_id,envelope_type,send_key,recipient_npub,event_id,event_json,queue_id,state,max_retries,retry_count,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,0,?,?)`,
		localNpub, opaqueHexTUI(t), "message", opaqueHexTUI(t), recipient, eventID, eventJSON, queueID,
		string(groupchat.RecipientQueued), maxRetries, now, now)
	require.NoError(t, err)
}

func patchTUIGroupOutboxRaw(t *testing.T, eventID string, mutate func(map[string]any)) {
	t.Helper()
	path, err := messaging.GetOutboxPath()
	require.NoError(t, err)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var doc map[string]any
	require.NoError(t, json.Unmarshal(data, &doc))
	entries := doc["entries"].([]any)
	found := false
	for i, raw := range entries {
		entry := raw.(map[string]any)
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

func setupTUIGroupModel(t *testing.T, relayURL string) (*ChatModel, *types.Identity, nostr.SecretKey, string, types.OutboxEntry) {
	t.Helper()
	cleanup := setupTestEnv(t)
	t.Cleanup(cleanup)

	ks, err := identity.LoadKeyStore()
	require.NoError(t, err)
	id, err := identity.GetIdentity(ks, "")
	require.NoError(t, err)
	sk, err := identity.GetSecretKey(ks, id.Nickname)
	require.NoError(t, err)
	peer, err := identity.CreateIdentity(ks, "tui-group-peer")
	require.NoError(t, err)
	require.NoError(t, identity.AddContact(ks, peer.Nickname, peer.Npub))

	model, err := NewChatModel(peer.Nickname, relayURL)
	require.NoError(t, err)
	t.Cleanup(func() { _ = model.Close() })

	recipientSK := nostr.Generate()
	recipient := common.EncodeNpub(recipientSK.Public())
	event, err := messaging.BuildAgentMessageEvent(sk, recipientSK.Public(), "tui group body", nostr.Now())
	require.NoError(t, err)
	eventJSON, err := json.Marshal(event)
	require.NoError(t, err)
	entry, _, err := messaging.EnqueueGroupOutboxEntry(string(eventJSON), recipient, []string{relayURL}, 3)
	require.NoError(t, err)
	seedTUIGroupFanout(t, model.db, id.Npub, recipient, entry.ID, string(eventJSON), entry.QueueID, 3)
	return model, id, sk, recipient, entry
}

func TestTUIRetryPendingGroupOutboxPublishesWithoutDMLeakage(t *testing.T) {
	var accept atomic.Bool
	accept.Store(true)
	relayURL, eventIDs := startTUIOutboxRelay(t, &accept, nil)
	model, _, _, _, entry := setupTUIGroupModel(t, relayURL)

	msgCountBefore := countMessages(t, model.db)
	model.retryPendingGroupOutbox(context.Background())

	require.Equal(t, entry.ID, receiveTUIEventID(t, eventIDs))
	assert.Equal(t, msgCountBefore, countMessages(t, model.db), "group retry must not insert into messages")

	select {
	case update := <-model.outboxUpdates:
		t.Fatalf("group retry must not publish DM delivery update: %+v", update)
	case <-time.After(100 * time.Millisecond):
	}

	outbox, err := messaging.LoadOutbox()
	require.NoError(t, err)
	require.Empty(t, messaging.GetPendingGroupOutbox(outbox))
}

func TestTUIRetryPendingOutboxSelectsGroupWhenDMEmpty(t *testing.T) {
	var accept atomic.Bool
	accept.Store(true)
	relayURL, eventIDs := startTUIOutboxRelay(t, &accept, nil)
	model, _, _, _, entry := setupTUIGroupModel(t, relayURL)
	require.Empty(t, messaging.GetPendingOutbox(mustLoadTUIOutbox(t)))
	require.Len(t, messaging.GetPendingGroupOutbox(mustLoadTUIOutbox(t)), 1)

	model.retryPendingOutbox(context.Background())
	require.Equal(t, entry.ID, receiveTUIEventID(t, eventIDs))
}

func TestTUIRetryPendingGroupOutboxBackoffSkips(t *testing.T) {
	var accept atomic.Bool
	accept.Store(true)
	relayURL, eventIDs := startTUIOutboxRelay(t, &accept, nil)
	model, _, _, _, entry := setupTUIGroupModel(t, relayURL)
	patchTUIGroupOutboxRaw(t, entry.ID, func(e map[string]any) {
		e["retry_count"] = 2
		e["last_attempt"] = time.Now().Unix() - 1
	})
	path, err := messaging.GetOutboxPath()
	require.NoError(t, err)
	before, err := os.ReadFile(path)
	require.NoError(t, err)

	model.retryPendingGroupOutbox(context.Background())

	select {
	case id := <-eventIDs:
		t.Fatalf("backoff must skip publish, got %s", id)
	case <-time.After(200 * time.Millisecond):
	}
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after))
}

func TestTUIRetryPendingGroupOutboxMissingHandlerFailClosed(t *testing.T) {
	var accept atomic.Bool
	accept.Store(true)
	relayURL, eventIDs := startTUIOutboxRelay(t, &accept, nil)
	model, _, _, _, _ := setupTUIGroupModel(t, relayURL)
	model.groupAttempt = nil
	path, err := messaging.GetOutboxPath()
	require.NoError(t, err)
	before, err := os.ReadFile(path)
	require.NoError(t, err)

	model.retryPendingGroupOutbox(context.Background())

	select {
	case id := <-eventIDs:
		t.Fatalf("missing handler must not publish, got %s", id)
	case <-time.After(200 * time.Millisecond):
	}
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after))
}

func TestTUIRetryPendingGroupOutboxWrongIdentityFailClosed(t *testing.T) {
	var accept atomic.Bool
	accept.Store(true)
	relayURL, eventIDs := startTUIOutboxRelay(t, &accept, nil)
	model, _, _, _, entry := setupTUIGroupModel(t, relayURL)
	foreign := common.EncodeNpub(nostr.Generate().Public())
	handler, err := groupchat.NewFanoutAttemptHandlerFor(model.groupStore, foreign)
	require.NoError(t, err)
	model.groupAttempt = handler
	path, err := messaging.GetOutboxPath()
	require.NoError(t, err)
	before, err := os.ReadFile(path)
	require.NoError(t, err)

	model.retryPendingGroupOutbox(context.Background())

	select {
	case id := <-eventIDs:
		t.Fatalf("wrong identity must not publish, got %s", id)
	case <-time.After(200 * time.Millisecond):
	}
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after))
	require.Len(t, messaging.GetPendingGroupOutbox(mustLoadTUIOutbox(t)), 1)
	_ = entry
}

func TestTUIRetryPendingGroupFailedNotRequeued(t *testing.T) {
	var accept atomic.Bool
	accept.Store(true)
	relayURL, eventIDs := startTUIOutboxRelay(t, &accept, nil)
	model, _, _, _, entry := setupTUIGroupModel(t, relayURL)
	patchTUIGroupOutboxRaw(t, entry.ID, func(e map[string]any) {
		e["status"] = messaging.OutboxStatusGroupFailed
		e["retry_count"] = e["max_retries"]
	})
	require.Empty(t, messaging.GetPendingGroupOutbox(mustLoadTUIOutbox(t)))

	model.retryPendingGroupOutbox(context.Background())
	select {
	case id := <-eventIDs:
		t.Fatalf("group_failed must not publish, got %s", id)
	case <-time.After(200 * time.Millisecond):
	}
	outbox := mustLoadTUIOutbox(t)
	require.Len(t, outbox.Entries, 1)
	assert.Equal(t, messaging.OutboxStatusGroupFailed, outbox.Entries[0].Status)
}

func TestTUIRetryPendingGroupLeavesConcurrentDMUnchanged(t *testing.T) {
	var accept atomic.Bool
	accept.Store(true)
	relayURL, eventIDs := startTUIOutboxRelay(t, &accept, nil)

	cleanup := setupTestEnv(t)
	t.Cleanup(cleanup)
	ks, err := identity.LoadKeyStore()
	require.NoError(t, err)
	id, err := identity.GetIdentity(ks, "")
	require.NoError(t, err)
	sk, err := identity.GetSecretKey(ks, id.Nickname)
	require.NoError(t, err)
	peer, err := identity.CreateIdentity(ks, "tui-dm-peer")
	require.NoError(t, err)
	require.NoError(t, identity.AddContact(ks, peer.Nickname, peer.Npub))

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

	model, err := NewChatModel(peer.Nickname, relayURL)
	require.NoError(t, err)
	t.Cleanup(func() { _ = model.Close() })

	recipientSK := nostr.Generate()
	recipient := common.EncodeNpub(recipientSK.Public())
	event, err := messaging.BuildAgentMessageEvent(sk, recipientSK.Public(), "tui group body", nostr.Now())
	require.NoError(t, err)
	eventJSON, err := json.Marshal(event)
	require.NoError(t, err)
	entry, _, err := messaging.EnqueueGroupOutboxEntry(string(eventJSON), recipient, []string{relayURL}, 3)
	require.NoError(t, err)
	seedTUIGroupFanout(t, model.db, id.Npub, recipient, entry.ID, string(eventJSON), entry.QueueID, 3)

	before := mustLoadTUIOutbox(t)
	var dmBefore types.OutboxEntry
	for _, e := range before.Entries {
		if e.ID == dmEntry.ID {
			dmBefore = e
		}
	}
	dmBeforeJSON, err := json.Marshal(dmBefore)
	require.NoError(t, err)

	model.retryPendingGroupOutbox(context.Background())
	require.Equal(t, entry.ID, receiveTUIEventID(t, eventIDs))

	after := mustLoadTUIOutbox(t)
	var dmAfter types.OutboxEntry
	found := false
	for _, e := range after.Entries {
		if e.ID == dmEntry.ID {
			dmAfter = e
			found = true
		}
	}
	require.True(t, found)
	dmAfterJSON, err := json.Marshal(dmAfter)
	require.NoError(t, err)
	assert.JSONEq(t, string(dmBeforeJSON), string(dmAfterJSON))
}

func mustLoadTUIOutbox(t *testing.T) *types.Outbox {
	t.Helper()
	ob, err := messaging.LoadOutbox()
	require.NoError(t, err)
	return ob
}

func countMessages(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	err := db.QueryRow(`SELECT COUNT(*) FROM messages`).Scan(&n)
	if err != nil {
		// messages table may be absent until first InitDB path; treat as zero
		return 0
	}
	return n
}

package daemon

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"github.com/gorilla/websocket"
	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/internal/identity"
	"github.com/iDoris-ai/hyphae/internal/messaging"
	"github.com/iDoris-ai/hyphae/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureStdout runs fn with os.Stdout redirected to a pipe and returns
// everything written to it -- processOutbox prints directly rather than
// taking an io.Writer, so this is the smallest way to assert on what it
// actually reported for a given outbox state.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w

	fn()

	require.NoError(t, w.Close())
	os.Stdout = orig

	out, err := io.ReadAll(r)
	require.NoError(t, err)
	return string(out)
}

// TestProcessOutbox_SkipsDuplicateIDEntriesWithoutMutating covers a Codex
// review finding on specs/m1.5/tasks/08-daemon-outbox-diagnostics.md: the
// original duplicate-ID fix only guarded the manual `storage outbox retry`
// CLI command, but the daemon's automatic retry loop calls
// messaging.AttemptSend directly with no awareness of its own -- a
// successful send would have deleted every entry sharing that ID via
// RemoveFromOutbox, not just the one processed. The guard now lives inside
// AttemptSend itself, so this exercises it through processOutbox (the
// actual daemon code path), not just through AttemptSend directly.
//
// A follow-up Codex round correctly pointed out the first version of this
// test used empty EventJSON, so it "passed" even without the fix -- an
// empty/invalid EventJSON hits the pre-existing parse-error skip regardless
// of the new guard, proving nothing about it specifically. This version
// uses a valid, parseable event and asserts on the specific "share this ID"
// warning text, which only the new guard produces.
func TestProcessOutbox_SkipsDuplicateIDEntriesWithoutMutating(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	event := &nostr.Event{Kind: 1, Content: "hi"}
	event.ID = [32]byte{9}
	eventJSON, err := json.Marshal(event)
	require.NoError(t, err)

	entry := types.OutboxEntry{
		ID: string(event.ID[:]), Status: "pending", EventJSON: string(eventJSON),
		RetryCount: 0, MaxRetries: 10,
	}
	ob := &types.Outbox{Entries: []types.OutboxEntry{entry, entry}}
	require.NoError(t, messaging.SaveOutbox(ob))

	out := captureStdout(t, func() {
		processOutbox(context.Background(), &types.Identity{Nickname: "test"}, []string{"ws://127.0.0.1:1"})
	})

	assert.Contains(t, out, "share this ID",
		"processOutbox must surface the new duplicate-ID guard specifically, not just some other unrelated skip path")

	ob2, err := messaging.LoadOutbox()
	require.NoError(t, err)
	assert.Len(t, ob2.Entries, 2, "duplicate-ID entries must be left untouched, not deleted or corrupted")
}

func TestSeenSet_HasAndAdd(t *testing.T) {
	seen := newSeenSet()
	assert.False(t, seen.Has("a"))

	seen.Add("a")
	assert.True(t, seen.Has("a"))
	assert.False(t, seen.Has("b"))
}

// TestSeenSet_EvictsOldestOnOverflow covers the bounded-FIFO eviction
// behavior: once the set exceeds maxSeenMessages, it must drop the oldest
// entries rather than growing unboundedly or panicking.
func TestSeenSet_EvictsOldestOnOverflow(t *testing.T) {
	seen := newSeenSet()
	for i := 0; i < maxSeenMessages+100; i++ {
		seen.Add(fmt.Sprintf("id-%d", i))
	}

	assert.False(t, seen.Has("id-0"), "the oldest entries must have been evicted")
	assert.True(t, seen.Has(fmt.Sprintf("id-%d", maxSeenMessages+99)), "the most recent entry must still be present")
}

func TestSafePrefix(t *testing.T) {
	assert.Equal(t, "short", safePrefix("short", 10))
	assert.Equal(t, "abc", safePrefix("abcdef", 3))
	assert.Equal(t, "abc", safePrefix("abc", 3))
	assert.Equal(t, "", safePrefix("", 5))
}

// TestShouldAutoReply_AntiLoopGuard is the explicit anti-storm-loop test
// specs/m1.5/tasks/09-nostr-daemon-test-coverage.md calls for: an
// auto-reply must never itself trigger another auto-reply, regardless of
// the other two conditions being true.
func TestShouldAutoReply_AntiLoopGuard(t *testing.T) {
	tests := []struct {
		name             string
		autoReplyEnabled bool
		decryptedOK      bool
		content          string
		want             bool
	}{
		{"ordinary message, all conditions met", true, true, "hello there", true},
		{"auto-reply disabled entirely", false, true, "hello there", false},
		{"undecrypted ciphertext is not eligible", true, false, "hello there", false},
		{
			name: "an auto-reply message must never trigger another auto-reply " +
				"(the anti-storm-loop case)",
			autoReplyEnabled: true, decryptedOK: true,
			content: "[auto-reply] bob received your message: hi", want: false,
		},
		{
			name:             "auto-reply message is rejected even if somehow marked undecrypted too",
			autoReplyEnabled: true, decryptedOK: false,
			content: "[auto-reply] bob received your message: hi", want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, shouldAutoReply(tt.autoReplyEnabled, tt.decryptedOK, tt.content))
		})
	}
}

// TestBuildAutoReplyText_IsRecognizedAsAutoReply proves the anti-loop
// contract end to end: whatever buildAutoReplyText produces must itself be
// recognized by isAutoReplyMessage, or a daemon replying to its own
// auto-reply (e.g. via a relay echoing it back, or a group context) would
// never be gated out by shouldAutoReply.
func TestBuildAutoReplyText_IsRecognizedAsAutoReply(t *testing.T) {
	text := buildAutoReplyText("alice", "some incoming message")
	assert.True(t, isAutoReplyMessage(text), "buildAutoReplyText's own output must satisfy isAutoReplyMessage")
	assert.Contains(t, text, "alice")
}

func TestCleanupOutbox_RemovesOldSentEntries(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	ob := &types.Outbox{Entries: []types.OutboxEntry{
		{ID: "old-sent", Status: "sent", LastAttempt: 1},
		{ID: "still-pending", Status: "pending"},
	}}
	require.NoError(t, messaging.SaveOutbox(ob))

	assert.NotPanics(t, func() { cleanupOutbox() })

	ob2, err := messaging.LoadOutbox()
	require.NoError(t, err)
	require.Len(t, ob2.Entries, 1)
	assert.Equal(t, "still-pending", ob2.Entries[0].ID)
}

func TestCleanupOutbox_MissingFileIsANoop(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	assert.NotPanics(t, func() { cleanupOutbox() })
}

func TestPreloadRecentSeen_MarksStoredIDsAsSeen(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	messaging.ResetStoreForTest()
	require.NoError(t, messaging.InitStorage())

	senderSK := nostr.Generate()
	mySK := nostr.Generate()
	myNpub := common.EncodeNpub(mySK.Public())

	// StoreIncomingMessage reads the recipient out of the event's "p" tag,
	// not from any argument -- an event with no "p" tag stores against an
	// empty recipient_npub and would never match a query for myNpub.
	event := &nostr.Event{
		Kind: 1, Content: "hi", PubKey: senderSK.Public(),
		Tags: nostr.Tags{{"p", common.PubKeyToHex(mySK.Public())}},
	}
	event.ID = [32]byte{7}
	require.NoError(t, messaging.StoreIncomingMessage(event, "hi", false))

	seen := newSeenSet()
	wantID := hex.EncodeToString(event.ID[:])
	assert.False(t, seen.Has(wantID))

	preloadRecentSeen(seen, myNpub)

	assert.True(t, seen.Has(wantID), "an already-stored incoming event must be pre-marked as seen")
}

// TestSendAutoReply_PublishesAndRecordsOutgoingMessage exercises
// sendAutoReply end to end (against an unreachable relay, so no live
// network is needed) -- construction (encrypt/compress/tag/sign), the
// relay-loop fallback-to-failure path, and the resulting local history
// write all get covered, not just the two pure helpers above.
func TestSendAutoReply_PublishesAndRecordsOutgoingMessage(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	messaging.ResetStoreForTest()
	require.NoError(t, messaging.InitStorage())

	ks := &types.KeyStore{
		Identities: make(map[string]*types.Identity),
		Contacts:   make(map[string]*types.Contact),
	}
	myIdentity, err := identity.CreateIdentity(ks, "alice")
	require.NoError(t, err)

	recipientSK := nostr.Generate()
	toNpub := common.EncodeNpub(recipientSK.Public())

	assert.NotPanics(t, func() {
		sendAutoReply(context.Background(), myIdentity, ks, toNpub, "hello there", []string{"ws://127.0.0.1:1"})
	})

	messages, err := messaging.GetConversation(nil, myIdentity.Npub, toNpub, 10)
	require.NoError(t, err)
	require.Len(t, messages, 1, "the auto-reply must be recorded in local history even though publish failed")
	assert.Contains(t, messages[0].Plaintext, "hello there")
}

// startFakeRelay runs a minimal in-process NIP-01 relay. On the first
// message it receives (the client's REQ), it replies with one pre-built
// EVENT, then EOSE, then blocks until closeWhenReady is closed before
// sending a CLOSED envelope for the same subscription.
//
// Two earlier versions of this were timing-based and both wrong in a
// different way: (1) time.Sleep + closing the connection depended on
// OS/network connection-death detection; (2) sending CLOSED after a fixed
// short sleep was still a race, just a smaller one -- fiatjaf.com/nostr's
// Subscription.dispatchEvent delivers an EVENT asynchronously via
// `select { case sub.Events <- evt: ...; case <-sub.Context.Done(): ... }`,
// and CLOSED cancels sub.Context, so a sleep that's merely "usually long
// enough" can still lose that race under scheduler contention (e.g. CI
// load). closeWhenReady removes the guesswork: the caller only closes it
// once it has confirmed the event was actually processed (e.g. durably
// stored), so CLOSED is never sent before delivery has already happened.
func startFakeRelay(t *testing.T, eventJSON json.RawMessage, closeWhenReady <-chan struct{}) string {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		_, msg, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var req []json.RawMessage
		if err := json.Unmarshal(msg, &req); err != nil || len(req) < 2 {
			return
		}
		var subID string
		_ = json.Unmarshal(req[1], &subID)

		eventMsg, _ := json.Marshal([]any{"EVENT", subID, json.RawMessage(eventJSON)})
		_ = conn.WriteMessage(websocket.TextMessage, eventMsg)
		eoseMsg, _ := json.Marshal([]any{"EOSE", subID})
		_ = conn.WriteMessage(websocket.TextMessage, eoseMsg)

		<-closeWhenReady

		closedMsg, _ := json.Marshal([]any{"CLOSED", subID, "test done"})
		_ = conn.WriteMessage(websocket.TextMessage, closedMsg)
	}))
	t.Cleanup(srv.Close)

	return "ws" + strings.TrimPrefix(srv.URL, "http")
}

// TestWatchInbox_ReceivesEventMarksSeenAndAutoReplies drives watchInbox (and
// through it, watchOneRelay) against a real local relay connection instead
// of testing the extracted helpers in isolation -- this is the one test
// that exercises the actual receive -> decrypt-gate -> auto-reply dispatch
// path end to end, which specs/m1.5/tasks/09-nostr-daemon-test-coverage.md
// calls out as the highest-risk logic in this package.
func TestWatchInbox_ReceivesEventMarksSeenAndAutoReplies(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	messaging.ResetStoreForTest()
	require.NoError(t, messaging.InitStorage())

	ks := &types.KeyStore{
		Identities: make(map[string]*types.Identity),
		Contacts:   make(map[string]*types.Contact),
	}
	myIdentity, err := identity.CreateIdentity(ks, "alice")
	require.NoError(t, err)
	myPK, err := identity.GetPublicKey(ks, "alice")
	require.NoError(t, err)

	senderSK := nostr.Generate()
	plaintext := "hello there"
	compressed, err := messaging.CompressText(plaintext)
	require.NoError(t, err)

	incoming := &nostr.Event{
		CreatedAt: nostr.Now(),
		Kind:      messaging.AgentKind,
		Tags:      nostr.Tags{{"p", common.PubKeyToHex(myPK)}},
		Content:   compressed,
		PubKey:    senderSK.Public(),
	}
	require.NoError(t, incoming.Sign(senderSK))
	eventJSON, err := json.Marshal(incoming)
	require.NoError(t, err)

	closeWhenReady := make(chan struct{})
	releaseFakeRelay := sync.OnceFunc(func() { close(closeWhenReady) })

	relayURL := startFakeRelay(t, eventJSON, closeWhenReady)

	seen := newSeenSet()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// watchInbox blocks until watchOneRelay's sub.Events channel closes,
	// which only happens once the fake relay sends CLOSED -- so it has to
	// run concurrently with the wait below, not before it.
	watchInboxDone := make(chan struct{})
	go func() {
		defer close(watchInboxDone)
		watchInbox(ctx, myIdentity, ks, seen, nostr.Timestamp(0), []string{relayURL}, false, true)
	}()

	// Deferred cleanup, registered so it unwinds in this order (LIFO):
	// releaseFakeRelay() first (unblocks the fake relay's handler so it can
	// send CLOSED), then wait for watchInboxDone (now it can actually
	// finish; also bounded by ctx's own 5s timeout regardless). Needed
	// because require.Eventually below calls t.FailNow() (runtime.Goexit())
	// on failure, which skips every subsequent line in this function --
	// including the explicit releaseFakeRelay() call further down. Without
	// this safety net, the fake relay's handler would stay parked on
	// <-closeWhenReady forever, and t.Cleanup(srv.Close) (which waits for
	// in-flight requests) would hang the whole test run instead of failing
	// cleanly. sync.OnceFunc makes calling releaseFakeRelay twice (here and
	// on the success path below) harmless.
	defer func() { <-watchInboxDone }()
	defer releaseFakeRelay()

	// Confirm the event was actually processed (durably stored -- SQLite is
	// safe for this concurrent read/write, unlike seen, a plain unsynchronized
	// map) before letting the fake relay send CLOSED. This is what makes the
	// test deterministic instead of racing dispatchEvent's async delivery
	// against a guessed sleep duration.
	require.Eventually(t, func() bool {
		inbox, err := messaging.GetInbox(nil, myIdentity.Npub, 10)
		return err == nil && len(inbox) == 1
	}, 2*time.Second, 5*time.Millisecond, "the incoming event must be durably stored before the fake relay is allowed to close the subscription")

	releaseFakeRelay()

	select {
	case <-watchInboxDone:
	case <-time.After(2 * time.Second):
		t.Fatal("watchInbox did not return after the fake relay sent CLOSED")
	}

	// Only safe to read `seen` now that the watchInbox goroutine has fully
	// returned -- it's a plain map with no locking of its own.
	assert.True(t, seen.Has(hex.EncodeToString(incoming.ID[:])), "the event must be marked seen after being processed")

	// The conversation ends up with two messages: the original incoming one
	// (stored by watchOneRelay itself) and the auto-reply sendAutoReply
	// fires off via `go sendAutoReply(...)` -- poll for the second one
	// rather than asserting synchronously, since it's dispatched in its own
	// goroutine.
	senderNpub := common.EncodeNpub(senderSK.Public())
	var convo []types.StoredMessage
	require.Eventually(t, func() bool {
		messages, err := messaging.GetConversation(nil, myIdentity.Npub, senderNpub, 10)
		convo = messages
		return err == nil && len(messages) == 2
	}, 2*time.Second, 20*time.Millisecond,
		"the plaintext (undecrypted, not enc=nip44-tagged) message should have triggered an auto-reply, recorded in local history")

	foundAutoReply := false
	for _, m := range convo {
		if !m.IsIncoming && isAutoReplyMessage(m.Plaintext) {
			foundAutoReply = true
		}
	}
	assert.True(t, foundAutoReply, "the outgoing message in the conversation must be the auto-reply")
}

func TestIsAutoReplyMessage(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"[auto-reply] bob received your message", true},
		{"[auto-reply] alice received your message: hello", true},
		{"Hello, how are you?", false},
		{"", false},
		{"[auto-reply]", false}, // too short
		{"not [auto-reply] prefix", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, isAutoReplyMessage(tt.input))
		})
	}
}

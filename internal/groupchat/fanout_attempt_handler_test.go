package groupchat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"github.com/gorilla/websocket"
	"github.com/iDoris-ai/hyphae/internal/messaging"
	"github.com/iDoris-ai/hyphae/pkg/types"
	"github.com/stretchr/testify/require"
)

func TestFanoutAttemptHandlerRejectsForeignSignedPayload(t *testing.T) {
	for _, pendingO3 := range []bool{false, true} {
		for _, sameSigner := range []bool{false, true} {
			t.Run(map[bool]string{false: "queued", true: "pending O3"}[pendingO3]+map[bool]string{false: " other signer", true: " same signer"}[sameSigner], func(t *testing.T) {
				f := newFailureFixture(t)
				if pendingO3 {
					f.failO3BeforeT4(t, f.startAttempt(t, 1))
				}
				entry := f.typedEntry(t)
				b := nostr.Event{Kind: 30078, CreatedAt: nostr.Now(), Content: "foreign ciphertext"}
				signer := nostr.Generate()
				if sameSigner {
					signer = f.member.sk
				}
				require.NoError(t, b.Sign(signer))
				require.True(t, b.CheckID())
				require.True(t, b.VerifySignature())
				data, err := json.Marshal(b)
				require.NoError(t, err)
				entry.EventJSON = string(data)
				unchanged := f.snapshot(t)
				h := NewFanoutAttemptHandler(f.member.store)
				_, err = h.Reserve(entry)
				require.ErrorIs(t, err, messaging.ErrGroupAttemptPayloadMismatch)
				unchanged()
			})
		}
	}
}

func TestFanoutAttemptHandlerRejectsDifferentBytesOfSameSignedEvent(t *testing.T) {
	f := newFailureFixture(t)
	entry := f.typedEntry(t)
	entry.EventJSON = " " + entry.EventJSON
	var event nostr.Event
	require.NoError(t, json.Unmarshal([]byte(entry.EventJSON), &event))
	require.Equal(t, entry.ID, event.ID.Hex())
	require.True(t, event.CheckID())
	require.True(t, event.VerifySignature())
	unchanged := f.snapshot(t)
	_, err := NewFanoutAttemptHandler(f.member.store).Reserve(entry)
	require.ErrorIs(t, err, messaging.ErrGroupAttemptPayloadMismatch)
	unchanged()
}

func TestFanoutAttemptHandlerReplaysPendingO3ForAuthenticPayload(t *testing.T) {
	f := newFailureFixture(t)
	f.failO3BeforeT4(t, f.startAttempt(t, 1))
	token, err := NewFanoutAttemptHandler(f.member.store).Reserve(f.typedEntry(t))
	require.NoError(t, err)
	require.EqualValues(t, 2, token.AttemptGeneration)
	require.EqualValues(t, 1, f.row(t).RetryCount)
}

func (f failureFixture) typedEntry(t *testing.T) types.OutboxEntry {
	t.Helper()
	return f.outboxEntry(t).Entry
}

// Real end-to-end failure path: AttemptSendRouted → FanoutAttemptHandler →
// R/S behind the barrier → publish to an unreachable relay → O3+T4.
func TestFanoutAttemptHandlerDrivesAttemptSendRouted(t *testing.T) {
	f := newFailureFixture(t)
	handler := NewFanoutAttemptHandler(f.member.store)
	opts := messaging.AttemptOptions{Handlers: messaging.OutboxHandlers{Attempt: handler}}
	relays := []string{"ws://127.0.0.1:1"}
	for round := int64(1); round <= 3; round++ {
		result, err := messaging.AttemptSendRouted(context.Background(), nil, f.typedEntry(t), relays, 200*time.Millisecond, opts)
		require.NoError(t, err, "round %d", round)
		require.True(t, result.Attempted)
		require.False(t, result.Sent)
		row := f.recovery(t)
		require.Equal(t, round, row.RetryCount)
		require.Equal(t, round, row.Generation)
		require.Equal(t, round, row.Attempts)
		if round < 3 {
			require.True(t, result.Queued)
			require.Equal(t, messaging.AgentMessageIssueSendFailed, result.Issue)
			require.Equal(t, string(RecipientQueued), row.State)
		} else {
			require.True(t, result.MarkedFailed)
			require.Equal(t, messaging.AgentMessageIssueRetryExhausted, result.Issue)
			require.Equal(t, string(RecipientFailed), row.State)
		}
		require.NotNil(t, f.outboxEntry(t).FailureResult)
	}
	// Exhausted: the barrier refuses before any publish; no writes.
	unchanged := f.snapshot(t)
	result, err := messaging.AttemptSendRouted(context.Background(), nil, f.typedEntry(t), relays, 200*time.Millisecond, opts)
	require.Error(t, err)
	require.Equal(t, messaging.AgentMessageIssueOutboxBookkeepingFailed, result.Issue)
	require.False(t, result.Attempted)
	unchanged()

	// Operator recovery, then the next attempt runs on the new epoch.
	newQueue, err := f.member.store.authorizeFanoutRecovery(f.key, f.recipient, time.Now().Unix())
	require.NoError(t, err)
	result, err = messaging.AttemptSendRouted(context.Background(), nil, f.typedEntry(t), relays, 200*time.Millisecond, opts)
	require.NoError(t, err)
	require.True(t, result.Queued)
	row := f.recovery(t)
	require.Equal(t, newQueue, row.QueueID)
	require.EqualValues(t, 1, row.RetryCount)
	require.EqualValues(t, 4, row.Generation)
}

func TestFanoutAttemptHandlerAcceptedAndMappings(t *testing.T) {
	f := newFailureFixture(t)
	handler := NewFanoutAttemptHandler(f.member.store)
	entry := f.typedEntry(t)
	token, err := handler.Reserve(entry)
	require.NoError(t, err)
	require.Equal(t, f.eventID, token.EventID)
	require.Equal(t, f.queueID, token.QueueID)
	require.Equal(t, f.recipient, token.Key.RecipientNpub)
	require.Equal(t, f.key.LocalNpub, token.Key.LocalNpub)
	require.EqualValues(t, 1, token.AttemptGeneration)
	require.NoError(t, handler.Start(token, 1, time.Now().Unix()))
	require.NoError(t, handler.Accepted(token, time.Now().Unix()))
	require.Equal(t, string(RecipientRelayAccepted), f.accepted(t).State)

	// Late callbacks map to the stale sentinel.
	_, err = handler.Failed(token)
	require.ErrorIs(t, err, messaging.ErrGroupAttemptStale)
	require.ErrorIs(t, err, errFanoutStaleAttempt, "store chain stays inspectable")
	require.ErrorIs(t, handler.Accepted(token, time.Now().Unix()), messaging.ErrGroupAttemptStale)

	// Recipient / row mismatches are conflicts.
	wrong := entry
	wrong.RecipientNpub = f.key.LocalNpub
	_, err = handler.Reserve(wrong)
	require.ErrorIs(t, err, messaging.ErrGroupAttemptConflict)
	unknown := entry
	unknown.ID = "0000000000000000000000000000000000000000000000000000000000000000"
	_, err = handler.Reserve(unknown)
	require.ErrorIs(t, err, messaging.ErrGroupAttemptConflict)
}

func TestFanoutAttemptHandlerPendingAndQueueMappings(t *testing.T) {
	f := newFailureFixture(t)
	handler := NewFanoutAttemptHandler(f.member.store)
	f.failO3BeforeT4(t, f.startAttempt(t, 1))
	_, err := f.member.db.Exec(`UPDATE groupchat_fanout SET attempts=attempts+1`)
	require.NoError(t, err)
	_, err = handler.Reserve(f.typedEntry(t))
	require.ErrorIs(t, err, messaging.ErrGroupAttemptConflict)

	f2 := newFailureFixture(t)
	h2 := NewFanoutAttemptHandler(f2.member.store)
	_, err = f2.member.db.Exec(`UPDATE groupchat_fanout SET queue_id='ffffffffffffffffffffffffffffffff'`)
	require.NoError(t, err)
	_, err = h2.Reserve(f2.typedEntry(t))
	require.ErrorIs(t, err, messaging.ErrGroupAttemptQueueConflict)

	f3 := newFailureFixture(t)
	h3 := NewFanoutAttemptHandler(f3.member.store)
	entry := f3.typedEntry(t)
	require.NoError(t, messaging.SaveOutbox(&types.Outbox{Entries: []types.OutboxEntry{}}))
	_, err = h3.Reserve(entry)
	require.ErrorIs(t, err, messaging.ErrGroupAttemptQueueMissing)

	require.NoError(t, mapFanoutError(nil))
	require.ErrorIs(t, mapFanoutError(ErrFanoutRecoveryPending), messaging.ErrGroupAttemptPending)
	require.ErrorIs(t, mapFanoutError(ErrFanoutCleanupPending), messaging.ErrGroupAttemptCleanupPending)
	require.ErrorIs(t, mapFanoutError(errors.New("other")), messaging.ErrGroupAttemptConflict)
}

// publishCounter is a localhost relay that counts EVENT frames. It NACKs
// them so a reached publisher returns without waiting out the dial timeout.
type publishCounter struct {
	n   atomic.Int32
	url string
}

func startPublishCounter(t *testing.T) *publishCounter {
	t.Helper()
	counter := &publishCounter{}
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var envelope []json.RawMessage
			if json.Unmarshal(data, &envelope) != nil || len(envelope) < 1 {
				continue
			}
			var command string
			if json.Unmarshal(envelope[0], &command) != nil || command != "EVENT" || len(envelope) < 2 {
				continue
			}
			counter.n.Add(1)
			var event struct {
				ID string `json:"id"`
			}
			if json.Unmarshal(envelope[1], &event) != nil {
				continue
			}
			response, err := json.Marshal([]any{"OK", event.ID, false, "local test relay"})
			if err != nil {
				return
			}
			if err := conn.WriteMessage(websocket.TextMessage, response); err != nil {
				return
			}
		}
	}))
	t.Cleanup(server.Close)
	counter.url = "ws" + strings.TrimPrefix(server.URL, "http")
	return counter
}

// rewriteOwnedOutboxMaxRetries changes only the temp outbox entry's max_retries.
// created_at and the signed event bytes stay as the fixture wrote them.
func rewriteOwnedOutboxMaxRetries(t *testing.T, path string, from, to int) {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	old := []byte(fmt.Sprintf(`"max_retries": %d`, from))
	next := []byte(fmt.Sprintf(`"max_retries": %d`, to))
	require.Equal(t, 1, bytes.Count(data, old), "owned outbox max_retries token")
	edited := bytes.Replace(data, old, next, 1)
	require.Equal(t, bytes.Count(data, []byte(`"created_at"`)), bytes.Count(edited, []byte(`"created_at"`)))
	require.NoError(t, os.WriteFile(path, edited, 0o600))
}

// TestFanoutAttemptHandlerRefusesOutboxMaxRetriesMismatch is the out-of-band
// outbox.json fault: frozen row max_retries stays 3 while the owned temp
// outbox says 1 or 5. Five scheduler picks must refuse before P.
func TestFanoutAttemptHandlerRefusesOutboxMaxRetriesMismatch(t *testing.T) {
	for _, outboxMax := range []int{1, 5} {
		t.Run(fmt.Sprintf("outbox_%d", outboxMax), func(t *testing.T) {
			f := newFailureFixture(t)
			entry := f.outboxEntry(t)
			require.Equal(t, 3, entry.Entry.MaxRetries)
			createdAt := entry.Entry.CreatedAt
			rewriteOwnedOutboxMaxRetries(t, f.outboxPath, 3, outboxMax)
			require.Equal(t, outboxMax, f.outboxEntry(t).Entry.MaxRetries)
			require.Equal(t, createdAt, f.outboxEntry(t).Entry.CreatedAt)
			var rowMax int64
			require.NoError(t, f.member.db.QueryRow(`SELECT max_retries FROM groupchat_fanout WHERE event_id=?`, f.eventID).Scan(&rowMax))
			require.EqualValues(t, 3, rowMax)

			counter := startPublishCounter(t)
			before := f.recovery(t)
			unchanged := f.snapshot(t)
			handler := NewFanoutAttemptHandler(f.member.store)
			opts := messaging.AttemptOptions{Handlers: messaging.OutboxHandlers{Attempt: handler}}
			type pickResult struct {
				attempted, sent bool
				issue           messaging.AgentMessageDeliveryIssue
				err             error
				pubs            int32
			}
			picks := make([]pickResult, 0, 5)
			for pick := 1; pick <= 5; pick++ {
				result, err := messaging.AttemptSendRouted(context.Background(), nil, f.typedEntry(t), []string{counter.url}, 200*time.Millisecond, opts)
				picks = append(picks, pickResult{result.Attempted, result.Sent, result.Issue, err, counter.n.Load()})
			}
			after := f.recovery(t)
			var detail strings.Builder
			for i, pick := range picks {
				fmt.Fprintf(&detail, "pick %d attempted=%v sent=%v issue=%s pubs=%d err=%v\n", i+1, pick.attempted, pick.sent, pick.issue, pick.pubs, pick.err)
			}
			fmt.Fprintf(&detail, "before attempts=%d gen=%d retry=%d state=%s\nafter attempts=%d gen=%d retry=%d state=%s\n",
				before.Attempts, before.Generation, before.RetryCount, before.State,
				after.Attempts, after.Generation, after.RetryCount, after.State)
			require.EqualValues(t, 0, counter.n.Load(), "publisher EVENT count\n%s", detail.String())
			for i, pick := range picks {
				require.Falsef(t, pick.attempted, "pick %d\n%s", i+1, detail.String())
				require.False(t, pick.sent)
				require.Equal(t, messaging.AgentMessageIssueOutboxBookkeepingFailed, pick.issue)
				require.ErrorContains(t, pick.err, fmt.Sprintf("outbox_max=%d", outboxMax))
				require.ErrorContains(t, pick.err, "row_max=3")
				require.NotContains(t, pick.err.Error(), f.typedEntry(t).EventJSON)
				require.NotContains(t, pick.err.Error(), "nsec")
			}
			t.Logf("refusal=%v", picks[0].err)
			unchanged()
			require.Equal(t, before.Attempts, after.Attempts)
			require.Equal(t, before.Generation, after.Generation)
			require.Equal(t, before.RetryCount, after.RetryCount)
			require.Equal(t, before.State, after.State)
			require.Equal(t, createdAt, f.outboxEntry(t).Entry.CreatedAt)
		})
	}
}

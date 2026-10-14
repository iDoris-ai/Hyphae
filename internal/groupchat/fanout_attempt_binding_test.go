package groupchat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"github.com/iDoris-ai/hyphae/internal/messaging"
	"github.com/iDoris-ai/hyphae/pkg/types"
	"github.com/stretchr/testify/require"
)

type countingAttempt struct {
	messaging.GroupAttemptHandler
	reserves, starts *atomic.Int32
}

func (c countingAttempt) Reserve(entry types.OutboxEntry) (messaging.GroupAttemptToken, error) {
	c.reserves.Add(1)
	return c.GroupAttemptHandler.Reserve(entry)
}

func (c countingAttempt) Start(token messaging.GroupAttemptToken, targets int, now int64) error {
	c.starts.Add(1)
	return c.GroupAttemptHandler.Start(token, targets, now)
}

// signedEventB re-signs the fixture's frozen event with a later created_at:
// same sender, recipient and tags, a different fully valid event ID.
func (f failureFixture) signedEventB(t *testing.T, sk nostr.SecretKey) string {
	t.Helper()
	var event nostr.Event
	require.NoError(t, json.Unmarshal([]byte(f.typedEntry(t).EventJSON), &event))
	event.CreatedAt++
	require.NoError(t, event.Sign(sk))
	require.True(t, event.CheckID() && event.VerifySignature())
	require.NotEqual(t, f.eventID, event.ID.Hex())
	raw, err := json.Marshal(event)
	require.NoError(t, err)
	return string(raw)
}

func countingRelay(t *testing.T) (string, *atomic.Int32) {
	var hits atomic.Int32
	relay := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	t.Cleanup(relay.Close)
	return "ws" + strings.TrimPrefix(relay.URL, "http"), &hits
}

// #221 repro on the real production handler: entry A metadata carrying a fully
// valid signed event B must not reserve, start, publish, or mutate SQL/outbox.
func TestFanoutAttemptHandlerRefusesEntryAWithSignedEventB(t *testing.T) {
	for name, signer := range map[string]func(failureFixture) nostr.SecretKey{
		"same sender":    func(f failureFixture) nostr.SecretKey { return f.member.sk },
		"foreign sender": func(failureFixture) nostr.SecretKey { return nostr.Generate() },
	} {
		t.Run(name+" via route", func(t *testing.T) {
			f := newFailureFixture(t)
			entry := f.typedEntry(t)
			entry.EventJSON = f.signedEventB(t, signer(f))
			unchanged := f.snapshot(t)
			url, hits := countingRelay(t)
			var reserves, starts atomic.Int32
			h := countingAttempt{GroupAttemptHandler: NewFanoutAttemptHandler(f.member.store), reserves: &reserves, starts: &starts}
			result, err := messaging.AttemptSendRouted(context.Background(), nil, entry, []string{url}, 200*time.Millisecond,
				messaging.AttemptOptions{Handlers: messaging.OutboxHandlers{Attempt: h}})
			require.Error(t, err)
			require.False(t, result.Attempted)
			require.False(t, result.Sent)
			require.Zero(t, reserves.Load(), "Reserve must not run")
			require.Zero(t, starts.Load(), "Start must not run")
			require.Zero(t, hits.Load(), "relay must not be contacted")
			unchanged()
		})
		t.Run(name+" direct Reserve", func(t *testing.T) {
			f := newFailureFixture(t)
			entry := f.typedEntry(t)
			entry.EventJSON = f.signedEventB(t, signer(f))
			unchanged := f.snapshot(t)
			_, err := NewFanoutAttemptHandler(f.member.store).Reserve(entry)
			require.Error(t, err)
			require.ErrorIs(t, err, messaging.ErrGroupAttemptStale)
			unchanged()
		})
	}
}

// The handler binds identity to the authoritative frozen bytes: a forged row
// sender or a live outbox diverging from the frozen event is a conflict.
func TestFanoutAttemptHandlerBindsFrozenSenderAndLiveBytes(t *testing.T) {
	f := newFailureFixture(t)
	other := newMember(t)
	_, err := f.member.db.Exec(`UPDATE groupchat_fanout SET local_npub=? WHERE event_id=?`, other.npub, f.eventID)
	require.NoError(t, err)
	f.key.LocalNpub = other.npub
	unchanged := f.snapshot(t)
	_, err = NewFanoutAttemptHandler(f.member.store).Reserve(f.typedEntry(t))
	require.ErrorIs(t, err, messaging.ErrGroupAttemptConflict)
	unchanged()

	g := newFailureFixture(t)
	b := g.signedEventB(t, g.member.sk)
	_, err = g.member.db.Exec(`UPDATE groupchat_fanout SET event_json=? WHERE event_id=?`, b, g.eventID)
	require.NoError(t, err)
	unchanged = g.snapshot(t)
	_, err = NewFanoutAttemptHandler(g.member.store).Reserve(g.typedEntry(t))
	require.ErrorIs(t, err, messaging.ErrGroupAttemptConflict)
	unchanged()
}

// Valid control: canonical escaped id/pubkey member names, persisted as the
// frozen bytes in both stores, still reserve on the production handler.
func TestFanoutAttemptHandlerReservesEscapedFrozenEvent(t *testing.T) {
	f := newFailureFixture(t)
	entry := f.typedEntry(t)
	entry.EventJSON = strings.Replace(entry.EventJSON, `"id":`, `"\u0069d":`, 1)
	entry.EventJSON = strings.Replace(entry.EventJSON, `"pubkey":`, `"pub\u006bey":`, 1)
	outbox, err := messaging.LoadOutbox()
	require.NoError(t, err)
	outbox.Entries[0].EventJSON = entry.EventJSON
	require.NoError(t, messaging.SaveOutbox(outbox))
	_, err = f.member.db.Exec(`UPDATE groupchat_fanout SET event_json=? WHERE event_id=?`, entry.EventJSON, entry.ID)
	require.NoError(t, err)
	require.Equal(t, entry.EventJSON, f.outboxEntry(t).Entry.EventJSON)
	token, err := NewFanoutAttemptHandler(f.member.store).Reserve(entry)
	require.NoError(t, err)
	require.Equal(t, entry.ID, token.EventID)
	require.EqualValues(t, 1, token.AttemptGeneration)
}

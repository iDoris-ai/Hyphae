package friend

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"github.com/stretchr/testify/require"

	"github.com/iDoris-ai/hyphae/internal/card"
	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/internal/identity"
	"github.com/iDoris-ai/hyphae/internal/relay"
	"github.com/iDoris-ai/hyphae/internal/relayquery"
	"github.com/iDoris-ai/hyphae/internal/social/contact"
	"github.com/iDoris-ai/hyphae/internal/storage"
	"github.com/iDoris-ai/hyphae/internal/wireevent"
)

// publishCard signs a card for a fresh key at the given time and sends it.
func publishCard(t *testing.T, url string, at nostr.Timestamp, name string, tags ...string) nostr.SecretKey {
	t.Helper()
	sk := nostr.Generate()
	ev, err := card.Card{Name: name, Tags: tags}.Event(sk.Public(), at)
	require.NoError(t, err)
	require.NoError(t, ev.Sign(sk))
	_, ok, _ := publish(context.Background(), []string{url}, []nostr.Event{*ev})
	require.Len(t, ok, 1)
	return sk
}

func names(t *testing.T, data map[string]any) []string {
	t.Helper()
	var out []string
	for _, r := range data["results"].([]any) {
		out = append(out, r.(map[string]any)["name"].(string))
	}
	return out
}

func TestFindRanksAndFilters(t *testing.T) {
	url, _ := setup(t)
	now := nostr.Now()
	publishCard(t, url, now-30, "bob", "友好", "火锅")
	publishCard(t, url, now-20, "carol", "友好")
	frank := publishCard(t, url, now-10, "frank", "火锅")
	dave := publishCard(t, url, now, "dave", "友好")
	erin := publishCard(t, url, now, "erin", "友好")
	tomb := card.Tombstone(erin.Public(), now+1)
	require.NoError(t, tomb.Sign(erin))
	_, ok, _ := publish(context.Background(), []string{url}, []nostr.Event{*tomb})
	require.Len(t, ok, 1)
	_, err := run(t, "visibility", "public", "--as", "alice", "--relay", url)
	require.NoError(t, err)

	ks, err := identity.LoadKeyStore()
	require.NoError(t, err)
	require.NoError(t, identity.AddContact(ks, "frank", common.EncodeNpub(frank.Public())))
	db, err := storage.OpenDB()
	require.NoError(t, err)
	_, err = storage.NewRelationStore(db).Update(ks.Identities["alice"].Npub, common.EncodeNpub(dave.Public()), int64(now),
		func(p storage.PeerRelation) (storage.PeerRelation, error) {
			p.Relation = contact.Block(p.Relation)
			return p, nil
		})
	require.NoError(t, err)
	require.NoError(t, db.Close())

	data, err := run(t, "find", "--as", "alice", "--relay", url, "--tag", "友好", "--tag", "HOTPOT", "--tag", "火锅")
	require.NoError(t, err)
	// bob matches two tags; then by card time. Not alice (self), dave
	// (blocked) or erin (hidden).
	require.Equal(t, []string{"bob", "frank", "carol"}, names(t, data))
	first := data["results"].([]any)[0].(map[string]any)
	require.Equal(t, []any{"友好", "火锅"}, first["matched"])
	require.Equal(t, "stranger", first["relation"])
	require.Equal(t, "friend", data["results"].([]any)[1].(map[string]any)["relation"])

	data, err = run(t, "find", "--as", "alice", "--relay", url, "--limit", "1")
	require.NoError(t, err)
	require.Equal(t, []string{"carol"}, names(t, data), "default tag 友好: bob and carol match one each, carol is newer")
}

func TestFindErrors(t *testing.T) {
	url, _ := setup(t)
	var ee *common.ExitError
	_, err := run(t, "find", "--as", "alice", "--relay", url, "--limit", "0")
	require.True(t, errors.As(err, &ee))
	require.Equal(t, common.ErrCodeUser, ee.Code)
	_, err = run(t, "find", "--as", "alice", "--relay", url, "--limit", "101")
	require.True(t, errors.As(err, &ee))
	require.Equal(t, common.ErrCodeUser, ee.Code)
	_, err = run(t, "find", "--as", "alice", "--relay", url, "--tag", "a b")
	require.True(t, errors.As(err, &ee))
	require.Equal(t, common.ErrCodeInvalidTag, ee.Code)
	_, err = run(t, "find", "--as", "alice", "--relay", "ws://127.0.0.1:1", "--timeout", "1")
	require.True(t, errors.As(err, &ee))
	require.Equal(t, common.ErrCodeNetwork, ee.Code)

	data, err := run(t, "find", "--as", "alice", "--relay", url, "--relay", "ws://127.0.0.1:1", "--timeout", "1")
	require.NoError(t, err, "one relay answering is enough")
	require.Equal(t, []any{"ws://127.0.0.1:1"}, data["failed_relays"])
	require.Empty(t, data["results"])
}

// Only one author's events are compared by time; an invalid newest card
// hides the author rather than falling back to an older one.
func TestRankSkipsInvalid(t *testing.T) {
	sk := nostr.Generate()
	ev, err := card.Card{Name: "x", Tags: []string{"友好"}}.Event(sk.Public(), 100)
	require.NoError(t, err)
	got, err := rank(map[nostr.PubKey]nostr.Event{sk.Public(): *ev}, []string{"友好"}, 100+card.Lifetime,
		func(string) (string, error) { return "stranger", nil })
	require.NoError(t, err)
	require.Empty(t, got, "expired card")
	boom := errors.New("boom")
	_, err = rank(map[nostr.PubKey]nostr.Event{sk.Public(): *ev}, []string{"友好"}, 100,
		func(string) (string, error) { return "", boom })
	require.ErrorIs(t, err, boom)
}

// Across relays the newest card of each author wins, even when a relay that
// missed the update still serves the old one.
func TestFindNewestCardAcrossRelays(t *testing.T) {
	url, _ := setup(t)
	handler, cleanup, err := relay.New(relay.Config{DataDir: t.TempDir()})
	require.NoError(t, err)
	srv := httptest.NewServer(handler)
	t.Cleanup(func() { srv.Close(); cleanup() })
	stale := "ws" + strings.TrimPrefix(srv.URL, "http")

	now := nostr.Now()
	sk := nostr.Generate()
	for _, c := range []struct {
		url  string
		at   nostr.Timestamp
		tags []string
	}{{url, now, []string{"友好", "火锅"}}, {stale, now - 100, []string{"友好"}}} {
		ev, err := card.Card{Name: "bob", Tags: c.tags}.Event(sk.Public(), c.at)
		require.NoError(t, err)
		require.NoError(t, ev.Sign(sk))
		_, ok, _ := publish(context.Background(), []string{c.url}, []nostr.Event{*ev})
		require.Len(t, ok, 1)
	}
	data, err := run(t, "find", "--as", "alice", "--relay", url, "--relay", stale)
	require.NoError(t, err)
	res := data["results"].([]any)
	require.Len(t, res, 1)
	require.Equal(t, []any{"友好", "火锅"}, res[0].(map[string]any)["tags"])
}

// A relay that missed a hide or a tag change cannot bring the old card back:
// find looks the author up on every relay without the tag filter, and a
// deletion request anywhere drops the card.
func TestFindStaleRelayCannotUndoHide(t *testing.T) {
	url, _ := setup(t)
	handler, cleanup, err := relay.New(relay.Config{DataDir: t.TempDir()})
	require.NoError(t, err)
	srv := httptest.NewServer(handler)
	t.Cleanup(func() { srv.Close(); cleanup() })
	stale := "ws" + strings.TrimPrefix(srv.URL, "http")
	now := nostr.Now()
	send := func(sk nostr.SecretKey, relayURL string, evs ...*nostr.Event) {
		var out []nostr.Event
		for _, ev := range evs {
			require.NoError(t, ev.Sign(sk))
			out = append(out, *ev)
		}
		_, ok, _ := publish(context.Background(), []string{relayURL}, out)
		require.Len(t, ok, 1)
	}
	oldCard := func(sk nostr.SecretKey, name string, tags ...string) *nostr.Event {
		ev, err := card.Card{Name: name, Tags: tags}.Event(sk.Public(), now-100)
		require.NoError(t, err)
		return ev
	}

	// erin hid on url (tombstone + earlier-dated deletion, as publishSetting
	// sends); the
	// stale relay still has her old card.
	erin := nostr.Generate()
	send(erin, url, oldCard(erin, "erin", "友好"))
	send(erin, stale, oldCard(erin, "erin", "友好"))
	del := deletion(erin.Public(), now-1)
	send(erin, url, card.Tombstone(erin.Public(), now), &del)

	// bob dropped 火锅 on url; the stale relay still has it.
	bob := nostr.Generate()
	send(bob, stale, oldCard(bob, "bob", "友好", "火锅"))
	newer, err := card.Card{Name: "bob", Tags: []string{"友好"}}.Event(bob.Public(), now)
	require.NoError(t, err)
	send(bob, url, newer)

	data, err := run(t, "find", "--as", "alice", "--relay", url, "--relay", stale, "--tag", "火锅")
	require.NoError(t, err)
	require.Empty(t, names(t, data), "bob's newest card has no 火锅")
	data, err = run(t, "find", "--as", "alice", "--relay", url, "--relay", stale)
	require.NoError(t, err)
	require.Equal(t, []string{"bob"}, names(t, data), "erin is hidden")
}

// A real hide leaves the tombstone on a relay that honours NIP-09: the
// deletion is dated before it, so find can see the hide without a separate
// (pageable) deletion query.
func TestHideKeepsTombstoneOnRelay(t *testing.T) {
	url, pk := setup(t)
	_, err := run(t, "visibility", "public", "--as", "alice", "--relay", url)
	require.NoError(t, err)
	_, err = run(t, "visibility", "hidden", "--as", "alice", "--relay", url)
	require.NoError(t, err)
	page, err := relayquery.FetchWithTimeout(context.Background(), url, nostr.Filter{
		Kinds: []nostr.Kind{wireevent.Kind30078}, Authors: []nostr.PubKey{pk},
		Tags: nostr.TagMap{"d": {wireevent.CardCategory}}}, 5*time.Second)
	require.NoError(t, err)
	require.Len(t, page.Events, 1)
	_, err = card.Parse(&page.Events[0], nostr.Now())
	require.ErrorIs(t, err, card.ErrHidden)
}

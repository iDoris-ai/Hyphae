package friend

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"

	"github.com/iDoris-ai/hyphae/internal/card"
	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/internal/identity"
	"github.com/iDoris-ai/hyphae/internal/relay"
	"github.com/iDoris-ai/hyphae/internal/relayquery"
	"github.com/iDoris-ai/hyphae/internal/storage"
	"github.com/iDoris-ai/hyphae/pkg/types"
)

// setup creates HOME with an unencrypted identity "alice" and a local relay.
func setup(t *testing.T) (relayURL string, pk nostr.PubKey) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("HYPHAE_OUTPUT", "")
	store := &types.KeyStore{Identities: map[string]*types.Identity{}, Contacts: map[string]*types.Contact{}}
	_, err := identity.CreateIdentity(store, "alice")
	require.NoError(t, err)
	sk, err := identity.GetSecretKey(store, "alice")
	require.NoError(t, err)

	handler, cleanup, err := relay.New(relay.Config{DataDir: t.TempDir()})
	require.NoError(t, err)
	srv := httptest.NewServer(handler)
	t.Cleanup(func() { srv.Close(); cleanup() })
	return "ws" + strings.TrimPrefix(srv.URL, "http"), sk.Public()
}

// run executes `hyphae --json friend <args>` in process and returns the data
// of the success envelope, or the error.
func run(t *testing.T, args ...string) (map[string]any, error) {
	t.Helper()
	root := &cli.Command{Name: "hyphae", Flags: []cli.Flag{&cli.BoolFlag{Name: "json"}}, Commands: []*cli.Command{NewCmd()}}
	r, w, err := os.Pipe()
	require.NoError(t, err)
	old := os.Stdout
	os.Stdout = w
	runErr := root.Run(context.Background(), append([]string{"hyphae", "--json", "friend"}, args...))
	_ = w.Close()
	os.Stdout = old
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	if runErr != nil {
		return nil, runErr
	}
	var env struct {
		OK   bool           `json:"ok"`
		Data map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &env), buf.String())
	require.True(t, env.OK)
	return env.Data, nil
}

func findCards(t *testing.T, url string, tags ...string) []nostr.Event {
	t.Helper()
	f, err := card.Filter(tags, 50)
	require.NoError(t, err)
	page, err := relayquery.FetchWithTimeout(context.Background(), url, f, 5*time.Second)
	require.NoError(t, err)
	return page.Events
}

func TestDefaultNameAndTag(t *testing.T) {
	url, _ := setup(t)
	data, err := run(t, "visibility", "public", "--as", "alice", "--relay", url)
	require.NoError(t, err)
	require.Equal(t, "public", data["visibility"])
	require.Equal(t, "alice", data["name"])
	require.Equal(t, []any{card.DefaultTag}, data["tags"])
	require.Len(t, findCards(t, url, card.DefaultTag), 1)
}

func TestPublicThenHidden(t *testing.T) {
	url, pk := setup(t)
	data, err := run(t, "visibility", "public", "--as", "alice", "--relay", url, "--tags", "友好,Hotpot", "--about", "周末吃火锅")
	require.NoError(t, err)
	require.EqualValues(t, 1, data["published_to"])
	require.Equal(t, []any{"友好", "hotpot"}, data["tags"])
	require.Greater(t, data["expires_at"], float64(time.Now().Unix()))

	// Found by the Chinese tag through the relay's #t index.
	evs := findCards(t, url, "友好")
	require.Len(t, evs, 1)
	require.Equal(t, pk, evs[0].PubKey)
	c, err := card.Parse(&evs[0], nostr.Now())
	require.NoError(t, err)
	require.Equal(t, "周末吃火锅", c.About)
	require.Len(t, findCards(t, url, "HOTPOT"), 1, "search normalizes the query")

	// Changing tags republishes, replacing the old card.
	_, err = run(t, "visibility", "public", "--as", "alice", "--relay", url, "--tags", "夜宵")
	require.NoError(t, err)
	require.Empty(t, findCards(t, url, "友好"))
	require.Len(t, findCards(t, url, "夜宵"), 1)

	// Hidden: the tombstone replaces the card; no tag finds it any more.
	other := "ws://127.0.0.1:1" // unreachable: hiding still succeeds on the stored relay
	data, err = run(t, "visibility", "hidden", "--as", "alice", "--relay", other)
	require.NoError(t, err)
	require.Equal(t, "hidden", data["visibility"])
	require.EqualValues(t, 1, data["published_to"], "tombstone goes to every relay the card ever reached")
	require.Empty(t, findCards(t, url, "夜宵"))

	// Going public again reuses the stored tags.
	data, err = run(t, "visibility", "public", "--as", "alice", "--relay", url)
	require.NoError(t, err)
	require.Equal(t, []any{"夜宵"}, data["tags"])
	require.Len(t, findCards(t, url, "夜宵"), 1, "a toggle back within the same second still replaces the tombstone")
}

func TestInvalidTagAndBadArg(t *testing.T) {
	url, _ := setup(t)
	_, err := run(t, "visibility", "public", "--as", "alice", "--relay", url, "--tags", "a b")
	var ee *common.ExitError
	require.True(t, errors.As(err, &ee))
	require.Equal(t, common.ErrCodeInvalidTag, ee.Code)

	_, err = run(t, "visibility", "public", "--as", "alice", "--relay", url, "--tags", "#x")
	require.True(t, errors.As(err, &ee))
	require.Equal(t, common.ErrCodeInvalidTag, ee.Code)

	_, err = run(t, "visibility", "maybe", "--as", "alice")
	require.True(t, errors.As(err, &ee))
	require.Equal(t, common.ErrCodeUser, ee.Code)
}

func TestNoRelayAcceptedSavesPreference(t *testing.T) {
	setup(t)
	_, err := run(t, "visibility", "public", "--as", "alice", "--relay", "ws://127.0.0.1:1")
	var ee *common.ExitError
	require.True(t, errors.As(err, &ee))
	require.Equal(t, common.ErrCodeNetwork, ee.Code)

	db, err := storage.OpenDB()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	pk, err := identity.LoadKeyStore()
	require.NoError(t, err)
	s, err := storage.LoadCardSetting(db, pk.Identities["alice"].Npub)
	require.NoError(t, err)
	require.True(t, s.Stored)
	require.Zero(t, s.PublishedAt, "nothing was published")
}

// A new card must be strictly newer than the last accepted event, or relays
// keep the old one on a created_at tie.
func TestReplacementIsStrictlyNewer(t *testing.T) {
	url, _ := setup(t)
	ks, err := identity.LoadKeyStore()
	require.NoError(t, err)
	db, err := storage.OpenDB()
	require.NoError(t, err)
	future := time.Now().Unix() + 100
	require.NoError(t, storage.SaveCardSetting(db, ks.Identities["alice"].Npub,
		storage.CardSetting{Visible: true, PublishedAt: future}))
	require.NoError(t, db.Close())

	_, err = run(t, "visibility", "public", "--as", "alice", "--relay", url)
	require.NoError(t, err)
	evs := findCards(t, url, card.DefaultTag)
	require.Len(t, evs, 1)
	require.EqualValues(t, future+1, evs[0].CreatedAt)
}

func TestEmptyTagsRefused(t *testing.T) {
	url, _ := setup(t)
	_, err := run(t, "visibility", "public", "--as", "alice", "--relay", url, "--tags", ",")
	var ee *common.ExitError
	require.True(t, errors.As(err, &ee), "err = %v", err)
	require.Equal(t, common.ErrCodeInvalidTag, ee.Code)
}

// A hide no relay accepted says the old card is still findable and keeps its
// expiry, so the next run knows a card may be out there.
func TestHideFailureKeepsExpiry(t *testing.T) {
	setup(t)
	ks, err := identity.LoadKeyStore()
	require.NoError(t, err)
	npub := ks.Identities["alice"].Npub
	dead := "ws://127.0.0.1:1"
	exp := time.Now().Unix() + 1000
	db, err := storage.OpenDB()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	require.NoError(t, storage.SaveCardSetting(db, npub, storage.CardSetting{
		Visible: true, Name: "alice", Tags: []string{card.DefaultTag}, PublishedAt: 1, ExpiresAt: exp, Relays: []string{dead}}))

	_, err = run(t, "visibility", "hidden", "--as", "alice", "--relay", dead)
	var ee *common.ExitError
	require.True(t, errors.As(err, &ee))
	require.Equal(t, common.ErrCodeNetwork, ee.Code)
	require.Contains(t, ee.Error(), "stays findable")
	s, err := storage.LoadCardSetting(db, npub)
	require.NoError(t, err)
	require.False(t, s.Visible)
	require.Equal(t, exp, s.ExpiresAt)
}

// A relay that accepts TCP but never finishes the handshake cannot hold the
// command past publishTimeout.
func TestPublishBoundsConnect(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { _ = c.Close() })
		}
	}()
	old := publishTimeout
	publishTimeout = 200 * time.Millisecond
	t.Cleanup(func() { publishTimeout = old })
	start := time.Now()
	results, ok, _ := publish(context.Background(), []string{"ws://" + ln.Addr().String()}, []nostr.Event{{}})
	require.Less(t, time.Since(start), 3*time.Second)
	require.Empty(t, ok)
	require.False(t, results[0].OK)
}

// A hide that misses one relay holding the card fails and keeps the expiry,
// so the person (and the daemon) knows to retry.
func TestPartialHideKeepsExpiry(t *testing.T) {
	url, _ := setup(t)
	_, err := run(t, "visibility", "public", "--as", "alice", "--relay", url)
	require.NoError(t, err)
	ks, err := identity.LoadKeyStore()
	require.NoError(t, err)
	npub := ks.Identities["alice"].Npub
	db, err := storage.OpenDB()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	s, err := storage.LoadCardSetting(db, npub)
	require.NoError(t, err)
	dead := "ws://127.0.0.1:1"
	s.Relays = append(s.Relays, dead) // the card also reached a relay now down
	require.NoError(t, storage.SaveCardSetting(db, npub, s))

	_, err = run(t, "visibility", "hidden", "--as", "alice", "--relay", url)
	var ee *common.ExitError
	require.True(t, errors.As(err, &ee), "err = %v", err)
	require.Equal(t, common.ErrCodeNetwork, ee.Code)
	require.Contains(t, ee.Error(), dead)
	require.Empty(t, findCards(t, url, card.DefaultTag), "the reachable relay is hidden")
	after, err := storage.LoadCardSetting(db, npub)
	require.NoError(t, err)
	require.Equal(t, s.ExpiresAt, after.ExpiresAt)
	require.Greater(t, after.PublishedAt, s.PublishedAt)

	// Once every card relay took the hide, the marker clears.
	after.Relays = []string{url}
	require.NoError(t, storage.SaveCardSetting(db, npub, after))
	_, err = run(t, "visibility", "hidden", "--as", "alice", "--relay", url)
	require.NoError(t, err)
	after, err = storage.LoadCardSetting(db, npub)
	require.NoError(t, err)
	require.Zero(t, after.ExpiresAt)
}

// A relay that took the first event but refused the second still holds an
// event at that created_at, so publish reports it as touched.
func TestPublishReportsPartialAccept(t *testing.T) {
	url, _ := setup(t)
	sk := nostr.Generate()
	good := card.Tombstone(sk.Public(), nostr.Now())
	require.NoError(t, good.Sign(sk))
	bad := deletion(sk.Public(), nostr.Now())
	require.NoError(t, bad.Sign(sk))
	bad.Content = "tampered" // signature no longer matches
	results, ok, touched := publish(context.Background(), []string{url}, []nostr.Event{*good, bad})
	require.Empty(t, ok)
	require.False(t, results[0].OK)
	require.True(t, touched)
	_, _, touched = publish(context.Background(), []string{"ws://127.0.0.1:1"}, []nostr.Event{*good})
	require.False(t, touched)
}

func TestHiddenValidatesEdits(t *testing.T) {
	url, _ := setup(t)
	_, err := run(t, "visibility", "hidden", "--as", "alice", "--relay", url, "--name", " Alice ")
	var ee *common.ExitError
	require.True(t, errors.As(err, &ee), "err = %v", err)
	require.Equal(t, common.ErrCodeUser, ee.Code)
	db, err := storage.OpenDB()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	ks, err := identity.LoadKeyStore()
	require.NoError(t, err)
	s, err := storage.LoadCardSetting(db, ks.Identities["alice"].Npub)
	require.NoError(t, err)
	require.False(t, s.Stored, "nothing saved")
}

// A stored publish time too far ahead would date the card past what readers
// accept; publishing refuses, hiding still works.
func TestPublishRefusesFarFuture(t *testing.T) {
	url, _ := setup(t)
	ks, err := identity.LoadKeyStore()
	require.NoError(t, err)
	npub := ks.Identities["alice"].Npub
	db, err := storage.OpenDB()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	require.NoError(t, storage.SaveCardSetting(db, npub,
		storage.CardSetting{Visible: true, PublishedAt: time.Now().Unix() + card.MaxClockSkew}))
	_, err = run(t, "visibility", "public", "--as", "alice", "--relay", url)
	var ee *common.ExitError
	require.True(t, errors.As(err, &ee), "err = %v", err)
	require.Equal(t, common.ErrCodeUser, ee.Code)
	require.Contains(t, ee.Error(), "clock")
	_, err = run(t, "visibility", "hidden", "--as", "alice", "--relay", url)
	require.NoError(t, err)
}

// tags alone never sends a first card: it republishes only a card that is
// live, and the first card comes from `visibility public` or the daemon.
func TestTagsPublishOnlyWhenPublic(t *testing.T) {
	url, _ := setup(t)
	data, err := run(t, "tags", "--as", "alice", "--relay", url, "--set", "火锅")
	require.NoError(t, err)
	require.EqualValues(t, 0, data["published_to"])
	require.Equal(t, []any{"火锅"}, data["tags"])
	require.Equal(t, false, data["published"])
	require.Empty(t, findCards(t, url, "火锅"), "a fresh identity's tags change publishes nothing")
	data, err = run(t, "tags", "--as", "alice", "--relay", url, "--set", "夜宵")
	require.NoError(t, err)
	require.EqualValues(t, 0, data["published_to"])
	require.Empty(t, findCards(t, url, "夜宵"), "nor does a second one: no card was ever published")

	_, err = run(t, "visibility", "hidden", "--as", "alice", "--relay", url)
	require.NoError(t, err)
	_, err = run(t, "tags", "--as", "alice", "--relay", url, "--set", "夜宵")
	require.NoError(t, err)
	require.Empty(t, findCards(t, url, "夜宵"), "hidden stays hidden")

	data, err = run(t, "visibility", "public", "--as", "alice", "--relay", url)
	require.NoError(t, err)
	require.Equal(t, []any{"夜宵"}, data["tags"])
	_, err = run(t, "tags", "--as", "alice", "--relay", url, "--set", "火锅")
	require.NoError(t, err)
	require.Len(t, findCards(t, url, "火锅"), 1, "public: the tags change republishes")
}

func TestTagsRejectsInvalid(t *testing.T) {
	url, _ := setup(t)
	for _, bad := range []string{"a b", ","} {
		_, err := run(t, "tags", "--as", "alice", "--relay", url, "--set", bad)
		var ee *common.ExitError
		require.True(t, errors.As(err, &ee), "err = %v", err)
		require.Equal(t, common.ErrCodeInvalidTag, ee.Code)
	}
}

func TestVisibilityShow(t *testing.T) {
	url, _ := setup(t)
	data, err := run(t, "visibility", "--as", "alice")
	require.NoError(t, err)
	require.Equal(t, "public", data["visibility"], "public by default")
	require.Equal(t, "alice", data["name"])
	require.Equal(t, []any{card.DefaultTag}, data["tags"])
	require.Equal(t, false, data["published"], "nothing published yet")

	_, err = run(t, "visibility", "public", "--as", "alice", "--relay", url)
	require.NoError(t, err)
	data, err = run(t, "visibility", "--as", "alice")
	require.NoError(t, err)
	require.Equal(t, true, data["published"])

	_, err = run(t, "visibility", "hidden", "--as", "alice", "--relay", url)
	require.NoError(t, err)
	data, err = run(t, "visibility", "--as", "alice")
	require.NoError(t, err)
	require.Equal(t, "hidden", data["visibility"])
	require.Equal(t, false, data["published"])
}

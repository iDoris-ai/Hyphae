package friend

import (
	"context"
	"testing"

	"fiatjaf.com/nostr"
	"github.com/stretchr/testify/require"

	"github.com/iDoris-ai/hyphae/internal/card"
	"github.com/iDoris-ai/hyphae/internal/identity"
	"github.com/iDoris-ai/hyphae/internal/storage"
)

func TestRefresh(t *testing.T) {
	url, _ := setup(t)
	ks, err := identity.LoadKeyStore()
	require.NoError(t, err)
	id := ks.Identities["alice"]
	sk, err := identity.GetSecretKey(ks, "alice")
	require.NoError(t, err)
	refresh := func(now int64) RefreshResult {
		res, err := Refresh(context.Background(), id, sk, []string{url}, now)
		require.NoError(t, err)
		return res
	}
	now := int64(nostr.Now())

	// No relay accepted: not announced as public, and retried next time.
	res, err := Refresh(context.Background(), id, sk, []string{"ws://127.0.0.1:1"}, now)
	require.NoError(t, err)
	require.False(t, res.Published)
	require.False(t, res.FirstTime)

	// A fresh identity is public by default: the first refresh publishes
	// the default card and says it is the first time.
	res = refresh(now)
	require.True(t, res.Published)
	require.True(t, res.FirstTime)
	require.Equal(t, []string{card.DefaultTag}, res.Card.Tags)
	evs := findCards(t, url, card.DefaultTag)
	require.Len(t, evs, 1)
	c, err := card.Parse(&evs[0], nostr.Now())
	require.NoError(t, err)
	require.Equal(t, "alice", c.Name)

	// Not due again until the card is RefreshAfter old.
	require.False(t, refresh(now).Published)
	published := res.Card.ExpiresAt - card.Lifetime
	require.False(t, refresh(published+RefreshAfter-1).Published)
	res = refresh(published + RefreshAfter)
	require.True(t, res.Published)
	require.False(t, res.FirstTime)

	// Hidden is left alone.
	_, err = run(t, "visibility", "hidden", "--as", "alice", "--relay", url)
	require.NoError(t, err)
	require.False(t, refresh(now+card.Lifetime).Published)
	require.Empty(t, findCards(t, url, card.DefaultTag))
}

// A hide that reached no relay is resent while the old card is live.
func TestRefreshRetriesHide(t *testing.T) {
	url, _ := setup(t)
	ks, err := identity.LoadKeyStore()
	require.NoError(t, err)
	id := ks.Identities["alice"]
	sk, err := identity.GetSecretKey(ks, "alice")
	require.NoError(t, err)
	_, err = run(t, "visibility", "public", "--as", "alice", "--relay", url)
	require.NoError(t, err)
	db, err := storage.OpenDB()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	s, err := storage.LoadCardSetting(db, id.Npub)
	require.NoError(t, err)
	s.Visible = false // as if `visibility hidden` reached no relay
	require.NoError(t, storage.SaveCardSetting(db, id.Npub, s))

	now := int64(nostr.Now())
	res, err := Refresh(context.Background(), id, sk, []string{url}, now)
	require.NoError(t, err)
	require.True(t, res.Published)
	require.False(t, res.FirstTime)
	require.Zero(t, res.Card.ExpiresAt)
	require.Empty(t, findCards(t, url, card.DefaultTag), "the tombstone replaced the card")

	res, err = Refresh(context.Background(), id, sk, []string{url}, now)
	require.NoError(t, err)
	require.False(t, res.Published, "nothing left to hide")
}

package friend

import (
	"testing"

	"fiatjaf.com/nostr"
	"github.com/stretchr/testify/require"

	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/internal/identity"
	"github.com/iDoris-ai/hyphae/internal/social/contact"
	"github.com/iDoris-ai/hyphae/internal/storage"
)

func TestListAndQuota(t *testing.T) {
	setup(t)
	alice := aliceNpub(t)
	carol := common.EncodeNpub(nostr.Generate().Public())
	frank := common.EncodeNpub(nostr.Generate().Public())
	db, err := storage.OpenDB()
	require.NoError(t, err)
	store := storage.NewRelationStore(db)
	now := nostr.Now()
	_, err = store.ReserveSend(alice, carol, false, int64(now))
	require.NoError(t, err)
	require.NoError(t, db.Close())
	ks, err := identity.LoadKeyStore()
	require.NoError(t, err)
	require.NoError(t, identity.AddContact(ks, "frank", frank))

	data, err := run(t, "list", "--as", "alice")
	require.NoError(t, err)
	peers := map[string]map[string]any{}
	for _, p := range data["peers"].([]any) {
		peers[p.(map[string]any)["npub"].(string)] = p.(map[string]any)
	}
	require.Equal(t, "friend", peers[frank]["relation"])
	require.EqualValues(t, -1, peers[frank]["can_send"])
	require.Equal(t, "stranger", peers[carol]["relation"])
	require.EqualValues(t, contact.PerStrangerLimit-1, peers[carol]["can_send"])
	require.EqualValues(t, 1, data["quota"].(map[string]any)["used"])

	data, err = run(t, "quota", "--as", "alice")
	require.NoError(t, err)
	require.EqualValues(t, contact.DailyLimit, data["daily_limit"])
	require.EqualValues(t, 1, data["used"])
	require.EqualValues(t, contact.DailyLimit-1, data["remaining"])
	require.EqualValues(t, int64(now)+contact.Window, data["resets_at"])
	require.EqualValues(t, contact.PerStrangerLimit, data["per_stranger_limit"])
}

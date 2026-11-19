package groupchat

import (
	"errors"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"github.com/stretchr/testify/require"

	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/internal/identity"
	"github.com/iDoris-ai/hyphae/internal/messaging"
	"github.com/iDoris-ai/hyphae/internal/social/contact"
	"github.com/iDoris-ai/hyphae/internal/storage"
	"github.com/iDoris-ai/hyphae/pkg/types"
)

// Inviting a stranger starts a conversation: it spends daily quota, and a
// blocked peer cannot be invited (docs/search/discover-friends.md rule 5).
func TestCreateInvitePassesStrangerGate(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("HYPHAE_OUTPUT", "json")
	messaging.ResetStoreForTest()
	t.Cleanup(messaging.ResetStoreForTest)
	ks := &types.KeyStore{}
	alice, err := identity.CreateIdentity(ks, "alice")
	require.NoError(t, err)
	require.NoError(t, identity.SaveKeyStore(ks))
	require.NoError(t, messaging.InitStorage())
	relations := storage.NewRelationStore(storage.DB)

	dave := common.EncodeNpub(nostr.Generate().Public())
	_, err = relations.Update(alice.Npub, dave, 1, func(p storage.PeerRelation) (storage.PeerRelation, error) {
		p.Relation = contact.Block(p.Relation)
		return p, nil
	})
	require.NoError(t, err)
	_, err = runGroupchat(t, "", "groupchat", "create", "--name", "x", "--identity", "alice", "--invitee", dave)
	var ee *common.ExitError
	require.True(t, errors.As(err, &ee), "err = %v", err)
	require.Equal(t, common.ErrCodePeerBlocked, ee.Code)

	carol := common.EncodeNpub(nostr.Generate().Public())
	_, err = runGroupchat(t, "", "groupchat", "create", "--name", "y", "--identity", "alice", "--invitee", carol)
	require.NoError(t, err)
	q, err := relations.Quota(alice.Npub, time.Now().Unix())
	require.NoError(t, err)
	require.Equal(t, 1, q.Used, "the invite counted as a new stranger")
}

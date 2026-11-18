package daemon

import (
	"fmt"
	"strings"
	"testing"

	"fiatjaf.com/nostr"
	"github.com/stretchr/testify/require"

	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/internal/identity"
	"github.com/iDoris-ai/hyphae/internal/messaging"
	"github.com/iDoris-ai/hyphae/internal/storage"
	"github.com/iDoris-ai/hyphae/pkg/types"
)

// A stranger's first message notifies as a request, the second notifies, the
// third is stored but silent; strangers are never auto-replied, friends are.
func TestDaemonStrangerReceive(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	messaging.ResetStoreForTest()
	t.Cleanup(messaging.ResetStoreForTest)
	require.NoError(t, messaging.InitStorage())
	ks := &types.KeyStore{Identities: map[string]*types.Identity{}, Contacts: map[string]*types.Contact{}}
	me, err := identity.CreateIdentity(ks, "alice")
	require.NoError(t, err)
	mySK, err := identity.GetSecretKey(ks, "alice")
	require.NoError(t, err)
	stranger, friend := nostr.Generate(), nostr.Generate()
	ks.Contacts["friend"] = &types.Contact{Nickname: "friend", Npub: common.EncodeNpub(friend.Public())}

	var titles []string
	replies := 0
	stored := make(chan error, 1)
	hooks := incomingReceiveHooks{
		store: func(e *nostr.Event, r, b string, enc bool) (bool, error) {
			first, err := messaging.StoreIncomingMessageOnce(e, r, b, enc)
			stored <- err
			return first, err
		},
		classify: func(sender, eventID string) (storage.Receipt, error) {
			return messaging.ClassifyIncoming(ks, me.Npub, sender, eventID)
		},
		notify: func(title, _ string) { titles = append(titles, title) },
		reply:  func(string, string) { replies++ },
		logf:   func(string, ...any) {},
	}
	deliver := func(from nostr.SecretKey, i int) watchResult {
		body, err := messaging.CompressText(fmt.Sprintf("hello %d", i))
		require.NoError(t, err)
		ev := signedIncomingEvent(t, from, mySK.Public(), body, nostr.Tags{{"z", messaging.CompressTag}})
		res := runWatchWithFakeEvent(t, ev, me, ks, mySK, newSeenSet(), true, true, hooks, stored)
		require.NoError(t, <-stored)
		require.NoError(t, res.err)
		return res
	}
	for i := 0; i < 3; i++ {
		require.Equal(t, 1, deliver(stranger, i).count, "every first arrival is stored")
	}
	require.Len(t, titles, 2, "the third stranger message is silent")
	require.True(t, strings.Contains(titles[0], "stranger request"), titles[0])
	require.False(t, strings.Contains(titles[1], "stranger request"), titles[1])
	require.Zero(t, replies, "strangers are never auto-replied")

	deliver(friend, 9)
	require.Len(t, titles, 3)
	require.Equal(t, 1, replies, "friends still get the auto-reply")
}

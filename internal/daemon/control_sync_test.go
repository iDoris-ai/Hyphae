package daemon

import (
	"bytes"
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"github.com/stretchr/testify/require"

	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/internal/identity"
	"github.com/iDoris-ai/hyphae/internal/messaging"
	"github.com/iDoris-ai/hyphae/internal/relay"
	"github.com/iDoris-ai/hyphae/internal/social/contact"
	"github.com/iDoris-ai/hyphae/internal/storage"
	"github.com/iDoris-ai/hyphae/pkg/types"
)

func TestControlSyncAppliesFriendAccept(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	messaging.ResetStoreForTest()
	t.Cleanup(messaging.ResetStoreForTest)
	ks := &types.KeyStore{Identities: map[string]*types.Identity{}, Contacts: map[string]*types.Contact{}}
	_, err := identity.CreateIdentity(ks, "alice")
	require.NoError(t, err)
	me := ks.Identities["alice"]
	handler, cleanup, err := relay.New(relay.Config{DataDir: t.TempDir()})
	require.NoError(t, err)
	srv := httptest.NewServer(handler)
	t.Cleanup(func() { srv.Close(); cleanup() })
	url := "ws" + strings.TrimPrefix(srv.URL, "http")

	// alice wrote to carol once; carol answers with a friend_accept.
	carol := nostr.Generate()
	carolNpub := common.EncodeNpub(carol.Public())
	require.NoError(t, messaging.InitStorage())
	_, err = storage.NewRelationStore(storage.DB).ReserveSend(me.Npub, carolNpub, false, time.Now().Unix())
	require.NoError(t, err)
	sentID := strings.Repeat("12", 32)
	_, err = storage.DB.Exec(`INSERT INTO messages (id, event_id, sender_npub, recipient_npub, plaintext, created_at, received_at, is_incoming)
		VALUES (?, ?, ?, ?, 'hi', ?, ?, 0)`, sentID, sentID, me.Npub, carolNpub, time.Now().Unix(), time.Now().Unix())
	require.NoError(t, err)
	aliceSK, err := identity.GetSecretKey(ks, "alice")
	require.NoError(t, err)
	ev, err := messaging.BuildFriendAccept(carol, aliceSK.Public(), sentID, nostr.Now())
	require.NoError(t, err)
	r, err := nostr.RelayConnect(context.Background(), url, nostr.RelayOptions{})
	require.NoError(t, err)
	require.NoError(t, r.Publish(context.Background(), *ev))
	r.Close()

	var out bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		runControlSync(ctx, me, ks, []string{url}, 50*time.Millisecond, newDaemonLogger(&out))
		close(done)
	}()
	require.Eventually(t, func() bool {
		p, err := storage.NewRelationStore(storage.DB).Get(me.Npub, carolNpub)
		return err == nil && p.Relation.State == contact.Friend
	}, 5*time.Second, 50*time.Millisecond)
	cancel()
	<-done
	require.Contains(t, out.String(), carolNpub+" accepted you as a friend")
}

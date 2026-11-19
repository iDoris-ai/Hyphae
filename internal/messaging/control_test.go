package messaging

import (
	"context"
	"strings"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"github.com/stretchr/testify/require"

	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/internal/identity"
	"github.com/iDoris-ai/hyphae/internal/social/contact"
	"github.com/iDoris-ai/hyphae/internal/storage"
	"github.com/iDoris-ai/hyphae/pkg/crypto"
)

func TestFriendAcceptRoundTrip(t *testing.T) {
	alice, bob := nostr.Generate(), nostr.Generate()
	answers := strings.Repeat("ab", 32)
	ev, err := BuildFriendAccept(alice, bob.Public(), answers, nostr.Now())
	require.NoError(t, err)
	require.Nil(t, ev.Tags.Find("z"), "never compressed")
	got, err := ParseFriendAccept(ev, bob)
	require.NoError(t, err)
	require.Equal(t, answers, got)

	_, err = ParseFriendAccept(ev, nostr.Generate())
	require.Error(t, err, "addressed to someone else")
	_, err = BuildFriendAccept(alice, bob.Public(), "short", nostr.Now())
	require.Error(t, err)

	resign := func(body string) *nostr.Event {
		e := *ev
		enc, err := crypto.EncryptMessage(body, alice, bob.Public())
		require.NoError(t, err)
		e.Content = enc
		require.NoError(t, e.Sign(alice))
		return &e
	}
	for name, body := range map[string]string{
		"unknown type":  `{"v":1,"type":"friend_reject","answers":"` + answers + `"}`,
		"version 2":     `{"v":2,"type":"friend_accept","answers":"` + answers + `"}`,
		"extra field":   `{"v":1,"type":"friend_accept","answers":"` + answers + `","x":1}`,
		"upper hex":     `{"v":1,"type":"friend_accept","answers":"` + strings.ToUpper(answers) + `"}`,
		"too long":      `{"v":1,"type":"friend_accept","answers":"` + answers + `","pad":"` + strings.Repeat("x", MaxControlPlaintext) + `"}`,
		"not json":      `hello`,
		"wrong answers": `{"v":1,"type":"friend_accept","answers":1}`,
	} {
		_, err := ParseFriendAccept(resign(body), bob)
		require.Error(t, err, name)
	}
	forged := *ev
	forged.Content = resign(`{"v":1,"type":"friend_accept","answers":"` + answers + `"}`).Content
	_, err = ParseFriendAccept(&forged, bob)
	require.Error(t, err, "signature no longer matches")
}

// sentEventID returns the event ID of alice's latest stored message to peer.
func sentEventID(t *testing.T, alice, peer string) string {
	t.Helper()
	var id string
	require.NoError(t, storage.DB.QueryRow(`SELECT event_id FROM messages WHERE sender_npub = ? AND recipient_npub = ?
		AND is_incoming = 0 ORDER BY received_at DESC LIMIT 1`, alice, peer).Scan(&id))
	return id
}

func relationOf(t *testing.T, me, peer string) contact.State {
	t.Helper()
	p, err := storage.NewRelationStore(storage.DB).Get(me, peer)
	require.NoError(t, err)
	return p.Relation.State
}

func TestApplyFriendAccept(t *testing.T) {
	setupAgentMsgCLI(t)
	url := startGateRelay(t)
	ks, err := identity.LoadKeyStore()
	require.NoError(t, err)
	aliceSK, err := identity.GetSecretKey(ks, "alice")
	require.NoError(t, err)
	alice := ks.Identities["alice"].Npub
	carol := nostr.Generate()
	carolNpub := common.EncodeNpub(carol.Public())

	require.NoError(t, sendTo(t, url, carolNpub))
	answers := sentEventID(t, alice, carolNpub)

	// An accept answering something alice never sent is ignored.
	bogus, err := BuildFriendAccept(carol, aliceSK.Public(), strings.Repeat("cd", 32), nostr.Now())
	require.NoError(t, err)
	changed, err := ApplyFriendAccept(bogus, aliceSK)
	require.Error(t, err)
	require.False(t, changed)
	require.Equal(t, contact.Stranger, relationOf(t, alice, carolNpub))

	// Blocked: ignored.
	ev, err := BuildFriendAccept(carol, aliceSK.Public(), answers, nostr.Now())
	require.NoError(t, err)
	store := storage.NewRelationStore(storage.DB)
	block := func(p storage.PeerRelation) (storage.PeerRelation, error) {
		p.Relation = contact.Block(p.Relation)
		return p, nil
	}
	_, err = store.Update(alice, carolNpub, 1, block)
	require.NoError(t, err)
	_, err = ApplyFriendAccept(ev, aliceSK)
	require.Error(t, err)

	// After unblock, the old accept answers a message sent before the unblock:
	// still ignored, so a replay cannot make them friends.
	_, err = store.Update(alice, carolNpub, 1<<40, func(p storage.PeerRelation) (storage.PeerRelation, error) {
		p.Relation, err = contact.Unblock(p.Relation, 1<<40)
		return p, err
	})
	require.NoError(t, err)
	_, err = ApplyFriendAccept(ev, aliceSK)
	require.Error(t, err)
	require.Equal(t, contact.Stranger, relationOf(t, alice, carolNpub))

	// A fresh accept of a message alice sent: friends; applying it again is a no-op.
	dave := nostr.Generate()
	daveNpub := common.EncodeNpub(dave.Public())
	require.NoError(t, sendTo(t, url, daveNpub))
	ok, err := BuildFriendAccept(dave, aliceSK.Public(), sentEventID(t, alice, daveNpub), nostr.Now())
	require.NoError(t, err)
	changed, err = ApplyFriendAccept(ok, aliceSK)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, contact.Friend, relationOf(t, alice, daveNpub))
	changed, err = ApplyFriendAccept(ok, aliceSK)
	require.NoError(t, err)
	require.False(t, changed)
	for i := 0; i < 3; i++ {
		require.NoError(t, sendTo(t, url, daveNpub), "friends have no limit")
	}
}

func TestSyncControlAppliesAccepts(t *testing.T) {
	setupAgentMsgCLI(t)
	url := startGateRelay(t)
	ks, err := identity.LoadKeyStore()
	require.NoError(t, err)
	aliceSK, err := identity.GetSecretKey(ks, "alice")
	require.NoError(t, err)
	alice := ks.Identities["alice"].Npub
	carol := nostr.Generate()
	carolNpub := common.EncodeNpub(carol.Public())
	require.NoError(t, sendTo(t, url, carolNpub))

	good, err := BuildFriendAccept(carol, aliceSK.Public(), sentEventID(t, alice, carolNpub), nostr.Now())
	require.NoError(t, err)
	bogus, err := BuildFriendAccept(nostr.Generate(), aliceSK.Public(), strings.Repeat("ef", 32), nostr.Now())
	require.NoError(t, err)
	for _, ev := range []*nostr.Event{good, bogus} {
		_, n := publishAgentMessageRelays(context.Background(), []string{url}, *ev, 5*time.Second)
		require.Equal(t, 1, n)
	}

	friends, dropped := SyncControl(context.Background(), []string{url, "ws://127.0.0.1:1"}, aliceSK, 2*time.Second)
	require.Equal(t, []string{carolNpub}, friends)
	require.Equal(t, 1, dropped)
	require.Equal(t, contact.Friend, relationOf(t, alice, carolNpub))
	friends, _ = SyncControl(context.Background(), []string{url}, aliceSK, 2*time.Second)
	require.Empty(t, friends, "re-reading is a no-op")
}

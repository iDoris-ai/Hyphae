package friend

import (
	"context"
	"errors"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"github.com/stretchr/testify/require"

	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/internal/identity"
	"github.com/iDoris-ai/hyphae/internal/messaging"
	"github.com/iDoris-ai/hyphae/internal/relayquery"
	"github.com/iDoris-ai/hyphae/internal/storage"
	"github.com/iDoris-ai/hyphae/internal/wireevent"
)

// receive records n messages from peer to alice the way the inbox does: a
// stored message plus one ClassifyReceive per event.
func receive(t *testing.T, alice, peer string, n int, first string) {
	t.Helper()
	db, err := storage.OpenDB()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	store := storage.NewRelationStore(db)
	for i := 0; i < n; i++ {
		id := nostr.Generate().Public().Hex() // any unique 64-hex event id
		text := first
		if i > 0 {
			text = "later"
		}
		_, err := db.Exec(`INSERT INTO messages (id, event_id, sender_npub, recipient_npub, plaintext, created_at, received_at, is_incoming)
			VALUES (?, ?, ?, ?, ?, ?, ?, 1)`, id, id, peer, alice, text, 100+i, 100+i)
		require.NoError(t, err)
		_, err = store.ClassifyReceive(alice, peer, id, false, int64(100+i))
		require.NoError(t, err)
	}
}

func aliceNpub(t *testing.T) string {
	t.Helper()
	ks, err := identity.LoadKeyStore()
	require.NoError(t, err)
	return ks.Identities["alice"].Npub
}

func TestPendingIgnoreBlockUnblock(t *testing.T) {
	setup(t)
	alice := aliceNpub(t)
	carol := common.EncodeNpub(nostr.Generate().Public())
	dave := common.EncodeNpub(nostr.Generate().Public())
	receive(t, alice, carol, 3, "你好，一起吃火锅吗")
	receive(t, alice, dave, 1, "hi")

	data, err := run(t, "pending", "--as", "alice")
	require.NoError(t, err)
	pending := data["pending"].([]any)
	require.Len(t, pending, 2)
	byNpub := map[string]map[string]any{}
	for _, p := range pending {
		byNpub[p.(map[string]any)["npub"].(string)] = p.(map[string]any)
	}
	require.Equal(t, "你好，一起吃火锅吗", byNpub[carol]["first_message"])
	require.EqualValues(t, 3, byNpub[carol]["received"])
	require.EqualValues(t, 1, byNpub[carol]["suppressed"], "the third message was not shown")

	// ignore clears the request; dave stays a stranger.
	data, err = run(t, "ignore", "--as", "alice", "--npub", dave)
	require.NoError(t, err)
	require.Equal(t, "stranger", data["relation"])
	data, err = run(t, "pending", "--as", "alice")
	require.NoError(t, err)
	require.Len(t, data["pending"].([]any), 1)

	// block a contact: removed from contacts, gone from pending, refused twice.
	ks, err := identity.LoadKeyStore()
	require.NoError(t, err)
	require.NoError(t, identity.AddContact(ks, "carol", carol))
	data, err = run(t, "block", "--as", "alice", "--npub", carol)
	require.NoError(t, err)
	require.Equal(t, "blocked", data["relation"])
	require.Equal(t, []any{"carol"}, data["removed_contacts"])
	ks, err = identity.LoadKeyStore()
	require.NoError(t, err)
	require.NotContains(t, ks.Contacts, "carol")
	data, err = run(t, "pending", "--as", "alice")
	require.NoError(t, err)
	require.Empty(t, data["pending"].([]any))
	_, err = run(t, "block", "--as", "alice", "--npub", carol)
	require.NoError(t, err, "blocking again is harmless")

	// ignore refuses a non-stranger; unblock returns the old counts.
	_, err = run(t, "ignore", "--as", "alice", "--npub", carol)
	var ee *common.ExitError
	require.True(t, errors.As(err, &ee), "err = %v", err)
	require.Equal(t, common.ErrCodeUser, ee.Code)
	data, err = run(t, "unblock", "--as", "alice", "--npub", carol)
	require.NoError(t, err)
	require.Equal(t, "stranger", data["relation"])
	_, err = run(t, "unblock", "--as", "alice", "--npub", carol)
	require.True(t, errors.As(err, &ee))
	require.Equal(t, common.ErrCodeUser, ee.Code)

	// a bad key and yourself are refused.
	_, err = run(t, "block", "--as", "alice", "--npub", "nope")
	require.True(t, errors.As(err, &ee))
	require.Equal(t, common.ErrCodeInvalidKey, ee.Code)
	_, err = run(t, "block", "--as", "alice", "--npub", alice)
	require.True(t, errors.As(err, &ee))
	require.Equal(t, common.ErrCodeUser, ee.Code)
}

func TestPrintableDropsControlCharacters(t *testing.T) {
	require.Equal(t, "hi[2Jthere你好", printable("hi\x1b[2J\x07there\u009b你好"))
}

func TestAcceptSendsFriendAccept(t *testing.T) {
	url, _ := setup(t)
	alice := aliceNpub(t)
	carol := nostr.Generate()
	carolNpub := common.EncodeNpub(carol.Public())
	_, err := run(t, "accept", "--as", "alice", "--npub", carolNpub, "--relay", url)
	var ee *common.ExitError
	require.True(t, errors.As(err, &ee), "nothing to accept yet: %v", err)
	require.Equal(t, common.ErrCodeUser, ee.Code)

	receive(t, alice, carolNpub, 1, "hi")
	// A nickname taken by someone else is refused before anything changes.
	ks0, err := identity.LoadKeyStore()
	require.NoError(t, err)
	require.NoError(t, identity.AddContact(ks0, "taken", common.EncodeNpub(nostr.Generate().Public())))
	_, err = run(t, "accept", "--as", "alice", "--npub", carolNpub, "--relay", url, "--nickname", "taken")
	require.True(t, errors.As(err, &ee), "err = %v", err)
	require.Equal(t, common.ErrCodeUser, ee.Code)
	db, err := storage.OpenDB()
	require.NoError(t, err)
	p, err := storage.NewRelationStore(db).Get(alice, carolNpub)
	require.NoError(t, err)
	require.Equal(t, "stranger", p.Relation.State.String(), "relation untouched")
	require.NoError(t, db.Close())
	db, err = storage.OpenDB()
	require.NoError(t, err)
	_, firstID, err := storage.FirstIncoming(db, alice, carolNpub)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	// No relay takes it: accepted locally, network_error, and a retry resends.
	_, err = run(t, "accept", "--as", "alice", "--npub", carolNpub, "--relay", "ws://127.0.0.1:1")
	require.True(t, errors.As(err, &ee), "err = %v", err)
	require.Equal(t, common.ErrCodeNetwork, ee.Code)

	data, err := run(t, "accept", "--as", "alice", "--npub", carolNpub, "--relay", url)
	require.NoError(t, err)
	require.Equal(t, "friend", data["relation"])
	require.EqualValues(t, 1, data["published_to"])
	nick := data["nickname"].(string)
	ks, err := identity.LoadKeyStore()
	require.NoError(t, err)
	require.Equal(t, carolNpub, ks.Contacts[nick].Npub)

	// carol can read it, and it answers her first message.
	alicePK, err := common.ParsePublicKey(alice)
	require.NoError(t, err)
	page, err := relayquery.FetchWithTimeout(context.Background(), url, nostr.Filter{
		Kinds: []nostr.Kind{wireevent.Kind30078}, Authors: []nostr.PubKey{alicePK},
		Tags: nostr.TagMap{"c": {wireevent.ControlCategory}}}, 5*time.Second)
	require.NoError(t, err)
	require.Len(t, page.Events, 1)
	answers, err := messaging.ParseFriendAccept(&page.Events[0], carol)
	require.NoError(t, err)
	require.Equal(t, firstID, answers)

	// Repeating resends and keeps the same contact.
	data, err = run(t, "accept", "--as", "alice", "--npub", carolNpub, "--relay", url)
	require.NoError(t, err)
	require.Equal(t, nick, data["nickname"])
}

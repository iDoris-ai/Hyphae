package daemon

import (
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"testing"

	"fiatjaf.com/nostr"
	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/internal/groupchat"
	"github.com/iDoris-ai/hyphae/internal/identity"
	"github.com/iDoris-ai/hyphae/internal/messaging"
	"github.com/iDoris-ai/hyphae/internal/storage"
	"github.com/iDoris-ai/hyphae/pkg/crypto"
	"github.com/iDoris-ai/hyphae/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGroupInboundRestart(t *testing.T) {
	t.Run("restart", func(t *testing.T) {
		store := openInboundFixture(t)
		ks, alice, aliceSK := newDaemonIdentity(t, "alice")
		bobSK := nostr.Generate()
		draft := activateWatcher(t, store, bobSK, aliceSK)
		logical, err := groupchat.NewOpaqueID()
		require.NoError(t, err)
		encoded, err := groupchat.Encode(groupchat.Envelope{
			Type: groupchat.EnvelopeMessage, Version: groupchat.Version,
			GroupID: draft.Group.ID, LogicalID: logical, Body: "hello",
		})
		require.NoError(t, err)
		event := encryptedIncoming(t, bobSK, aliceSK, encoded)
		logs, stores, notes, replies := receiveCounters()
		hooks := countingHooks(logs, stores, notes, replies)
		processed, err := processIncomingEvent(event, aliceSK, newSeenSet(), true, true, alice, ks, hooks)
		require.NoError(t, err)
		assert.True(t, processed)
		require.Len(t, *logs, 1)
		assert.Contains(t, (*logs)[0], "Group update")
		assert.Zero(t, *stores)
		assert.Zero(t, *notes)
		assert.Zero(t, *replies)
		assert.Equal(t, 1, groupMessageRows(t))
		assertNoDM(t, event)

		processed, err = processIncomingEvent(event, aliceSK, newSeenSet(), true, true, alice, ks, hooks)
		require.NoError(t, err)
		assert.False(t, processed)
		assert.Len(t, *logs, 1)
		assert.Equal(t, 1, groupMessageRows(t))
		assert.Zero(t, fanoutRows(t))
		assertNoDM(t, event)
	})

	t.Run("bad_version", func(t *testing.T) {
		openInboundDB(t)
		_, alice, aliceSK := newDaemonIdentity(t, "alice")
		bobSK := nostr.Generate()
		event := encryptedIncoming(t, bobSK, aliceSK, groupchat.ReservedPrefix+"v99\n{}")
		logs, stores, notes, replies := receiveCounters()
		processed, err := processIncomingEvent(event, aliceSK, newSeenSet(), true, true, alice, &types.KeyStore{}, countingHooks(logs, stores, notes, replies))
		require.NoError(t, err)
		assert.False(t, processed)
		assert.Empty(t, *logs)
		assert.Zero(t, *stores)
		assert.Zero(t, *notes)
		assert.Zero(t, *replies)
		assertNoDM(t, event)
		var name string
		err = storage.DB.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'groupchat_messages'`).Scan(&name)
		assert.ErrorIs(t, err, sql.ErrNoRows)
	})

	t.Run("direct_message", func(t *testing.T) {
		openInboundDB(t)
		ks, alice, aliceSK := newDaemonIdentity(t, "alice")
		bobSK := nostr.Generate()
		ciphertext, err := crypto.EncryptMessage("ordinary dm", bobSK, aliceSK.Public())
		require.NoError(t, err)
		compressed, err := messaging.CompressText(ciphertext)
		require.NoError(t, err)
		event := signedIncomingEvent(t, bobSK, aliceSK.Public(), compressed, nostr.Tags{{"enc", "nip44"}, {"z", messaging.CompressTag}})
		logs := []string{}
		hooks := incomingReceiveHooks{
			store: func(event *nostr.Event, recipient, body string, encrypted bool) (bool, error) {
				return messaging.StoreIncomingMessageOnce(event, recipient, body, encrypted)
			},
			logf: func(format string, args ...any) {
				logs = append(logs, fmt.Sprintf(format, args...))
			},
		}
		processed, err := processIncomingEvent(event, aliceSK, newSeenSet(), false, false, alice, ks, hooks)
		require.NoError(t, err)
		assert.True(t, processed)
		require.Len(t, logs, 1)
		assert.Contains(t, logs[0], "New message")
		assert.Equal(t, 1, dmRows(t))
		got, err := messaging.GetStore()
		require.NoError(t, err)
		stored, err := got.GetMessage(hex.EncodeToString(event.ID[:]))
		require.NoError(t, err)
		require.NotNil(t, stored)
		assert.Equal(t, "ordinary dm", stored.Plaintext)

		processed, err = processIncomingEvent(event, aliceSK, newSeenSet(), false, false, alice, ks, hooks)
		require.NoError(t, err)
		assert.False(t, processed)
		assert.Len(t, logs, 1)
		assert.Equal(t, 1, dmRows(t))
	})

	t.Run("accept", func(t *testing.T) {
		store := openInboundFixture(t)
		ks, alice, aliceSK := newDaemonIdentity(t, "alice")
		bobSK := nostr.Generate()
		bobNpub := common.EncodeNpub(bobSK.Public())
		draft, err := store.CreateGroup("planning", alice.Npub, []string{bobNpub})
		require.NoError(t, err)
		require.Len(t, draft.Invitations, 1)
		invite := draft.Invitations[0]
		_, err = store.ReceiveInvite(verifiedFrom(t, aliceSK, bobSK, invite))
		require.NoError(t, err)
		accept, err := store.AcceptInvite(invite.InviteID, bobNpub)
		require.NoError(t, err)
		encoded, err := groupchat.Encode(accept)
		require.NoError(t, err)
		event := encryptedIncoming(t, bobSK, aliceSK, encoded)
		logs, stores, notes, replies := receiveCounters()
		processed, err := processIncomingEvent(event, aliceSK, newSeenSet(), true, true, alice, ks, countingHooks(logs, stores, notes, replies))
		require.NoError(t, err)
		assert.True(t, processed)
		require.Len(t, *logs, 1)
		assert.Zero(t, *stores)
		assert.Zero(t, *notes)
		assert.Zero(t, *replies)
		group, err := store.GetGroup(alice.Npub, draft.Group.ID)
		require.NoError(t, err)
		// the last acceptance makes the creator queue one activation for bob
		assert.Equal(t, groupchat.StateActive, group.State)
		assertNoDM(t, event)
		assert.Equal(t, 1, fanoutRows(t))

		processed, err = processIncomingEvent(event, aliceSK, newSeenSet(), true, true, alice, ks, countingHooks(logs, stores, notes, replies))
		require.NoError(t, err)
		assert.False(t, processed)
		assert.Len(t, *logs, 1)
		assert.Equal(t, 1, fanoutRows(t), "a replayed accept must not queue a second activation")
	})

	t.Run("decline", func(t *testing.T) {
		store := openInboundFixture(t)
		ks, alice, aliceSK := newDaemonIdentity(t, "alice")
		bobSK := nostr.Generate()
		bobNpub := common.EncodeNpub(bobSK.Public())
		draft, err := store.CreateGroup("planning", alice.Npub, []string{bobNpub})
		require.NoError(t, err)
		require.Len(t, draft.Invitations, 1)
		invite := draft.Invitations[0]
		_, err = store.ReceiveInvite(verifiedFrom(t, aliceSK, bobSK, invite))
		require.NoError(t, err)
		decline, err := store.DeclineInvite(invite.InviteID, bobNpub)
		require.NoError(t, err)
		encoded, err := groupchat.Encode(decline)
		require.NoError(t, err)
		event := encryptedIncoming(t, bobSK, aliceSK, encoded)
		logs, stores, notes, replies := receiveCounters()
		processed, err := processIncomingEvent(event, aliceSK, newSeenSet(), true, true, alice, ks, countingHooks(logs, stores, notes, replies))
		require.NoError(t, err)
		assert.True(t, processed)
		require.Len(t, *logs, 1)
		assert.Zero(t, *stores)
		group, err := store.GetGroup(alice.Npub, draft.Group.ID)
		require.NoError(t, err)
		assert.Equal(t, groupchat.StateCancelled, group.State)
		assertNoDM(t, event)

		processed, err = processIncomingEvent(event, aliceSK, newSeenSet(), true, true, alice, ks, countingHooks(logs, stores, notes, replies))
		require.NoError(t, err)
		assert.False(t, processed)
		assert.Len(t, *logs, 1)
		assert.Zero(t, fanoutRows(t))
	})

	t.Run("cancel", func(t *testing.T) {
		store := openInboundFixture(t)
		ks, bob, bobSK := newDaemonIdentity(t, "bob")
		aliceSK := nostr.Generate()
		aliceNpub := common.EncodeNpub(aliceSK.Public())
		draft, err := store.CreateGroup("planning", aliceNpub, []string{bob.Npub})
		require.NoError(t, err)
		require.Len(t, draft.Invitations, 1)
		_, err = store.ReceiveInvite(verifiedFrom(t, aliceSK, bobSK, draft.Invitations[0]))
		require.NoError(t, err)
		notices, err := store.CancelGroup(aliceNpub, draft.Group.ID)
		require.NoError(t, err)
		require.Len(t, notices, 1)
		encoded, err := groupchat.Encode(notices[0])
		require.NoError(t, err)
		event := encryptedIncoming(t, aliceSK, bobSK, encoded)
		logs, stores, notes, replies := receiveCounters()
		processed, err := processIncomingEvent(event, bobSK, newSeenSet(), true, true, bob, ks, countingHooks(logs, stores, notes, replies))
		require.NoError(t, err)
		assert.True(t, processed)
		require.Len(t, *logs, 1)
		assert.Zero(t, *stores)
		group, err := store.GetGroup(bob.Npub, draft.Group.ID)
		require.NoError(t, err)
		assert.Equal(t, groupchat.StateCancelled, group.State)
		assertNoDM(t, event)

		processed, err = processIncomingEvent(event, bobSK, newSeenSet(), true, true, bob, ks, countingHooks(logs, stores, notes, replies))
		require.NoError(t, err)
		assert.False(t, processed)
		assert.Len(t, *logs, 1)
		assert.Zero(t, fanoutRows(t))
	})
}

func openInboundDB(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	messaging.ResetStoreForTest()
	t.Cleanup(messaging.ResetStoreForTest)
	require.NoError(t, messaging.InitStorage())
}

func openInboundFixture(t *testing.T) *groupchat.Store {
	t.Helper()
	openInboundDB(t)
	store, err := groupchat.NewStore(storage.DB)
	require.NoError(t, err)
	return store
}

func newDaemonIdentity(t *testing.T, name string) (*types.KeyStore, *types.Identity, nostr.SecretKey) {
	t.Helper()
	ks := &types.KeyStore{Identities: map[string]*types.Identity{}, Contacts: map[string]*types.Contact{}}
	id, err := identity.CreateIdentity(ks, name)
	require.NoError(t, err)
	sk, err := identity.GetSecretKey(ks, name)
	require.NoError(t, err)
	return ks, id, sk
}

func encryptedIncoming(t *testing.T, sender, recipient nostr.SecretKey, plaintext string) *nostr.Event {
	t.Helper()
	ciphertext, err := crypto.EncryptMessage(plaintext, sender, recipient.Public())
	require.NoError(t, err)
	compressed, err := messaging.CompressText(ciphertext)
	require.NoError(t, err)
	return signedIncomingEvent(t, sender, recipient.Public(), compressed, nostr.Tags{{"enc", "nip44"}, {"z", messaging.CompressTag}})
}

func verifiedFrom(t *testing.T, sender, recipient nostr.SecretKey, envelope groupchat.Envelope) groupchat.VerifiedIncoming {
	t.Helper()
	encoded, err := groupchat.Encode(envelope)
	require.NoError(t, err)
	event := encryptedIncoming(t, sender, recipient, encoded)
	incoming, err := groupchat.VerifyIncoming(*event, recipient)
	require.NoError(t, err)
	return incoming
}

func activateWatcher(t *testing.T, store *groupchat.Store, creator, invitee nostr.SecretKey) groupchat.GroupDraft {
	t.Helper()
	creatorNpub := common.EncodeNpub(creator.Public())
	inviteeNpub := common.EncodeNpub(invitee.Public())
	draft, err := store.CreateGroup("planning", creatorNpub, []string{inviteeNpub})
	require.NoError(t, err)
	require.Len(t, draft.Invitations, 1)
	invite := draft.Invitations[0]
	created, err := store.ReceiveInvite(verifiedFrom(t, creator, invitee, invite))
	require.NoError(t, err)
	require.True(t, created)
	accept, err := store.AcceptInvite(invite.InviteID, inviteeNpub)
	require.NoError(t, err)
	batch, err := store.ReceiveAcceptance(verifiedFrom(t, invitee, creator, accept))
	require.NoError(t, err)
	require.Len(t, batch, 1)
	encoded, err := groupchat.Encode(batch[0])
	require.NoError(t, err)
	event := encryptedIncoming(t, creator, invitee, encoded)
	require.NoError(t, store.MarkActivationQueued(creatorNpub, draft.Group.ID, batch[0].InviteID, event.ID.Hex(), true))
	applied, err := store.ReceiveActivation(verifiedEvent(t, event, invitee))
	require.NoError(t, err)
	require.True(t, applied)
	return draft
}

func verifiedEvent(t *testing.T, event *nostr.Event, recipient nostr.SecretKey) groupchat.VerifiedIncoming {
	t.Helper()
	incoming, err := groupchat.VerifyIncoming(*event, recipient)
	require.NoError(t, err)
	return incoming
}

func receiveCounters() (*[]string, *int, *int, *int) {
	logs := []string{}
	stores, notes, replies := 0, 0, 0
	return &logs, &stores, &notes, &replies
}

func countingHooks(logs *[]string, stores, notes, replies *int) incomingReceiveHooks {
	return incomingReceiveHooks{
		store: func(*nostr.Event, string, string, bool) (bool, error) {
			*stores++
			return true, nil
		},
		notify: func(string, string) { *notes++ },
		reply:  func(string, string) { *replies++ },
		logf: func(format string, args ...any) {
			*logs = append(*logs, fmt.Sprintf(format, args...))
		},
	}
}

func groupMessageRows(t *testing.T) int {
	t.Helper()
	var rows int
	require.NoError(t, storage.DB.QueryRow(`SELECT COUNT(*) FROM groupchat_messages`).Scan(&rows))
	return rows
}

func fanoutRows(t *testing.T) int {
	t.Helper()
	var rows int
	require.NoError(t, storage.DB.QueryRow(`SELECT COUNT(*) FROM groupchat_fanout`).Scan(&rows))
	return rows
}

func dmRows(t *testing.T) int {
	t.Helper()
	var rows int
	require.NoError(t, storage.DB.QueryRow(`SELECT COUNT(*) FROM messages`).Scan(&rows))
	return rows
}

func assertNoDM(t *testing.T, event *nostr.Event) {
	t.Helper()
	msg, err := messaging.GetStore()
	require.NoError(t, err)
	got, err := msg.GetMessage(hex.EncodeToString(event.ID[:]))
	require.NoError(t, err)
	assert.Nil(t, got)
}

// useHome points the process at one device's HOME, keeping what earlier calls
// stored there, so a test can play two machines that share only relay events.
func useHome(t *testing.T, home string) *groupchat.Store {
	t.Helper()
	t.Setenv("HOME", home)
	messaging.ResetStoreForTest()
	t.Cleanup(messaging.ResetStoreForTest)
	require.NoError(t, messaging.InitStorage())
	store, err := groupchat.NewStore(storage.DB)
	require.NoError(t, err)
	return store
}

func queuedEvent(t *testing.T, envelopeType, recipient string) *nostr.Event {
	t.Helper()
	var raw string
	require.NoError(t, storage.DB.QueryRow(`SELECT event_json FROM groupchat_fanout WHERE envelope_type = ? AND recipient_npub = ?`, envelopeType, recipient).Scan(&raw))
	var event nostr.Event
	require.NoError(t, json.Unmarshal([]byte(raw), &event))
	return &event
}

// deliveries numbers the throwaway receiver identities one test creates.
var deliveries int

func deliver(t *testing.T, event *nostr.Event, recipientSK nostr.SecretKey) {
	t.Helper()
	deliveries++
	ks, me, _ := newDaemonIdentity(t, fmt.Sprintf("receiver%d", deliveries))
	logs, stores, notes, replies := receiveCounters()
	processed, err := processIncomingEvent(event, recipientSK, newSeenSet(), true, true, me, ks, countingHooks(logs, stores, notes, replies))
	require.NoError(t, err)
	require.True(t, processed)
}

func groupState(t *testing.T, npub, groupID string) string {
	t.Helper()
	var state string
	require.NoError(t, storage.DB.QueryRow(`SELECT state FROM groupchat_groups WHERE local_npub = ? AND group_id = ?`, npub, groupID).Scan(&state))
	return state
}

// Two devices, each holding only its own key, reach an active group using
// nothing but the events the relay would carry: invite, accept, activate.
func TestInviteAcceptActivateAcrossTwoHomes(t *testing.T) {
	aliceSK, bobSK := nostr.Generate(), nostr.Generate()
	alice, bob := common.EncodeNpub(aliceSK.Public()), common.EncodeNpub(bobSK.Public())
	homeA, homeB := t.TempDir(), t.TempDir()

	creator := useHome(t, homeA)
	draft, err := creator.CreateGroup("planning", alice, []string{bob})
	require.NoError(t, err)
	_, err = creator.PrepareControlFanout(aliceSK, draft.Invitations[0], 0, 10)
	require.NoError(t, err)
	invite := queuedEvent(t, "invite", bob)

	invitee := useHome(t, homeB)
	deliver(t, invite, bobSK)
	accept, err := invitee.AcceptInvite(draft.Invitations[0].InviteID, bob)
	require.NoError(t, err)
	_, err = invitee.PrepareControlFanout(bobSK, accept, 0, 10)
	require.NoError(t, err)
	acceptEvent := queuedEvent(t, "accept", alice)
	assert.Equal(t, "pending", groupState(t, bob, draft.Group.ID), "bob waits for the creator's activation")

	useHome(t, homeA)
	deliver(t, acceptEvent, aliceSK)
	assert.Equal(t, "active", groupState(t, alice, draft.Group.ID))
	activation := queuedEvent(t, "activate", bob)

	useHome(t, homeB)
	deliver(t, activation, bobSK)
	assert.Equal(t, "active", groupState(t, bob, draft.Group.ID))
	assert.Zero(t, dmRows(t), "group control traffic is not a DM")
}

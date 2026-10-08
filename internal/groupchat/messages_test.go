package groupchat

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLogicalIDConflictAndUnknownMemberRejected(t *testing.T) {
	g := newThreeMemberGroup(t)
	g.activate()
	logicalID := mustOpaque(t)
	first := buildIncoming(t, g.alice, g.bob, Envelope{Type: EnvelopeMessage, Version: Version,
		GroupID: g.draft.Group.ID, LogicalID: logicalID, Body: "original"})
	inserted, err := g.bob.store.ReceiveMessage(first)
	require.NoError(t, err)
	require.True(t, inserted)
	conflict := buildIncoming(t, g.alice, g.bob, Envelope{Type: EnvelopeMessage, Version: Version,
		GroupID: g.draft.Group.ID, LogicalID: logicalID, Body: "altered"})
	_, err = g.bob.store.ReceiveMessage(conflict)
	require.ErrorIs(t, err, ErrLogicalIDConflict)

	stranger := newMember(t)
	unknown := buildIncoming(t, stranger, g.bob, Envelope{Type: EnvelopeMessage, Version: Version,
		GroupID: g.draft.Group.ID, LogicalID: mustOpaque(t), Body: "untrusted sender"})
	_, err = g.bob.store.ReceiveMessage(unknown)
	require.ErrorIs(t, err, ErrProtocolMismatch)
}

func TestStoreLocalMessageIdempotencyAndConflict(t *testing.T) {
	g := newThreeMemberGroup(t)
	g.activate()

	logicalID := mustOpaque(t)
	body := "hello groupchat"

	// First local store succeeds.
	msg1, err := g.alice.store.StoreLocalMessage(g.alice.npub, g.draft.Group.ID, logicalID, body, 1000)
	require.NoError(t, err)
	require.Equal(t, logicalID, msg1.ID)
	require.Equal(t, body, msg1.Plaintext)

	// Same logical ID + same body: idempotent, succeeds and does not duplicate in DB.
	msg2, err := g.alice.store.StoreLocalMessage(g.alice.npub, g.draft.Group.ID, logicalID, body, 1000)
	require.NoError(t, err)
	require.Equal(t, logicalID, msg2.ID)
	require.Equal(t, body, msg2.Plaintext)

	messages, err := g.alice.store.GroupMessages(g.alice.npub, g.draft.Group.ID, 10)
	require.NoError(t, err)
	require.Len(t, messages, 1)
	require.Equal(t, logicalID, messages[0].ID)

	// Same logical ID + altered body: returns ErrLogicalIDConflict.
	_, err = g.alice.store.StoreLocalMessage(g.alice.npub, g.draft.Group.ID, logicalID, "altered body", 1000)
	require.ErrorIs(t, err, ErrLogicalIDConflict)
}


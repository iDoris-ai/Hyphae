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

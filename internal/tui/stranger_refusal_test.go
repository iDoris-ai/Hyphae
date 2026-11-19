package tui

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"fiatjaf.com/nostr"
	"github.com/stretchr/testify/require"

	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/internal/identity"
	"github.com/iDoris-ai/hyphae/internal/messaging"
	"github.com/iDoris-ai/hyphae/internal/social/contact"
	"github.com/iDoris-ai/hyphae/internal/storage"
)

func TestRefusalShownInStatus(t *testing.T) {
	limit := storage.SendRefusal(contact.ErrStrangerMessageLimit, "npub1x")
	require.Contains(t, refusalText(limit), "already sent 2 messages")
	daily := storage.SendRefusal(&contact.DailyLimitError{Used: 10, ResetsAt: 1_700_000_000}, "npub1x")
	require.Contains(t, refusalText(daily), "the next one frees at")
	blocked := storage.SendRefusal(contact.ErrPeerBlocked, "npub1x")
	require.Contains(t, refusalText(blocked), "friend unblock")
	require.Equal(t, "boom", refusalText(errors.New("boom")))

	status := formatOutboxStatus(outboxDeliveryUpdate{refused: refusalText(blocked)})
	require.True(t, strings.HasPrefix(status, "Not sent: "), status)
}

// The TUI send path runs the stranger gate: a blocked peer's message is
// refused with a reason and never queued.
func TestTUISendRefusedForBlockedPeer(t *testing.T) {
	cleanup := setupTestEnv(t)
	defer cleanup()
	ks, err := identity.LoadKeyStore()
	require.NoError(t, err)
	dave := common.EncodeNpub(nostr.Generate().Public())
	require.NoError(t, identity.AddContact(ks, "dave", dave))
	var accept atomic.Bool
	relayURL, _ := startTUIOutboxRelay(t, &accept, nil)
	model, err := NewChatModel("dave", relayURL)
	require.NoError(t, err)
	defer model.Close()
	require.NoError(t, messaging.InitStorage())
	_, err = storage.NewRelationStore(storage.DB).Update(model.myIdentity.Npub, dave, 1,
		func(p storage.PeerRelation) (storage.PeerRelation, error) {
			p.Relation = contact.Block(p.Relation)
			return p, nil
		})
	require.NoError(t, err)

	model.sendQueuedMessage(context.Background(), outboxSendRequest{requestID: 7, content: "hi"})
	update := waitTUIOutboxUpdate(t, model.outboxUpdates, func(u outboxDeliveryUpdate) bool { return u.requestID == 7 })
	require.Equal(t, messaging.AgentMessageFailed, update.state)
	require.Contains(t, update.refused, "unblock")
	outbox, err := messaging.LoadOutbox()
	require.NoError(t, err)
	require.Empty(t, outbox.Entries)
}

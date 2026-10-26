package daemon

import (
	"encoding/json"
	"testing"

	"fiatjaf.com/nostr"
	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An invite queued on the creator's device must reach an invitee whose HOME
// holds no creator key: the daemon of the second HOME alone stores it.
func TestQueuedInviteReachesInviteeOnAnotherHome(t *testing.T) {
	aliceSK := nostr.Generate()
	bobSK := nostr.Generate()
	bobNpub := common.EncodeNpub(bobSK.Public())

	// HOME A: Alice creates the group and queues the invite for Bob.
	creator := openInboundFixture(t)
	draft, err := creator.CreateGroup("planning", common.EncodeNpub(aliceSK.Public()), []string{bobNpub})
	require.NoError(t, err)
	require.Len(t, draft.Invitations, 1)
	_, err = creator.PrepareControlFanout(aliceSK, draft.Invitations[0], 0, 10)
	require.NoError(t, err)
	var raw string
	require.NoError(t, storage.DB.QueryRow(`SELECT event_json FROM groupchat_fanout WHERE envelope_type = 'invite' AND recipient_npub = ?`, bobNpub).Scan(&raw))
	var event nostr.Event
	require.NoError(t, json.Unmarshal([]byte(raw), &event))

	// HOME B: only Bob's key exists. The same relay event is processed.
	openInboundDB(t)
	ks, bob, _ := newDaemonIdentity(t, "bob")
	logs, stores, notes, replies := receiveCounters()
	processed, err := processIncomingEvent(&event, bobSK, newSeenSet(), true, true, bob, ks, countingHooks(logs, stores, notes, replies))
	require.NoError(t, err)
	assert.True(t, processed)

	var state, invitee, groupID string
	require.NoError(t, storage.DB.QueryRow(`SELECT state, invitee_npub, group_id FROM groupchat_invites`).Scan(&state, &invitee, &groupID))
	assert.Equal(t, "pending", state)
	assert.Equal(t, bobNpub, invitee)
	assert.Equal(t, draft.Group.ID, groupID)
	assert.Zero(t, dmRows(t), "an invite is a group control message, not a DM")
}

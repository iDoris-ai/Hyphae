package groupchat

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTxVariantsRollbackLeavesNoRows(t *testing.T) {
	t.Parallel()

	t.Run("createGroupTx rollback leaves no rows", func(t *testing.T) {
		alice := newMember(t)
		bob := newMember(t)

		tx, err := alice.store.beginImmediate()
		require.NoError(t, err)

		draft, err := alice.store.createGroupTx(tx, "rollback-group", alice.npub, []string{bob.npub})
		require.NoError(t, err)
		require.NotEmpty(t, draft.Group.ID)

		require.NoError(t, tx.Rollback())

		var groupCount, memberCount, inviteCount int
		require.NoError(t, alice.db.QueryRow(`SELECT COUNT(*) FROM groupchat_groups WHERE group_id = ?`, draft.Group.ID).Scan(&groupCount))
		require.Equal(t, 0, groupCount, "groupchat_groups must have 0 rows after rollback")

		require.NoError(t, alice.db.QueryRow(`SELECT COUNT(*) FROM groupchat_members WHERE group_id = ?`, draft.Group.ID).Scan(&memberCount))
		require.Equal(t, 0, memberCount, "groupchat_members must have 0 rows after rollback")

		require.NoError(t, alice.db.QueryRow(`SELECT COUNT(*) FROM groupchat_invites WHERE group_id = ?`, draft.Group.ID).Scan(&inviteCount))
		require.Equal(t, 0, inviteCount, "groupchat_invites must have 0 rows after rollback")
	})

	t.Run("acceptInviteTx rollback leaves no state changes", func(t *testing.T) {
		g := newThreeMemberGroup(t)
		bobInvite := g.invites[g.bob.npub]

		tx, err := g.bob.store.beginImmediate()
		require.NoError(t, err)

		env, err := g.bob.store.acceptInviteTx(tx, bobInvite.InviteID, g.bob.npub)
		require.NoError(t, err)
		require.Equal(t, EnvelopeAccept, env.Type)

		require.NoError(t, tx.Rollback())

		var inviteState, memberState string
		require.NoError(t, g.bob.db.QueryRow(`SELECT state FROM groupchat_invites WHERE invite_id = ?`, bobInvite.InviteID).Scan(&inviteState))
		require.Equal(t, string(InvitePending), inviteState, "invite state must remain pending after rollback")

		require.NoError(t, g.bob.db.QueryRow(`SELECT state FROM groupchat_members WHERE group_id = ? AND npub = ?`, g.draft.Group.ID, g.bob.npub).Scan(&memberState))
		require.Equal(t, "pending", memberState, "member state must remain pending after rollback")
	})

	t.Run("declineInviteTx rollback leaves no state changes", func(t *testing.T) {
		g := newThreeMemberGroup(t)
		bobInvite := g.invites[g.bob.npub]

		tx, err := g.bob.store.beginImmediate()
		require.NoError(t, err)

		env, err := g.bob.store.declineInviteTx(tx, bobInvite.InviteID, g.bob.npub)
		require.NoError(t, err)
		require.Equal(t, EnvelopeDecline, env.Type)

		require.NoError(t, tx.Rollback())

		var inviteState, groupState string
		require.NoError(t, g.bob.db.QueryRow(`SELECT state FROM groupchat_invites WHERE invite_id = ?`, bobInvite.InviteID).Scan(&inviteState))
		require.Equal(t, string(InvitePending), inviteState, "invite state must remain pending after rollback")

		require.NoError(t, g.bob.db.QueryRow(`SELECT state FROM groupchat_groups WHERE group_id = ?`, g.draft.Group.ID).Scan(&groupState))
		require.Equal(t, string(StatePending), groupState, "group state must remain pending after rollback")
	})

	t.Run("receiveAcceptanceTx rollback leaves no state changes", func(t *testing.T) {
		g := newThreeMemberGroup(t)
		bobInvite := g.invites[g.bob.npub]

		bobAccept, err := g.bob.store.AcceptInvite(bobInvite.InviteID, g.bob.npub)
		require.NoError(t, err)
		verifiedBob := buildIncoming(t, g.bob, g.alice, bobAccept)

		tx, err := g.alice.store.beginImmediate()
		require.NoError(t, err)

		activations, err := g.alice.store.receiveAcceptanceTx(tx, verifiedBob)
		require.NoError(t, err)
		require.Empty(t, activations, "first acceptance must not produce activation envelopes")

		require.NoError(t, tx.Rollback())

		var inviteState, acceptEventID, memberState, groupState string
		require.NoError(t, g.alice.db.QueryRow(`SELECT state, accept_event_id FROM groupchat_invites WHERE invite_id = ?`, bobInvite.InviteID).Scan(&inviteState, &acceptEventID))
		require.Equal(t, string(InvitePending), inviteState, "invite state must remain pending after rollback")
		require.Empty(t, acceptEventID, "accept_event_id must remain empty after rollback")

		require.NoError(t, g.alice.db.QueryRow(`SELECT state FROM groupchat_members WHERE group_id = ? AND npub = ?`, g.draft.Group.ID, g.bob.npub).Scan(&memberState))
		require.Equal(t, "pending", memberState, "member state must remain pending after rollback")

		require.NoError(t, g.alice.db.QueryRow(`SELECT state FROM groupchat_groups WHERE group_id = ?`, g.draft.Group.ID).Scan(&groupState))
		require.Equal(t, string(StatePending), groupState, "group state must remain pending after rollback")

		// Now commit bob's acceptance and test rollback on the final acceptance (carol)
		_, err = g.alice.store.ReceiveAcceptance(verifiedBob)
		require.NoError(t, err)

		carolInvite := g.invites[g.carol.npub]
		carolAccept, err := g.carol.store.AcceptInvite(carolInvite.InviteID, g.carol.npub)
		require.NoError(t, err)
		verifiedCarol := buildIncoming(t, g.carol, g.alice, carolAccept)

		txFinal, err := g.alice.store.beginImmediate()
		require.NoError(t, err)

		finalActivations, err := g.alice.store.receiveAcceptanceTx(txFinal, verifiedCarol)
		require.NoError(t, err)
		require.Len(t, finalActivations, 2, "final acceptance must return activation envelopes")

		require.NoError(t, txFinal.Rollback())

		var finalGroupState string
		require.NoError(t, g.alice.db.QueryRow(`SELECT state FROM groupchat_groups WHERE group_id = ?`, g.draft.Group.ID).Scan(&finalGroupState))
		require.Equal(t, string(StatePending), finalGroupState, "group state must still be pending because activating transition was rolled back")
	})

	t.Run("cancelGroupTx rollback leaves no state changes", func(t *testing.T) {
		g := newThreeMemberGroup(t)

		tx, err := g.alice.store.beginImmediate()
		require.NoError(t, err)

		envs, err := g.alice.store.cancelGroupTx(tx, g.alice.npub, g.draft.Group.ID)
		require.NoError(t, err)
		require.Len(t, envs, 2)

		require.NoError(t, tx.Rollback())

		var groupState string
		require.NoError(t, g.alice.db.QueryRow(`SELECT state FROM groupchat_groups WHERE group_id = ?`, g.draft.Group.ID).Scan(&groupState))
		require.Equal(t, string(StatePending), groupState, "group state must remain pending after rollback")

		var cancelledInvites int
		require.NoError(t, g.alice.db.QueryRow(`SELECT COUNT(*) FROM groupchat_invites WHERE group_id = ? AND state = ?`, g.draft.Group.ID, InviteCancelled).Scan(&cancelledInvites))
		require.Equal(t, 0, cancelledInvites, "no invites should be marked cancelled after rollback")
	})
}

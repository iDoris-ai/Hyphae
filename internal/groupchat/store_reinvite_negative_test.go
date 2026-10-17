package groupchat

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"
)

// reinviteSnapshot captures, in a deterministic order, every local row the
// invitee holds for one group across the group, member and invite tables.
type reinviteSnapshot struct {
	groups  [][]any
	members [][]any
	invites [][]any
}

func scanOrderedRows(t *testing.T, db *sql.DB, query string, args ...any) [][]any {
	t.Helper()
	rows, err := db.Query(query, args...)
	require.NoError(t, err)
	defer rows.Close()
	cols, err := rows.Columns()
	require.NoError(t, err)
	var out [][]any
	for rows.Next() {
		values := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		require.NoError(t, rows.Scan(ptrs...))
		out = append(out, values)
	}
	require.NoError(t, rows.Err())
	return out
}

func takeReinviteSnapshot(t *testing.T, member testMember, groupID string) reinviteSnapshot {
	t.Helper()
	return reinviteSnapshot{
		groups: scanOrderedRows(t, member.db, `SELECT local_npub, group_id, name, creator_npub, roster_json, roster_hash, state, created_at, updated_at
			FROM groupchat_groups WHERE local_npub = ? AND group_id = ? ORDER BY group_id`, member.npub, groupID),
		members: scanOrderedRows(t, member.db, `SELECT local_npub, group_id, npub, invite_id, state, is_creator
			FROM groupchat_members WHERE local_npub = ? AND group_id = ? ORDER BY npub`, member.npub, groupID),
		invites: scanOrderedRows(t, member.db, `SELECT local_npub, invite_id, group_id, creator_npub, invitee_npub, name, roster_json, roster_hash,
			state, invite_event_id, accept_event_id, activation_event_id, created_at, updated_at
			FROM groupchat_invites WHERE local_npub = ? AND group_id = ? ORDER BY invite_id`, member.npub, groupID),
	}
}

// TestReceiveInviteFreshInviteIDForExistingInviteeRefusedWithoutStateChange
// pins current-contract coverage: a creator-signed, otherwise identical
// invitation that carries a fresh invite_id for an invitee who already holds
// an invitation in the same group is refused by ReceiveInvite, and the
// invitee's local group/member/invite rows are byte-for-byte unchanged. The
// current store does not map this refusal to a sentinel error, so the test
// asserts refusal (non-nil error, created=false) and no state change only.
func TestReceiveInviteFreshInviteIDForExistingInviteeRefusedWithoutStateChange(t *testing.T) {
	cases := []struct {
		name       string
		prepare    func(g *threeMemberGroup)
		wantGroup  GroupState
		wantInvite InviteState
	}{
		{name: "pending_group_pending_invite", prepare: func(*threeMemberGroup) {}, wantGroup: StatePending, wantInvite: InvitePending},
		{name: "pending_group_accepted_invite", prepare: func(g *threeMemberGroup) {
			_, err := g.bob.store.AcceptInvite(g.invites[g.bob.npub].InviteID, g.bob.npub)
			require.NoError(g.t, err)
		}, wantGroup: StatePending, wantInvite: InviteAccepted},
		{name: "active_group_active_invite", prepare: func(g *threeMemberGroup) { g.activate() }, wantGroup: StateActive, wantInvite: InviteActive},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := newThreeMemberGroup(t)
			tc.prepare(g)
			groupID := g.draft.Group.ID
			original := g.invites[g.bob.npub]

			group, err := g.bob.store.GetGroup(g.bob.npub, groupID)
			require.NoError(t, err)
			require.Equal(t, tc.wantGroup, group.State, "fixture group state")
			var inviteState string
			require.NoError(t, g.bob.db.QueryRow(`SELECT state FROM groupchat_invites WHERE local_npub = ? AND invite_id = ?`,
				g.bob.npub, original.InviteID).Scan(&inviteState))
			require.Equal(t, string(tc.wantInvite), inviteState, "fixture invite state")

			fresh := original
			fresh.InviteID = mustOpaque(t)
			require.NotEqual(t, original.InviteID, fresh.InviteID)

			before := takeReinviteSnapshot(t, g.bob, groupID)
			require.Len(t, before.groups, 1)
			require.Len(t, before.members, 3)
			require.Len(t, before.invites, 1)

			// Creator-signed, real NIP-44 envelope via the production verifier.
			created, err := g.bob.store.ReceiveInvite(buildIncoming(t, g.alice, g.bob, fresh))
			require.Error(t, err, "fresh invite_id for an existing invitee must be refused")
			t.Logf("refusal: %v", err)
			require.False(t, created)

			after := takeReinviteSnapshot(t, g.bob, groupID)
			require.Equal(t, before.groups, after.groups, "group row must not change")
			require.Equal(t, before.members, after.members, "member rows must not change")
			require.Equal(t, before.invites, after.invites, "invite rows must not change")

			var freshRows int
			require.NoError(t, g.bob.db.QueryRow(`SELECT
				(SELECT COUNT(*) FROM groupchat_invites WHERE local_npub = ? AND invite_id = ?) +
				(SELECT COUNT(*) FROM groupchat_members WHERE local_npub = ? AND invite_id = ?)`,
				g.bob.npub, fresh.InviteID, g.bob.npub, fresh.InviteID).Scan(&freshRows))
			require.Zero(t, freshRows, "fresh invite_id must not be persisted anywhere")

			// Replaying the original invitation remains an idempotent no-op.
			created, err = g.bob.store.ReceiveInvite(buildIncoming(t, g.alice, g.bob, original))
			require.NoError(t, err)
			require.False(t, created)
			require.Equal(t, before, takeReinviteSnapshot(t, g.bob, groupID), "original replay must not change state")
		})
	}
}

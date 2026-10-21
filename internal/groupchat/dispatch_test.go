package groupchat

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iDoris-ai/hyphae/internal/messaging"
	"github.com/stretchr/testify/require"
)

func TestVerifiedGroupDispatch(t *testing.T) {
	t.Run("bad version", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		alice, bob, carol := newMember(t), newMember(t), newMember(t)
		draft, err := alice.store.CreateGroup("planning", alice.npub, []string{bob.npub, carol.npub})
		require.NoError(t, err)
		invite := draft.Invitations[0]
		plain := strings.Replace(mustEncode(t, invite), MagicV1, ReservedPrefix+"v99\n", 1)
		calls := &dispatchCalls{}
		err = messaging.DispatchVerifiedGroup(dispatchMachines(bob.store, calls), verifiedPlain(t, alice, bob, plain))
		require.ErrorIs(t, err, messaging.ErrUnsupportedGroupVersion)
		require.Equal(t, dispatchCalls{}, *calls)
		require.Zero(t, groupTableRows(t, bob.db))
		requireNoDMHistory(t)
	})

	t.Run("invite", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		alice, bob, carol := newMember(t), newMember(t), newMember(t)
		draft, err := alice.store.CreateGroup("planning", alice.npub, []string{bob.npub, carol.npub})
		require.NoError(t, err)
		var invite Envelope
		for _, item := range draft.Invitations {
			if item.InviteeNpub == bob.npub {
				invite = item
			}
		}
		calls := &dispatchCalls{}
		require.NoError(t, messaging.DispatchVerifiedGroup(dispatchMachines(bob.store, calls), verifiedEnvelope(t, alice, bob, invite)))
		require.Equal(t, 1, calls.invite)
		require.Zero(t, calls.accept+calls.activate+calls.message)
		var state string
		require.NoError(t, bob.db.QueryRow(`SELECT state FROM groupchat_invites WHERE local_npub = ? AND invite_id = ?`, bob.npub, invite.InviteID).Scan(&state))
		require.Equal(t, string(InvitePending), state)
		requireNoDMHistory(t)
	})

	t.Run("accept", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		g := newThreeMemberGroup(t)
		invite := g.invites[g.bob.npub]
		accept, err := g.bob.store.AcceptInvite(invite.InviteID, g.bob.npub)
		require.NoError(t, err)
		calls := &dispatchCalls{}
		require.NoError(t, messaging.DispatchVerifiedGroup(dispatchMachines(g.alice.store, calls), verifiedEnvelope(t, g.bob, g.alice, accept)))
		require.Equal(t, 1, calls.accept)
		require.Zero(t, calls.invite+calls.activate+calls.message)
		var state string
		require.NoError(t, g.alice.db.QueryRow(`SELECT state FROM groupchat_invites WHERE local_npub = ? AND invite_id = ?`, g.alice.npub, invite.InviteID).Scan(&state))
		require.Equal(t, string(InviteAccepted), state)
		requireNoDMHistory(t)
	})

	t.Run("activation", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		g := newThreeMemberGroup(t)
		bobAccept, err := g.bob.store.AcceptInvite(g.invites[g.bob.npub].InviteID, g.bob.npub)
		require.NoError(t, err)
		carolAccept, err := g.carol.store.AcceptInvite(g.invites[g.carol.npub].InviteID, g.carol.npub)
		require.NoError(t, err)
		_, err = g.alice.store.ReceiveAcceptance(buildIncoming(t, g.bob, g.alice, bobAccept))
		require.NoError(t, err)
		batch, err := g.alice.store.ReceiveAcceptance(buildIncoming(t, g.carol, g.alice, carolAccept))
		require.NoError(t, err)
		var activation Envelope
		for _, item := range batch {
			if item.InviteeNpub == g.bob.npub {
				activation = item
			}
		}
		require.Equal(t, EnvelopeActivate, activation.Type)
		calls := &dispatchCalls{}
		require.NoError(t, messaging.DispatchVerifiedGroup(dispatchMachines(g.bob.store, calls), verifiedEnvelope(t, g.alice, g.bob, activation)))
		require.Equal(t, 1, calls.activate)
		require.Zero(t, calls.invite+calls.accept+calls.message)
		group, err := g.bob.store.GetGroup(g.bob.npub, g.draft.Group.ID)
		require.NoError(t, err)
		require.Equal(t, StateActive, group.State)
		requireNoDMHistory(t)
	})

	t.Run("message", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		g := newThreeMemberGroup(t)
		g.activate()
		envelope := Envelope{Type: EnvelopeMessage, Version: Version, GroupID: g.draft.Group.ID, LogicalID: mustOpaque(t), Body: "hello"}
		msg := verifiedEnvelope(t, g.alice, g.bob, envelope)
		calls := &dispatchCalls{}
		machines := dispatchMachines(g.bob.store, calls)
		require.NoError(t, messaging.DispatchVerifiedGroup(machines, msg))
		require.NoError(t, messaging.DispatchVerifiedGroup(machines, msg))
		require.Equal(t, 2, calls.message)
		require.Zero(t, calls.invite+calls.accept+calls.activate)
		var rows int
		require.NoError(t, g.bob.db.QueryRow(`SELECT COUNT(*) FROM groupchat_messages`).Scan(&rows))
		require.Equal(t, 1, rows)
		requireNoDMHistory(t)
	})

	t.Run("no daemon", func(t *testing.T) {
		src, err := os.ReadFile("../messaging/group_dispatch.go")
		require.NoError(t, err)
		text := string(src)
		require.NotContains(t, text, "daemon")
		require.NotContains(t, text, "Start(")
	})
}

type dispatchCalls struct {
	invite, accept, activate, message int
}

func dispatchMachines(store *Store, calls *dispatchCalls) messaging.GroupMachines {
	apply := func(counter *int, fn func(VerifiedIncoming) error) func(messaging.VerifiedAgentMessage) error {
		return func(message messaging.VerifiedAgentMessage) error {
			*counter++
			incoming, err := FromVerifiedAgentMessage(message)
			if err != nil {
				return err
			}
			return fn(incoming)
		}
	}
	return messaging.GroupMachines{
		Invite:   apply(&calls.invite, func(v VerifiedIncoming) error { _, err := store.ReceiveInvite(v); return err }),
		Accept:   apply(&calls.accept, func(v VerifiedIncoming) error { _, err := store.ReceiveAcceptance(v); return err }),
		Activate: apply(&calls.activate, func(v VerifiedIncoming) error { _, err := store.ReceiveActivation(v); return err }),
		Message:  apply(&calls.message, func(v VerifiedIncoming) error { _, err := store.ReceiveMessage(v); return err }),
	}
}

func verifiedEnvelope(t *testing.T, sender, recipient testMember, envelope Envelope) messaging.VerifiedAgentMessage {
	t.Helper()
	return verifiedPlain(t, sender, recipient, mustEncode(t, envelope))
}

func verifiedPlain(t *testing.T, sender, recipient testMember, plaintext string) messaging.VerifiedAgentMessage {
	t.Helper()
	event := testAgentEvent(t, plaintext, sender.sk, recipient.sk, true)
	message, err := messaging.VerifyAgentMessage(event, recipient.sk)
	require.NoError(t, err)
	return message
}

func groupTableRows(t *testing.T, db *sql.DB) int {
	t.Helper()
	total := 0
	for _, table := range []string{"groupchat_groups", "groupchat_invites", "groupchat_members", "groupchat_messages", "groupchat_fanout"} {
		var n int
		require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM `+table).Scan(&n))
		total += n
	}
	return total
}

func requireNoDMHistory(t *testing.T) {
	t.Helper()
	_, err := os.Stat(filepath.Join(os.Getenv("HOME"), ".hyphae", "messages.db"))
	require.True(t, os.IsNotExist(err))
}

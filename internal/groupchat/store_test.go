package groupchat

import (
	"database/sql"
	"path/filepath"
	"testing"

	"fiatjaf.com/nostr"
	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

type testMember struct {
	sk    nostr.SecretKey
	npub  string
	db    *sql.DB
	store *Store
}

type threeMemberGroup struct {
	t       *testing.T
	alice   testMember
	bob     testMember
	carol   testMember
	draft   GroupDraft
	invites map[string]Envelope
}

func newMember(t *testing.T) testMember {
	t.Helper()
	sk := nostr.Generate()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "groupchat.db"))
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	store, err := NewStore(db)
	require.NoError(t, err)
	return testMember{sk: sk, npub: common.EncodeNpub(sk.Public()), db: db, store: store}
}

func buildIncoming(t *testing.T, sender, recipient testMember, envelope Envelope) VerifiedIncoming {
	t.Helper()
	encoded, err := Encode(envelope)
	require.NoError(t, err)
	event := testAgentEvent(t, encoded, sender.sk, recipient.sk, true)
	verified, err := VerifyIncoming(event, recipient.sk)
	require.NoError(t, err)
	return verified
}

func newThreeMemberGroup(t *testing.T) *threeMemberGroup {
	t.Helper()
	g := &threeMemberGroup{t: t, alice: newMember(t), bob: newMember(t), carol: newMember(t)}
	draft, err := g.alice.store.CreateGroup("planning", g.alice.npub, []string{g.bob.npub, g.carol.npub})
	require.NoError(t, err)
	g.draft = draft
	g.invites = make(map[string]Envelope)
	for _, invite := range draft.Invitations {
		g.invites[invite.InviteeNpub] = invite
		target := g.member(invite.InviteeNpub)
		created, err := target.store.ReceiveInvite(buildIncoming(t, g.alice, target, invite))
		require.NoError(t, err)
		require.True(t, created, "invite must be explicit pending state, not auto-accepted")
	}
	return g
}

func (g *threeMemberGroup) member(npub string) testMember {
	if npub == g.alice.npub {
		return g.alice
	}
	if npub == g.bob.npub {
		return g.bob
	}
	if npub == g.carol.npub {
		return g.carol
	}
	return testMember{}
}

func (g *threeMemberGroup) activate() {
	g.t.Helper()
	var activations []Envelope
	for _, member := range []testMember{g.bob, g.carol} {
		invite := g.invites[member.npub]
		accept, err := member.store.AcceptInvite(invite.InviteID, member.npub)
		require.NoError(g.t, err)
		activationBatch, err := g.alice.store.ReceiveAcceptance(buildIncoming(g.t, member, g.alice, accept))
		require.NoError(g.t, err)
		if len(activationBatch) > 0 {
			activations = activationBatch
		}
	}
	require.Len(g.t, activations, 2, "all fixed-roster invitees must accept before activation")
	for _, activation := range activations {
		target := g.member(activation.InviteeNpub)
		event := testAgentEvent(g.t, mustEncode(g.t, activation), g.alice.sk, target.sk, true)
		require.NoError(g.t, g.alice.store.MarkActivationQueued(g.alice.npub, g.draft.Group.ID,
			activation.InviteID, event.ID.Hex(), true))
		require.NoError(g.t, g.alice.store.MarkActivationQueued(g.alice.npub, g.draft.Group.ID,
			activation.InviteID, event.ID.Hex(), true), "replaying the same durable activation is idempotent")
		require.ErrorIs(g.t, g.alice.store.MarkActivationQueued(g.alice.npub, g.draft.Group.ID,
			activation.InviteID, mustOpaque(g.t)+mustOpaque(g.t), true), ErrProtocolMismatch)
		applied, err := target.store.ReceiveActivation(mustVerify(g.t, event, target.sk))
		require.NoError(g.t, err)
		require.True(g.t, applied)
		applied, err = target.store.ReceiveActivation(mustVerify(g.t, event, target.sk))
		require.NoError(g.t, err)
		require.False(g.t, applied, "activation replay is idempotent")
	}
	creatorGroup, err := g.alice.store.GetGroup(g.alice.npub, g.draft.Group.ID)
	require.NoError(g.t, err)
	require.Equal(g.t, StateActive, creatorGroup.State)
	for _, member := range []testMember{g.bob, g.carol} {
		group, err := member.store.GetGroup(member.npub, g.draft.Group.ID)
		require.NoError(g.t, err)
		require.Equal(g.t, StateActive, group.State)
	}
}

func TestExplicitFixedRosterAcceptanceActivationAndRestart(t *testing.T) {
	g := newThreeMemberGroup(t)
	group, err := g.bob.store.GetGroup(g.bob.npub, g.draft.Group.ID)
	require.NoError(t, err)
	require.Equal(t, StatePending, group.State)
	_, err = g.bob.store.AcceptInvite(g.invites[g.bob.npub].InviteID, g.carol.npub)
	require.ErrorIs(t, err, ErrIdentityMismatch)

	accept, err := g.bob.store.AcceptInvite(g.invites[g.bob.npub].InviteID, g.bob.npub)
	require.NoError(t, err)
	replay, err := g.bob.store.AcceptInvite(g.invites[g.bob.npub].InviteID, g.bob.npub)
	require.NoError(t, err)
	require.Equal(t, accept, replay)
	partial, err := g.alice.store.ReceiveAcceptance(buildIncoming(t, g.bob, g.alice, accept))
	require.NoError(t, err)
	require.Empty(t, partial, "a subset cannot activate")
	group, err = g.alice.store.GetGroup(g.alice.npub, g.draft.Group.ID)
	require.NoError(t, err)
	require.Equal(t, StatePending, group.State)

	accept, err = g.carol.store.AcceptInvite(g.invites[g.carol.npub].InviteID, g.carol.npub)
	require.NoError(t, err)
	activations, err := g.alice.store.ReceiveAcceptance(buildIncoming(t, g.carol, g.alice, accept))
	require.NoError(t, err)
	require.Len(t, activations, 2)
	// Reopening the database preserves the creator's activating state and
	// acceptance set; it does not need to infer activation from a relay ACK.
	reopened, err := NewStore(g.alice.db)
	require.NoError(t, err)
	group, err = reopened.GetGroup(g.alice.npub, g.draft.Group.ID)
	require.NoError(t, err)
	require.Equal(t, StateActivating, group.State)

	for _, activation := range activations {
		target := g.member(activation.InviteeNpub)
		event := testAgentEvent(t, mustEncode(t, activation), g.alice.sk, target.sk, true)
		require.NoError(t, reopened.MarkActivationQueued(g.alice.npub, g.draft.Group.ID, activation.InviteID, event.ID.Hex(), true))
		_, err = target.store.ReceiveActivation(mustVerify(t, event, target.sk))
		require.NoError(t, err)
	}
	group, err = reopened.GetGroup(g.alice.npub, g.draft.Group.ID)
	require.NoError(t, err)
	require.Equal(t, StateActive, group.State)
}

func TestCreatorAuthorityRosterBindingAndLocalLeave(t *testing.T) {
	g := newThreeMemberGroup(t)
	g.activate()
	invite := g.invites[g.bob.npub]
	attackerRoster := []string{g.carol.npub, g.bob.npub}
	attackerHash, canonicalAttackerRoster, err := CanonicalRosterHash(invite.GroupID, g.carol.npub, invite.Name, attackerRoster)
	require.NoError(t, err)
	spoof := Envelope{Type: EnvelopeActivate, Version: Version, GroupID: invite.GroupID,
		CreatorNpub: g.carol.npub, InviteID: invite.InviteID, RosterHash: attackerHash,
		InviteeNpub: g.bob.npub, Name: invite.Name, Members: canonicalAttackerRoster}
	_, err = g.bob.store.ReceiveActivation(buildIncoming(t, g.carol, g.bob, spoof))
	require.ErrorIs(t, err, ErrProtocolMismatch)
	spoofRoster := []string{g.alice.npub, g.bob.npub}
	spoofHash, spoofRoster, err := CanonicalRosterHash(invite.GroupID, g.alice.npub, invite.Name, spoofRoster)
	require.NoError(t, err)
	spoof = Envelope{Type: EnvelopeActivate, Version: Version, GroupID: invite.GroupID,
		CreatorNpub: g.alice.npub, InviteID: invite.InviteID, RosterHash: spoofHash,
		InviteeNpub: g.bob.npub, Name: invite.Name, Members: spoofRoster}
	_, err = g.bob.store.ReceiveActivation(buildIncoming(t, g.alice, g.bob, spoof))
	require.Error(t, err)

	require.NoError(t, g.bob.store.LeaveGroup(g.bob.npub, g.draft.Group.ID))
	_, err = g.bob.store.GroupMessages(g.bob.npub, g.draft.Group.ID, 10)
	require.ErrorIs(t, err, ErrGroupLeft)
	_, err = g.bob.store.ReceiveMessage(buildIncoming(t, g.alice, g.bob, Envelope{
		Type: EnvelopeMessage, Version: Version, GroupID: g.draft.Group.ID,
		LogicalID: mustOpaque(t), Body: "after local leave",
	}))
	require.ErrorIs(t, err, ErrGroupLeft)
	group, err := g.bob.store.GetGroup(g.bob.npub, g.draft.Group.ID)
	require.NoError(t, err)
	require.Equal(t, StateLeft, group.State, "leave is local; creator state is unaffected")
	creatorGroup, err := g.alice.store.GetGroup(g.alice.npub, g.draft.Group.ID)
	require.NoError(t, err)
	require.Equal(t, StateActive, creatorGroup.State)
}

func TestExplicitDeclineCancelsInsteadOfShrinkingRoster(t *testing.T) {
	g := newThreeMemberGroup(t)
	decline, err := g.bob.store.DeclineInvite(g.invites[g.bob.npub].InviteID, g.bob.npub)
	require.NoError(t, err)
	declineReplay, err := g.bob.store.DeclineInvite(g.invites[g.bob.npub].InviteID, g.bob.npub)
	require.NoError(t, err)
	require.Equal(t, decline, declineReplay)
	cancellations, err := g.alice.store.ReceiveDecline(buildIncoming(t, g.bob, g.alice, decline))
	require.NoError(t, err)
	require.Len(t, cancellations, 1, "creator must be able to cancel the remaining invitee")
	require.Equal(t, g.carol.npub, cancellations[0].InviteeNpub)
	err = g.carol.store.ReceiveCancel(buildIncoming(t, g.alice, g.carol, cancellations[0]))
	require.NoError(t, err)
	for _, member := range []testMember{g.alice, g.bob, g.carol} {
		group, err := member.store.GetGroup(member.npub, g.draft.Group.ID)
		require.NoError(t, err)
		require.Equal(t, StateCancelled, group.State, "decline cancels the immutable roster")
	}
	_, err = g.carol.store.AcceptInvite(g.invites[g.carol.npub].InviteID, g.carol.npub)
	require.ErrorIs(t, err, ErrInvalidTransition)
}

func mustEncode(t *testing.T, e Envelope) string {
	t.Helper()
	value, err := Encode(e)
	require.NoError(t, err)
	return value
}

func mustVerify(t *testing.T, event nostr.Event, recipient nostr.SecretKey) VerifiedIncoming {
	t.Helper()
	value, err := VerifyIncoming(event, recipient)
	require.NoError(t, err)
	return value
}

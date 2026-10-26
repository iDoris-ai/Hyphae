package groupchat

import (
	"database/sql"
	"errors"
	"fmt"
	"os"

	"fiatjaf.com/nostr"
	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/internal/identity"
	"github.com/iDoris-ai/hyphae/internal/messaging"
	"github.com/iDoris-ai/hyphae/internal/storage"
	"github.com/iDoris-ai/hyphae/pkg/types"
	"github.com/urfave/cli/v3"
)

// controlFanoutRetries is the retry budget stored with each queued invite.
const controlFanoutRetries = 10

type groupIDView struct {
	GroupID string `json:"group_id"`
}

// acceptView is the accept result. State is "active" once every member has
// accepted and this device holds the activation, "pending" while it waits.
type acceptView struct {
	GroupID string `json:"group_id"`
	State   string `json:"state"`
}

type inviteListEntry struct {
	InviteID    string `json:"invite_id"`
	InviteeNpub string `json:"invitee_npub"`
	GroupID     string `json:"group_id"`
	State       string `json:"state"`
}

type cliSession struct {
	ks    *types.KeyStore
	sk    nostr.SecretKey
	me    string
	db    *sql.DB
	store *Store
}

func openSession(c *cli.Command) (cliSession, error) {
	ks, err := identity.LoadKeyStoreForCommand(identity.KeyStoreCommandOptions{
		JSONMode:      common.JSONMode(c),
		RequireSecret: true,
		PasswordStdin: c.Bool("password-stdin"),
		Stdin:         os.Stdin,
	})
	if err != nil {
		return cliSession{}, err
	}
	sk, err := identity.GetSecretKey(ks, c.String("identity"))
	if err != nil {
		return cliSession{}, err
	}
	me, err := canonicalLocalNpub(common.EncodeNpub(sk.Public()))
	if err != nil {
		return cliSession{}, err
	}
	db, err := storage.OpenDB()
	if err != nil {
		return cliSession{}, err
	}
	store, err := NewStore(db)
	if err != nil {
		_ = db.Close()
		return cliSession{}, err
	}
	return cliSession{ks: ks, sk: sk, me: me, db: db, store: store}, nil
}

func secretForNpub(ks *types.KeyStore, npub string) (nostr.SecretKey, bool, error) {
	want, err := canonicalLocalNpub(npub)
	if err != nil {
		return nostr.SecretKey{}, false, err
	}
	for nickname, ident := range ks.Identities {
		if ident == nil || ident.Npub != want {
			continue
		}
		sk, err := identity.GetSecretKey(ks, nickname)
		if err != nil {
			return nostr.SecretKey{}, false, err
		}
		return sk, true, nil
	}
	return nostr.SecretKey{}, false, nil
}

func verifyDelivery(sender, recipient nostr.SecretKey, envelope Envelope) (VerifiedIncoming, nostr.Event, error) {
	plaintext, err := Encode(envelope)
	if err != nil {
		return VerifiedIncoming{}, nostr.Event{}, err
	}
	event, err := messaging.BuildAgentMessageEvent(sender, recipient.Public(), plaintext, nostr.Now())
	if err != nil {
		return VerifiedIncoming{}, nostr.Event{}, err
	}
	verified, err := VerifyIncoming(*event, recipient)
	if err != nil {
		return VerifiedIncoming{}, nostr.Event{}, err
	}
	return verified, *event, nil
}

func createGroupCommand(c *cli.Command) (groupIDView, error) {
	session, err := openSession(c)
	if err != nil {
		return groupIDView{}, err
	}
	defer session.db.Close()
	draft, err := session.store.CreateGroup(c.String("name"), session.me, c.StringSlice("invitee"))
	if err != nil {
		return groupIDView{}, err
	}
	for _, invitation := range draft.Invitations {
		recipientSK, found, err := secretForNpub(session.ks, invitation.InviteeNpub)
		if err != nil {
			return groupIDView{}, err
		}
		if !found {
			// The invitee lives on another device: queue the signed invite for
			// the outbox so the daemon delivers it through the relay.
			if _, err := session.store.PrepareControlFanout(session.sk, invitation, 0, controlFanoutRetries); err != nil {
				return groupIDView{}, fmt.Errorf("group %s was created but queueing the invite for %s failed: %w", draft.Group.ID, invitation.InviteeNpub, err)
			}
			continue
		}
		verified, _, err := verifyDelivery(session.sk, recipientSK, invitation)
		if err != nil {
			return groupIDView{}, err
		}
		if _, err := session.store.ReceiveInvite(verified); err != nil {
			return groupIDView{}, err
		}
	}
	return groupIDView{GroupID: draft.Group.ID}, nil
}

func (s *Store) listInvites(localNpub, groupID string) ([]inviteListEntry, error) {
	if _, err := s.GetGroup(localNpub, groupID); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT invite_id, invitee_npub, group_id, state FROM groupchat_invites WHERE local_npub = ? AND group_id = ? ORDER BY invitee_npub, invite_id`, localNpub, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := []inviteListEntry{}
	for rows.Next() {
		var entry inviteListEntry
		if err := rows.Scan(&entry.InviteID, &entry.InviteeNpub, &entry.GroupID, &entry.State); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

func listInvitesCommand(c *cli.Command) ([]inviteListEntry, error) {
	session, err := openSession(c)
	if err != nil {
		return nil, err
	}
	defer session.db.Close()
	return session.store.listInvites(session.me, c.String("group"))
}

func acceptGroupCommand(c *cli.Command) (acceptView, error) {
	session, err := openSession(c)
	if err != nil {
		return acceptView{}, err
	}
	defer session.db.Close()
	var groupID, inviteState, groupState, invitee string
	err = session.store.db.QueryRow(`SELECT i.group_id, i.state, g.state, i.invitee_npub FROM groupchat_invites i JOIN groupchat_groups g ON g.local_npub = i.local_npub AND g.group_id = i.group_id WHERE i.local_npub = ? AND i.invite_id = ?`, session.me, c.String("invite")).Scan(&groupID, &inviteState, &groupState, &invitee)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	if err != nil {
		return acceptView{}, err
	}
	if invitee == session.me && inviteState == string(InviteActive) && groupState == string(StateActive) {
		if err := finishLocalActivation(session, groupID); err != nil {
			return acceptView{}, err
		}
		return acceptView{GroupID: groupID, State: string(StateActive)}, nil
	}
	env, err := session.store.AcceptInvite(c.String("invite"), session.me)
	if err != nil {
		return acceptView{}, err
	}
	creatorSK, found, err := secretForNpub(session.ks, env.CreatorNpub)
	if err != nil {
		return acceptView{}, err
	}
	if !found {
		// The creator is on another device: send the acceptance through the
		// relay. The group turns active when the creator's activation arrives.
		if _, err := session.store.PrepareControlFanout(session.sk, env, 0, controlFanoutRetries); err != nil {
			return acceptView{}, fmt.Errorf("queue acceptance for %s: %w", env.CreatorNpub, err)
		}
		return session.acceptResult(env.GroupID)
	}
	verified, _, err := verifyDelivery(session.sk, creatorSK, env)
	if err != nil {
		return acceptView{}, err
	}
	activations, err := session.store.ReceiveAcceptance(verified)
	if err != nil {
		return acceptView{}, err
	}
	if len(activations) == 0 {
		activations, err = session.store.localActivations(env.CreatorNpub, env.GroupID)
		if err != nil {
			return acceptView{}, err
		}
	}
	if err := deliverActivations(session, creatorSK, env.CreatorNpub, activations); err != nil {
		return acceptView{}, err
	}
	return session.acceptResult(env.GroupID)
}

func (s cliSession) acceptResult(groupID string) (acceptView, error) {
	group, err := s.store.GetGroup(s.me, groupID)
	if err != nil {
		return acceptView{}, err
	}
	return acceptView{GroupID: group.ID, State: string(group.State)}, nil
}

func finishLocalActivation(session cliSession, groupID string) error {
	group, err := session.store.GetGroup(session.me, groupID)
	if err != nil {
		return err
	}
	creatorSK, found, err := secretForNpub(session.ks, group.Creator)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	activations, err := session.store.localActivations(group.Creator, groupID)
	if err != nil {
		return err
	}
	return deliverActivations(session, creatorSK, group.Creator, activations)
}

func deliverActivations(session cliSession, creatorSK nostr.SecretKey, creator string, activations []Envelope) error {
	recipients := make([]nostr.SecretKey, len(activations))
	local := make([]bool, len(activations))
	remote := []Envelope(nil)
	for i, activation := range activations {
		recipientSK, ok, lookupErr := secretForNpub(session.ks, activation.InviteeNpub)
		if lookupErr != nil {
			return lookupErr
		}
		recipients[i], local[i] = recipientSK, ok
		if !ok {
			remote = append(remote, activation)
		}
	}
	// invitees on other devices get their activation through the relay
	if err := session.store.QueueActivations(creatorSK, remote); err != nil {
		return err
	}
	for i, activation := range activations {
		if !local[i] {
			continue
		}
		verifiedAct, _, deliverErr := verifyDelivery(creatorSK, recipients[i], activation)
		if deliverErr != nil {
			return deliverErr
		}
		if err := session.store.MarkLocalActivationDelivered(creator, activation.GroupID, activation.InviteID); err != nil {
			return err
		}
		if os.Getenv("HYPHAE_HALT_BEFORE_ACTIVATION_RECEIVE") == activation.InviteID {
			os.Exit(75)
		}
		if _, err := session.store.ReceiveActivation(verifiedAct); err != nil {
			return err
		}
	}
	return nil
}

package daemon

import (
	"errors"
	"fmt"

	"fiatjaf.com/nostr"
	"github.com/iDoris-ai/hyphae/internal/groupchat"
	"github.com/iDoris-ai/hyphae/internal/messaging"
	"github.com/iDoris-ai/hyphae/internal/storage"
)

// groupInboundOutcome records whether the reserved payload was applied and
// whether that apply wrote a new durable group row.
type groupInboundOutcome struct {
	handled  bool
	inserted bool
}

// acceptReservedGroup delivers one reserved payload through the group machines.
// A non-v1 payload stops before the group tables are opened.
func acceptReservedGroup(event *nostr.Event, recipientSK nostr.SecretKey) (groupInboundOutcome, error) {
	message, err := messaging.VerifyAgentMessage(*event, recipientSK)
	if err != nil {
		return groupInboundOutcome{}, err
	}
	probeErr := messaging.DispatchVerifiedGroup(messaging.GroupMachines{}, message)
	if errors.Is(probeErr, messaging.ErrUnsupportedGroupVersion) {
		return groupInboundOutcome{}, nil
	}
	store, err := groupInboxStore()
	if err != nil {
		return groupInboundOutcome{}, err
	}
	inserted := false
	err = messaging.DispatchVerifiedGroup(groupInboxMachines(store, &inserted), message)
	if err != nil {
		return groupInboundOutcome{}, err
	}
	return groupInboundOutcome{handled: true, inserted: inserted}, nil
}

func groupInboxStore() (*groupchat.Store, error) {
	if err := messaging.InitStorage(); err != nil {
		return nil, err
	}
	if storage.DB == nil {
		return nil, errors.New("group inbox database is required")
	}
	return groupchat.NewStore(storage.DB)
}

func groupInboxMachines(store *groupchat.Store, inserted *bool) messaging.GroupMachines {
	callBool := func(fn func(groupchat.VerifiedIncoming) (bool, error)) func(messaging.VerifiedAgentMessage) error {
		return func(message messaging.VerifiedAgentMessage) error {
			incoming, err := groupchat.FromVerifiedAgentMessage(message)
			if err != nil {
				return err
			}
			first, err := fn(incoming)
			if err != nil {
				return err
			}
			if first {
				*inserted = true
			}
			return nil
		}
	}
	callChange := func(fn func(groupchat.VerifiedIncoming) error) func(messaging.VerifiedAgentMessage) error {
		return func(message messaging.VerifiedAgentMessage) error {
			incoming, err := groupchat.FromVerifiedAgentMessage(message)
			if err != nil {
				return err
			}
			before, err := groupInboxStamp()
			if err != nil {
				return err
			}
			if err := fn(incoming); err != nil {
				return err
			}
			after, err := groupInboxStamp()
			if err != nil {
				return err
			}
			if before != after {
				*inserted = true
			}
			return nil
		}
	}
	return messaging.GroupMachines{
		Invite:   callBool(store.ReceiveInvite),
		Activate: callBool(store.ReceiveActivation),
		Message:  callBool(store.ReceiveMessage),
		Accept: callChange(func(incoming groupchat.VerifiedIncoming) error {
			_, err := store.ReceiveAcceptance(incoming)
			return err
		}),
		Decline: callChange(func(incoming groupchat.VerifiedIncoming) error {
			_, err := store.ReceiveDecline(incoming)
			return err
		}),
		Cancel: callChange(func(incoming groupchat.VerifiedIncoming) error {
			return store.ReceiveCancel(incoming)
		}),
	}
}

func groupInboxStamp() (string, error) {
	if storage.DB == nil {
		return "", errors.New("group inbox database is required")
	}
	const query = `SELECT
		(SELECT COUNT(*) FROM groupchat_groups),
		(SELECT COALESCE(SUM(CASE state WHEN 'pending' THEN 1 WHEN 'activating' THEN 2 WHEN 'active' THEN 4 WHEN 'left' THEN 8 WHEN 'cancelled' THEN 16 ELSE 32 END), 0) FROM groupchat_groups),
		(SELECT COALESCE(SUM(updated_at), 0) FROM groupchat_groups),
		(SELECT COUNT(*) FROM groupchat_invites),
		(SELECT COALESCE(SUM(CASE state WHEN 'pending' THEN 1 WHEN 'accepted' THEN 2 WHEN 'declined' THEN 4 WHEN 'active' THEN 8 WHEN 'cancelled' THEN 16 ELSE 32 END), 0) FROM groupchat_invites),
		(SELECT COALESCE(SUM(updated_at), 0) FROM groupchat_invites),
		(SELECT COALESCE(SUM(CASE state WHEN 'pending' THEN 1 WHEN 'accepted' THEN 2 WHEN 'declined' THEN 4 WHEN 'active' THEN 8 WHEN 'cancelled' THEN 16 ELSE 32 END), 0) FROM groupchat_members),
		(SELECT COUNT(*) FROM groupchat_messages),
		(SELECT COALESCE(SUM(LENGTH(body)), 0) FROM groupchat_messages)`
	var groups, groupState, groupUpdated int64
	var invites, inviteState, inviteUpdated int64
	var members, messages, bodyBytes int64
	err := storage.DB.QueryRow(query).Scan(
		&groups, &groupState, &groupUpdated,
		&invites, &inviteState, &inviteUpdated,
		&members, &messages, &bodyBytes,
	)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%d,%d,%d,%d,%d,%d,%d,%d,%d",
		groups, groupState, groupUpdated,
		invites, inviteState, inviteUpdated,
		members, messages, bodyBytes), nil
}

func emitGroupReceive(hooks incomingReceiveHooks, senderNpub string) {
	name := safePrefix(senderNpub, 16) + "..."
	if hooks.logf != nil {
		hooks.logf("\n📨 Group update from %s\n", name)
		return
	}
	fmt.Printf("\n📨 Group update from %s\n", name)
}

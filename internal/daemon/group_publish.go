package daemon

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/iDoris-ai/hyphae/internal/groupchat"
	"github.com/iDoris-ai/hyphae/internal/messaging"
)

// groupPublishTarget is where the production group publisher sends: the
// context and relays of the most recent daemon outbox pass.
var groupPublishTarget struct {
	sync.Mutex
	ctx    context.Context
	relays []string
}

// ensureGroupPublisher registers the production publisher for group outbox
// rows unless a provider is already registered (tests inject their own).
// Without it, queued group rows -- invites, accepts, activations, messages --
// would sit in the outbox forever.
func ensureGroupPublisher(ctx context.Context, relays []string) {
	groupPublishTarget.Lock()
	groupPublishTarget.ctx, groupPublishTarget.relays = ctx, relays
	groupPublishTarget.Unlock()
	if _, err := daemonGroupProviders.Lookup(daemonGroupProviderName); errors.Is(err, groupchat.ErrGroupProviderNotRegistered) {
		_ = daemonGroupProviders.Register(daemonGroupProviderName, groupPublisher{})
	}
}

// groupPublisher publishes one group outbox row through the fanout attempt
// handler, which records the attempt, the relay acceptance or the failure in
// the group tables as well as the outbox.
type groupPublisher struct{}

func (groupPublisher) Send(call groupchat.GroupSendCall) error {
	groupPublishTarget.Lock()
	ctx, relays := groupPublishTarget.ctx, groupPublishTarget.relays
	groupPublishTarget.Unlock()
	if ctx == nil {
		return errors.New("group publisher has no daemon context")
	}
	store, err := groupInboxStore()
	if err != nil {
		return err
	}
	outbox, err := messaging.LoadOutbox()
	if err != nil {
		return err
	}
	for _, entry := range outbox.Entries {
		if entry.ID != call.Token.ID || entry.Route != messaging.OutboxRouteGroup {
			continue
		}
		result, err := messaging.AttemptSendRouted(ctx, outbox, entry, relays, relayDialTimeout, messaging.AttemptOptions{
			Handlers: messaging.OutboxHandlers{Attempt: groupchat.NewFanoutAttemptHandler(store)},
		})
		if err != nil {
			return err
		}
		if !result.Sent {
			return fmt.Errorf("no relay accepted group event %s", safePrefix(entry.ID, 16))
		}
		return nil
	}
	return fmt.Errorf("group outbox row %s is gone", safePrefix(call.Token.ID, 16))
}

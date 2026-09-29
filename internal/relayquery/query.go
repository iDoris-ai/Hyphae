package relayquery

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"fiatjaf.com/nostr"
)

const queryTimeout = 5 * time.Second

// Page is the set of matching events received before the relay's EOSE.
// Hints are returned unchanged; EOSE does not prove that this is full history.
type Page struct {
	Events []nostr.Event
	Hints  []string
}

// Fetch performs one relay query. It succeeds only after a real EOSE arrives;
// callers may use Page.Hints to decide whether and how to request another page.
func Fetch(ctx context.Context, url string, filter nostr.Filter) (Page, error) {
	page := Page{Events: make([]nostr.Event, 0)}
	queryCtx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	relay, err := nostr.RelayConnect(queryCtx, url, nostr.RelayOptions{})
	if relay != nil {
		defer relay.Close()
	}
	if err != nil {
		return page, fmt.Errorf("connect to relay %s: %w", url, err)
	}

	sub, err := relay.Subscribe(queryCtx, filter, nostr.SubscriptionOptions{
		MaxWaitForEOSE: time.Duration(math.MaxInt64),
	})
	if err != nil {
		return page, fmt.Errorf("subscribe to relay %s: %w", url, err)
	}
	defer sub.Unsub()

	events := sub.Events
	eoses := sub.EndOfStoredEvents
	closed := sub.ClosedReason
	subDone := sub.Context.Done()
	relayDone := relay.Context().Done()
	var terminalErr error
	connectionEnded := false
	for {
		// EOSE can race with CLOSED or connection cancellation in the SDK's
		// independently dispatched channels. Prefer an already delivered EOSE.
		if eose, ok := receiveEOSE(sub); ok {
			return finishQuery(page, eose, url, queryCtx, relay, connectionEnded)
		}

		select {
		case event, ok := <-events:
			if ok {
				page.Events = append(page.Events, event)
			} else {
				events = nil
				if terminalErr == nil {
					terminalErr = fmt.Errorf("relay %s disconnected before EOSE: %w", url, subscriptionCause(sub))
				}
			}
		case eose, ok := <-eoses:
			if ok {
				return finishQuery(page, eose, url, queryCtx, relay, connectionEnded)
			}
			eoses = nil
			if terminalErr == nil {
				terminalErr = fmt.Errorf("relay %s ended EOSE stream without EOSE", url)
			}
		case reason, ok := <-closed:
			closed = nil
			if ok && terminalErr == nil {
				terminalErr = fmt.Errorf("relay %s closed subscription before EOSE: %s", url, reason)
			}
		case <-subDone:
			subDone = nil
			if terminalErr == nil {
				terminalErr = fmt.Errorf("relay %s subscription ended before EOSE: %w", url, subscriptionCause(sub))
			}
		case <-relayDone:
			relayDone = nil
			connectionEnded = true
		case <-queryCtx.Done():
			if eose, ok := receiveEOSE(sub); ok {
				return finishQuery(page, eose, url, queryCtx, relay, connectionEnded)
			}
			if terminalErr != nil {
				return page, terminalErr
			}
			return page, fmt.Errorf("relay %s query incomplete: did not receive EOSE: %w", url, queryCtx.Err())
		}
	}
}

func finish(page Page, eose nostr.EndOfStoredEvent) (Page, error) {
	page.Hints = append(page.Hints, eose.Hint...)
	for _, hint := range page.Hints {
		if hint == "auth" {
			return page, fmt.Errorf("relay requires authorization: %q", hint)
		}
	}
	return page, nil
}

func finishQuery(page Page, eose nostr.EndOfStoredEvent, url string, queryCtx context.Context, relay *nostr.Relay, connectionEnded bool) (Page, error) {
	page, err := finish(page, eose)
	if err != nil {
		return page, err
	}
	if queryCtx.Err() != nil || connectionEnded || relay.Context().Err() != nil {
		return page, fmt.Errorf("relay %s query ended while finalizing EOSE; result may be incomplete", url)
	}
	return page, nil
}

func receiveEOSE(sub *nostr.Subscription) (nostr.EndOfStoredEvent, bool) {
	select {
	case eose, ok := <-sub.EndOfStoredEvents:
		return eose, ok
	default:
		return nostr.EndOfStoredEvent{}, false
	}
}

func subscriptionCause(sub *nostr.Subscription) error {
	if cause := context.Cause(sub.Context); cause != nil {
		return cause
	}
	return errors.New("connection closed")
}

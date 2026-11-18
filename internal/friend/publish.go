package friend

import (
	"context"
	"fmt"
	"time"

	"fiatjaf.com/nostr"
)

type relayResult struct {
	URL   string `json:"url"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// publishTimeout bounds the whole exchange with one relay, connect included.
var publishTimeout = 5 * time.Second

// publish sends every event to every relay and returns the per-relay results,
// the relays that accepted all events, and whether any relay accepted any
// event (then a later publish must not reuse this created_at).
func publish(ctx context.Context, relays []string, events []nostr.Event) ([]relayResult, []string, bool) {
	results := make([]relayResult, 0, len(relays))
	var ok []string
	touched := false
	for _, url := range relays {
		n, err := publishToRelay(ctx, url, events)
		touched = touched || n > 0
		if err != nil {
			results = append(results, relayResult{URL: url, Error: err.Error()})
			continue
		}
		results = append(results, relayResult{URL: url, OK: true})
		ok = append(ok, url)
	}
	return results, ok, touched
}

// publishToRelay returns how many events the relay accepted before any error.
func publishToRelay(ctx context.Context, url string, events []nostr.Event) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, publishTimeout)
	defer cancel()
	relay, err := nostr.RelayConnect(ctx, url, nostr.RelayOptions{})
	if err != nil {
		return 0, fmt.Errorf("connect failed: %w", err)
	}
	defer func() { _ = relay.Close() }()
	for i, ev := range events {
		if err := relay.Publish(ctx, ev); err != nil {
			return i, fmt.Errorf("publish failed: %w", err)
		}
	}
	return len(events), nil
}

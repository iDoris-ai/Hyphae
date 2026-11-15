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

// publishTimeout bounds each relay publish.
const publishTimeout = 5 * time.Second

// publish sends every event to every relay and returns the per-relay results
// and the relays that accepted all events.
func publish(ctx context.Context, relays []string, events []nostr.Event) ([]relayResult, []string) {
	results := make([]relayResult, 0, len(relays))
	var ok []string
	for _, url := range relays {
		if err := publishToRelay(ctx, url, events); err != nil {
			results = append(results, relayResult{URL: url, Error: err.Error()})
			continue
		}
		results = append(results, relayResult{URL: url, OK: true})
		ok = append(ok, url)
	}
	return results, ok
}

func publishToRelay(ctx context.Context, url string, events []nostr.Event) error {
	relay, err := nostr.RelayConnect(ctx, url, nostr.RelayOptions{})
	if err != nil {
		return fmt.Errorf("connect failed: %w", err)
	}
	defer func() { _ = relay.Close() }()
	for _, ev := range events {
		pubCtx, cancel := context.WithTimeout(ctx, publishTimeout)
		err := relay.Publish(pubCtx, ev)
		cancel()
		if err != nil {
			return fmt.Errorf("publish failed: %w", err)
		}
	}
	return nil
}

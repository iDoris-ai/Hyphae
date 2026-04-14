package common

import (
	"context"
	"time"

	"fiatjaf.com/nostr"
)

// PublishToRelays 发布事件到多个 relay
func PublishToRelays(ctx context.Context, event *nostr.Event, relays []string) map[string]error {
	results := make(map[string]error)
	for _, url := range relays {
		relay, err := nostr.RelayConnect(ctx, url, nostr.RelayOptions{})
		if err != nil {
			results[url] = err
			continue
		}

		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err = relay.Publish(ctx, *event)
		cancel()
		relay.Close()

		results[url] = err
	}
	return results
}

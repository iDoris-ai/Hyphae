package messaging

import (
	"context"
	"fmt"
	"time"

	"fiatjaf.com/nostr"
)

// QueuedAgentRelayResult reports a relay acknowledgment or a safe diagnostic.
// It never contains message content or encryption material.
type QueuedAgentRelayResult struct {
	URL   string
	OK    bool
	Error string
}

// QueuedAgentMessageResult describes the durable queue/send outcome. A relay
// acknowledgment confirms relay acceptance only, not delivery to a recipient.
type QueuedAgentMessageResult struct {
	EventID           string
	Relays            []QueuedAgentRelayResult
	PublishedTo       int
	RelayCount        int
	QueuedForRetry    bool
	HistoryStored     bool
	Superseded        bool
	QueueStateUnknown bool
}

// SendQueuedAgentMessage writes plaintext history, persists the signed event
// to the outbox, then uses the shared QueueID-guarded send path. The caller is
// responsible for constructing and signing the event. plaintext is stored
// locally; it is never used as the published content here.
func SendQueuedAgentMessage(
	ctx context.Context,
	event *nostr.Event,
	recipientNpub, plaintext string,
	isEncrypted bool,
	relays []string,
	dialTimeout time.Duration,
) (QueuedAgentMessageResult, error) {
	if event == nil {
		return QueuedAgentMessageResult{}, fmt.Errorf("event is required")
	}
	if dialTimeout <= 0 {
		return QueuedAgentMessageResult{}, fmt.Errorf("dial timeout must be positive")
	}
	result, err := sendQueuedAgentMessage(
		ctx, event, recipientNpub, plaintext, "", "", isEncrypted, relays, dialTimeout,
		StoreOutgoingMessage, enqueueOutboxEntry, publishAgentMessageRelays,
	)
	wrapped := QueuedAgentMessageResult{
		EventID: result.EventID, PublishedTo: result.PublishedTo, RelayCount: result.RelayCount,
		QueuedForRetry: result.QueuedForRetry, HistoryStored: result.HistoryStored,
		Superseded: result.Superseded, QueueStateUnknown: result.QueueStateUnknown,
		Relays: make([]QueuedAgentRelayResult, 0, len(result.Relays)),
	}
	for _, relay := range result.Relays {
		wrapped.Relays = append(wrapped.Relays, QueuedAgentRelayResult{URL: relay.URL, OK: relay.OK, Error: relay.Error})
	}
	return wrapped, err
}

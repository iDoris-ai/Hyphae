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

// AgentMessageDeliveryState is a safe, content-free summary of a queued send.
// Relay acceptance does not imply that the recipient received or read it.
type AgentMessageDeliveryState string

const (
	AgentMessageFailed        AgentMessageDeliveryState = "failed"
	AgentMessageQueued        AgentMessageDeliveryState = "queued"
	AgentMessageRelayAccepted AgentMessageDeliveryState = "relay_accepted"
)

// AgentMessageDeliveryIssue is a content-free safe reason accompanying a
// delivery state. It intentionally excludes the underlying error, relay URL,
// event payload, and encryption material.
type AgentMessageDeliveryIssue string

const (
	AgentMessageIssueNone                    AgentMessageDeliveryIssue = ""
	AgentMessageIssueSendFailed              AgentMessageDeliveryIssue = "send_failed"
	AgentMessageIssueHistoryNotStored        AgentMessageDeliveryIssue = "history_not_stored"
	AgentMessageIssueQueueStateUnknown       AgentMessageDeliveryIssue = "queue_state_unknown"
	AgentMessageIssueQueueSuperseded         AgentMessageDeliveryIssue = "queue_superseded"
	AgentMessageIssueOutboxBookkeepingFailed AgentMessageDeliveryIssue = "outbox_bookkeeping_failed"
	AgentMessageIssueRetryExhausted          AgentMessageDeliveryIssue = "retry_exhausted"
)

// QueuedAgentMessageResult describes the durable queue/send outcome. A relay
// acknowledgment confirms relay acceptance only, not delivery to a recipient.
type QueuedAgentMessageResult struct {
	State             AgentMessageDeliveryState
	Issue             AgentMessageDeliveryIssue
	EventID           string
	Relays            []QueuedAgentRelayResult
	PublishedTo       int
	RelayCount        int
	QueuedForRetry    bool
	HistoryStored     bool
	Superseded        bool
	QueueStateUnknown bool
}

// SendQueuedAgentMessage persists the signed event to the outbox, writes
// plaintext history, then uses the shared QueueID-guarded send path. The caller is
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
		State:   deliveryState(result),
		Issue:   deliveryIssue(result, err),
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

func deliveryState(result agentMsgResult) AgentMessageDeliveryState {
	switch {
	case result.PublishedTo > 0:
		return AgentMessageRelayAccepted
	case result.QueuedForRetry && !result.QueueStateUnknown && !result.Superseded:
		return AgentMessageQueued
	default:
		return AgentMessageFailed
	}
}

func deliveryIssue(result agentMsgResult, err error) AgentMessageDeliveryIssue {
	switch {
	case result.QueueStateUnknown:
		return AgentMessageIssueQueueStateUnknown
	case !result.HistoryStored:
		return AgentMessageIssueHistoryNotStored
	case result.Superseded:
		return AgentMessageIssueQueueSuperseded
	case err != nil && result.PublishedTo > 0:
		return AgentMessageIssueOutboxBookkeepingFailed
	case err != nil:
		return AgentMessageIssueSendFailed
	default:
		return AgentMessageIssueNone
	}
}

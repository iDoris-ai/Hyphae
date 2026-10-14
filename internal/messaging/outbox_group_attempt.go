package messaging

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/pkg/types"
)

// GroupAttemptToken is the rev7 AttemptToken as seen by the outbox send loop:
// the complete fanout key plus event, queue and generation. It is opaque to
// messaging; only the handler interprets it.
type GroupAttemptToken struct {
	Key               OutboxFanoutKey
	EventID, QueueID  string
	AttemptGeneration int64
}

// Sentinel classes a GroupAttemptHandler wraps its errors with so the send
// loop can map them to SendResult without importing the store package.
var (
	ErrGroupAttemptStale          = errors.New("group attempt is stale")
	ErrGroupAttemptPending        = errors.New("group attempt bookkeeping is pending recovery")
	ErrGroupAttemptConflict       = errors.New("group attempt bookkeeping conflict")
	ErrGroupAttemptQueueConflict  = errors.New("group attempt queue conflict")
	ErrGroupAttemptQueueMissing   = errors.New("group attempt queue entry missing")
	ErrGroupAttemptCleanupPending = errors.New("group relay acceptance is durable; outbox cleanup pending")
)

// GroupAttemptHandler is the rev7 §3.3 R/S/T3+O2/O3+T4 boundary (W-1 of
// docs/agent/m1-f3-wiring-plan-20261011.md). Every method runs under the
// outbox lock + SQLite transaction inside the handler; the send loop never
// writes the outbox itself on this route. Reserve passes the barrier (replay,
// D14, active budget); Start records the publish start; Accepted/Failed are
// the current-token result boundaries. Failed reports whether the budget is
// now exhausted.
type GroupAttemptHandler interface {
	Reserve(entry types.OutboxEntry) (GroupAttemptToken, error)
	Start(token GroupAttemptToken, targets int, now int64) error
	Accepted(token GroupAttemptToken, now int64) error
	Failed(token GroupAttemptToken) (exhausted bool, err error)
}

// attemptSendGroupAttempt drives one group publish: bind → reserve → start →
// P (both locks released) → accepted | failed. The caller's typed outbox
// snapshot is not refreshed: the raw writers own the file on this route.
//
// The signed event is strictly decoded and bound to the entry (event ID,
// signature, single recipient and, when ks is known, a local sender) before
// Reserve, so entry A's metadata can never reserve while a different signed
// event B is published. The handler then binds entry bytes to the
// authoritative frozen event under its lock/transaction.
func attemptSendGroupAttempt(ctx context.Context, entry types.OutboxEntry, defaultRelays []string, dialTimeout time.Duration, h GroupAttemptHandler, ks *types.KeyStore, publish outboxPublisher) (SendResult, error) {
	event, err := DecodeVerifiedGroupEvent([]byte(entry.EventJSON), entry)
	if err == nil && ks != nil && !keyStoreHasNpub(ks, common.EncodeNpub(event.PubKey)) {
		err = errors.New("event sender is not a local identity")
	}
	if err != nil {
		return SendResult{Issue: AgentMessageIssueOutboxBookkeepingFailed}, fmt.Errorf("parse frozen event: %w", err)
	}
	token, err := h.Reserve(entry)
	if err != nil {
		return groupAttemptRefusal(err)
	}
	targets := entry.Relays
	if len(targets) == 0 {
		targets = defaultRelays
	}
	if err := h.Start(token, len(targets), time.Now().Unix()); err != nil {
		return groupAttemptRefusal(err)
	}
	result := SendResult{Attempted: true}
	if !publish(ctx, targets, *event, dialTimeout) {
		exhausted, err := h.Failed(token)
		if err != nil {
			refusal, err := groupAttemptRefusal(err)
			refusal.Attempted = true
			return refusal, err
		}
		result.Issue, result.Queued, result.MarkedFailed = AgentMessageIssueSendFailed, !exhausted, exhausted
		if exhausted {
			result.Issue = AgentMessageIssueRetryExhausted
		}
		return result, nil
	}
	result.Sent = true
	if err := h.Accepted(token, time.Now().Unix()); err != nil {
		if errors.Is(err, ErrGroupAttemptCleanupPending) {
			// T3 is durable; only the precise O2 is outstanding.
			result.QueueStateUnknown = true
			return result, err
		}
		refusal, err := groupAttemptRefusal(err)
		refusal.Attempted, refusal.Sent = true, true
		return refusal, err
	}
	result.Issue = AgentMessageIssueNone
	return result, nil
}

// groupAttemptRefusal maps a handler refusal to the SendResult the daemon,
// TUI and `outbox retry` already understand. Nothing was published.
func groupAttemptRefusal(err error) (SendResult, error) {
	switch {
	case errors.Is(err, ErrGroupAttemptStale):
		return SendResult{Superseded: true, Issue: AgentMessageIssueQueueSuperseded}, err
	case errors.Is(err, ErrGroupAttemptPending):
		return SendResult{QueueStateUnknown: true, Issue: AgentMessageIssueQueueStateUnknown}, err
	case errors.Is(err, ErrGroupAttemptQueueConflict):
		return SendResult{Issue: AgentMessageIssueQueueConflict}, err
	case errors.Is(err, ErrGroupAttemptQueueMissing):
		return SendResult{Issue: AgentMessageIssueQueueMissing}, err
	default:
		return SendResult{Issue: AgentMessageIssueOutboxBookkeepingFailed}, err
	}
}

func keyStoreHasNpub(ks *types.KeyStore, npub string) bool {
	for _, identity := range ks.Identities {
		if identity != nil && identity.Npub == npub {
			return true
		}
	}
	return false
}

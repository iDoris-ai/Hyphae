package messaging

import (
	"encoding/json"
	"fmt"
	"time"

	"fiatjaf.com/nostr"
	"github.com/iDoris-ai/hyphae/pkg/types"
)

const (
	OutboxRouteGroup         = "group"
	OutboxStatusGroupPending = "group_pending"
	OutboxStatusGroupFailed  = "group_failed"
)

// validGroupOutboxEntry enforces Route == group iff Status is a group status.
func validGroupOutboxEntry(entry types.OutboxEntry) bool {
	groupStatus := entry.Status == OutboxStatusGroupPending || entry.Status == OutboxStatusGroupFailed
	return (entry.Route == OutboxRouteGroup) == groupStatus
}

// normalizeOutboxEntryRoute recovers Route on load for an entry that an
// older binary round-tripped: Route is a new field (`omitempty`), so a
// binary built before it existed reads outbox.json, ignores it, and writes
// the entry back without it -- while leaving the known Status value
// (group_pending/group_failed) exactly as this binary wrote it. Without
// this, that entry fails validGroupOutboxEntry on the next load (Route=""
// but Status is a group status) and silently drops out of both
// GetPendingGroupOutbox and the failed/stuck listing forever.
//
// Only an empty Route is treated as a legacy round trip. A non-empty Route
// that still disagrees with Status (e.g. Route="dm" with Status=
// "group_pending") is left untouched and must keep failing
// validGroupOutboxEntry -- that is genuine corruption, not round-trip loss.
func normalizeOutboxEntryRoute(entry *types.OutboxEntry) {
	if entry.Route == "" && (entry.Status == OutboxStatusGroupPending || entry.Status == OutboxStatusGroupFailed) {
		entry.Route = OutboxRouteGroup
	}
}

func outboxRouteStatuses(entry types.OutboxEntry) (pending, failed string) {
	if entry.Route == OutboxRouteGroup {
		return OutboxStatusGroupPending, OutboxStatusGroupFailed
	}
	return "pending", "failed"
}

// EnqueueGroupOutboxEntry persists the original signed JSON once per event ID.
// existed is true when a matching pending or failed entry was already queued.
func EnqueueGroupOutboxEntry(eventJSON, recipientNpub string, relays []string, maxRetries int) (types.OutboxEntry, bool, error) {
	return enqueueGroupOutboxEntry(eventJSON, recipientNpub, relays, maxRetries, false)
}

// RequeueGroupOutboxEntry replaces a failed entry with a fresh queue identity in
// one transaction. A matching pending entry is returned unchanged (existed=true).
func RequeueGroupOutboxEntry(eventJSON, recipientNpub string, relays []string, maxRetries int) (types.OutboxEntry, bool, error) {
	return enqueueGroupOutboxEntry(eventJSON, recipientNpub, relays, maxRetries, true)
}

func enqueueGroupOutboxEntry(eventJSON, recipientNpub string, relays []string, maxRetries int, requeue bool) (types.OutboxEntry, bool, error) {
	var event nostr.Event
	if err := json.Unmarshal([]byte(eventJSON), &event); err != nil {
		return types.OutboxEntry{}, false, fmt.Errorf("parse group outbox event: %w", err)
	}
	if !event.CheckID() || !event.VerifySignature() {
		return types.OutboxEntry{}, false, fmt.Errorf("invalid signed group outbox event")
	}
	if recipientNpub == "" || maxRetries < 1 {
		return types.OutboxEntry{}, false, fmt.Errorf("group outbox requires recipient and positive max retries")
	}
	var entry types.OutboxEntry
	existed := false
	_, err := UpdateOutbox(func(latest *types.Outbox) error {
		if countByID(latest.Entries, event.ID.Hex()) > 1 {
			return fmt.Errorf("duplicate group outbox event ID")
		}
		index := -1
		for i, current := range latest.Entries {
			if current.ID != event.ID.Hex() {
				continue
			}
			if !validGroupOutboxEntry(current) || current.Route != OutboxRouteGroup || current.EventJSON != eventJSON || current.RecipientNpub != recipientNpub {
				return fmt.Errorf("conflicting group outbox entry")
			}
			// A pending entry is only "active" -- and so left alone on requeue
			// -- while it's still below its retry limit. Once RetryCount
			// reaches MaxRetries, GetPendingGroupOutbox stops returning it, so
			// without this check it would keep Status=="group_pending" forever
			// (only a send attempt flips it to group_failed, and send attempts
			// are exactly what a pending-but-exhausted entry never gets
			// scheduled for again) and RequeueGroupOutboxEntry would return it
			// unchanged on every call, permanently ineligible for reconciliation.
			activePending := current.Status == OutboxStatusGroupPending && current.RetryCount < current.MaxRetries
			if !requeue || activePending {
				entry, existed = current, true
				return nil
			}
			index = i
		}
		queueID, err := newOutboxQueueID()
		if err != nil {
			return err
		}
		entry = types.OutboxEntry{
			QueueID: queueID, ID: event.ID.Hex(), Route: OutboxRouteGroup,
			EventJSON: eventJSON, RecipientNpub: recipientNpub,
			Relays: append([]string(nil), relays...), MaxRetries: maxRetries,
			CreatedAt: time.Now().Unix(), Status: OutboxStatusGroupPending,
		}
		if index >= 0 {
			latest.Entries = append(latest.Entries[:index], latest.Entries[index+1:]...)
		}
		latest.Entries = append(latest.Entries, entry)
		return nil
	})
	if err != nil {
		return types.OutboxEntry{}, false, err
	}
	return entry, existed, nil
}

// GetPendingGroupOutbox returns valid group entries below their retry limit.
func GetPendingGroupOutbox(ob *types.Outbox) []types.OutboxEntry {
	var pending []types.OutboxEntry
	if ob == nil {
		return pending
	}
	for _, entry := range ob.Entries {
		if validGroupOutboxEntry(entry) && entry.Route == OutboxRouteGroup && entry.Status == OutboxStatusGroupPending && entry.RetryCount < entry.MaxRetries {
			pending = append(pending, entry)
		}
	}
	return pending
}

// GroupOutboxEntriesByEventID includes corrupt group route/status pairs so
// reconciliation can report them rather than enqueue another copy.
func GroupOutboxEntriesByEventID(ob *types.Outbox, eventID string) []types.OutboxEntry {
	var matches []types.OutboxEntry
	if ob == nil {
		return matches
	}
	for _, entry := range ob.Entries {
		if entry.ID == eventID && (entry.Route == OutboxRouteGroup || entry.Status == OutboxStatusGroupPending || entry.Status == OutboxStatusGroupFailed) {
			matches = append(matches, entry)
		}
	}
	return matches
}

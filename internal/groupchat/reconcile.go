package groupchat

import "github.com/iDoris-ai/hyphae/internal/messaging"

// ReconcileDecision is the pure result of one evidence snapshot.
type ReconcileDecision string

const (
	ReconcileAccepted ReconcileDecision = "accepted"
	ReconcileHold     ReconcileDecision = "hold"
	ReconcileOrphan   ReconcileDecision = "orphan"
)

// reconcileEvidence is the raw classification, receipt, and witness snapshot.
// Orphan is a nil row beside a present entry. A nil row with no entry is hold.
// The snapshot is not a store path.
type reconcileEvidence struct {
	row        *fanoutFailureRow
	entry      *messaging.RawGroupEntry
	receipt    string
	relayAcks  int64
	acceptedAt int64
}

// decideReconcile maps one snapshot through classifyFanoutBookkeeping.
// Accepted also requires a group route, matching the O2 premise.
// The same snapshot returns the same decision. It writes nothing.
func decideReconcile(ev reconcileEvidence) ReconcileDecision {
	if ev.row == nil {
		if ev.entry != nil {
			return ReconcileOrphan
		}
		return ReconcileHold
	}
	class := classifyFanoutBookkeeping(ev.row, ev.entry, ev.receipt, ev.relayAcks, ev.acceptedAt)
	if class == BookkeepingCleanupPending && ev.entry != nil && ev.entry.Route == messaging.OutboxRouteGroup {
		return ReconcileAccepted
	}
	return ReconcileHold
}

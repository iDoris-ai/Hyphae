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
// A nil row is an orphan. The snapshot is not a store path.
type reconcileEvidence struct {
	row        *fanoutFailureRow
	entry      *messaging.RawGroupEntry
	receipt    string
	relayAcks  int64
	acceptedAt int64
}

// decideReconcile maps one snapshot through classifyFanoutBookkeeping.
// The same snapshot returns the same decision. It writes nothing.
func decideReconcile(ev reconcileEvidence) ReconcileDecision {
	if ev.row == nil {
		return ReconcileOrphan
	}
	switch classifyFanoutBookkeeping(ev.row, ev.entry, ev.receipt, ev.relayAcks, ev.acceptedAt) {
	case BookkeepingCleanupPending:
		return ReconcileAccepted
	default:
		return ReconcileHold
	}
}

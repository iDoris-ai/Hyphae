package groupchat

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/iDoris-ai/hyphae/internal/messaging"
)

// FanoutBookkeeping is the read-only classification of one recipient's two
// durable records for `groupchat status` (wiring plan W-5). It mirrors the
// barrier's first-match order without performing any of its writes.
type FanoutBookkeeping string

const (
	BookkeepingNormal          FanoutBookkeeping = "normal"
	BookkeepingPendingFailure  FanoutBookkeeping = "pending_failure"  // O3 durable, T4 not yet projected; the barrier will replay it
	BookkeepingPendingRecovery FanoutBookkeeping = "pending_recovery" // D6 durable, T2' not yet projected; the barrier will replay it
	BookkeepingCleanupPending  FanoutBookkeeping = "cleanup_pending"  // relay accepted; the precise O2 has not removed the entry
	BookkeepingQueueMissing    FanoutBookkeeping = "queue_missing"
	BookkeepingQueueConflict   FanoutBookkeeping = "queue_conflict" // D14
	BookkeepingConflict        FanoutBookkeeping = "bookkeeping_conflict"
	BookkeepingFileCorrupt     FanoutBookkeeping = "file_corrupt"
)

// inspectFanoutBookkeeping reads both stores under the outbox lock and
// classifies them. It never writes: no replay, no mtime change, no SQLite
// transaction. A missing row is sql.ErrNoRows.
func (s *Store) inspectFanoutBookkeeping(key FanoutKey, recipient string) (FanoutBookkeeping, error) {
	local, err := canonicalNpub(key.LocalNpub)
	if err != nil {
		return "", err
	}
	key.LocalNpub = local
	var class FanoutBookkeeping
	err = messaging.WithGroupOutboxLock(func(path string) error {
		row := fanoutFailureRow{token: fanoutAttemptToken{key: key, recipient: recipient}}
		if err := row.scan(s.db.QueryRow(`SELECT `+fanoutFailureColumns+` FROM groupchat_fanout
            WHERE local_npub=? AND group_id=? AND envelope_type=? AND send_key=? AND recipient_npub=?`,
			local, key.GroupID, key.EnvelopeType, key.SendKey, recipient)); err != nil {
			return err
		}
		entry, receipt, err := messaging.ReadGroupEntryLocked(path, row.token.eventID)
		if err != nil {
			class = BookkeepingFileCorrupt
			return nil
		}
		class = classifyFanoutBookkeeping(&row, entry, receipt)
		return nil
	})
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("fanout row not found: %w", err)
	}
	return class, err
}

func classifyFanoutBookkeeping(row *fanoutFailureRow, entry *messaging.RawGroupEntry, receipt string) FanoutBookkeeping {
	state := RecipientDeliveryState(row.state)
	switch {
	case state == RecipientPrepared:
		if entry != nil {
			return BookkeepingConflict // O1 landed, T2 did not: re-running the enqueue adopts it
		}
		return BookkeepingNormal
	case state == RecipientRelayAccepted:
		if entry != nil {
			return BookkeepingCleanupPending
		}
		return BookkeepingNormal
	case entry == nil:
		if state == RecipientFailed {
			return BookkeepingNormal // the old epoch may have been cleaned; D6 reason=missing applies
		}
		return BookkeepingQueueMissing
	case entry.QueueID != row.token.queueID:
		if state == RecipientFailed && entry.RecoveryWitness != nil {
			if matchesRecoveryWitness(entry, row) {
				return BookkeepingPendingRecovery
			}
			return BookkeepingConflict
		}
		return BookkeepingQueueConflict
	}
	if w := messaging.OutboxWitnessDigest(entry); w != "" && w != row.recoveryDigest {
		return BookkeepingConflict
	}
	if receipt != "" && receipt != row.receipt {
		if state == RecipientQueued && row.phase == "started" && row.lastResultGeneration < row.token.generation &&
			row.retryCount.Valid && matchesFailureResult(entry, row.failureResult(), row) {
			return BookkeepingPendingFailure
		}
		return BookkeepingConflict
	}
	return BookkeepingNormal
}

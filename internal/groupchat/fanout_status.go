package groupchat

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/iDoris-ai/hyphae/internal/messaging"
)

// FanoutBookkeeping classifies one recipient's visible SQLite row and outbox
// entry for `groupchat status` (wiring plan W-5). It does not replay, and it
// does not confirm directory fsync. It is not a complete mirror of every
// barrier refusal.
type FanoutBookkeeping string

const (
	BookkeepingNormal          FanoutBookkeeping = "normal"
	BookkeepingPendingFailure  FanoutBookkeeping = "pending_failure"  // visible unprojected O3 evidence; the barrier will replay it
	BookkeepingPendingRecovery FanoutBookkeeping = "pending_recovery" // visible unprojected D6 evidence; the barrier will replay it
	BookkeepingCleanupPending  FanoutBookkeeping = "cleanup_pending"  // relay accepted; the precise O2 has not removed the entry
	BookkeepingQueueMissing    FanoutBookkeeping = "queue_missing"
	BookkeepingQueueConflict   FanoutBookkeeping = "queue_conflict" // D14
	BookkeepingConflict        FanoutBookkeeping = "bookkeeping_conflict"
	BookkeepingFileCorrupt     FanoutBookkeeping = "file_corrupt"
)

// FanoutBookkeepingReport is one recipient's bookkeeping class for the
// status surface (W-5b). It is read-only and inert until `groupchat status`
// is wired to it.
type FanoutBookkeepingReport struct {
	RecipientNpub string            `json:"recipient_npub"`
	Bookkeeping   FanoutBookkeeping `json:"bookkeeping"`
}

// InspectFanoutBookkeeping classifies every recipient row of key under one
// outbox lock, so all classes come from the same outbox snapshot. It never
// writes. Recipients are returned in npub order; a key without rows yields an
// empty slice, not an error.
func (s *Store) InspectFanoutBookkeeping(key FanoutKey) ([]FanoutBookkeepingReport, error) {
	local, err := canonicalNpub(key.LocalNpub)
	if err != nil {
		return nil, err
	}
	key.LocalNpub = local
	reports := []FanoutBookkeepingReport{}
	err = messaging.WithGroupOutboxLock(func(path string) error {
		rows, err := s.db.Query(`SELECT recipient_npub FROM groupchat_fanout
            WHERE local_npub=? AND group_id=? AND envelope_type=? AND send_key=? ORDER BY recipient_npub`,
			local, key.GroupID, key.EnvelopeType, key.SendKey)
		if err != nil {
			return err
		}
		defer rows.Close()
		var recipients []string
		for rows.Next() {
			var r string
			if err := rows.Scan(&r); err != nil {
				return err
			}
			recipients = append(recipients, r)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
		for _, recipient := range recipients {
			class, err := s.inspectFanoutBookkeepingLocked(path, key, recipient)
			if err != nil {
				return fmt.Errorf("inspect %s: %w", recipient, err)
			}
			reports = append(reports, FanoutBookkeepingReport{RecipientNpub: recipient, Bookkeeping: class})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return reports, nil
}

// inspectFanoutBookkeeping reads both stores under the outbox lock and
// classifies them. It does not write the outbox or SQLite. Taking the lock
// may create the lock file. A missing row is sql.ErrNoRows.
func (s *Store) inspectFanoutBookkeeping(key FanoutKey, recipient string) (FanoutBookkeeping, error) {
	local, err := canonicalNpub(key.LocalNpub)
	if err != nil {
		return "", err
	}
	key.LocalNpub = local
	var class FanoutBookkeeping
	err = messaging.WithGroupOutboxLock(func(path string) error {
		class, err = s.inspectFanoutBookkeepingLocked(path, key, recipient)
		return err
	})
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("fanout row not found: %w", err)
	}
	return class, err
}

// inspectFanoutBookkeepingLocked is the per-recipient classification; the
// caller holds the outbox lock and has canonicalised key.LocalNpub.
func (s *Store) inspectFanoutBookkeepingLocked(path string, key FanoutKey, recipient string) (FanoutBookkeeping, error) {
	row := fanoutFailureRow{token: fanoutAttemptToken{key: key, recipient: recipient}}
	if err := row.scan(s.db.QueryRow(`SELECT `+fanoutFailureColumns+` FROM groupchat_fanout
            WHERE local_npub=? AND group_id=? AND envelope_type=? AND send_key=? AND recipient_npub=?`,
		key.LocalNpub, key.GroupID, key.EnvelopeType, key.SendKey, recipient)); err != nil {
		return "", err
	}
	entry, receipt, err := messaging.ReadGroupEntryLocked(path, row.token.eventID)
	if err != nil {
		return BookkeepingFileCorrupt, nil
	}
	var relayAcks, acceptedAt int64
	if err := s.db.QueryRow(`SELECT relay_acks, accepted_at FROM groupchat_fanout
		WHERE local_npub=? AND group_id=? AND envelope_type=? AND send_key=? AND recipient_npub=?`,
		key.LocalNpub, key.GroupID, key.EnvelopeType, key.SendKey, recipient).Scan(&relayAcks, &acceptedAt); err != nil {
		return "", err
	}
	return classifyFanoutBookkeeping(&row, entry, receipt, relayAcks, acceptedAt), nil
}

func classifyFanoutBookkeeping(row *fanoutFailureRow, entry *messaging.RawGroupEntry, receipt string, relayAcks, acceptedAt int64) FanoutBookkeeping {
	state := RecipientDeliveryState(row.state)
	switch {
	case state == RecipientPrepared:
		if entry != nil {
			return BookkeepingConflict // O1 landed, T2 did not: re-running the enqueue adopts it
		}
		return BookkeepingNormal
	case state == RecipientRelayAccepted:
		if entry == nil {
			return BookkeepingNormal
		}
		if entry.QueueID != row.token.queueID || entry.Entry.EventJSON != row.eventJSON || int64(entry.Entry.MaxRetries) != row.maxRetries {
			return BookkeepingQueueConflict
		}
		if receipt != "" && receipt != row.receipt {
			return BookkeepingConflict
		}
		if w := messaging.OutboxWitnessDigest(entry); w != "" && w != row.recoveryDigest {
			return BookkeepingConflict
		}
		if err := validateAcceptedEvidence(row.token.queueID, relayAcks, acceptedAt); err != nil {
			return BookkeepingConflict
		}
		return BookkeepingCleanupPending
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
	if state == RecipientQueued && (entry.Entry.EventJSON != row.eventJSON || int64(entry.Entry.MaxRetries) != row.maxRetries) {
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

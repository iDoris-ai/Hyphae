package groupchat

import "github.com/iDoris-ai/hyphae/internal/messaging"

// applyReconcile applies one decideReconcile snapshot under the outbox lock.
// Accepted evidence runs the existing O2 cleanup.
// Pending recovery calls authorizeFanoutRecovery after this lock returns.
// Any other decision leaves the row and the outbox bytes as they were.
func (s *Store) applyReconcile(key FanoutKey, recipient string, now int64) error {
	var recover bool
	err := messaging.WithGroupOutboxLock(func(path string) error {
		tx, err := s.beginImmediate()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		row, err := loadFanoutFailureRow(tx, key, recipient)
		var ev reconcileEvidence
		if err != nil {
			if err.Error() != "fanout row not found" {
				return err
			}
		} else {
			ev.row = &row
			err = tx.QueryRow(`SELECT relay_acks, accepted_at FROM groupchat_fanout
				WHERE local_npub=? AND group_id=? AND envelope_type=? AND send_key=? AND recipient_npub=?`,
				row.token.key.LocalNpub, key.GroupID, key.EnvelopeType, key.SendKey, recipient).Scan(&ev.relayAcks, &ev.acceptedAt)
			if err != nil {
				return err
			}
		}
		ev.entry, ev.receipt, err = messaging.ReadGroupEntryLocked(path, reconcileEventID(ev.row))
		if err != nil {
			return err
		}
		if decideReconcile(ev) == ReconcileAccepted {
			if err := tx.Rollback(); err != nil {
				return err
			}
			return s.cleanupAcceptedLocked(path, row)
		}
		if ev.row != nil && classifyFanoutBookkeeping(ev.row, ev.entry, ev.receipt, ev.relayAcks, ev.acceptedAt) == BookkeepingPendingRecovery {
			recover = true
		}
		return nil
	})
	if err != nil {
		return err
	}
	if recover {
		_, err = s.authorizeFanoutRecovery(key, recipient, now)
	}
	return err
}

func reconcileEventID(row *fanoutFailureRow) string {
	if row == nil {
		return ""
	}
	return row.token.eventID
}

package groupchat

import (
	"fmt"
	"time"

	"github.com/iDoris-ai/hyphae/internal/messaging"
	"github.com/iDoris-ai/hyphae/pkg/types"
)

type fanoutQueuedRow struct {
	RecipientNpub string
	EventID       string
	QueueID       string
}

func onFanoutQueuedTx(tx queryExecer, key FanoutKey, row fanoutQueuedRow) error {
	switch key.EnvelopeType {
	case EnvelopeMessage:
		return nil
	default:
		return fmt.Errorf("onFanoutQueuedTx: unsupported envelope type %q", key.EnvelopeType)
	}
}

type fanoutQueueTarget struct {
	recipient  string
	eventID    string
	eventJSON  string
	maxRetries int
}

// EnqueuePrepared pushes prepared fanout rows to the group outbox and advances them to queued.
func (s *Store) EnqueuePrepared(key FanoutKey) (FanoutReport, error) {
	return s.enqueuePreparedRaw(key, time.Now().Unix())
}

// RequeueFailed moves failed recipients back to queued using fresh QueueIDs for identical events.
func (s *Store) RequeueFailed(key FanoutKey, recipient string) (FanoutReport, error) {
	return s.requeueFailedRaw(key, recipient, time.Now().Unix())
}

// FanoutOutboxHandler coordinates fanout intent updates with outbox lifecycle events.
type FanoutOutboxHandler struct {
	store *Store
	// beforeRelayAcceptedTx is an internal synchronization hook used to exercise
	// the stale-read boundary in deterministic tests.
	beforeRelayAcceptedTx func()
}

// NewFanoutOutboxHandler builds a handler bound to the store.
func NewFanoutOutboxHandler(store *Store) *FanoutOutboxHandler {
	return &FanoutOutboxHandler{store: store}
}

type fanoutDBRow struct {
	key       FanoutKey
	recipient string
	eventID   string
	queueID   string
	state     RecipientDeliveryState
	attempts  int
}

func (h *FanoutOutboxHandler) findRowByEventID(eventID string) (fanoutDBRow, error) {
	var r fanoutDBRow
	var envType string
	err := h.store.db.QueryRow(`SELECT local_npub, group_id, envelope_type, send_key, recipient_npub, event_id, queue_id, state, attempts FROM groupchat_fanout WHERE event_id = ?`, eventID).Scan(&r.key.LocalNpub, &r.key.GroupID, &envType, &r.key.SendKey, &r.recipient, &r.eventID, &r.queueID, &r.state, &r.attempts)
	if err != nil {
		return fanoutDBRow{}, err
	}
	r.key.EnvelopeType = EnvelopeType(envType)
	return r, nil
}

// BeforePublish checks whether the fanout row has already achieved terminal relay acceptance.
func (h *FanoutOutboxHandler) BeforePublish(entry types.OutboxEntry) (bool, error) {
	row, err := h.findRowByEventID(entry.ID)
	if err != nil {
		return false, err
	}
	return row.state == RecipientRelayAccepted, nil
}

// MarkRelayAccepted moves the fanout intent into relay_accepted upon relay acknowledgment.
func (h *FanoutOutboxHandler) MarkRelayAccepted(entry types.OutboxEntry, relayAcks, relayCount int) error {
	row, err := h.findRowByEventID(entry.ID)
	if err != nil {
		return err
	}
	if entry.RecipientNpub != "" && row.recipient != entry.RecipientNpub {
		return fmt.Errorf("recipient mismatch for event %s", entry.ID)
	}
	if row.state == RecipientRelayAccepted {
		return nil
	}
	if row.state == RecipientPrepared {
		return ErrInvalidTransition
	}
	if row.queueID == "" {
		return fmt.Errorf("cannot mark relay accepted without persisted queue_id")
	}
	if entry.QueueID != "" && row.queueID != entry.QueueID {
		return fmt.Errorf("queue_id mismatch: persisted %q vs entry %q", row.queueID, entry.QueueID)
	}
	if relayAcks != 1 {
		return fmt.Errorf("fanout relay acceptance requires relay_acks = 1")
	}
	if h.beforeRelayAcceptedTx != nil {
		h.beforeRelayAcceptedTx()
	}
	tx, err := h.store.beginImmediate()
	if err != nil {
		return err
	}
	acceptedAt := time.Now().Unix()
	if acceptedAt <= 0 {
		acceptedAt = 1
	}
	fields := map[string]any{"relay_acks": relayAcks, "relay_count": relayCount, "accepted_at": acceptedAt}
	changed, err := transitionFanoutTx(tx, row.key, row.recipient, row.state, RecipientRelayAccepted, fields)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	if !changed {
		// The row moved between the pre-transaction read and this CAS. Re-read
		// the authoritative state inside the transaction instead of committing
		// silently: a concurrent RecordAttemptFailure can push a queued row to
		// failed, and swallowing that would permanently record a delivery the
		// relay actually accepted as failed while the caller deletes the outbox
		// entry. Main returned an error here; keep that, and additionally
		// re-apply the rank-legal failed -> relay_accepted step.
		var curState string
		if err := tx.QueryRow(`SELECT state FROM groupchat_fanout
			WHERE local_npub = ? AND group_id = ? AND envelope_type = ? AND send_key = ? AND recipient_npub = ?`,
			row.key.LocalNpub, row.key.GroupID, row.key.EnvelopeType, row.key.SendKey, row.recipient).Scan(&curState); err != nil {
			_ = tx.Rollback()
			return err
		}
		switch current := RecipientDeliveryState(curState); current {
		case RecipientRelayAccepted:
			// Already accepted: replaying the ACK is a genuine no-op.
		case RecipientFailed:
			changed, err = transitionFanoutTx(tx, row.key, row.recipient, current, RecipientRelayAccepted, fields)
			if err != nil {
				_ = tx.Rollback()
				return err
			}
			if !changed {
				_ = tx.Rollback()
				return fmt.Errorf("fanout relay acceptance was not applied for event %s (state %s)", entry.ID, current)
			}
		default:
			_ = tx.Rollback()
			return fmt.Errorf("fanout relay acceptance was not applied for event %s (state %s)", entry.ID, current)
		}
	}
	return tx.Commit()
}

// RecordAttemptFailure updates attempt count and conditionally marks the row failed when exhausted.
func (h *FanoutOutboxHandler) RecordAttemptFailure(entry types.OutboxEntry, exhausted bool, issue messaging.AgentMessageDeliveryIssue) error {
	row, err := h.findRowByEventID(entry.ID)
	if err != nil {
		return err
	}
	if entry.RecipientNpub != "" && row.recipient != entry.RecipientNpub {
		return fmt.Errorf("recipient mismatch for event %s", entry.ID)
	}
	if row.state == RecipientRelayAccepted || (entry.QueueID != "" && row.queueID != "" && entry.QueueID != row.queueID) {
		return nil
	}

	targetState := row.state
	if exhausted {
		targetState = RecipientFailed
	}
	if issue == "" {
		issue = messaging.AgentMessageIssueSendFailed
	}

	tx, err := h.store.beginImmediate()
	if err != nil {
		return err
	}
	fields := map[string]any{"last_attempt_at": time.Now().Unix(), "issue": issue}
	changed, err := transitionFanoutTx(tx, row.key, row.recipient, row.state, targetState, fields)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	if changed {
		// row.attempts was read before this transaction; a stale read would lose
		// one increment when two failures race, so bump the column in place.
		if _, err := tx.Exec(`UPDATE groupchat_fanout SET attempts = attempts + 1
			WHERE local_npub = ? AND group_id = ? AND envelope_type = ? AND send_key = ? AND recipient_npub = ?`,
			row.key.LocalNpub, row.key.GroupID, row.key.EnvelopeType, row.key.SendKey, row.recipient); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

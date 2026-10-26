package groupchat

import (
	"database/sql"
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
	case EnvelopeMessage, EnvelopeInvite, EnvelopeAccept, EnvelopeActivate:
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

// EnqueuePrepared moves prepared rows to queued through the raw O1/T2 writer.
// The frozen event stays. A negative clock is treated as zero.
func (s *Store) EnqueuePrepared(key FanoutKey) (FanoutReport, error) {
	now := time.Now().Unix()
	if now < 0 {
		now = 0
	}
	return s.enqueuePreparedRaw(key, now)
}

// RequeueFailed moves failed recipients back to queued. Each recipient gets a
// fresh QueueID through authorizeFanoutRecovery; the frozen event stays.
func (s *Store) RequeueFailed(key FanoutKey, recipient string) (FanoutReport, error) {
	local, err := canonicalNpub(key.LocalNpub)
	if err != nil {
		return FanoutReport{}, err
	}
	key.LocalNpub = local
	if recipient != "" {
		if c, err := canonicalNpub(recipient); err == nil {
			recipient = c
		}
	}
	failed, err := s.selectFailedRecipients(key, recipient)
	if err != nil {
		return FanoutReport{}, err
	}
	authorizedAt := time.Now().Unix()
	if authorizedAt < 0 {
		authorizedAt = 0
	}
	for _, npub := range failed {
		if _, err := s.authorizeFanoutRecovery(key, npub, authorizedAt); err != nil {
			return FanoutReport{}, err
		}
	}
	return s.LoadFanoutReport(key)
}

// selectFailedRecipients reads the selection and closes the rows before the
// caller takes the outbox lock.
func (s *Store) selectFailedRecipients(key FanoutKey, recipient string) ([]string, error) {
	query := `SELECT recipient_npub, state FROM groupchat_fanout
		WHERE local_npub = ? AND group_id = ? AND envelope_type = ? AND send_key = ?`
	args := []any{key.LocalNpub, key.GroupID, key.EnvelopeType, key.SendKey}
	if recipient != "" {
		query += " AND recipient_npub = ?"
		args = append(args, recipient)
	}
	rows, err := s.db.Query(query+" ORDER BY recipient_npub", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var failed []string
	matched := 0
	for rows.Next() {
		matched++
		var npub string
		var state RecipientDeliveryState
		if err := rows.Scan(&npub, &state); err != nil {
			return nil, err
		}
		if state != RecipientFailed {
			if recipient != "" {
				return nil, fmt.Errorf("cannot requeue recipient in %s state: only failed recipients can be requeued", state)
			}
			continue
		}
		failed = append(failed, npub)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if matched == 0 {
		return nil, sql.ErrNoRows
	}
	if len(failed) == 0 {
		return nil, fmt.Errorf("no failed recipients to requeue")
	}
	return failed, nil
}

// FanoutOutboxHandler coordinates fanout intent updates with outbox lifecycle events.
type FanoutOutboxHandler struct {
	store *Store
	// beforeRelayAcceptedTx is an internal synchronization hook used to exercise
	// the stale-read boundary in deterministic tests.
	beforeRelayAcceptedTx func()
	// beforeAttemptFailureTx runs where the old unlocked failure read happened.
	// Tests commit a competing update here so the in-transaction read observes it.
	beforeAttemptFailureTx func()
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

func failureReadNoop(row fanoutDBRow, entry types.OutboxEntry) bool {
	return row.state == RecipientRelayAccepted || (entry.QueueID != "" && row.queueID != "" && entry.QueueID != row.queueID)
}

func failureTarget(state RecipientDeliveryState, exhausted bool) RecipientDeliveryState {
	if state == RecipientFailed || state == RecipientRelayAccepted {
		return state
	}
	if exhausted {
		return RecipientFailed
	}
	return state
}

func (h *FanoutOutboxHandler) findFailureRowLocked(tx queryExecer, eventID string) (fanoutDBRow, error) {
	var r fanoutDBRow
	var envType string
	err := tx.QueryRow(`SELECT local_npub, group_id, envelope_type, send_key, recipient_npub, event_id, queue_id, state, attempts FROM groupchat_fanout WHERE event_id = ?`, eventID).Scan(&r.key.LocalNpub, &r.key.GroupID, &envType, &r.key.SendKey, &r.recipient, &r.eventID, &r.queueID, &r.state, &r.attempts)
	if err != nil {
		return fanoutDBRow{}, err
	}
	r.key.EnvelopeType = EnvelopeType(envType)
	return r, nil
}

// RecordAttemptFailure updates attempt count and conditionally marks the row failed when exhausted.
// The row read and the exhaustion decision both run inside the immediate transaction.
func (h *FanoutOutboxHandler) RecordAttemptFailure(entry types.OutboxEntry, exhausted bool, issue messaging.AgentMessageDeliveryIssue) error {
	if issue == "" {
		issue = messaging.AgentMessageIssueSendFailed
	}
	if h.beforeAttemptFailureTx != nil {
		h.beforeAttemptFailureTx()
	}
	tx, err := h.store.beginImmediate()
	if err != nil {
		return err
	}
	row, err := h.findFailureRowLocked(tx, entry.ID)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	if entry.RecipientNpub != "" && row.recipient != entry.RecipientNpub {
		_ = tx.Rollback()
		return fmt.Errorf("recipient mismatch for event %s", entry.ID)
	}
	if failureReadNoop(row, entry) {
		_ = tx.Rollback()
		return nil
	}
	targetState := failureTarget(row.state, exhausted)
	fields := map[string]any{"last_attempt_at": time.Now().Unix(), "issue": issue}
	changed, err := transitionFanoutTx(tx, row.key, row.recipient, row.state, targetState, fields)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	if !changed {
		_ = tx.Rollback()
		return fmt.Errorf("fanout attempt failure was not applied for event %s (state %s)", entry.ID, row.state)
	}
	if _, err := tx.Exec(`UPDATE groupchat_fanout SET attempts = attempts + 1
		WHERE local_npub = ? AND group_id = ? AND envelope_type = ? AND send_key = ? AND recipient_npub = ?`,
		row.key.LocalNpub, row.key.GroupID, row.key.EnvelopeType, row.key.SendKey, row.recipient); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

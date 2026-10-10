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

func (s *Store) queueTargets(key FanoutKey, targets []fanoutQueueTarget, from RecipientDeliveryState, requeue bool) error {
	enqueue := messaging.EnqueueGroupOutboxEntry
	if requeue {
		enqueue = messaging.RequeueGroupOutboxEntry
	}
	for _, target := range targets {
		entry, _, err := enqueue(target.eventJSON, target.recipient, nil, target.maxRetries)
		if err != nil {
			return err
		}
		tx, err := s.beginImmediate()
		if err != nil {
			return err
		}
		changed, err := transitionFanoutTx(tx, key, target.recipient, from, RecipientQueued, map[string]any{"queue_id": entry.QueueID})
		if err != nil {
			_ = tx.Rollback()
			return err
		}
		if changed {
			if err := onFanoutQueuedTx(tx, key, fanoutQueuedRow{RecipientNpub: target.recipient, EventID: target.eventID, QueueID: entry.QueueID}); err != nil {
				_ = tx.Rollback()
				return err
			}
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// EnqueuePrepared pushes prepared fanout rows to the group outbox and advances them to queued.
func (s *Store) EnqueuePrepared(key FanoutKey) (FanoutReport, error) {
	local, err := canonicalNpub(key.LocalNpub)
	if err != nil {
		return FanoutReport{}, err
	}
	key.LocalNpub = local

	rows, err := s.db.Query(`SELECT recipient_npub, event_id, event_json, max_retries
		FROM groupchat_fanout WHERE local_npub = ? AND group_id = ? AND envelope_type = ? AND send_key = ? AND state = ?
		ORDER BY recipient_npub`, local, key.GroupID, key.EnvelopeType, key.SendKey, RecipientPrepared)
	if err != nil {
		return FanoutReport{}, err
	}
	defer rows.Close()

	var targets []fanoutQueueTarget
	for rows.Next() {
		var r fanoutQueueTarget
		if err := rows.Scan(&r.recipient, &r.eventID, &r.eventJSON, &r.maxRetries); err != nil {
			return FanoutReport{}, err
		}
		targets = append(targets, r)
	}
	if err := rows.Err(); err != nil {
		return FanoutReport{}, err
	}
	if err := s.queueTargets(key, targets, RecipientPrepared, false); err != nil {
		return FanoutReport{}, err
	}
	return s.LoadFanoutReport(key)
}

// RequeueFailed moves failed recipients back to queued using fresh QueueIDs for identical events.
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

	query := `SELECT recipient_npub, event_id, event_json, state, max_retries FROM groupchat_fanout
		WHERE local_npub = ? AND group_id = ? AND envelope_type = ? AND send_key = ?`
	args := []any{local, key.GroupID, key.EnvelopeType, key.SendKey}
	if recipient != "" {
		query += " AND recipient_npub = ?"
		args = append(args, recipient)
	}
	rows, err := s.db.Query(query+" ORDER BY recipient_npub", args...)
	if err != nil {
		return FanoutReport{}, err
	}
	defer rows.Close()

	var targets []fanoutQueueTarget
	matched := 0
	for rows.Next() {
		matched++
		var r fanoutQueueTarget
		var state RecipientDeliveryState
		if err := rows.Scan(&r.recipient, &r.eventID, &r.eventJSON, &state, &r.maxRetries); err != nil {
			return FanoutReport{}, err
		}
		if state != RecipientFailed {
			if recipient != "" {
				return FanoutReport{}, fmt.Errorf("cannot requeue recipient in %s state: only failed recipients can be requeued", state)
			}
			continue
		}
		targets = append(targets, r)
	}
	if err := rows.Err(); err != nil {
		return FanoutReport{}, err
	}
	if matched == 0 {
		return FanoutReport{}, sql.ErrNoRows
	}
	if len(targets) == 0 {
		return FanoutReport{}, fmt.Errorf("no failed recipients to requeue")
	}
	if err := s.queueTargets(key, targets, RecipientFailed, true); err != nil {
		return FanoutReport{}, err
	}
	return s.LoadFanoutReport(key)
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
		// A stale ACK is a harmless no-op. transitionFanoutTx only returns
		// unchanged without an error for a stale CAS (or an absent row).
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
	fields := map[string]any{"attempts": row.attempts + 1, "last_attempt_at": time.Now().Unix(), "issue": issue}
	if _, err := transitionFanoutTx(tx, row.key, row.recipient, row.state, targetState, fields); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

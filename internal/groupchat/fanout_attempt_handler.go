package groupchat

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/iDoris-ai/hyphae/internal/messaging"
	"github.com/iDoris-ai/hyphae/pkg/types"
)

// FanoutAttemptHandler adapts the F3/F4 store boundaries to
// messaging.GroupAttemptHandler (wiring plan W-1). It is inert until a caller
// passes it in AttemptOptions.Handlers.Attempt.
type FanoutAttemptHandler struct {
	store     *Store
	localNpub string
}

// NewFanoutAttemptHandler returns an unbound handler retained ONLY for current
// inert tests/backcompat. Future production callers MUST use
// NewFanoutAttemptHandlerFor.
func NewFanoutAttemptHandler(store *Store) *FanoutAttemptHandler {
	return &FanoutAttemptHandler{store: store}
}

// NewFanoutAttemptHandlerFor constructs a bound FanoutAttemptHandler for a
// trusted caller process identity. It validates localNpub at construction
// consistent with existing identity types. Future production callers MUST use
// this bound API.
func NewFanoutAttemptHandlerFor(store *Store, localNpub string) (*FanoutAttemptHandler, error) {
	if store == nil {
		return nil, errors.New("nil store")
	}
	canonical, err := canonicalNpub(localNpub)
	if err != nil {
		return nil, fmt.Errorf("validate caller local npub: %w", err)
	}
	return &FanoutAttemptHandler{store: store, localNpub: canonical}, nil
}

// LocalNpub returns the bound local npub identity, or empty string if unbound.
func (h *FanoutAttemptHandler) LocalNpub() string {
	return h.localNpub
}

// Reserve validates the caller's immutable snapshot against both stores before
// R, while the stable outbox lock and SQLite transaction are held.
func (h *FanoutAttemptHandler) Reserve(entry types.OutboxEntry) (messaging.GroupAttemptToken, error) {
	token, err := h.store.reserveFanoutAttemptSnapshot(entry, h.localNpub)
	if err != nil {
		return messaging.GroupAttemptToken{}, mapFanoutError(err)
	}
	return toGroupAttemptToken(token), nil
}

// reserveFanoutAttemptSnapshot keeps caller validation, the live outbox read,
// the recovery barrier, and R within the same lock → BEGIN IMMEDIATE window.
func (s *Store) reserveFanoutAttemptSnapshot(snapshot types.OutboxEntry, localNpub string) (token fanoutAttemptToken, err error) {
	err = messaging.WithGroupOutboxLock(func(path string) error {
		tx, err := s.beginImmediate()
		if err != nil {
			return err
		}
		defer tx.Rollback()

		var key FanoutKey
		var envelope, recipient, frozenEvent, queueID string
		var query string
		var args []any
		if localNpub != "" {
			query = `SELECT local_npub,group_id,envelope_type,send_key,recipient_npub,event_json,queue_id
				FROM groupchat_fanout WHERE local_npub=? AND event_id=?`
			args = []any{localNpub, snapshot.ID}
		} else {
			query = `SELECT local_npub,group_id,envelope_type,send_key,recipient_npub,event_json,queue_id
				FROM groupchat_fanout WHERE event_id=?`
			args = []any{snapshot.ID}
		}
		err = tx.QueryRow(query, args...).
			Scan(&key.LocalNpub, &key.GroupID, &envelope, &key.SendKey, &recipient, &frozenEvent, &queueID)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: no fanout row froze event %s", messaging.ErrGroupAttemptConflict, snapshot.ID)
		}
		if err != nil {
			return err
		}
		if snapshot.RecipientNpub != recipient {
			return fmt.Errorf("%w: snapshot recipient %q differs from frozen row recipient %q", messaging.ErrGroupAttemptConflict, snapshot.RecipientNpub, recipient)
		}
		key.EnvelopeType = EnvelopeType(envelope)
		row, err := loadFanoutFailureRow(tx, key, recipient)
		if err != nil {
			return err
		}
		current, _, err := messaging.ReadGroupEntryLocked(path, row.token.eventID)
		if err != nil {
			return err
		}
		if current == nil {
			return messaging.ErrGroupAttemptQueueMissing
		}
		if err := validateFanoutAttemptImmutable(snapshot, current.Entry, frozenEvent, recipient, key.LocalNpub); err != nil {
			return err
		}
		barrierRow, replayed, err := s.fanoutBarrierTx(tx, path, key, recipient)
		if err != nil {
			return commitReplayOnRefusal(tx, replayed, err)
		}
		if err := validateFanoutAttemptEpoch(snapshot, current.Entry, barrierRow.token.queueID); err != nil {
			return commitReplayOnRefusal(tx, replayed, err)
		}
		if token, err = reserveFanoutAttemptTx(tx, key, recipient); err != nil {
			return err
		}
		return tx.Commit()
	})
	if err != nil {
		return fanoutAttemptToken{}, err
	}
	return token, nil
}

// validateFanoutAttemptSnapshot intentionally treats QueueID as an opaque
// nonempty epoch. Legacy values need not be hex. It compares snapshot identity
// and event bytes with the current locked entry and frozen event. Event ID and
// sender use the strict reader's validation and canonicalized member names;
// the raw event string is compared byte for byte above.
func validateFanoutAttemptSnapshot(snapshot, current types.OutboxEntry, frozenEvent, rowQueueID, recipient, localNpub string) error {
	if err := validateFanoutAttemptImmutable(snapshot, current, frozenEvent, recipient, localNpub); err != nil {
		return err
	}
	return validateFanoutAttemptEpoch(snapshot, current, rowQueueID)
}

func validateFanoutAttemptImmutable(snapshot, current types.OutboxEntry, frozenEvent, recipient, localNpub string) error {
	if snapshot.ID != current.ID || snapshot.RecipientNpub != recipient || current.RecipientNpub != recipient {
		return fmt.Errorf("%w: event or recipient identity differs from the frozen fanout row", messaging.ErrGroupAttemptConflict)
	}
	if current.EventJSON != frozenEvent {
		return fmt.Errorf("%w: live outbox event content differs from the frozen fanout event", messaging.ErrGroupAttemptConflict)
	}
	if snapshot.EventJSON != frozenEvent {
		return fmt.Errorf("%w: snapshot event content differs from the frozen fanout event", messaging.ErrGroupAttemptStale)
	}
	eventID, sender, err := messaging.VerifiedGroupEventIdentity([]byte(frozenEvent), current)
	if err != nil {
		return fmt.Errorf("%w: frozen event is invalid: %v", messaging.ErrGroupAttemptConflict, err)
	}
	if eventID != snapshot.ID || sender != localNpub {
		return fmt.Errorf("%w: frozen event ID or sender identity differs from the fanout row", messaging.ErrGroupAttemptConflict)
	}
	return nil
}

func validateFanoutAttemptEpoch(snapshot, current types.OutboxEntry, rowQueueID string) error {
	if rowQueueID != current.QueueID {
		return fmt.Errorf("%w: persisted queue %q differs from current outbox epoch %q", messaging.ErrGroupAttemptQueueConflict, rowQueueID, current.QueueID)
	}
	if snapshot.QueueID == "" || snapshot.QueueID != current.QueueID {
		return fmt.Errorf("%w: snapshot queue %q is not the current epoch %q", messaging.ErrGroupAttemptStale, snapshot.QueueID, current.QueueID)
	}
	return nil
}

func (h *FanoutAttemptHandler) validateTokenIdentity(token messaging.GroupAttemptToken) error {
	if h.localNpub != "" && token.Key.LocalNpub != h.localNpub {
		return fmt.Errorf("%w: token local npub %q differs from bound identity %q", messaging.ErrGroupAttemptConflict, token.Key.LocalNpub, h.localNpub)
	}
	return nil
}

func (h *FanoutAttemptHandler) Start(token messaging.GroupAttemptToken, targets int, now int64) error {
	if err := h.validateTokenIdentity(token); err != nil {
		return err
	}
	return mapFanoutError(h.store.startFanoutAttempt(fromGroupAttemptToken(token), targets, now))
}

func (h *FanoutAttemptHandler) Accepted(token messaging.GroupAttemptToken, now int64) error {
	if err := h.validateTokenIdentity(token); err != nil {
		return err
	}
	return mapFanoutError(h.store.commitFanoutAccepted(fromGroupAttemptToken(token), now))
}

// Failed runs O3+T4 and reports whether the row is now failed (budget exhausted).
func (h *FanoutAttemptHandler) Failed(token messaging.GroupAttemptToken) (bool, error) {
	if err := h.validateTokenIdentity(token); err != nil {
		return false, err
	}
	t := fromGroupAttemptToken(token)
	if err := h.store.commitFanoutFailure(t); err != nil {
		return false, mapFanoutError(err)
	}
	var state string
	err := h.store.db.QueryRow(`SELECT state FROM groupchat_fanout WHERE local_npub=? AND group_id=? AND envelope_type=? AND send_key=? AND recipient_npub=?`,
		t.key.LocalNpub, t.key.GroupID, t.key.EnvelopeType, t.key.SendKey, t.recipient).Scan(&state)
	if err != nil {
		return false, fmt.Errorf("%w: read state after T4: %v", messaging.ErrGroupAttemptPending, err)
	}
	return RecipientDeliveryState(state) == RecipientFailed, nil
}

func toGroupAttemptToken(t fanoutAttemptToken) messaging.GroupAttemptToken {
	return messaging.GroupAttemptToken{
		Key: messaging.OutboxFanoutKey{
			LocalNpub: t.key.LocalNpub, RecipientNpub: t.recipient,
			GroupID: t.key.GroupID, SendKey: t.key.SendKey, EnvelopeType: string(t.key.EnvelopeType),
		},
		EventID: t.eventID, QueueID: t.queueID, AttemptGeneration: t.generation,
	}
}

func fromGroupAttemptToken(t messaging.GroupAttemptToken) fanoutAttemptToken {
	return fanoutAttemptToken{
		key:       FanoutKey{LocalNpub: t.Key.LocalNpub, GroupID: t.Key.GroupID, EnvelopeType: EnvelopeType(t.Key.EnvelopeType), SendKey: t.Key.SendKey},
		recipient: t.Key.RecipientNpub, eventID: t.EventID, queueID: t.QueueID, generation: t.AttemptGeneration,
	}
}

// mapFanoutError wraps store errors with the messaging sentinel the send loop
// maps to SendResult; both chains stay inspectable with errors.Is.
func mapFanoutError(err error) error {
	if err == nil {
		return nil
	}
	for _, sentinel := range []error{
		messaging.ErrGroupAttemptStale,
		messaging.ErrGroupAttemptPending,
		messaging.ErrGroupAttemptCleanupPending,
		messaging.ErrGroupAttemptQueueConflict,
		messaging.ErrGroupAttemptQueueMissing,
		messaging.ErrGroupAttemptConflict,
	} {
		if errors.Is(err, sentinel) {
			return err
		}
	}
	for _, m := range []struct {
		from, to error
	}{
		{errFanoutStaleAttempt, messaging.ErrGroupAttemptStale},
		{ErrFanoutBookkeepingPending, messaging.ErrGroupAttemptPending},
		{ErrFanoutRecoveryPending, messaging.ErrGroupAttemptPending},
		{ErrFanoutCleanupPending, messaging.ErrGroupAttemptCleanupPending},
		{ErrFanoutBookkeepingConflict, messaging.ErrGroupAttemptConflict},
		{ErrFanoutQueueConflict, messaging.ErrGroupAttemptQueueConflict},
		{ErrFanoutQueueMissing, messaging.ErrGroupAttemptQueueMissing},
	} {
		if errors.Is(err, m.from) {
			return fmt.Errorf("%w: %w", m.to, err)
		}
	}
	return fmt.Errorf("%w: %w", messaging.ErrGroupAttemptConflict, err)
}

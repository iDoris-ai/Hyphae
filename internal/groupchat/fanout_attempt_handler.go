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
type FanoutAttemptHandler struct{ store *Store }

func NewFanoutAttemptHandler(store *Store) *FanoutAttemptHandler {
	return &FanoutAttemptHandler{store: store}
}

// Reserve binds the caller's snapshot to the authoritative frozen event and
// the live outbox entry, then runs R behind the barrier, all inside one
// outbox lock → BEGIN IMMEDIATE window. Nothing is written on a refusal.
func (h *FanoutAttemptHandler) Reserve(entry types.OutboxEntry) (messaging.GroupAttemptToken, error) {
	token, err := h.store.reserveFanoutAttemptSnapshot(entry)
	if err != nil {
		return messaging.GroupAttemptToken{}, mapFanoutError(err)
	}
	return toGroupAttemptToken(token), nil
}

func (s *Store) reserveFanoutAttemptSnapshot(snapshot types.OutboxEntry) (token fanoutAttemptToken, err error) {
	err = messaging.WithGroupOutboxLock(func(path string) error {
		tx, err := s.beginImmediate()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		var key FanoutKey
		var envelope, recipient, frozenEvent string
		err = tx.QueryRow(`SELECT local_npub,group_id,envelope_type,send_key,recipient_npub,event_json FROM groupchat_fanout WHERE event_id=?`, snapshot.ID).
			Scan(&key.LocalNpub, &key.GroupID, &envelope, &key.SendKey, &recipient, &frozenEvent)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: no fanout row froze event %s", messaging.ErrGroupAttemptConflict, snapshot.ID)
		}
		if err != nil {
			return err
		}
		if snapshot.RecipientNpub != recipient {
			return fmt.Errorf("%w: outbox recipient %q is not the fanout row's %q", messaging.ErrGroupAttemptConflict, snapshot.RecipientNpub, recipient)
		}
		key.EnvelopeType = EnvelopeType(envelope)
		current, _, err := messaging.ReadGroupEntryLocked(path, snapshot.ID)
		if err != nil {
			return err
		}
		if current == nil {
			return messaging.ErrGroupAttemptQueueMissing
		}
		if err := validateFanoutAttemptSnapshot(snapshot, current.Entry, frozenEvent, recipient, key.LocalNpub); err != nil {
			return err
		}
		barrierRow, replayed, err := s.fanoutBarrierTx(tx, path, key, recipient)
		if err != nil {
			return commitReplayOnRefusal(tx, replayed, err)
		}
		if snapshot.QueueID != "" && snapshot.QueueID != barrierRow.token.queueID {
			// The caller's snapshot predates a D6: refuse before R.
			return commitReplayOnRefusal(tx, replayed, fmt.Errorf("%w: snapshot queue %q is not the current epoch %q",
				messaging.ErrGroupAttemptStale, snapshot.QueueID, barrierRow.token.queueID))
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

// validateFanoutAttemptSnapshot requires the snapshot and live entry to carry
// the frozen event's exact bytes, and the frozen event's strictly decoded ID,
// recipient and sender to be the row's identity.
func validateFanoutAttemptSnapshot(snapshot, current types.OutboxEntry, frozenEvent, recipient, localNpub string) error {
	if snapshot.ID != current.ID || snapshot.RecipientNpub != recipient || current.RecipientNpub != recipient {
		return fmt.Errorf("%w: event or recipient identity differs from the frozen fanout row", messaging.ErrGroupAttemptConflict)
	}
	if current.EventJSON != frozenEvent {
		return fmt.Errorf("%w: live outbox event differs from the frozen fanout event", messaging.ErrGroupAttemptConflict)
	}
	if snapshot.EventJSON != frozenEvent {
		return fmt.Errorf("%w: snapshot event differs from the frozen fanout event", messaging.ErrGroupAttemptStale)
	}
	eventID, sender, err := messaging.VerifiedGroupEventIdentity([]byte(frozenEvent), current)
	if err != nil {
		return fmt.Errorf("%w: frozen event is invalid: %v", messaging.ErrGroupAttemptConflict, err)
	}
	if eventID != snapshot.ID || sender != localNpub {
		return fmt.Errorf("%w: frozen event ID or sender differs from the fanout row", messaging.ErrGroupAttemptConflict)
	}
	return nil
}

func (h *FanoutAttemptHandler) Start(token messaging.GroupAttemptToken, targets int, now int64) error {
	return mapFanoutError(h.store.startFanoutAttempt(fromGroupAttemptToken(token), targets, now))
}

func (h *FanoutAttemptHandler) Accepted(token messaging.GroupAttemptToken, now int64) error {
	return mapFanoutError(h.store.commitFanoutAccepted(fromGroupAttemptToken(token), now))
}

// Failed runs O3+T4 and reports whether the row is now failed (budget exhausted).
func (h *FanoutAttemptHandler) Failed(token messaging.GroupAttemptToken) (bool, error) {
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
		messaging.ErrGroupAttemptStale, messaging.ErrGroupAttemptPending, messaging.ErrGroupAttemptCleanupPending,
		messaging.ErrGroupAttemptQueueConflict, messaging.ErrGroupAttemptQueueMissing, messaging.ErrGroupAttemptConflict,
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

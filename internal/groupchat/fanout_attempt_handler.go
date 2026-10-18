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

// Reserve resolves the row that froze entry's event and runs R behind the
// barrier. The lookup and the reservation share one BEGIN IMMEDIATE
// transaction. The entry's recipient must be the row's recipient.
func (h *FanoutAttemptHandler) Reserve(entry types.OutboxEntry) (messaging.GroupAttemptToken, error) {
	token, err := h.store.reserveFanoutAttemptInTx(func(tx *immediateTx) (FanoutKey, string, string, error) {
		var key FanoutKey
		var envelope, recipient string
		err := tx.QueryRow(`SELECT local_npub,group_id,envelope_type,send_key,recipient_npub FROM groupchat_fanout WHERE event_id=?`, entry.ID).
			Scan(&key.LocalNpub, &key.GroupID, &envelope, &key.SendKey, &recipient)
		if errors.Is(err, sql.ErrNoRows) {
			return FanoutKey{}, "", "", attemptReadRefusal{fmt.Errorf("%w: no fanout row froze event %s", messaging.ErrGroupAttemptConflict, entry.ID)}
		}
		if err != nil {
			return FanoutKey{}, "", "", attemptReadRefusal{err}
		}
		key.EnvelopeType = EnvelopeType(envelope)
		if entry.RecipientNpub != recipient {
			return FanoutKey{}, "", "", attemptReadRefusal{fmt.Errorf("%w: outbox recipient %q is not the fanout row's %q", messaging.ErrGroupAttemptConflict, entry.RecipientNpub, recipient)}
		}
		if entry.EventJSON == "" {
			return FanoutKey{}, "", "", attemptReadRefusal{messaging.ErrGroupAttemptPayloadMismatch}
		}
		return key, recipient, entry.EventJSON, nil
	})
	if err != nil {
		var refusal attemptReadRefusal
		if errors.As(err, &refusal) {
			return messaging.GroupAttemptToken{}, refusal.err
		}
		return messaging.GroupAttemptToken{}, mapFanoutError(err)
	}
	if entry.QueueID != "" && entry.QueueID != token.queueID {
		// The caller's snapshot predates a D6; the barrier already judged the
		// current epoch, so report the stale snapshot without undoing R.
		return toGroupAttemptToken(token), fmt.Errorf("%w: snapshot queue %q is not the current epoch %q", messaging.ErrGroupAttemptStale, entry.QueueID, token.queueID)
	}
	return toGroupAttemptToken(token), nil
}

func (h *FanoutAttemptHandler) Start(token messaging.GroupAttemptToken, targets int, now int64) error {
	return mapFanoutError(h.store.startFanoutAttempt(fromGroupAttemptToken(token), targets, now))
}

func (h *FanoutAttemptHandler) Accepted(token messaging.GroupAttemptToken, now int64) error {
	return mapFanoutError(h.store.commitFanoutAccepted(fromGroupAttemptToken(token), now))
}

// Failed runs O3+T4 and reports whether the row is now failed (budget exhausted).
// The state read shares T4's BEGIN IMMEDIATE transaction.
func (h *FanoutAttemptHandler) Failed(token messaging.GroupAttemptToken) (bool, error) {
	failed, err := h.store.commitFanoutFailureState(fromGroupAttemptToken(token), true)
	if err != nil {
		return false, mapFanoutError(err)
	}
	return failed, nil
}

// attemptReadRefusal is a handler decision made inside the reserve transaction.
// Reserve returns it as-is so mapFanoutError does not reclassify it.
type attemptReadRefusal struct{ err error }

func (e attemptReadRefusal) Error() string { return e.err.Error() }
func (e attemptReadRefusal) Unwrap() error { return e.err }

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
	for _, m := range []struct {
		from, to error
	}{
		{errFanoutStaleAttempt, messaging.ErrGroupAttemptStale},
		{ErrFanoutBookkeepingPending, messaging.ErrGroupAttemptPending},
		{ErrFanoutRecoveryPending, messaging.ErrGroupAttemptPending},
		{ErrFanoutCleanupPending, messaging.ErrGroupAttemptCleanupPending},
		{ErrFanoutBookkeepingConflict, messaging.ErrGroupAttemptConflict},
		{ErrFanoutQueueConflict, messaging.ErrGroupAttemptQueueConflict},
		{errFanoutPayloadMismatch, messaging.ErrGroupAttemptPayloadMismatch},
		{ErrFanoutQueueMissing, messaging.ErrGroupAttemptQueueMissing},
	} {
		if errors.Is(err, m.from) {
			return fmt.Errorf("%w: %w", m.to, err)
		}
	}
	return fmt.Errorf("%w: %w", messaging.ErrGroupAttemptConflict, err)
}

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
// barrier. The entry's recipient must be the row's recipient.
func (h *FanoutAttemptHandler) Reserve(entry types.OutboxEntry) (messaging.GroupAttemptToken, error) {
	var key FanoutKey
	var envelope, recipient string
	err := h.store.db.QueryRow(`SELECT local_npub,group_id,envelope_type,send_key,recipient_npub FROM groupchat_fanout WHERE event_id=?`, entry.ID).
		Scan(&key.LocalNpub, &key.GroupID, &envelope, &key.SendKey, &recipient)
	if errors.Is(err, sql.ErrNoRows) {
		return messaging.GroupAttemptToken{}, fmt.Errorf("%w: no fanout row froze event %s", messaging.ErrGroupAttemptConflict, entry.ID)
	}
	if err != nil {
		return messaging.GroupAttemptToken{}, err
	}
	key.EnvelopeType = EnvelopeType(envelope)
	if entry.RecipientNpub != recipient {
		return messaging.GroupAttemptToken{}, fmt.Errorf("%w: outbox recipient %q is not the fanout row's %q", messaging.ErrGroupAttemptConflict, entry.RecipientNpub, recipient)
	}
	token, err := h.store.reserveFanoutAttempt(key, recipient)
	if err != nil {
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

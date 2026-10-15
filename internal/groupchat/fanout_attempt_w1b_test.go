package groupchat

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/iDoris-ai/hyphae/internal/messaging"
	"github.com/stretchr/testify/require"
)

// W1b-C6a: a max_retries divergence with an unconsumed O3 receipt is refused
// as the 9fd core D14 conflict before any directory confirm or O3 replay; it
// is not the O3 replay's bookkeeping conflict.
func TestW1bMaxDivergenceRefusedBeforeO3(t *testing.T) {
	f := newFailureFixture(t)
	f.failO3BeforeT4(t, f.startAttempt(t, 1))
	rawEditEntryField(t, f.outboxPath, func(fields map[string]json.RawMessage) {
		fields["max_retries"] = json.RawMessage("1")
	})
	orig := confirmOutboxDurable
	t.Cleanup(func() { confirmOutboxDurable = orig })
	calls := 0
	confirmOutboxDurable = func(string) error { calls++; return nil }
	unchanged := f.snapshot(t)
	_, err := f.member.store.reserveFanoutAttempt(f.key, f.recipient)
	require.ErrorIs(t, err, ErrFanoutQueueConflict)
	require.ErrorIs(t, err, errFanoutMaxRetriesDiverged)
	require.False(t, errors.Is(err, ErrFanoutBookkeepingConflict))
	require.ErrorContains(t, err, "outbox_max=1 row_max=3")
	require.Zero(t, calls)
	unchanged()
}

// W1b-C6b: through the handler the same divergence keeps the core sentinel in
// the chain but reports the public bookkeeping conflict, never queue_conflict.
// A queue_id-only conflict still reports queue_conflict.
func TestW1bMaxDivergenceHandlerContract(t *testing.T) {
	f := newFailureFixture(t)
	rawEditEntryField(t, f.outboxPath, func(fields map[string]json.RawMessage) {
		fields["max_retries"] = json.RawMessage("2")
	})
	unchanged := f.snapshot(t)
	_, err := NewFanoutAttemptHandler(f.member.store).Reserve(f.typedEntry(t))
	require.ErrorIs(t, err, ErrFanoutQueueConflict)
	require.ErrorIs(t, err, messaging.ErrGroupAttemptConflict)
	require.False(t, errors.Is(err, messaging.ErrGroupAttemptQueueConflict))
	unchanged()

	g := newFailureFixture(t)
	_, err = g.member.db.Exec(`UPDATE groupchat_fanout SET queue_id='ffffffffffffffffffffffffffffffff'`)
	require.NoError(t, err)
	_, err = NewFanoutAttemptHandler(g.member.store).Reserve(g.typedEntry(t))
	require.ErrorIs(t, err, messaging.ErrGroupAttemptQueueConflict)
	require.False(t, errors.Is(err, errFanoutMaxRetriesDiverged))
}

// W1b-C4: the store keeps D14 for frozen-byte drift; the handler's caller bind
// sees the drift first and reports the dual payload error without a write.
func TestW1bPayloadDriftStoreConflictHandlerPayload(t *testing.T) {
	f := newFailureFixture(t)
	_, err := f.member.db.Exec(`UPDATE groupchat_fanout SET event_json = event_json || ' ' WHERE event_id=?`, f.eventID)
	require.NoError(t, err)
	unchanged := f.snapshot(t)
	_, err = f.member.store.reserveFanoutAttempt(f.key, f.recipient)
	require.ErrorIs(t, err, ErrFanoutQueueConflict)
	_, err = NewFanoutAttemptHandler(f.member.store).Reserve(f.typedEntry(t))
	require.ErrorIs(t, err, messaging.ErrGroupAttemptPayloadMismatch)
	require.ErrorIs(t, err, messaging.ErrGroupAttemptConflict)
	require.False(t, errors.Is(err, messaging.ErrGroupAttemptQueueConflict))
	unchanged()
}

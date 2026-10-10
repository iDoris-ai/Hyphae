package groupchat

import (
	"database/sql"
	"testing"
	"time"

	"github.com/iDoris-ai/hyphae/pkg/types"
	"github.com/stretchr/testify/require"
)

func TestFanoutAcceptanceUsesFinalAttemptTimestamp(t *testing.T) {
	member := newMember(t)
	key := FanoutKey{member.npub, "g", EnvelopeMessage, "m"}
	insertFanout(t, member, key, "r", RecipientQueued, "e")
	_, err := member.db.Exec(`UPDATE groupchat_fanout SET last_attempt_at=80`)
	require.NoError(t, err)
	before := fanoutRowSnapshot(t, member, "e")
	futureAttempt := time.Now().Unix() + 100_000

	changed, err := applyFanout(t, member, key, "r", RecipientQueued, RecipientRelayAccepted,
		map[string]any{"relay_acks": 1, "accepted_at": int64(100), "last_attempt_at": futureAttempt})
	require.NoError(t, err)
	require.True(t, changed)

	after := fanoutRowSnapshot(t, member, "e")
	expected := append([]any(nil), before...)
	expected[8] = string(RecipientRelayAccepted)
	expected[9] = ""
	expected[10] = int64(1)
	expected[14] = before[14].(int64) + 1
	expected[16] = "accepted"
	expected[26] = after[26] // updated_at is wall-clock monotonic bookkeeping.
	expected[27] = futureAttempt
	expected[28] = futureAttempt
	require.Equal(t, expected, after, "acceptance should change only its terminal fields and timestamps")
	require.GreaterOrEqual(t, after[28].(int64), after[27].(int64))
	require.Greater(t, after[28].(int64), int64(0))
}

func TestMarkRelayAcceptedStaleReadIsNoOp(t *testing.T) {
	for _, target := range []RecipientDeliveryState{RecipientRelayAccepted, RecipientFailed} {
		t.Run(string(target), func(t *testing.T) {
			member := newMember(t)
			key := FanoutKey{member.npub, "g", EnvelopeMessage, "m"}
			insertFanout(t, member, key, "r", RecipientQueued, "e")
			other, err := sql.Open("sqlite", member.dbPath)
			require.NoError(t, err)
			defer other.Close()

			var afterCompeting []any
			handler := NewFanoutOutboxHandler(member.store)
			handler.beforeRelayAcceptedTx = func() {
				phase, acks, acceptedAt := "failed", 0, int64(0)
				if target == RecipientRelayAccepted {
					phase, acks, acceptedAt = "accepted", 1, 100
				}
				_, err := other.Exec(`UPDATE groupchat_fanout
					SET state=?,attempt_phase=?,relay_acks=?,accepted_at=?,row_revision=row_revision+1
					WHERE event_id='e'`, target, phase, acks, acceptedAt)
				require.NoError(t, err)
				otherMember := member
				otherMember.db = other
				afterCompeting = fanoutRowSnapshot(t, otherMember, "e")
			}

			err = handler.MarkRelayAccepted(types.OutboxEntry{ID: "e", QueueID: "original-queue", RecipientNpub: "r"}, 1, 2)
			require.NoError(t, err, "a stale ACK after a competing state change is harmless")
			require.NotNil(t, afterCompeting, "the competing update must run at the injected boundary")
			require.Equal(t, afterCompeting, fanoutRowSnapshot(t, member, "e"), "stale ACK must not mutate the competing row")
		})
	}
}

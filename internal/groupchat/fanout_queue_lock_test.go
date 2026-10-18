package groupchat

import (
	"database/sql"
	"testing"

	"github.com/iDoris-ai/hyphae/internal/messaging"
	"github.com/iDoris-ai/hyphae/pkg/types"
	"github.com/stretchr/testify/require"
)

func TestRecordAttemptFailureLocksExhaustionDecision(t *testing.T) {
	t.Run("stale_exhausted_does_not_mark_failed", func(t *testing.T) {
		member := newMemberWithSecret(t, fixedQueueTestSecret(1))
		key := FanoutKey{LocalNpub: member.npub, GroupID: mustOpaque(t), EnvelopeType: EnvelopeMessage, SendKey: mustOpaque(t)}
		_, eventJSON, recipient := signedGroupEvent(t, member.sk, fixedQueueTestSecret(2), key)
		insertTestFanout(t, member, key, recipient, RecipientQueued, "e-stale", eventJSON, "q", 0)
		other, err := sql.Open("sqlite", member.dbPath)
		require.NoError(t, err)
		t.Cleanup(func() { _ = other.Close() })
		handler := NewFanoutOutboxHandler(member.store)
		handler.beforeAttemptFailureTx = func() {
			_, err := other.Exec(`UPDATE groupchat_fanout SET attempts=4 WHERE event_id=?`, "e-stale")
			require.NoError(t, err)
		}
		entry := types.OutboxEntry{ID: "e-stale", QueueID: "q", RecipientNpub: recipient, RetryCount: 0, MaxRetries: 10}
		require.NoError(t, handler.RecordAttemptFailure(entry, true, messaging.AgentMessageIssueRetryExhausted))

		var state string
		var attempts int
		require.NoError(t, member.db.QueryRow(`SELECT state, attempts FROM groupchat_fanout WHERE event_id=?`, "e-stale").Scan(&state, &attempts))
		require.Equal(t, string(RecipientQueued), state)
		require.Equal(t, 5, attempts)
	})
}

package groupchat

import (
	"database/sql"
	"os"
	"testing"

	"github.com/iDoris-ai/hyphae/internal/messaging"
	"github.com/iDoris-ai/hyphae/pkg/types"
	"github.com/stretchr/testify/require"
)

func TestRecordAttemptFailureLocksExhaustionDecision(t *testing.T) {
	t.Run("competing_commit_keeps_attempt_increment", func(t *testing.T) {
		member := newMemberWithSecret(t, fixedQueueTestSecret(1))
		key := FanoutKey{LocalNpub: member.npub, GroupID: mustOpaque(t), EnvelopeType: EnvelopeMessage, SendKey: mustOpaque(t)}
		_, eventJSON, recipient := signedGroupEvent(t, member.sk, fixedQueueTestSecret(2), key)
		insertTestFanout(t, member, key, recipient, RecipientQueued, "e-race", eventJSON, "q", 8)
		other, err := sql.Open("sqlite", member.dbPath)
		require.NoError(t, err)
		t.Cleanup(func() { _ = other.Close() })
		handler := NewFanoutOutboxHandler(member.store)
		handler.beforeAttemptFailureTx = func() {
			_, err := other.Exec(`UPDATE groupchat_fanout SET state=?, attempts=9 WHERE event_id=?`, RecipientFailed, "e-race")
			require.NoError(t, err)
		}
		entry := types.OutboxEntry{ID: "e-race", QueueID: "q", RecipientNpub: recipient, RetryCount: 8, MaxRetries: 10}
		require.NoError(t, handler.RecordAttemptFailure(entry, false, messaging.AgentMessageIssueSendFailed))

		var state string
		var attempts int
		require.NoError(t, member.db.QueryRow(`SELECT state, attempts FROM groupchat_fanout WHERE event_id=?`, "e-race").Scan(&state, &attempts))
		require.Equal(t, string(RecipientFailed), state)
		require.Equal(t, 10, attempts)
	})

	t.Run("requeue_first_failure_stays_queued", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		member := newMemberWithSecret(t, fixedQueueTestSecret(3))
		key := FanoutKey{LocalNpub: member.npub, GroupID: mustOpaque(t), EnvelopeType: EnvelopeMessage, SendKey: mustOpaque(t)}
		eventID, eventJSON, recipient := signedGroupEvent(t, member.sk, fixedQueueTestSecret(4), key)
		insertTestFanout(t, member, key, recipient, RecipientFailed, eventID, eventJSON, "old-q", 10)
		path, err := messaging.GetOutboxPath()
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(path, []byte(`{"entries":[]}`), 0o600))

		_, err = member.store.RequeueFailed(key, recipient)
		require.NoError(t, err)
		ob, err := messaging.LoadOutbox()
		require.NoError(t, err)
		entries := messaging.GroupOutboxEntriesByEventID(ob, eventID)
		require.Len(t, entries, 1)
		require.Equal(t, 0, entries[0].RetryCount)
		require.Equal(t, 10, entries[0].MaxRetries)
		require.Equal(t, messaging.OutboxStatusGroupPending, entries[0].Status)

		handler := NewFanoutOutboxHandler(member.store)
		require.NoError(t, handler.RecordAttemptFailure(entries[0], false, messaging.AgentMessageIssueSendFailed))

		var state string
		var attempts int
		require.NoError(t, member.db.QueryRow(`SELECT state, attempts FROM groupchat_fanout WHERE event_id=?`, eventID).Scan(&state, &attempts))
		require.Equal(t, string(RecipientQueued), state)
		require.Equal(t, 11, attempts)
		latest, err := messaging.LoadOutbox()
		require.NoError(t, err)
		again := messaging.GroupOutboxEntriesByEventID(latest, eventID)
		require.Len(t, again, 1)
		require.Equal(t, messaging.OutboxStatusGroupPending, again[0].Status)
		require.Equal(t, 0, again[0].RetryCount)
	})

	t.Run("lagging_attempts_still_honor_caller_exhausted", func(t *testing.T) {
		member := newMemberWithSecret(t, fixedQueueTestSecret(5))
		key := FanoutKey{LocalNpub: member.npub, GroupID: mustOpaque(t), EnvelopeType: EnvelopeMessage, SendKey: mustOpaque(t)}
		_, eventJSON, recipient := signedGroupEvent(t, member.sk, fixedQueueTestSecret(6), key)
		insertTestFanout(t, member, key, recipient, RecipientQueued, "e-lag", eventJSON, "q", 8)
		handler := NewFanoutOutboxHandler(member.store)
		entry := types.OutboxEntry{ID: "e-lag", QueueID: "q", RecipientNpub: recipient, RetryCount: 9, MaxRetries: 10}
		require.NoError(t, handler.RecordAttemptFailure(entry, true, messaging.AgentMessageIssueRetryExhausted))

		var state string
		var attempts int
		require.NoError(t, member.db.QueryRow(`SELECT state, attempts FROM groupchat_fanout WHERE event_id=?`, "e-lag").Scan(&state, &attempts))
		require.Equal(t, string(RecipientFailed), state)
		require.Equal(t, 9, attempts)
	})
}

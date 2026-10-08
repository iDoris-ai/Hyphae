package groupchat

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/iDoris-ai/hyphae/internal/messaging"
	"github.com/stretchr/testify/require"
)

func insertFanout(t *testing.T, member testMember, key FanoutKey, recipient string, state RecipientDeliveryState, eventID string) {
	t.Helper()
	_, err := member.db.Exec(`INSERT INTO groupchat_fanout
 (local_npub, group_id, envelope_type, send_key, recipient_npub, event_id, event_json,
 queue_id, state, max_retries, created_at, updated_at)
 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 10, 123, 123)`,
		key.LocalNpub, key.GroupID, key.EnvelopeType, key.SendKey, recipient, eventID,
		`{"content":"frozen ciphertext"}`, "original-queue", state)
	require.NoError(t, err)
}

func applyFanout(t *testing.T, member testMember, key FanoutKey, recipient string, from, to RecipientDeliveryState, fields map[string]any) (bool, error) {
	t.Helper()
	tx, err := member.store.beginImmediate()
	require.NoError(t, err)
	defer tx.Rollback()
	changed, err := transitionFanoutTx(tx, key, recipient, from, to, fields)
	// Commit even rejected transitions to prove the guard, rather than rollback,
	// is what preserves the row.
	require.NoError(t, tx.Commit())
	return changed, err
}

func TestFanoutTransitionMatrix(t *testing.T) {
	states := []RecipientDeliveryState{RecipientPrepared, RecipientQueued, RecipientRelayAccepted, RecipientFailed}
	// Rows and columns: prepared, queued, relay_accepted, failed. D14 permits
	// same-state bookkeeping and prepared->failed, but requires queued before ACK.
	allowed := [4][4]bool{
		{true, true, false, true},
		{false, true, true, true},
		{false, false, true, false},
		{false, true, true, true},
	}
	for i, from := range states {
		for j, to := range states {
			t.Run(string(from)+"_to_"+string(to), func(t *testing.T) {
				member := newMember(t)
				key := FanoutKey{member.npub, "group", EnvelopeMessage, "logical"}
				insertFanout(t, member, key, "recipient", from, "event")
				before, err := member.store.LoadFanoutReport(key)
				require.NoError(t, err)
				changed, err := applyFanout(t, member, key, "recipient", from, to, map[string]any{"attempts": 2, "issue": messaging.AgentMessageIssueSendFailed})
				after, loadErr := member.store.LoadFanoutReport(key)
				require.NoError(t, loadErr)
				if allowed[i][j] {
					require.NoError(t, err)
					require.True(t, changed)
					require.Equal(t, to, after.Recipients[0].State)
					require.Equal(t, 2, after.Recipients[0].Attempts)
				} else {
					require.ErrorIs(t, err, ErrInvalidTransition)
					require.False(t, changed)
					require.Equal(t, before, after)
					var updatedAt int64
					require.NoError(t, member.db.QueryRow(`SELECT updated_at FROM groupchat_fanout`).Scan(&updatedAt))
					require.Equal(t, int64(123), updatedAt)
				}
				var eventJSON string
				require.NoError(t, member.db.QueryRow(`SELECT event_json FROM groupchat_fanout`).Scan(&eventJSON))
				require.Equal(t, `{"content":"frozen ciphertext"}`, eventJSON)
			})
		}
	}
	t.Run("same_state_preserves_evidence", func(t *testing.T) {
		member := newMember(t)
		key := FanoutKey{member.npub, "group", EnvelopeMessage, "logical"}
		insertFanout(t, member, key, "recipient", RecipientQueued, "event")
		changed, err := applyFanout(t, member, key, "recipient", RecipientQueued, RecipientQueued, map[string]any{"attempts": 3, "queue_id": ""})
		require.NoError(t, err)
		require.True(t, changed)
		report, err := member.store.LoadFanoutReport(key)
		require.NoError(t, err)
		require.Equal(t, RecipientQueued, report.Recipients[0].State)
		require.Equal(t, "original-queue", report.Recipients[0].QueueID)
		require.Equal(t, 3, report.Recipients[0].Attempts)
		changed, err = applyFanout(t, member, key, "recipient", RecipientQueued, RecipientQueued, map[string]any{"attempts": 1})
		require.NoError(t, err)
		require.True(t, changed)
		report, err = member.store.LoadFanoutReport(key)
		require.NoError(t, err)
		require.Equal(t, 3, report.Recipients[0].Attempts)
	})
	t.Run("stale_concurrent_failure_after_ack", func(t *testing.T) {
		member := newPoolMember(t)
		key := FanoutKey{member.npub, "group", EnvelopeMessage, "logical"}
		insertFanout(t, member, key, "recipient", RecipientQueued, "event")
		changed, err := applyFanout(t, member, key, "recipient", RecipientQueued, RecipientRelayAccepted,
			map[string]any{"relay_acks": 1, "relay_count": 3, "accepted_at": int64(456)})
		require.NoError(t, err)
		require.True(t, changed)
		before, err := member.store.LoadFanoutReport(key)
		require.NoError(t, err)
		var wg sync.WaitGroup
		failures := make(chan error, 8)
		for range 8 {
			wg.Go(func() {
				tx, err := member.store.beginImmediate()
				if err != nil {
					failures <- err
					return
				}
				defer tx.Rollback()
				changed, err := transitionFanoutTx(tx, key, "recipient", RecipientQueued, RecipientFailed,
					map[string]any{"issue": messaging.AgentMessageIssueRetryExhausted})
				if err == nil && changed {
					err = fmt.Errorf("stale failure changed an accepted row")
				}
				if err == nil {
					err = tx.Commit()
				}
				failures <- err
			})
		}
		wg.Wait()
		close(failures)
		for err := range failures {
			require.NoError(t, err)
		}
		after, err := member.store.LoadFanoutReport(key)
		require.NoError(t, err)
		require.Equal(t, before, after)
	})
	t.Run("accepted_evidence_cannot_be_cleared", func(t *testing.T) {
		member := newMember(t)
		key := FanoutKey{member.npub, "group", EnvelopeMessage, "logical"}
		insertFanout(t, member, key, "recipient", RecipientQueued, "event")
		changed, err := applyFanout(t, member, key, "recipient", RecipientQueued, RecipientRelayAccepted,
			map[string]any{"relay_acks": 1, "accepted_at": int64(456)})
		require.NoError(t, err)
		require.True(t, changed)
		before, err := member.store.LoadFanoutReport(key)
		require.NoError(t, err)
		changed, err = applyFanout(t, member, key, "recipient", RecipientRelayAccepted, RecipientRelayAccepted,
			map[string]any{"relay_acks": 0, "accepted_at": int64(0), "queue_id": ""})
		require.NoError(t, err)
		require.True(t, changed)
		after, err := member.store.LoadFanoutReport(key)
		require.NoError(t, err)
		require.Equal(t, before, after)
	})

	t.Run("reject_unknown_states_and_frozen_fields", func(t *testing.T) {
		member := newMember(t)
		key := FanoutKey{member.npub, "group", EnvelopeMessage, "logical"}
		insertFanout(t, member, key, "recipient", RecipientQueued, "event")
		for _, pair := range [][2]RecipientDeliveryState{{"unknown", RecipientQueued}, {RecipientQueued, "unknown"}} {
			changed, err := applyFanout(t, member, key, "recipient", pair[0], pair[1], nil)
			require.ErrorIs(t, err, ErrInvalidTransition)
			require.False(t, changed)
		}
		for _, column := range []string{"event_id", "event_json", "local_npub", "state", "issue = ''; DROP TABLE groupchat_fanout; --"} {
			changed, err := applyFanout(t, member, key, "recipient", RecipientQueued, RecipientFailed, map[string]any{column: "bad"})
			require.Error(t, err)
			require.False(t, changed)
		}
		for _, acks := range []int{-1, 2} {
			changed, err := applyFanout(t, member, key, "recipient", RecipientQueued, RecipientRelayAccepted, map[string]any{"relay_acks": acks})
			require.Error(t, err)
			require.False(t, changed)
		}
	})
}

func TestFanoutReport(t *testing.T) {
	member := newMember(t)
	key := FanoutKey{member.npub, "group", EnvelopeMessage, "logical"}
	t.Run("missing", func(t *testing.T) {
		_, err := member.store.LoadFanoutReport(key)
		require.ErrorIs(t, err, sql.ErrNoRows)
		badKey := key
		badKey.LocalNpub = "invalid"
		_, err = member.store.LoadFanoutReport(badKey)
		require.Error(t, err)
	})
	for i, recipient := range []string{"z", "b", "a", "c"} {
		insertFanout(t, member, key, recipient, []RecipientDeliveryState{RecipientFailed, RecipientQueued, RecipientRelayAccepted, RecipientPrepared}[i], fmt.Sprint(i))
	}
	_, err := member.db.Exec(`UPDATE groupchat_fanout SET relay_acks = 1, relay_count = 5,
 attempts = 2, last_attempt_at = 200, accepted_at = 201 WHERE recipient_npub = 'a'`)
	require.NoError(t, err)
	_, err = member.db.Exec(`UPDATE groupchat_fanout SET issue = ? WHERE recipient_npub = 'z'`, messaging.AgentMessageIssueQueueMissing)
	require.NoError(t, err)
	// Other identity, group, envelope type and send key must never enter the report.
	for i := range 4 {
		other := key
		switch i {
		case 0:
			other.LocalNpub = "other"
		case 1:
			other.GroupID = "other"
		case 2:
			other.EnvelopeType = EnvelopeInvite
		case 3:
			other.SendKey = "other"
		}
		insertFanout(t, member, other, "noise", RecipientFailed, fmt.Sprintf("noise%d", i))
	}
	require.NoError(t, member.store.migrate())
	require.NoError(t, member.store.migrate())
	report, err := member.store.LoadFanoutReport(key)
	require.NoError(t, err)
	require.Equal(t, messaging.AgentMessageFailed, report.State)
	require.Equal(t, key.GroupID, report.GroupID)
	require.Equal(t, key.SendKey, report.LogicalID)
	require.Equal(t, int64(123), report.CreatedAt)
	require.Equal(t, 1, report.Accepted)
	require.Equal(t, 2, report.Queued)
	require.Equal(t, 1, report.Failed)
	require.Len(t, report.Recipients, 4)
	require.Equal(t, []string{"a", "b", "c", "z"}, []string{report.Recipients[0].RecipientNpub, report.Recipients[1].RecipientNpub, report.Recipients[2].RecipientNpub, report.Recipients[3].RecipientNpub})
	require.Equal(t, RecipientDelivery{RecipientNpub: "a", EventID: "2", QueueID: "original-queue",
		State: RecipientRelayAccepted, RelayAcks: 1, RelayCount: 5, Attempts: 2, MaxRetries: 10,
		LastAttemptAt: 200, AcceptedAt: 201}, report.Recipients[0])
	encoded, err := json.Marshal(report)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "relays")
	require.NotContains(t, string(encoded), "ciphertext")
	require.NotContains(t, string(encoded), "event_json")
	require.Contains(t, string(encoded), `"issue":"queue_missing"`)
	require.Contains(t, string(encoded), `"relay_acks":1,"relay_count":5`)
	changed, err := applyFanout(t, member, key, "z", RecipientFailed, RecipientQueued, nil)
	require.NoError(t, err)
	require.True(t, changed)
	report, err = member.store.LoadFanoutReport(key)
	require.NoError(t, err)
	require.Equal(t, messaging.AgentMessageQueued, report.State)
	require.Equal(t, 3, report.Queued)
	require.Zero(t, report.Failed)
	for recipient, from := range map[string]RecipientDeliveryState{"b": RecipientQueued, "c": RecipientPrepared, "z": RecipientQueued} {
		if from == RecipientPrepared {
			changed, err = applyFanout(t, member, key, recipient, from, RecipientQueued, nil)
			require.NoError(t, err)
			require.True(t, changed)
		}
		changed, err = applyFanout(t, member, key, recipient, RecipientQueued, RecipientRelayAccepted, map[string]any{"relay_acks": 1})
		require.NoError(t, err)
		require.True(t, changed)
	}
	report, err = member.store.LoadFanoutReport(key)
	require.NoError(t, err)
	require.Equal(t, messaging.AgentMessageRelayAccepted, report.State)
	require.Equal(t, 4, report.Accepted)
	require.Zero(t, report.Queued)
	require.Zero(t, report.Failed)
	t.Run("database_error", func(t *testing.T) {
		closed := newMember(t)
		require.NoError(t, closed.db.Close())
		_, err := closed.store.LoadFanoutReport(key)
		require.Error(t, err)
	})
}

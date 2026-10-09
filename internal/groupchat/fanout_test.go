package groupchat

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"sync"
	"testing"

	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/internal/messaging"
	"github.com/stretchr/testify/require"
)

func insertFanout(t *testing.T, member testMember, key FanoutKey, recipient string, state RecipientDeliveryState, eventID string) {
	t.Helper()
	// A prepared row has not been queued yet, so it carries no queue_id;
	// every other starting state simulates a prior confirmed assignment.
	queueID := "original-queue"
	if state == RecipientPrepared {
		queueID = ""
	}
	_, err := member.db.Exec(`INSERT INTO groupchat_fanout
 (local_npub, group_id, envelope_type, send_key, recipient_npub, event_id, event_json,
 queue_id, state, max_retries, created_at, updated_at)
 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 10, 123, 123)`,
		key.LocalNpub, key.GroupID, key.EnvelopeType, key.SendKey, recipient, eventID,
		`{"content":"frozen ciphertext"}`, queueID, state)
	require.NoError(t, err)
}

// requiredFanoutFields returns the evidence a legitimate caller must supply
// to enter a given target state, mirroring the invariants transitionFanoutTx
// enforces: a fresh queue_id to become queued, and relay_acks=1 plus a
// nonzero accepted_at to become relay_accepted.
func requiredFanoutFields(from, to RecipientDeliveryState, recipient string) map[string]any {
	if from == to {
		return nil
	}
	switch to {
	case RecipientQueued:
		return map[string]any{"queue_id": "assigned-queue-" + recipient}
	case RecipientRelayAccepted:
		return map[string]any{"relay_acks": 1, "accepted_at": int64(999)}
	default:
		return nil
	}
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
				fields := map[string]any{"attempts": 2, "issue": messaging.AgentMessageIssueSendFailed}
				if from == RecipientRelayAccepted && to == RecipientRelayAccepted {
					fields = nil
				}
				for column, value := range requiredFanoutFields(from, to, "recipient") {
					fields[column] = value
				}
				changed, err := applyFanout(t, member, key, "recipient", from, to, fields)
				after, loadErr := member.store.LoadFanoutReport(key)
				require.NoError(t, loadErr)
				if allowed[i][j] {
					require.NoError(t, err)
					require.True(t, changed)
					require.Equal(t, to, after.Recipients[0].State)
					if from != RecipientRelayAccepted {
						require.Equal(t, 2, after.Recipients[0].Attempts)
					}
					if to == RecipientRelayAccepted || (from == RecipientFailed && to == RecipientQueued) {
						require.Equal(t, messaging.AgentMessageIssueNone, after.Recipients[0].Issue,
							"entering %s from %s must clear any stale issue", to, from)
					}
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
			map[string]any{"relay_acks": 0, "accepted_at": int64(0)})
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
		for _, column := range []string{"event_id", "event_json", "local_npub", "state", "max_retries", "issue = ''; DROP TABLE groupchat_fanout; --"} {
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

	t.Run("relay_acceptance_requires_evidence", func(t *testing.T) {
		member := newMember(t)
		key := FanoutKey{member.npub, "group", EnvelopeMessage, "logical"}
		insertFanout(t, member, key, "recipient", RecipientQueued, "event")
		for _, fields := range []map[string]any{
			nil,
			{"relay_acks": 1},         // missing accepted_at
			{"accepted_at": int64(1)}, // missing relay_acks
			{"relay_acks": 1, "accepted_at": int64(0)}, // accepted_at must be nonzero
		} {
			changed, err := applyFanout(t, member, key, "recipient", RecipientQueued, RecipientRelayAccepted, fields)
			require.Error(t, err)
			require.False(t, changed)
		}
		report, err := member.store.LoadFanoutReport(key)
		require.NoError(t, err)
		require.Equal(t, RecipientQueued, report.Recipients[0].State)
	})

	t.Run("relay_acceptance_rejects_unsupported_value_types", func(t *testing.T) {
		member := newMember(t)
		key := FanoutKey{member.npub, "group", EnvelopeMessage, "logical"}
		insertFanout(t, member, key, "recipient", RecipientQueued, "event")
		// Strings would otherwise be silently accepted by SQLite's dynamic
		// typing and corrupt LoadFanoutReport's later int64 scan.
		for _, fields := range []map[string]any{
			{"relay_acks": "1", "accepted_at": int64(1)},
			{"relay_acks": 1, "accepted_at": "1"},
			{"relay_acks": 1.5, "accepted_at": int64(1)},
		} {
			changed, err := applyFanout(t, member, key, "recipient", RecipientQueued, RecipientRelayAccepted, fields)
			require.Error(t, err)
			require.False(t, changed)
		}
	})

	t.Run("queued_requires_nonempty_queue_id", func(t *testing.T) {
		member := newMember(t)
		key := FanoutKey{member.npub, "group", EnvelopeMessage, "logical"}
		insertFanout(t, member, key, "recipient", RecipientPrepared, "event")
		for _, fields := range []map[string]any{nil, {"queue_id": ""}, {"queue_id": 5}} {
			changed, err := applyFanout(t, member, key, "recipient", RecipientPrepared, RecipientQueued, fields)
			require.Error(t, err)
			require.False(t, changed)
		}
		changed, err := applyFanout(t, member, key, "recipient", RecipientPrepared, RecipientQueued, map[string]any{"queue_id": "fresh-queue"})
		require.NoError(t, err)
		require.True(t, changed)
		report, err := member.store.LoadFanoutReport(key)
		require.NoError(t, err)
		require.Equal(t, "fresh-queue", report.Recipients[0].QueueID)
	})

	t.Run("same_state_cannot_replace_confirmed_queue_id", func(t *testing.T) {
		member := newMember(t)
		key := FanoutKey{member.npub, "group", EnvelopeMessage, "logical"}
		insertFanout(t, member, key, "recipient", RecipientQueued, "event") // queue_id = "original-queue"
		changed, err := applyFanout(t, member, key, "recipient", RecipientQueued, RecipientQueued, map[string]any{"queue_id": "different-queue"})
		require.NoError(t, err)
		require.True(t, changed) // the row updates (updated_at bumps), but the identity is preserved.
		report, err := member.store.LoadFanoutReport(key)
		require.NoError(t, err)
		require.Equal(t, "original-queue", report.Recipients[0].QueueID)
	})

	t.Run("failed_to_queued_retry_assigns_new_queue_id", func(t *testing.T) {
		member := newMember(t)
		key := FanoutKey{member.npub, "group", EnvelopeMessage, "logical"}
		insertFanout(t, member, key, "recipient", RecipientFailed, "event") // queue_id = "original-queue"
		changed, err := applyFanout(t, member, key, "recipient", RecipientFailed, RecipientQueued, map[string]any{"queue_id": "retry-queue"})
		require.NoError(t, err)
		require.True(t, changed)
		report, err := member.store.LoadFanoutReport(key)
		require.NoError(t, err)
		require.Equal(t, "retry-queue", report.Recipients[0].QueueID)
		require.Equal(t, RecipientQueued, report.Recipients[0].State)
	})

	t.Run("bookkeeping_never_regresses_across_a_state_change", func(t *testing.T) {
		member := newMember(t)
		key := FanoutKey{member.npub, "group", EnvelopeMessage, "logical"}
		insertFanout(t, member, key, "recipient", RecipientQueued, "event")
		_, err := member.db.Exec(`UPDATE groupchat_fanout SET attempts = 5, last_attempt_at = 500,
 relay_count = 7, max_retries = 20 WHERE recipient_npub = 'recipient'`)
		require.NoError(t, err)
		// A failure transition carrying smaller bookkeeping values (e.g. a
		// stale in-flight attempt) must not roll any of these back.
		changed, err := applyFanout(t, member, key, "recipient", RecipientQueued, RecipientFailed,
			map[string]any{"attempts": 1, "last_attempt_at": int64(1), "relay_count": 2,
				"issue": messaging.AgentMessageIssueSendFailed})
		require.NoError(t, err)
		require.True(t, changed)
		report, err := member.store.LoadFanoutReport(key)
		require.NoError(t, err)
		r := report.Recipients[0]
		require.Equal(t, 5, r.Attempts)
		require.Equal(t, int64(500), r.LastAttemptAt)
		require.Equal(t, 2, r.RelayCount)
		require.Equal(t, 20, r.MaxRetries)
	})

	t.Run("rejects_malformed_numeric_field_types", func(t *testing.T) {
		member := newMember(t)
		key := FanoutKey{member.npub, "group", EnvelopeMessage, "logical"}
		insertFanout(t, member, key, "recipient", RecipientQueued, "event")
		for _, column := range []string{"attempts", "last_attempt_at", "relay_count", "max_retries"} {
			changed, err := applyFanout(t, member, key, "recipient", RecipientQueued, RecipientFailed, map[string]any{column: "x"})
			require.Error(t, err)
			require.False(t, changed)
		}
		report, err := member.store.LoadFanoutReport(key)
		require.NoError(t, err)
		require.Equal(t, RecipientQueued, report.Recipients[0].State)
	})

	t.Run("failed_to_queued_clears_stale_issue", func(t *testing.T) {
		member := newMember(t)
		key := FanoutKey{member.npub, "group", EnvelopeMessage, "logical"}
		insertFanout(t, member, key, "recipient", RecipientFailed, "event")
		_, err := member.db.Exec(`UPDATE groupchat_fanout SET issue = ? WHERE recipient_npub = 'recipient'`,
			string(messaging.AgentMessageIssueRetryExhausted))
		require.NoError(t, err)
		changed, err := applyFanout(t, member, key, "recipient", RecipientFailed, RecipientQueued, map[string]any{"queue_id": "retry-queue"})
		require.NoError(t, err)
		require.True(t, changed)
		report, err := member.store.LoadFanoutReport(key)
		require.NoError(t, err)
		require.Equal(t, messaging.AgentMessageIssueNone, report.Recipients[0].Issue)
	})

	t.Run("relay_accepted_ignores_further_issue_changes", func(t *testing.T) {
		member := newMember(t)
		key := FanoutKey{member.npub, "group", EnvelopeMessage, "logical"}
		insertFanout(t, member, key, "recipient", RecipientQueued, "event")
		changed, err := applyFanout(t, member, key, "recipient", RecipientQueued, RecipientRelayAccepted,
			map[string]any{"relay_acks": 1, "accepted_at": int64(100)})
		require.NoError(t, err)
		require.True(t, changed)
		// A same-state call that tries to report a failure issue on an
		// already-accepted row must leave it untouched.
		changed, err = applyFanout(t, member, key, "recipient", RecipientRelayAccepted, RecipientRelayAccepted,
			map[string]any{"issue": messaging.AgentMessageIssueSendFailed})
		require.NoError(t, err)
		require.True(t, changed)
		report, err := member.store.LoadFanoutReport(key)
		require.NoError(t, err)
		require.Equal(t, messaging.AgentMessageIssueNone, report.Recipients[0].Issue)
		require.Equal(t, "original-queue", report.Recipients[0].QueueID)
	})

	t.Run("queue_id_rejected_outside_queued_target", func(t *testing.T) {
		member := newMember(t)
		key := FanoutKey{member.npub, "group", EnvelopeMessage, "logical"}
		insertFanout(t, member, key, "recipient", RecipientQueued, "event")
		// Supplying queue_id when the target state is not queued must be a
		// hard error, not a silent no-op -- otherwise a same-state call on
		// a different state could still be used to smuggle a queue_id write.
		changed, err := applyFanout(t, member, key, "recipient", RecipientQueued, RecipientRelayAccepted,
			map[string]any{"relay_acks": 1, "accepted_at": int64(100), "queue_id": "someone-elses-queue"})
		require.Error(t, err)
		require.False(t, changed)
		report, err := member.store.LoadFanoutReport(key)
		require.NoError(t, err)
		require.Equal(t, RecipientQueued, report.Recipients[0].State)
		require.Equal(t, "original-queue", report.Recipients[0].QueueID)
	})

	t.Run("relay_accepted_requires_a_persisted_queue_id", func(t *testing.T) {
		member := newMember(t)
		key := FanoutKey{member.npub, "group", EnvelopeMessage, "logical"}
		// prepared -> failed is a legitimate transition (e.g. preparation
		// itself failed) that never assigns a queue_id. The row must then
		// never be markable relay_accepted, even though the rank check alone
		// allows failed -> relay_accepted (a legitimate post-queued retry
		// path).
		insertFanout(t, member, key, "recipient", RecipientPrepared, "event")
		changed, err := applyFanout(t, member, key, "recipient", RecipientPrepared, RecipientFailed,
			map[string]any{"issue": messaging.AgentMessageIssueSendFailed})
		require.NoError(t, err)
		require.True(t, changed)
		before, err := member.store.LoadFanoutReport(key)
		require.NoError(t, err)
		require.Equal(t, "", before.Recipients[0].QueueID)
		changed, err = applyFanout(t, member, key, "recipient", RecipientFailed, RecipientRelayAccepted,
			map[string]any{"relay_acks": 1, "accepted_at": int64(100)})
		require.NoError(t, err)
		require.False(t, changed, "a never-queued row must not be markable relay_accepted")
		after, err := member.store.LoadFanoutReport(key)
		require.NoError(t, err)
		require.Equal(t, before, after)
	})

	t.Run("same_state_prepared_cannot_forge_a_queue_id", func(t *testing.T) {
		member := newMember(t)
		key := FanoutKey{member.npub, "group", EnvelopeMessage, "logical"}
		insertFanout(t, member, key, "recipient", RecipientPrepared, "event")
		// A same-state prepared->prepared call must not be usable to plant a
		// queue_id while the row never actually reaches queued.
		changed, err := applyFanout(t, member, key, "recipient", RecipientPrepared, RecipientPrepared,
			map[string]any{"queue_id": "forged-queue"})
		require.Error(t, err)
		require.False(t, changed)
		report, err := member.store.LoadFanoutReport(key)
		require.NoError(t, err)
		require.Equal(t, "", report.Recipients[0].QueueID)
	})

	t.Run("transition_canonicalizes_local_npub_like_load_does", func(t *testing.T) {
		member := newMember(t)
		key := FanoutKey{member.npub, "group", EnvelopeMessage, "logical"}
		insertFanout(t, member, key, "recipient", RecipientQueued, "event")
		hexKey := key
		hexKey.LocalNpub = common.PubKeyToHex(member.sk.Public())
		require.NotEqual(t, key.LocalNpub, hexKey.LocalNpub)
		// The hex and npub1 forms of the same identity must resolve to the
		// same row; otherwise a caller using one form over the other would
		// see every transition silently rejected as stale.
		changed, err := applyFanout(t, member, hexKey, "recipient", RecipientQueued, RecipientFailed,
			map[string]any{"issue": messaging.AgentMessageIssueSendFailed})
		require.NoError(t, err)
		require.True(t, changed)
		report, err := member.store.LoadFanoutReport(key)
		require.NoError(t, err)
		require.Equal(t, RecipientFailed, report.Recipients[0].State)
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
		AttemptPhase: "accepted", AccountingOrigin: "native", LastAttemptAt: 200, AcceptedAt: 201}, report.Recipients[0])
	encoded, err := json.Marshal(report)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "relays")
	require.NotContains(t, string(encoded), "ciphertext")
	require.NotContains(t, string(encoded), "event_json")
	require.Contains(t, string(encoded), `"issue":"queue_missing"`)
	require.Contains(t, string(encoded), `"relay_acks":1,"relay_count":5`)
	changed, err := applyFanout(t, member, key, "z", RecipientFailed, RecipientQueued, map[string]any{"queue_id": "retry-queue-z"})
	require.NoError(t, err)
	require.True(t, changed)
	report, err = member.store.LoadFanoutReport(key)
	require.NoError(t, err)
	require.Equal(t, messaging.AgentMessageQueued, report.State)
	require.Equal(t, 3, report.Queued)
	require.Zero(t, report.Failed)
	require.Equal(t, messaging.AgentMessageIssueNone, report.Recipients[3].Issue, "retry must clear z's stale queue_missing issue")
	for recipient, from := range map[string]RecipientDeliveryState{"b": RecipientQueued, "c": RecipientPrepared, "z": RecipientQueued} {
		if from == RecipientPrepared {
			changed, err = applyFanout(t, member, key, recipient, from, RecipientQueued, map[string]any{"queue_id": "assigned-queue-" + recipient})
			require.NoError(t, err)
			require.True(t, changed)
		}
		changed, err = applyFanout(t, member, key, recipient, RecipientQueued, RecipientRelayAccepted,
			map[string]any{"relay_acks": 1, "accepted_at": int64(999)})
		require.NoError(t, err)
		require.True(t, changed)
	}
	report, err = member.store.LoadFanoutReport(key)
	require.NoError(t, err)
	require.Equal(t, messaging.AgentMessageRelayAccepted, report.State)
	require.Equal(t, 4, report.Accepted)
	require.Zero(t, report.Queued)
	require.Zero(t, report.Failed)
	_, err = member.db.Exec(`UPDATE groupchat_fanout SET created_at=created_at+1 WHERE recipient_npub='b'`)
	require.NoError(t, err)
	_, err = member.store.LoadFanoutReport(key)
	require.ErrorContains(t, err, "inconsistent created_at")
	t.Run("database_error", func(t *testing.T) {
		closed := newMember(t)
		require.NoError(t, closed.db.Close())
		_, err := closed.store.LoadFanoutReport(key)
		require.Error(t, err)
	})
}

func TestFanoutMigrationAccounting(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+t.TempDir()+"/legacy.db")
	require.NoError(t, err)
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE groupchat_fanout (
 local_npub TEXT NOT NULL, group_id TEXT NOT NULL, envelope_type TEXT NOT NULL, send_key TEXT NOT NULL,
 recipient_npub TEXT NOT NULL, event_id TEXT NOT NULL, event_json TEXT NOT NULL, queue_id TEXT NOT NULL DEFAULT '',
 state TEXT NOT NULL, issue TEXT NOT NULL DEFAULT '', relay_acks INTEGER NOT NULL DEFAULT 0, relay_count INTEGER NOT NULL DEFAULT 0,
 attempts INTEGER NOT NULL DEFAULT 0, max_retries INTEGER NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL,
 last_attempt_at INTEGER NOT NULL DEFAULT 0, accepted_at INTEGER NOT NULL DEFAULT 0,
 PRIMARY KEY(local_npub,group_id,envelope_type,send_key,recipient_npub), UNIQUE(local_npub,event_id))`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO groupchat_fanout(local_npub,group_id,envelope_type,send_key,recipient_npub,event_id,event_json,queue_id,state,attempts,max_retries,created_at,updated_at)
 VALUES('legacy','g','message','m','r','e','{}','old-q','queued',7,4,1,1)`)
	require.NoError(t, err)
	_, err = NewStore(db)
	require.NoError(t, err)
	var attempts, base, generation, results int64
	var retry sql.NullInt64
	var origin, phase, queueID, receipt string
	require.NoError(t, db.QueryRow(`SELECT attempts,legacy_attempts_base,attempt_generation,last_result_generation,retry_count,accounting_origin,attempt_phase,retry_queue_id,last_failure_receipt FROM groupchat_fanout`).Scan(&attempts, &base, &generation, &results, &retry, &origin, &phase, &queueID, &receipt))
	require.EqualValues(t, 7, attempts)
	require.EqualValues(t, 7, base)
	require.EqualValues(t, 7, generation)
	require.Zero(t, results)
	require.False(t, retry.Valid, "legacy failures cannot be inferred from publish attempts")
	require.Equal(t, "legacy", origin)
	require.Equal(t, "idle", phase)
	require.Empty(t, queueID)
	require.Empty(t, receipt, "migration must not fabricate historical attempt tokens")
	_, err = NewStore(db)
	require.NoError(t, err, "migration is idempotent")
}

func TestFanoutAccountingCAS(t *testing.T) {
	member := newMember(t)
	key := FanoutKey{member.npub, "g", EnvelopeMessage, "m"}
	insertFanout(t, member, key, "r", RecipientQueued, "e")
	changed, err := applyFanout(t, member, key, "r", RecipientQueued, RecipientQueued, map[string]any{"relay_count": 3})
	require.NoError(t, err)
	require.True(t, changed)
	changed, err = applyFanout(t, member, key, "r", RecipientQueued, RecipientQueued, map[string]any{"relay_count": 1})
	require.NoError(t, err)
	require.True(t, changed)
	report, err := member.store.LoadFanoutReport(key)
	require.NoError(t, err)
	require.Equal(t, 1, report.Recipients[0].RelayCount)
	var revision int64
	require.NoError(t, member.db.QueryRow(`SELECT row_revision FROM groupchat_fanout`).Scan(&revision))
	require.EqualValues(t, 2, revision)
	for _, fields := range []map[string]any{{"attempts": -1}, {"relay_count": float64(math.MaxInt64)}, {"event_json": "tampered"}} {
		changed, err = applyFanout(t, member, key, "r", RecipientQueued, RecipientQueued, fields)
		require.Error(t, err)
		require.False(t, changed)
	}
	changed, err = applyFanout(t, member, key, "r", RecipientQueued, RecipientRelayAccepted, map[string]any{"relay_acks": 1, "accepted_at": int64(10)})
	require.NoError(t, err)
	require.True(t, changed)
	changed, err = applyFanout(t, member, key, "r", RecipientRelayAccepted, RecipientRelayAccepted, map[string]any{"relay_count": 4})
	require.Error(t, err)
	require.False(t, changed)
	var phase string
	require.NoError(t, member.db.QueryRow(`SELECT attempt_phase FROM groupchat_fanout`).Scan(&phase))
	require.Equal(t, "accepted", phase)
	_, err = member.db.Exec(`UPDATE groupchat_fanout SET row_revision=?`, int64(math.MaxInt64))
	require.NoError(t, err)
	changed, err = applyFanout(t, member, key, "r", RecipientRelayAccepted, RecipientRelayAccepted, nil)
	require.Error(t, err, "revision overflow fails closed")
	require.False(t, changed)
}

func TestFanoutAccountingReservationAndStart(t *testing.T) {
	member := newMember(t)
	key := FanoutKey{member.npub, "g", EnvelopeMessage, "m"}
	insertFanout(t, member, key, "r", RecipientQueued, "e")
	_, err := member.db.Exec(`UPDATE groupchat_fanout SET attempts=4,last_attempt_at=80,relay_count=3,issue='send_failed'`)
	require.NoError(t, err)
	tx, err := member.store.beginImmediate()
	require.NoError(t, err)
	token, err := reserveFanoutAttemptTx(tx, key, "r")
	require.NoError(t, err)
	require.EqualValues(t, 1, token.generation)
	require.NoError(t, tx.Commit())
	report, err := member.store.LoadFanoutReport(key)
	require.NoError(t, err)
	r := report.Recipients[0]
	require.Equal(t, 4, r.Attempts)
	require.EqualValues(t, 80, r.LastAttemptAt)
	require.Equal(t, 3, r.RelayCount)
	require.Equal(t, "send_failed", string(r.Issue))
	require.Nil(t, r.RetryCount)
	require.Equal(t, "reserved", r.AttemptPhase)
	tx, err = member.store.beginImmediate()
	require.NoError(t, err)
	require.NoError(t, startFanoutAttemptTx(tx, token, 1, 90))
	require.NoError(t, tx.Commit())
	report, err = member.store.LoadFanoutReport(key)
	require.NoError(t, err)
	r = report.Recipients[0]
	require.Equal(t, 5, r.Attempts)
	require.EqualValues(t, 90, r.LastAttemptAt)
	require.Equal(t, 1, r.RelayCount)
	require.Equal(t, messaging.AgentMessageIssueNone, r.Issue)
	require.Nil(t, r.RetryCount, "publish start does not consume the queue failure budget")
	require.Equal(t, "started", r.AttemptPhase)
	tx, err = member.store.beginImmediate()
	require.NoError(t, err)
	require.Error(t, startFanoutAttemptTx(tx, token, 1, 91), "the same reservation cannot start twice")
	require.NoError(t, tx.Rollback())
}

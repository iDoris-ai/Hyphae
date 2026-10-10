package groupchat

import (
	"encoding/json"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"github.com/iDoris-ai/hyphae/internal/messaging"
	"github.com/iDoris-ai/hyphae/pkg/types"
	"github.com/stretchr/testify/require"
)

func signedGroupEvent(t *testing.T) (string, string) {
	t.Helper()
	event := nostr.Event{Kind: 30078, CreatedAt: nostr.Now(), Content: "ciphertext"}
	require.NoError(t, event.Sign(nostr.Generate()))
	data, err := json.Marshal(event)
	require.NoError(t, err)
	return event.ID.Hex(), string(data)
}

func insertTestFanout(t *testing.T, member testMember, key FanoutKey, recipient string, state RecipientDeliveryState, eventID, eventJSON, queueID string, attempts int) {
	t.Helper()
	now := time.Now().Unix()
	acceptedAt := int64(0)
	relayAcks := 0
	if state == RecipientRelayAccepted {
		acceptedAt = now
		relayAcks = 1
	}
	_, err := member.db.Exec(`INSERT INTO groupchat_fanout
		(local_npub, group_id, envelope_type, send_key, recipient_npub, event_id, event_json,
		queue_id, state, issue, relay_acks, relay_count, attempts, max_retries, created_at, updated_at, accepted_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 10, ?, ?, ?)`,
		key.LocalNpub, key.GroupID, key.EnvelopeType, key.SendKey, recipient, eventID, eventJSON,
		queueID, state, "", relayAcks, 2, attempts, now, now, acceptedAt)
	require.NoError(t, err)
}

func TestEnqueuePrepared(t *testing.T) {
	t.Run("success_prepared_to_queued", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		member := newMember(t)
		key := FanoutKey{LocalNpub: member.npub, GroupID: "group1", EnvelopeType: EnvelopeMessage, SendKey: "logical1"}
		eID1, eJSON1 := signedGroupEvent(t)
		eID2, eJSON2 := signedGroupEvent(t)

		insertTestFanout(t, member, key, "recipient1", RecipientPrepared, eID1, eJSON1, "", 0)
		insertTestFanout(t, member, key, "recipient2", RecipientPrepared, eID2, eJSON2, "", 0)

		report, err := member.store.EnqueuePrepared(key)
		require.NoError(t, err)
		require.Equal(t, 2, report.Queued)
		require.Equal(t, 0, report.Failed)
		require.Equal(t, messaging.AgentMessageQueued, report.State)
		require.Len(t, report.Recipients, 2)
		require.NotEmpty(t, report.Recipients[0].QueueID)
		require.NotEmpty(t, report.Recipients[1].QueueID)
		require.Equal(t, RecipientQueued, report.Recipients[0].State)
		require.Equal(t, RecipientQueued, report.Recipients[1].State)

		ob, err := messaging.LoadOutbox()
		require.NoError(t, err)
		pending := messaging.GetPendingGroupOutbox(ob)
		require.Len(t, pending, 2)
	})

	t.Run("crash_idempotence_O1_to_T2", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		member := newMember(t)
		key := FanoutKey{LocalNpub: member.npub, GroupID: "group1", EnvelopeType: EnvelopeMessage, SendKey: "logical2"}
		eID, eJSON := signedGroupEvent(t)
		insertTestFanout(t, member, key, "recipient1", RecipientPrepared, eID, eJSON, "", 0)

		// Simulate crash right after O1: entry is in outbox, but SQLite is still prepared
		outboxEntry, existed, err := messaging.EnqueueGroupOutboxEntry(eJSON, "recipient1", nil, 10)
		require.NoError(t, err)
		require.False(t, existed)
		require.NotEmpty(t, outboxEntry.QueueID)

		// Resume: EnqueuePrepared should adopt the existing QueueID and move row to queued
		report, err := member.store.EnqueuePrepared(key)
		require.NoError(t, err)
		require.Equal(t, RecipientQueued, report.Recipients[0].State)
		require.Equal(t, outboxEntry.QueueID, report.Recipients[0].QueueID)

		// Check outbox has not duplicated
		ob, err := messaging.LoadOutbox()
		require.NoError(t, err)
		matches := messaging.GroupOutboxEntriesByEventID(ob, eID)
		require.Len(t, matches, 1)

		// Re-invoking is a no-op idempotency check
		report2, err := member.store.EnqueuePrepared(key)
		require.NoError(t, err)
		require.Equal(t, report, report2)
	})

	t.Run("message_only_extension_guard", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		member := newMember(t)
		key := FanoutKey{LocalNpub: member.npub, GroupID: "group1", EnvelopeType: EnvelopeType("activate"), SendKey: "invite1"}
		eID, eJSON := signedGroupEvent(t)
		insertTestFanout(t, member, key, "recipient1", RecipientPrepared, eID, eJSON, "", 0)

		_, err := member.store.EnqueuePrepared(key)
		require.Error(t, err)
		require.Contains(t, err.Error(), "unsupported envelope type")

		// Verify row remains prepared because tx was rolled back
		report, err := member.store.LoadFanoutReport(key)
		require.NoError(t, err)
		require.Equal(t, RecipientPrepared, report.Recipients[0].State)
	})
}

func TestFanoutOutboxHandler(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	member := newMember(t)
	handler := NewFanoutOutboxHandler(member.store)
	key := FanoutKey{LocalNpub: member.npub, GroupID: "group1", EnvelopeType: EnvelopeMessage, SendKey: "logical3"}
	eID, eJSON := signedGroupEvent(t)

	t.Run("BeforePublish", func(t *testing.T) {
		insertTestFanout(t, member, key, "recipient1", RecipientQueued, eID, eJSON, "q1", 0)
		entry := types.OutboxEntry{ID: eID, QueueID: "q1", RecipientNpub: "recipient1"}

		skip, err := handler.BeforePublish(entry)
		require.NoError(t, err)
		require.False(t, skip)

		// Non-existent event returns error
		_, err = handler.BeforePublish(types.OutboxEntry{ID: "nonexistent"})
		require.Error(t, err)
	})

	t.Run("MarkRelayAccepted_guards_and_success", func(t *testing.T) {
		entry := types.OutboxEntry{ID: eID, QueueID: "q1", RecipientNpub: "recipient1"}
		const futureAttempt = int64(2000000000)
		_, err := member.db.Exec(`UPDATE groupchat_fanout SET last_attempt_at=? WHERE event_id=?`, futureAttempt, eID)
		require.NoError(t, err)

		// Invalid relay acks count (must be 1)
		require.Error(t, handler.MarkRelayAccepted(entry, 0, 2))

		// Mismatched queue id
		require.Error(t, handler.MarkRelayAccepted(types.OutboxEntry{ID: eID, QueueID: "wrong_q"}, 1, 2))

		// Mismatched recipient
		require.Error(t, handler.MarkRelayAccepted(types.OutboxEntry{ID: eID, QueueID: "q1", RecipientNpub: "other"}, 1, 2))

		// Valid mark relay accepted
		require.NoError(t, handler.MarkRelayAccepted(entry, 1, 2))

		report, err := member.store.LoadFanoutReport(key)
		require.NoError(t, err)
		require.Equal(t, RecipientRelayAccepted, report.Recipients[0].State)
		require.Equal(t, 1, report.Recipients[0].RelayAcks)
		require.Equal(t, 2, report.Recipients[0].RelayCount)
		require.Greater(t, report.Recipients[0].AcceptedAt, int64(0))
		require.Equal(t, futureAttempt, report.Recipients[0].AcceptedAt, "acceptance cannot precede publish start")

		// BeforePublish returns skip=true after relay accepted
		skip, err := handler.BeforePublish(entry)
		require.NoError(t, err)
		require.True(t, skip)

		// A replayed ACK is a true no-op, including timestamps and row revision.
		var revisionBefore, updatedBefore, acceptedBefore int64
		require.NoError(t, member.db.QueryRow(`SELECT row_revision,updated_at,accepted_at FROM groupchat_fanout WHERE event_id=?`, eID).Scan(&revisionBefore, &updatedBefore, &acceptedBefore))
		require.NoError(t, handler.MarkRelayAccepted(entry, 1, 2))
		var revisionAfter, updatedAfter, acceptedAfter int64
		require.NoError(t, member.db.QueryRow(`SELECT row_revision,updated_at,accepted_at FROM groupchat_fanout WHERE event_id=?`, eID).Scan(&revisionAfter, &updatedAfter, &acceptedAfter))
		require.Equal(t, revisionBefore, revisionAfter)
		require.Equal(t, updatedBefore, updatedAfter)
		require.Equal(t, acceptedBefore, acceptedAfter)
	})

	t.Run("MarkRelayAccepted_prepared_rejected", func(t *testing.T) {
		keyPrep := FanoutKey{LocalNpub: member.npub, GroupID: "group1", EnvelopeType: EnvelopeMessage, SendKey: "logical_prep"}
		eIDPrep, eJSONPrep := signedGroupEvent(t)
		insertTestFanout(t, member, keyPrep, "recipient1", RecipientPrepared, eIDPrep, eJSONPrep, "", 0)

		entry := types.OutboxEntry{ID: eIDPrep, QueueID: "", RecipientNpub: "recipient1"}
		require.ErrorIs(t, handler.MarkRelayAccepted(entry, 1, 2), ErrInvalidTransition)
	})

	t.Run("RecordAttemptFailure_and_terminal_guard", func(t *testing.T) {
		keyFail := FanoutKey{LocalNpub: member.npub, GroupID: "group1", EnvelopeType: EnvelopeMessage, SendKey: "logical_fail"}
		eIDFail, eJSONFail := signedGroupEvent(t)
		insertTestFanout(t, member, keyFail, "recipient1", RecipientQueued, eIDFail, eJSONFail, "q_fail", 0)

		entry := types.OutboxEntry{ID: eIDFail, QueueID: "q_fail", RecipientNpub: "recipient1"}

		// Non-exhausted attempt failure: attempts incremented, state remains queued
		require.NoError(t, handler.RecordAttemptFailure(entry, false, messaging.AgentMessageIssueSendFailed))
		report, err := member.store.LoadFanoutReport(keyFail)
		require.NoError(t, err)
		require.Equal(t, RecipientQueued, report.Recipients[0].State)
		require.Equal(t, 1, report.Recipients[0].Attempts)
		require.Equal(t, messaging.AgentMessageIssueSendFailed, report.Recipients[0].Issue)

		// Exhausted attempt failure: state becomes failed
		require.NoError(t, handler.RecordAttemptFailure(entry, true, messaging.AgentMessageIssueRetryExhausted))
		report, err = member.store.LoadFanoutReport(keyFail)
		require.NoError(t, err)
		require.Equal(t, RecipientFailed, report.Recipients[0].State)
		require.Equal(t, 2, report.Recipients[0].Attempts)
		require.Equal(t, messaging.AgentMessageIssueRetryExhausted, report.Recipients[0].Issue)

		// Terminal accepted guard: terminal row ignores failure attempts
		entryAccepted := types.OutboxEntry{ID: eID, QueueID: "q1", RecipientNpub: "recipient1"}
		require.NoError(t, handler.RecordAttemptFailure(entryAccepted, true, messaging.AgentMessageIssueSendFailed))
		repAccepted, err := member.store.LoadFanoutReport(key)
		require.NoError(t, err)
		require.Equal(t, RecipientRelayAccepted, repAccepted.Recipients[0].State)
	})
}

func TestRequeueFailed(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	member := newMember(t)
	key := FanoutKey{LocalNpub: member.npub, GroupID: "group1", EnvelopeType: EnvelopeMessage, SendKey: "logical_requeue"}
	eID1, eJSON1 := signedGroupEvent(t)
	eID2, eJSON2 := signedGroupEvent(t)

	// Bob is failed with prior outbox entry
	insertTestFanout(t, member, key, "bob", RecipientFailed, eID1, eJSON1, "old_q_bob", 2)
	// Carol is relay_accepted
	insertTestFanout(t, member, key, "carol", RecipientRelayAccepted, eID2, eJSON2, "q_carol", 1)

	// Seed outbox with group_failed for Bob
	_, _, err := messaging.EnqueueGroupOutboxEntry(eJSON1, "bob", nil, 1)
	require.NoError(t, err)
	ob, err := messaging.LoadOutbox()
	require.NoError(t, err)
	require.NoError(t, messaging.UpdateOutboxStatus(ob, eID1, messaging.OutboxStatusGroupFailed))

	t.Run("guard_relay_accepted_cannot_requeue", func(t *testing.T) {
		_, err := member.store.RequeueFailed(key, "carol")
		require.Error(t, err)
		require.Contains(t, err.Error(), "only failed recipients can be requeued")
	})

	t.Run("requeue_failed_preserves_event_with_new_queue_id", func(t *testing.T) {
		report, err := member.store.RequeueFailed(key, "bob")
		require.NoError(t, err)
		require.Equal(t, 1, report.Queued)
		require.Equal(t, 1, report.Accepted)
		require.Equal(t, 0, report.Failed)

		bob := report.Recipients[0]
		require.Equal(t, "bob", bob.RecipientNpub)
		require.Equal(t, RecipientQueued, bob.State)
		require.Equal(t, eID1, bob.EventID)
		require.NotEmpty(t, bob.QueueID)
		require.NotEqual(t, "old_q_bob", bob.QueueID)
		require.Equal(t, messaging.AgentMessageIssueNone, bob.Issue)

		// Outbox contains new queue_id and group_pending
		latestOB, err := messaging.LoadOutbox()
		require.NoError(t, err)
		entries := messaging.GroupOutboxEntriesByEventID(latestOB, eID1)
		require.Len(t, entries, 1)
		require.Equal(t, bob.QueueID, entries[0].QueueID)
		require.Equal(t, messaging.OutboxStatusGroupPending, entries[0].Status)
		require.Equal(t, eJSON1, entries[0].EventJSON)
	})

	t.Run("no_failed_recipients_error", func(t *testing.T) {
		// All rows are now queued or accepted
		_, err := member.store.RequeueFailed(key, "")
		require.Error(t, err)
		require.Contains(t, err.Error(), "no failed recipients to requeue")
	})
}

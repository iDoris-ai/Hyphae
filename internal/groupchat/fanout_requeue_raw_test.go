package groupchat

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"github.com/iDoris-ai/hyphae/internal/messaging"
	"github.com/stretchr/testify/require"
)

func enqueueRawPair(t *testing.T, maxRetries int) (*threeMemberGroup, FanoutKey, FanoutReport) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	g := newThreeMemberGroup(t)
	g.activate()
	logicalID := mustOpaque(t)
	report, err := g.alice.store.EnqueueMessageFanout(g.alice.sk, g.draft.Group.ID, logicalID, "n1 body", 1234, maxRetries)
	require.NoError(t, err)
	require.Len(t, report.Recipients, 2)
	key := FanoutKey{LocalNpub: g.alice.npub, GroupID: g.draft.Group.ID, EnvelopeType: EnvelopeMessage, SendKey: logicalID}
	entries := rawOutboxEntries(t)
	require.Len(t, entries, 2)
	for _, entry := range entries {
		require.NotNil(t, entry.FanoutProtocol)
		require.EqualValues(t, 1, *entry.FanoutProtocol)
	}
	return g, key, report
}

func exhaustRecipient(t *testing.T, member testMember, key FanoutKey, recipient string, times int) {
	t.Helper()
	for range times {
		var eventJSON string
		require.NoError(t, member.db.QueryRow(`SELECT event_json FROM groupchat_fanout
			WHERE local_npub=? AND group_id=? AND envelope_type=? AND send_key=? AND recipient_npub=?`,
			key.LocalNpub, key.GroupID, key.EnvelopeType, key.SendKey, recipient).Scan(&eventJSON))
		token, err := member.store.reserveFanoutAttemptFrozen(key, recipient, eventJSON)
		require.NoError(t, err)
		require.NoError(t, member.store.startFanoutAttempt(token, 1, time.Now().Unix()))
		require.NoError(t, member.store.commitFanoutFailure(token))
	}
	var state string
	require.NoError(t, member.db.QueryRow(`SELECT state FROM groupchat_fanout
		WHERE local_npub=? AND group_id=? AND envelope_type=? AND send_key=? AND recipient_npub=?`,
		key.LocalNpub, key.GroupID, key.EnvelopeType, key.SendKey, recipient).Scan(&state))
	require.Equal(t, string(RecipientFailed), state)
}

func recipientByNpub(t *testing.T, report FanoutReport, npub string) RecipientDelivery {
	t.Helper()
	for _, recipient := range report.Recipients {
		if recipient.RecipientNpub == npub {
			return recipient
		}
	}
	t.Fatalf("recipient %s missing from report", npub)
	return RecipientDelivery{}
}

func rawOutboxEntries(t *testing.T) []*messaging.RawGroupEntry {
	t.Helper()
	path, err := messaging.GetOutboxPath()
	require.NoError(t, err)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var doc struct {
		Entries []json.RawMessage `json:"entries"`
	}
	require.NoError(t, json.Unmarshal(data, &doc))
	out := make([]*messaging.RawGroupEntry, 0, len(doc.Entries))
	for _, raw := range doc.Entries {
		entry, err := messaging.ParseRawGroupEntry(raw)
		require.NoError(t, err)
		out = append(out, entry)
	}
	return out
}

func entryByEvent(t *testing.T, eventID string) *messaging.RawGroupEntry {
	t.Helper()
	for _, entry := range rawOutboxEntries(t) {
		if entry.Entry.ID == eventID {
			return entry
		}
	}
	t.Fatalf("outbox entry %s missing", eventID)
	return nil
}

type requeueBudget struct {
	Attempts, Generation, RetryCount int64
	RecoveryID, QueueID, EventJSON   string
}

func readBudget(t *testing.T, member testMember, eventID string) requeueBudget {
	t.Helper()
	var b requeueBudget
	require.NoError(t, member.db.QueryRow(`SELECT attempts, attempt_generation, retry_count, last_recovery_id, queue_id, event_json
		FROM groupchat_fanout WHERE event_id=?`, eventID).Scan(&b.Attempts, &b.Generation, &b.RetryCount, &b.RecoveryID, &b.QueueID, &b.EventJSON))
	return b
}

func TestN1RawEnqueueExhaustRequeue(t *testing.T) {
	g, key, seeded := enqueueRawPair(t, 3)
	frozen := map[string]string{}
	for _, recipient := range seeded.Recipients {
		var eventJSON string
		require.NoError(t, g.alice.db.QueryRow(`SELECT event_json FROM groupchat_fanout WHERE event_id=?`, recipient.EventID).Scan(&eventJSON))
		frozen[recipient.RecipientNpub] = eventJSON
		exhaustRecipient(t, g.alice, key, recipient.RecipientNpub, 3)
	}
	before := map[string]requeueBudget{}
	for _, recipient := range seeded.Recipients {
		before[recipient.RecipientNpub] = readBudget(t, g.alice, recipient.EventID)
		require.Greater(t, before[recipient.RecipientNpub].Attempts, int64(0))
	}

	report, err := g.alice.store.RequeueFailed(key, "")
	require.NoError(t, err)
	require.Equal(t, 2, report.Queued)
	require.Equal(t, 0, report.Failed)
	for _, recipient := range report.Recipients {
		require.Equal(t, RecipientQueued, recipient.State)
		require.Equal(t, messaging.AgentMessageIssueNone, recipient.Issue)
		require.NotEqual(t, before[recipient.RecipientNpub].QueueID, recipient.QueueID)
		entry := entryByEvent(t, recipient.EventID)
		require.Equal(t, recipient.QueueID, entry.QueueID)
		require.Equal(t, messaging.OutboxStatusGroupPending, entry.Entry.Status)
		require.Equal(t, 0, entry.Entry.RetryCount)
		require.NotNil(t, entry.FanoutProtocol)
		require.EqualValues(t, 1, *entry.FanoutProtocol)
		require.NotNil(t, entry.RecoveryWitness)
		require.Equal(t, "exhausted_failed", entry.RecoveryWitness.Reason)
		require.Equal(t, frozen[recipient.RecipientNpub], entry.Entry.EventJSON)
		var event nostr.Event
		require.NoError(t, json.Unmarshal([]byte(entry.Entry.EventJSON), &event))
		require.Equal(t, recipient.EventID, event.ID.Hex())
		require.True(t, event.VerifySignature())
		sk := g.bob.sk
		if recipient.RecipientNpub == g.carol.npub {
			sk = g.carol.sk
		}
		verified, err := VerifyIncoming(event, sk)
		require.NoError(t, err)
		decoded, err := envelopeFrom(verified, EnvelopeMessage)
		require.NoError(t, err)
		require.Equal(t, "n1 body", decoded.Body)
		after := readBudget(t, g.alice, recipient.EventID)
		require.Equal(t, before[recipient.RecipientNpub].Attempts, after.Attempts)
		require.Equal(t, before[recipient.RecipientNpub].Generation, after.Generation)
		require.EqualValues(t, 0, after.RetryCount)
		require.NotEmpty(t, after.RecoveryID)
	}

	t.Run("second_call_keeps_budget", func(t *testing.T) {
		snap := map[string]requeueBudget{}
		for _, recipient := range report.Recipients {
			snap[recipient.EventID] = readBudget(t, g.alice, recipient.EventID)
		}
		_, err := g.alice.store.RequeueFailed(key, "")
		require.Error(t, err)
		require.Contains(t, err.Error(), "no failed recipients to requeue")
		for eventID, want := range snap {
			require.Equal(t, want, readBudget(t, g.alice, eventID))
		}
	})
}

func TestN1RawRequeueKeepsEarlierCommit(t *testing.T) {
	g, key, seeded := enqueueRawPair(t, 3)
	for _, recipient := range seeded.Recipients {
		exhaustRecipient(t, g.alice, key, recipient.RecipientNpub, 3)
	}
	ordered := append([]RecipientDelivery(nil), seeded.Recipients...)
	if ordered[0].RecipientNpub > ordered[1].RecipientNpub {
		ordered[0], ordered[1] = ordered[1], ordered[0]
	}
	first, second := ordered[0], ordered[1]
	_, err := g.alice.db.Exec(`UPDATE groupchat_fanout SET queue_id=? WHERE event_id=?`, "ffffffffffffffffffffffffffffffff", second.EventID)
	require.NoError(t, err)

	_, err = g.alice.store.RequeueFailed(key, "")
	require.ErrorIs(t, err, ErrFanoutQueueConflict)

	got, err := g.alice.store.LoadFanoutReport(key)
	require.NoError(t, err)
	kept := recipientByNpub(t, got, first.RecipientNpub)
	blocked := recipientByNpub(t, got, second.RecipientNpub)
	require.Equal(t, RecipientQueued, kept.State)
	require.NotEqual(t, first.QueueID, kept.QueueID)
	require.Equal(t, first.EventID, kept.EventID)
	require.Equal(t, RecipientFailed, blocked.State)
	require.Equal(t, "ffffffffffffffffffffffffffffffff", blocked.QueueID)
	entry := entryByEvent(t, first.EventID)
	require.Equal(t, kept.QueueID, entry.QueueID)
	require.NotNil(t, entry.RecoveryWitness)
}

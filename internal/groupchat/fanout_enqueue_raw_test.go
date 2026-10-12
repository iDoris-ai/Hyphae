package groupchat

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/internal/messaging"
	"github.com/stretchr/testify/require"
)

// newPreparedFixture mirrors T1: prepared rows with frozen real agent events
// (retry_count 0, no queue_id) and no outbox file at all.
func newPreparedFixture(t *testing.T, recipients int) (failureFixture, []string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	member := newMemberWithSecret(t, fixedQueueTestSecret(21))
	key := FanoutKey{LocalNpub: member.npub, GroupID: mustOpaque(t), EnvelopeType: EnvelopeMessage, SendKey: mustOpaque(t)}
	now := time.Now().Unix()
	var npubs []string
	var first failureFixture
	var firstEventJSON string
	for i := 0; i < recipients; i++ {
		recipientSK := fixedQueueTestSecret(byte(22 + i))
		recipient := common.EncodeNpub(recipientSK.Public())
		logicalID := key.SendKey
		payload, err := Encode(Envelope{Type: EnvelopeMessage, Version: Version, GroupID: key.GroupID,
			LogicalID: logicalID, Body: "prepared hello"})
		require.NoError(t, err)
		event := testAgentEvent(t, payload, member.sk, recipientSK, true)
		require.True(t, event.CheckID())
		require.True(t, event.VerifySignature())
		verified, err := VerifyIncoming(event, recipientSK)
		require.NoError(t, err)
		require.Equal(t, member.npub, verified.SenderNpub())
		require.Equal(t, recipient, verified.RecipientNpub())
		decoded, err := envelopeFrom(verified, EnvelopeMessage)
		require.NoError(t, err)
		require.Equal(t, key.GroupID, decoded.GroupID)
		require.Equal(t, logicalID, decoded.LogicalID)
		require.Equal(t, "prepared hello", decoded.Body)
		eventJSON, err := json.Marshal(event)
		require.NoError(t, err)
		if i == 0 {
			firstEventJSON = string(eventJSON)
		} else {
			requireDistinctEncryptedEvents(t, firstEventJSON, string(eventJSON))
		}
		_, err = member.db.Exec(`INSERT INTO groupchat_fanout
            (local_npub,group_id,envelope_type,send_key,recipient_npub,event_id,event_json,queue_id,state,max_retries,retry_count,created_at,updated_at)
            VALUES (?,?,?,?,?,?,?,'',?,3,0,?,?)`,
			key.LocalNpub, key.GroupID, key.EnvelopeType, key.SendKey, recipient, event.ID.Hex(), string(eventJSON), RecipientPrepared, now, now)
		require.NoError(t, err)
		npubs = append(npubs, recipient)
		if i == 0 {
			path, err := messaging.GetOutboxPath()
			require.NoError(t, err)
			first = failureFixture{member: member, key: key, recipient: recipient, eventID: event.ID.Hex(), outboxPath: path}
		}
	}
	if recipients > 1 {
		require.NotEqual(t, npubs[0], npubs[1], "fanout recipients are distinct")
	}
	return first, npubs
}

func TestEnqueuePreparedRawO1ThenT2(t *testing.T) {
	f, npubs := newPreparedFixture(t, 2)
	_, err := os.Stat(f.outboxPath)
	require.True(t, os.IsNotExist(err), "no outbox before O1")

	report, err := f.member.store.enqueuePreparedRaw(f.key, 1_800_000_000)
	require.NoError(t, err)
	require.Equal(t, f.key.SendKey, report.LogicalID)
	require.Equal(t, 2, report.Queued)
	require.Len(t, report.Recipients, 2)
	data, err := os.ReadFile(f.outboxPath)
	require.NoError(t, err)
	evidence, err := messaging.ParseRawOutboxEvidence(data)
	require.Error(t, err, "F2a's legacy-only reader must see the protocol tag")
	for _, r := range report.Recipients {
		require.Equal(t, RecipientQueued, r.State)
		require.Len(t, r.QueueID, 32)
		entry, _, err := messaging.ReadGroupEntryLocked(f.outboxPath, r.EventID)
		require.NoError(t, err)
		require.NotNil(t, entry)
		require.Equal(t, r.QueueID, entry.QueueID)
		require.NotNil(t, entry.FanoutProtocol)
		require.EqualValues(t, 1_800_000_000, entry.Entry.CreatedAt)
		require.Equal(t, messaging.OutboxStatusGroupPending, entry.Entry.Status)
	}
	require.Len(t, evidence.Entries, 2)
	require.Contains(t, npubs, report.Recipients[0].RecipientNpub)
	row := f.recovery(t)
	require.Equal(t, string(RecipientQueued), row.State)
	require.EqualValues(t, 0, row.RetryCount)
	require.Equal(t, row.QueueID, row.RetryQueueID)

	// Idempotent: nothing prepared is left; outbox untouched.
	before, _ := os.ReadFile(f.outboxPath)
	again, err := f.member.store.enqueuePreparedRaw(f.key, 1_800_000_001)
	require.NoError(t, err)
	require.Equal(t, 2, again.Queued)
	after, _ := os.ReadFile(f.outboxPath)
	require.Equal(t, string(before), string(after))

	// The queued epoch is immediately usable by the F3 boundaries.
	token, err := f.member.store.reserveFanoutAttempt(f.key, f.recipient)
	require.NoError(t, err)
	require.Equal(t, row.QueueID, token.queueID)
	require.NoError(t, f.member.store.startFanoutAttempt(token, 1, time.Now().Unix()))
	require.NoError(t, f.member.store.commitFanoutFailure(token))
	require.EqualValues(t, 1, f.recovery(t).RetryCount)
	entry, receipt, err := messaging.ReadGroupEntryLocked(f.outboxPath, f.eventID)
	require.NoError(t, err)
	require.NotNil(t, entry.FailureResult)
	require.Equal(t, row.QueueID, entry.QueueID, "failure result must retain the raw O1 QueueID")
	require.Equal(t, messaging.FailureReceipt(entry.FailureResult.ResultID, sha256Hex(entry.Fields["failure_result"])), receipt)
	require.Equal(t, receipt, f.row(t).Receipt, "T4 receipt must match the exact raw failure fragment")
}

func TestEnqueuePreparedRawReusesO1AfterCrashBeforeT2(t *testing.T) {
	f, _ := newPreparedFixture(t, 1)
	var eventJSON string
	require.NoError(t, f.member.db.QueryRow(`SELECT event_json FROM groupchat_fanout WHERE event_id=?`, f.eventID).Scan(&eventJSON))
	// O1 landed, T2 did not (simulated crash): the outbox already holds the entry.
	var queueID string
	require.NoError(t, messaging.WithGroupOutboxLock(func(path string) error {
		id, existed, err := messaging.CommitGroupEnqueueLocked(path, messaging.GroupEnqueueRecord{EventJSON: eventJSON, RecipientNpub: f.recipient, MaxRetries: 3, CreatedAt: 7})
		require.False(t, existed)
		queueID = id
		return err
	}))
	require.Equal(t, string(RecipientPrepared), f.recovery(t).State)

	report, err := f.member.store.enqueuePreparedRaw(f.key, 8)
	require.NoError(t, err)
	require.Equal(t, 1, report.Queued)
	require.Equal(t, queueID, report.Recipients[0].QueueID, "the durable O1 epoch is adopted, not duplicated")
	data, err := os.ReadFile(f.outboxPath)
	require.NoError(t, err)
	entries, err := messaging.ParseRawOutboxEvidence(data)
	require.Error(t, err)
	require.Len(t, entries.Entries, 1)
}

func TestEnqueuePreparedRawRefusals(t *testing.T) {
	f, _ := newPreparedFixture(t, 1)
	_, err := f.member.store.enqueuePreparedRaw(f.key, -1)
	require.Error(t, err)
	_, err = f.member.store.enqueuePreparedRaw(FanoutKey{LocalNpub: "not-an-npub"}, 1)
	require.Error(t, err)
	// An exhausted epoch for the frozen event is D6 territory: O1 refuses it
	// and the (inconsistently reset) row stays prepared.
	_, err = f.member.store.enqueuePreparedRaw(f.key, 1)
	require.NoError(t, err)
	for range 3 {
		require.NoError(t, f.member.store.commitFanoutFailure(f.startAttempt(t, 1)))
	}
	require.Equal(t, string(RecipientFailed), f.recovery(t).State)
	_, err = f.member.db.Exec(`UPDATE groupchat_fanout SET state='prepared', queue_id=''`)
	require.NoError(t, err)
	before, err := os.ReadFile(f.outboxPath)
	require.NoError(t, err)
	_, err = f.member.store.enqueuePreparedRaw(f.key, 2)
	require.ErrorIs(t, err, messaging.ErrOutboxEnqueueConflict)
	require.Equal(t, string(RecipientPrepared), f.recovery(t).State)
	after, err := os.ReadFile(f.outboxPath)
	require.NoError(t, err)
	require.Equal(t, string(before), string(after))
}

func TestRequeueFailedRawSuccess(t *testing.T) {
	f, npubs := newPreparedFixture(t, 2)
	reportPrep, err := f.member.store.EnqueuePrepared(f.key)
	require.NoError(t, err)
	require.Equal(t, 2, reportPrep.Queued)

	// Exhaust both recipients to failed state.
	for _, recipient := range npubs {
		for range 3 {
			token, err := f.member.store.reserveFanoutAttempt(f.key, recipient)
			require.NoError(t, err)
			require.NoError(t, f.member.store.startFanoutAttempt(token, 1, time.Now().Unix()))
			require.NoError(t, f.member.store.commitFanoutFailure(token))
		}
	}

	reportBefore, err := f.member.store.LoadFanoutReport(f.key)
	require.NoError(t, err)
	require.Equal(t, 2, reportBefore.Failed)
	require.Equal(t, 0, reportBefore.Queued)

	// 1. Requeue single recipient.
	report1, err := f.member.store.RequeueFailed(f.key, npubs[0])
	require.NoError(t, err)
	require.Equal(t, 1, report1.Queued)
	require.Equal(t, 1, report1.Failed)

	row0 := f.recovery(t)
	require.Equal(t, string(RecipientQueued), row0.State)
	require.EqualValues(t, 0, row0.RetryCount)
	require.Equal(t, row0.QueueID, row0.RetryQueueID)
	require.NotEmpty(t, row0.RecoveryID)
	require.NotEmpty(t, row0.RecoveryDigest)

	entry0, _, err := messaging.ReadGroupEntryLocked(f.outboxPath, f.eventID)
	require.NoError(t, err)
	require.NotNil(t, entry0)
	require.Equal(t, row0.QueueID, entry0.QueueID)
	require.NotNil(t, entry0.RecoveryWitness)
	require.Equal(t, row0.RecoveryID, entry0.RecoveryWitness.RecoveryID)
	require.Equal(t, messaging.OutboxWitnessDigest(entry0), row0.RecoveryDigest)

	// 2. Requeue remaining failed recipients with recipient == "".
	report2, err := f.member.store.RequeueFailed(f.key, "")
	require.NoError(t, err)
	require.Equal(t, 2, report2.Queued)
	require.Equal(t, 0, report2.Failed)

	for _, recipient := range npubs {
		var state, queueID, retryQueueID, recID, recDigest string
		var retryCount int64
		require.NoError(t, f.member.db.QueryRow(`SELECT state, queue_id, retry_queue_id, retry_count, last_recovery_id, last_recovery_digest
			FROM groupchat_fanout WHERE local_npub=? AND group_id=? AND send_key=? AND recipient_npub=?`,
			f.key.LocalNpub, f.key.GroupID, f.key.SendKey, recipient).
			Scan(&state, &queueID, &retryQueueID, &retryCount, &recID, &recDigest))
		require.Equal(t, string(RecipientQueued), state)
		require.Equal(t, queueID, retryQueueID)
		require.EqualValues(t, 0, retryCount)
		require.NotEmpty(t, recID)
		require.NotEmpty(t, recDigest)
	}
}

func TestEnqueueAndRequeuePreservesUnrelatedOutboxEntries(t *testing.T) {
	f, _ := newPreparedFixture(t, 1)

	// Pre-populate outbox with a valid DM entry.
	dmFixturePath := filepath.Join("..", "messaging", "testdata", "raw-outbox", "legacy-dm-nil-relays.json")
	dmData, err := os.ReadFile(dmFixturePath)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(f.outboxPath, dmData, 0600))

	// EnqueuePrepared must append group entry and preserve the DM entry.
	report, err := f.member.store.EnqueuePrepared(f.key)
	require.NoError(t, err)
	require.Equal(t, 1, report.Queued)

	latestOB, err := messaging.LoadOutbox()
	require.NoError(t, err)
	require.Len(t, latestOB.Entries, 2)
	require.Equal(t, "219bdbf9ed70b96893cc2382f365f24c", latestOB.Entries[0].QueueID)
	require.Equal(t, "", latestOB.Entries[0].Route)
	require.Equal(t, report.Recipients[0].QueueID, latestOB.Entries[1].QueueID)
	require.Equal(t, messaging.OutboxRouteGroup, latestOB.Entries[1].Route)

	// Exhaust the group entry.
	for range 3 {
		token, err := f.member.store.reserveFanoutAttempt(f.key, f.recipient)
		require.NoError(t, err)
		require.NoError(t, f.member.store.startFanoutAttempt(token, 1, time.Now().Unix()))
		require.NoError(t, f.member.store.commitFanoutFailure(token))
	}
	require.Equal(t, string(RecipientFailed), f.recovery(t).State)

	// RequeueFailed must replace the group entry with D6 while still preserving DM entry.
	requeueReport, err := f.member.store.RequeueFailed(f.key, f.recipient)
	require.NoError(t, err)
	require.Equal(t, 1, requeueReport.Queued)

	latestOBAfter, err := messaging.LoadOutbox()
	require.NoError(t, err)
	require.Len(t, latestOBAfter.Entries, 2)
	require.Equal(t, "219bdbf9ed70b96893cc2382f365f24c", latestOBAfter.Entries[0].QueueID)
	require.Equal(t, "", latestOBAfter.Entries[0].Route)
	require.Equal(t, requeueReport.Recipients[0].QueueID, latestOBAfter.Entries[1].QueueID)
	require.Equal(t, messaging.OutboxRouteGroup, latestOBAfter.Entries[1].Route)
}

func TestEnqueueAndRequeueFailClosedOnCorruption(t *testing.T) {
	f, _ := newPreparedFixture(t, 1)

	corruptContent := []byte("{\n  \"entries\": [{\"corrupted\":true\n}\n")
	require.NoError(t, os.WriteFile(f.outboxPath, corruptContent, 0600))

	// EnqueuePrepared must fail closed without mutating SQLite or outbox.
	_, err := f.member.store.EnqueuePrepared(f.key)
	require.Error(t, err)
	row := f.recovery(t)
	require.Equal(t, string(RecipientPrepared), row.State)
	outboxData, err := os.ReadFile(f.outboxPath)
	require.NoError(t, err)
	require.Equal(t, corruptContent, outboxData)

	// Fresh fixture to test RequeueFailed fail-closed behavior.
	f2, _ := newPreparedFixture(t, 1)
	_, err = f2.member.store.EnqueuePrepared(f2.key)
	require.NoError(t, err)
	for range 3 {
		token, err := f2.member.store.reserveFanoutAttempt(f2.key, f2.recipient)
		require.NoError(t, err)
		require.NoError(t, f2.member.store.startFanoutAttempt(token, 1, time.Now().Unix()))
		require.NoError(t, f2.member.store.commitFanoutFailure(token))
	}
	require.Equal(t, string(RecipientFailed), f2.recovery(t).State)

	// Corrupt outbox and verify RequeueFailed fails closed.
	require.NoError(t, os.WriteFile(f2.outboxPath, corruptContent, 0600))
	_, err = f2.member.store.RequeueFailed(f2.key, f2.recipient)
	require.Error(t, err)
	row2 := f2.recovery(t)
	require.Equal(t, string(RecipientFailed), row2.State)
	outboxData2, err := os.ReadFile(f2.outboxPath)
	require.NoError(t, err)
	require.Equal(t, corruptContent, outboxData2)
}

func TestRequeueFailedRawMissingFile(t *testing.T) {
	t.Run("missing outbox file successfully recovered via D6 missing witness", func(t *testing.T) {
		f, _ := newPreparedFixture(t, 1)
		_, err := f.member.store.EnqueuePrepared(f.key)
		require.NoError(t, err)

		for range 3 {
			token, err := f.member.store.reserveFanoutAttempt(f.key, f.recipient)
			require.NoError(t, err)
			require.NoError(t, f.member.store.startFanoutAttempt(token, 1, time.Now().Unix()))
			require.NoError(t, f.member.store.commitFanoutFailure(token))
		}
		require.Equal(t, string(RecipientFailed), f.recovery(t).State)

		// Physically remove outbox file.
		require.NoError(t, os.Remove(f.outboxPath))
		_, err = os.Stat(f.outboxPath)
		require.True(t, os.IsNotExist(err), "outbox file must be physically absent")

		report, err := f.member.store.RequeueFailed(f.key, f.recipient)
		require.NoError(t, err)
		require.Equal(t, 1, report.Queued)
		require.Equal(t, 0, report.Failed)

		row := f.recovery(t)
		require.Equal(t, string(RecipientQueued), row.State)
		require.EqualValues(t, 0, row.RetryCount)
		require.Equal(t, row.QueueID, row.RetryQueueID)
		require.NotEmpty(t, row.RecoveryID)
		require.NotEmpty(t, row.RecoveryDigest)

		entry, _, err := messaging.ReadGroupEntryLocked(f.outboxPath, f.eventID)
		require.NoError(t, err)
		require.NotNil(t, entry)
		require.Equal(t, row.QueueID, entry.QueueID)
		require.NotNil(t, entry.RecoveryWitness)
		require.Equal(t, "missing", entry.RecoveryWitness.Reason)
		require.True(t, entry.RecoveryWitness.ObservedAbsent)
		require.Equal(t, row.RecoveryID, entry.RecoveryWitness.RecoveryID)
		require.Equal(t, messaging.OutboxWitnessDigest(entry), row.RecoveryDigest)
	})

	t.Run("injected failure on absent file rolls back SQLite and does not create outbox", func(t *testing.T) {
		f, _ := newPreparedFixture(t, 1)
		_, err := f.member.store.EnqueuePrepared(f.key)
		require.NoError(t, err)

		for range 3 {
			token, err := f.member.store.reserveFanoutAttempt(f.key, f.recipient)
			require.NoError(t, err)
			require.NoError(t, f.member.store.startFanoutAttempt(token, 1, time.Now().Unix()))
			require.NoError(t, f.member.store.commitFanoutFailure(token))
		}
		require.Equal(t, string(RecipientFailed), f.recovery(t).State)

		require.NoError(t, os.Remove(f.outboxPath))
		_, err = os.Stat(f.outboxPath)
		require.True(t, os.IsNotExist(err))

		origSeam := commitGroupRecoveryLocked
		defer func() { commitGroupRecoveryLocked = origSeam }()
		commitGroupRecoveryLocked = func(path string, rec messaging.GroupRecoveryRecord) (string, error) {
			return "", errors.New("injected locked recovery write failure")
		}

		_, err = f.member.store.RequeueFailed(f.key, f.recipient)
		require.Error(t, err)
		require.Contains(t, err.Error(), "injected locked recovery write failure")

		// SQLite remains failed.
		require.Equal(t, string(RecipientFailed), f.recovery(t).State)
		// Outbox file remains absent.
		_, err = os.Stat(f.outboxPath)
		require.True(t, os.IsNotExist(err), "outbox file must remain absent after rollback")
	})
}

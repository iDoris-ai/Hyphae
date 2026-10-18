package groupchat

import (
	"crypto/sha256"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/iDoris-ai/hyphae/internal/messaging"
	"github.com/stretchr/testify/require"
)

// writeLegacyPendingOutbox writes a single legacy (no fanout_protocol) group
// pending entry for the fixture's frozen event with the given budget.
func writeLegacyPendingOutbox(t *testing.T, f failureFixture, queueID string, retry, max int) string {
	t.Helper()
	var eventJSON string
	require.NoError(t, f.member.db.QueryRow(`SELECT event_json FROM groupchat_fanout WHERE event_id=?`, f.eventID).Scan(&eventJSON))
	entry := `{"queue_id":"` + queueID + `","route":"group","id":"` + f.eventID + `","event_json":` + marshalJSONString(t, eventJSON) +
		`,"recipient_npub":"` + f.recipient + `","relays":null,"retry_count":` + itoa(retry) + `,"max_retries":` + itoa(max) +
		`,"last_attempt":0,"created_at":7,"status":"group_pending"}`
	require.NoError(t, os.WriteFile(f.outboxPath, []byte("{\n  \"entries\": ["+entry+"]\n}\n"), 0600))
	return eventJSON
}

func itoa(n int) string {
	if n < 0 || n > 9 {
		panic("itoa: single digit only")
	}
	return string(rune('0' + n))
}

// F1: an active legacy entry whose max_retries differs from the frozen
// prepared max_retries must not be adopted (nor its budget overwritten).
func TestEnqueuePreparedRawRefusesMaxRetriesMismatchWithoutWrites(t *testing.T) {
	f, _ := newPreparedFixture(t, 1) // prepared max_retries=3
	writeLegacyPendingOutbox(t, f, "abababababababababababababababab", 1, 4)
	assertUnchanged := f.snapshot(t)

	_, err := f.member.store.enqueuePreparedRaw(f.key, 8)
	require.ErrorIs(t, err, messaging.ErrOutboxEnqueueConflict)
	assertUnchanged()
	require.Equal(t, string(RecipientPrepared), f.recovery(t).State)
}

// F2: a native prepared row that already counted a retry, with no outbox
// entry to adopt, is not fresh: refuse before O1 (no outbox created, row kept).
func TestEnqueuePreparedRawRefusesSeededNativeWithoutOutbox(t *testing.T) {
	for _, tc := range []struct {
		name            string
		attempts, retry int
	}{{"retry1", 0, 1}, {"attempts1", 1, 0}} {
		t.Run(tc.name, func(t *testing.T) {
			f, _ := newPreparedFixture(t, 1)
			_, err := f.member.db.Exec(`UPDATE groupchat_fanout SET attempts=?, retry_count=? WHERE event_id=?`, tc.attempts, tc.retry, f.eventID)
			require.NoError(t, err)
			before := fanoutRowSnapshotByRecipient(t, f.member, f.key, f.recipient)

			_, err = f.member.store.enqueuePreparedRaw(f.key, 8)
			require.ErrorIs(t, err, errPreparedCountersWithoutEpoch)
			_, statErr := os.Stat(f.outboxPath)
			require.True(t, os.IsNotExist(statErr), "refusal must not create the outbox")
			require.Equal(t, before, fanoutRowSnapshotByRecipient(t, f.member, f.key, f.recipient))
			require.EqualValues(t, tc.retry, f.recovery(t).RetryCount)
		})
	}
}

// F2: the guard only fires on nonzero native counters; NULL retry_count is
// unknown, not nonzero, and a zeroed native row still takes the happy path.
func TestEnqueuePreparedRawFreshNativeStillEnqueues(t *testing.T) {
	for _, retry := range []any{0, nil} {
		f, _ := newPreparedFixture(t, 1)
		_, err := f.member.db.Exec(`UPDATE groupchat_fanout SET retry_count=? WHERE event_id=?`, retry, f.eventID)
		require.NoError(t, err)
		report, err := f.member.store.enqueuePreparedRaw(f.key, 8)
		require.NoError(t, err)
		require.Equal(t, 1, report.Queued)
		require.EqualValues(t, 0, f.recovery(t).RetryCount)
	}
}

// Compatible legacy retry1/max3 is still adopted for a native row whose
// counters already match it, and for a migrated legacy row (attempts7/base7):
// accounting origin and base are preserved and the next failure is retry 2.
func TestEnqueuePreparedRawAdoptsCompatibleLegacyForSeededRows(t *testing.T) {
	for _, tc := range []struct {
		name, origin   string
		attempts, base int
		retry          any
	}{{"native-retry1", "native", 0, 0, 1}, {"legacy-attempts7", "legacy", 7, 7, nil}} {
		t.Run(tc.name, func(t *testing.T) {
			f, _ := newPreparedFixture(t, 1)
			_, err := f.member.db.Exec(`UPDATE groupchat_fanout SET accounting_origin=?, attempts=?, legacy_attempts_base=?, retry_count=? WHERE event_id=?`,
				tc.origin, tc.attempts, tc.base, tc.retry, f.eventID)
			require.NoError(t, err)
			const legacyQueueID = "cdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcd"
			writeLegacyPendingOutbox(t, f, legacyQueueID, 1, 3)
			bytesBefore, err := os.ReadFile(f.outboxPath)
			require.NoError(t, err)

			report, err := f.member.store.enqueuePreparedRaw(f.key, 8)
			require.NoError(t, err)
			require.Equal(t, legacyQueueID, report.Recipients[0].QueueID)
			bytesAfter, err := os.ReadFile(f.outboxPath)
			require.NoError(t, err)
			require.Equal(t, string(bytesBefore), string(bytesAfter))
			row := f.recovery(t)
			require.EqualValues(t, 1, row.RetryCount)
			require.EqualValues(t, tc.attempts, row.Attempts)
			var origin string
			var base int
			require.NoError(t, f.member.db.QueryRow(`SELECT accounting_origin, legacy_attempts_base FROM groupchat_fanout WHERE event_id=?`, f.eventID).Scan(&origin, &base))
			require.Equal(t, tc.origin, origin)
			require.Equal(t, tc.base, base)

			token, err := f.reserveBound()
			require.NoError(t, err)
			require.NoError(t, f.member.store.startFanoutAttempt(token, 1, time.Now().Unix()))
			require.NoError(t, f.member.store.commitFanoutFailure(token))
			after := f.recovery(t)
			require.EqualValues(t, 2, after.RetryCount)
			require.Equal(t, string(RecipientQueued), after.State, "one retry remains of max 3")
		})
	}
}

func writeExhaustedOutboxForRecipient(t *testing.T, f failureFixture, recipient, queueID string) {
	t.Helper()
	var eventID, eventJSON string
	require.NoError(t, f.member.db.QueryRow(`SELECT event_id, event_json FROM groupchat_fanout WHERE recipient_npub=?`, recipient).Scan(&eventID, &eventJSON))
	entry := `{"queue_id":"` + queueID + `","route":"group","id":"` + eventID + `","event_json":` + marshalJSONString(t, eventJSON) +
		`,"recipient_npub":"` + recipient + `","relays":null,"retry_count":3,"max_retries":3,"last_attempt":0,"created_at":7,"status":"group_pending"}`
	require.NoError(t, os.WriteFile(f.outboxPath, []byte("{\n  \"entries\": ["+entry+"]\n}\n"), 0600))
}

func TestEnqueuePreparedRawHeldRecipientBlocksWholeKeyZeroWrites(t *testing.T) {
	f, npubs := newPreparedFixture(t, 2)
	require.Len(t, npubs, 2)
	rFirst, rLast := npubs[0], npubs[1]
	if rFirst > rLast {
		rFirst, rLast = rLast, rFirst
	}

	// Held recipient sorts LAST.
	writeExhaustedOutboxForRecipient(t, f, rLast, "deadbeefdeadbeefdeadbeefdeadbeef")
	bytesBefore, err := os.ReadFile(f.outboxPath)
	require.NoError(t, err)
	shaBefore := sha256.Sum256(bytesBefore)

	snapFirstBefore := fanoutRowSnapshotByRecipient(t, f.member, f.key, rFirst)
	snapLastBefore := fanoutRowSnapshotByRecipient(t, f.member, f.key, rLast)

	_, err = f.member.store.enqueuePreparedRaw(f.key, 8)
	require.ErrorIs(t, err, messaging.ErrOutboxEnqueueConflict)

	bytesAfter, err := os.ReadFile(f.outboxPath)
	require.NoError(t, err)
	shaAfter := sha256.Sum256(bytesAfter)
	require.Equal(t, shaBefore, shaAfter, "outbox bytes sha256 must be unchanged")
	require.Equal(t, string(bytesBefore), string(bytesAfter), "outbox bytes must be identical")

	require.Equal(t, snapFirstBefore, fanoutRowSnapshotByRecipient(t, f.member, f.key, rFirst), "first recipient row must be unchanged")
	require.Equal(t, snapLastBefore, fanoutRowSnapshotByRecipient(t, f.member, f.key, rLast), "last recipient row must be unchanged")

	// Second call -> identical error and bytes
	_, err = f.member.store.enqueuePreparedRaw(f.key, 9)
	require.ErrorIs(t, err, messaging.ErrOutboxEnqueueConflict)
	bytesAfter2, err := os.ReadFile(f.outboxPath)
	require.NoError(t, err)
	require.Equal(t, string(bytesBefore), string(bytesAfter2), "outbox bytes must stay identical on rerun")
}

func TestEnqueuePreparedRawHeldRecipientBlocksWholeKeyZeroWritesSortsFirst(t *testing.T) {
	f, npubs := newPreparedFixture(t, 2)
	require.Len(t, npubs, 2)
	rFirst, rLast := npubs[0], npubs[1]
	if rFirst > rLast {
		rFirst, rLast = rLast, rFirst
	}

	// Held recipient sorts FIRST.
	writeExhaustedOutboxForRecipient(t, f, rFirst, "deadbeefdeadbeefdeadbeefdeadbeef")
	bytesBefore, err := os.ReadFile(f.outboxPath)
	require.NoError(t, err)
	shaBefore := sha256.Sum256(bytesBefore)

	snapFirstBefore := fanoutRowSnapshotByRecipient(t, f.member, f.key, rFirst)
	snapLastBefore := fanoutRowSnapshotByRecipient(t, f.member, f.key, rLast)

	_, err = f.member.store.enqueuePreparedRaw(f.key, 8)
	require.ErrorIs(t, err, messaging.ErrOutboxEnqueueConflict)

	bytesAfter, err := os.ReadFile(f.outboxPath)
	require.NoError(t, err)
	shaAfter := sha256.Sum256(bytesAfter)
	require.Equal(t, shaBefore, shaAfter, "outbox bytes sha256 must be unchanged")
	require.Equal(t, string(bytesBefore), string(bytesAfter), "outbox bytes must be identical")

	require.Equal(t, snapFirstBefore, fanoutRowSnapshotByRecipient(t, f.member, f.key, rFirst), "first recipient row must be unchanged")
	require.Equal(t, snapLastBefore, fanoutRowSnapshotByRecipient(t, f.member, f.key, rLast), "last recipient row must be unchanged")

	// Second call -> identical error and bytes
	_, err = f.member.store.enqueuePreparedRaw(f.key, 9)
	require.ErrorIs(t, err, messaging.ErrOutboxEnqueueConflict)
	bytesAfter2, err := os.ReadFile(f.outboxPath)
	require.NoError(t, err)
	require.Equal(t, string(bytesBefore), string(bytesAfter2), "outbox bytes must stay identical on rerun")
}

func TestEnqueuePreparedRawRefusesEntryWithUnconsumedResult(t *testing.T) {
	f := newFailureFixture(t)
	token := f.startAttempt(t, 1)
	f.failO3BeforeT4(t, token)

	// Reset row to prepared to simulate an inconsistent prepared row against the unconsumed failure outbox entry.
	_, err := f.member.db.Exec(`UPDATE groupchat_fanout SET state = ?, queue_id = '' WHERE event_id = ?`, RecipientPrepared, f.eventID)
	require.NoError(t, err)

	assertUnchanged := f.snapshot(t)

	_, err = f.member.store.enqueuePreparedRaw(f.key, 8)
	require.ErrorIs(t, err, messaging.ErrOutboxEnqueueConflict)
	assertUnchanged()
	require.Equal(t, string(RecipientPrepared), f.recovery(t).State)
}

func TestEnqueuePreparedRawAdoptedO1RequiresDurability(t *testing.T) {
	f, _ := newPreparedFixture(t, 1)
	var eventJSON string
	require.NoError(t, f.member.db.QueryRow(`SELECT event_json FROM groupchat_fanout WHERE event_id=?`, f.eventID).Scan(&eventJSON))

	var preQ string
	require.NoError(t, messaging.WithGroupOutboxLock(func(path string) error {
		q, existed, err := messaging.CommitGroupEnqueueLocked(path, messaging.GroupEnqueueRecord{
			EventJSON: eventJSON, RecipientNpub: f.recipient, MaxRetries: 3, CreatedAt: 7,
		})
		require.NoError(t, err)
		require.False(t, existed)
		preQ = q
		return nil
	}))

	orig := confirmOutboxDurable
	defer func() { confirmOutboxDurable = orig }()
	confirmOutboxDurable = func(path string) error {
		return messaging.MarkOutboxCommitUncertain(syscall.EIO)
	}

	// First call while failing
	_, err := f.member.store.enqueuePreparedRaw(f.key, 8)
	require.ErrorIs(t, err, errFanoutEnqueuePending)
	require.True(t, messaging.IsOutboxCommitUncertain(err))
	require.Equal(t, string(RecipientPrepared), f.recovery(t).State)

	// Second call while still failing
	_, err = f.member.store.enqueuePreparedRaw(f.key, 9)
	require.ErrorIs(t, err, errFanoutEnqueuePending)
	require.True(t, messaging.IsOutboxCommitUncertain(err))
	require.Equal(t, string(RecipientPrepared), f.recovery(t).State)

	// Restore hook -> queues row with pre-appended Q, exactly one outbox entry
	confirmOutboxDurable = orig
	report, err := f.member.store.enqueuePreparedRaw(f.key, 10)
	require.NoError(t, err)
	require.Equal(t, 1, report.Queued)
	require.Equal(t, preQ, report.Recipients[0].QueueID)
	require.Equal(t, string(RecipientQueued), f.recovery(t).State)

	data, err := os.ReadFile(f.outboxPath)
	require.NoError(t, err)
	evidence, err := messaging.ParseRawOutboxEvidence(data)
	require.Error(t, err)
	require.Len(t, evidence.Entries, 1)
}

func TestEnqueuePreparedRawFreshO1DurabilityFailureHolds(t *testing.T) {
	f, npubs := newPreparedFixture(t, 2)
	require.Len(t, npubs, 2)

	orig := confirmOutboxDurable
	defer func() { confirmOutboxDurable = orig }()
	confirmOutboxDurable = func(path string) error {
		return messaging.MarkOutboxCommitUncertain(syscall.EIO)
	}

	_, err := f.member.store.enqueuePreparedRaw(f.key, 8)
	require.ErrorIs(t, err, errFanoutEnqueuePending)
	require.True(t, messaging.IsOutboxCommitUncertain(err))

	for _, recipient := range npubs {
		var state string
		require.NoError(t, f.member.db.QueryRow(`SELECT state FROM groupchat_fanout WHERE recipient_npub=?`, recipient).Scan(&state))
		require.Equal(t, string(RecipientPrepared), state)
	}

	// Restore hook -> queues both rows, entries not duplicated
	confirmOutboxDurable = orig
	report, err := f.member.store.enqueuePreparedRaw(f.key, 9)
	require.NoError(t, err)
	require.Equal(t, 2, report.Queued)
	for _, recipient := range npubs {
		var state string
		require.NoError(t, f.member.db.QueryRow(`SELECT state FROM groupchat_fanout WHERE recipient_npub=?`, recipient).Scan(&state))
		require.Equal(t, string(RecipientQueued), state)
	}

	data, err := os.ReadFile(f.outboxPath)
	require.NoError(t, err)
	evidence, err := messaging.ParseRawOutboxEvidence(data)
	require.Error(t, err)
	require.Len(t, evidence.Entries, 2)
}

package groupchat

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/internal/messaging"
	"github.com/stretchr/testify/require"
)

type failureFixture struct {
	member     testMember
	key        FanoutKey
	recipient  string
	eventID    string
	queueID    string
	outboxPath string
}

// newFailureFixture queues one signed group event in a temp-HOME outbox and a
// matching queued fanout row with retry_count 0 and max_retries 3.
func newFailureFixture(t *testing.T) failureFixture {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	member := newMember(t)
	recipientSK := nostr.Generate()
	recipient := common.EncodeNpub(recipientSK.Public())
	event := testAgentEvent(t, "group hello", member.sk, recipientSK, true)
	eventJSON, err := json.Marshal(event)
	require.NoError(t, err)
	entry, _, err := messaging.EnqueueGroupOutboxEntry(string(eventJSON), recipient, nil, 3)
	require.NoError(t, err)
	key := FanoutKey{LocalNpub: member.npub, GroupID: mustOpaque(t), EnvelopeType: EnvelopeMessage, SendKey: mustOpaque(t)}
	now := time.Now().Unix()
	_, err = member.db.Exec(`INSERT INTO groupchat_fanout
        (local_npub,group_id,envelope_type,send_key,recipient_npub,event_id,event_json,queue_id,state,max_retries,retry_count,created_at,updated_at)
        VALUES (?,?,?,?,?,?,?,?,?,3,0,?,?)`,
		key.LocalNpub, key.GroupID, key.EnvelopeType, key.SendKey, recipient, entry.ID, string(eventJSON), entry.QueueID, RecipientQueued, now, now)
	require.NoError(t, err)
	path, err := messaging.GetOutboxPath()
	require.NoError(t, err)
	return failureFixture{member: member, key: key, recipient: recipient, eventID: entry.ID, queueID: entry.QueueID, outboxPath: path}
}

// startAttempt runs R and S for the row and returns the started token.
func (f failureFixture) startAttempt(t *testing.T, targets int) fanoutAttemptToken {
	t.Helper()
	tx, err := f.member.store.beginImmediate()
	require.NoError(t, err)
	token, err := reserveFanoutAttemptTx(tx, f.key, f.recipient)
	require.NoError(t, err)
	require.NoError(t, startFanoutAttemptTx(tx, token, targets, time.Now().Unix()))
	require.NoError(t, tx.Commit())
	return token
}

func (f failureFixture) outboxEntry(t *testing.T) *messaging.RawGroupEntry {
	t.Helper()
	data, err := os.ReadFile(f.outboxPath)
	require.NoError(t, err)
	var doc struct{ Entries []json.RawMessage }
	require.NoError(t, json.Unmarshal(data, &doc))
	require.Len(t, doc.Entries, 1)
	entry, err := messaging.ParseRawGroupEntry(doc.Entries[0])
	require.NoError(t, err)
	return entry
}

type failureRow struct {
	State, Phase, Issue, Receipt, RetryQueueID string
	RetryCount, Revision, LastResultGeneration int64
}

func (f failureFixture) row(t *testing.T) failureRow {
	t.Helper()
	var r failureRow
	require.NoError(t, f.member.db.QueryRow(`SELECT state,attempt_phase,issue,last_failure_receipt,retry_queue_id,retry_count,row_revision,last_result_generation
        FROM groupchat_fanout WHERE event_id=?`, f.eventID).Scan(&r.State, &r.Phase, &r.Issue, &r.Receipt, &r.RetryQueueID, &r.RetryCount, &r.Revision, &r.LastResultGeneration))
	return r
}

// snapshot returns a check that outbox bytes, outbox mtime and the full row are unchanged.
func (f failureFixture) snapshot(t *testing.T) func() {
	t.Helper()
	data, err := os.ReadFile(f.outboxPath)
	require.NoError(t, err)
	info, err := os.Stat(f.outboxPath)
	require.NoError(t, err)
	row := fanoutRowSnapshot(t, f.member, f.eventID)
	return func() {
		t.Helper()
		after, err := os.ReadFile(f.outboxPath)
		require.NoError(t, err)
		require.Equal(t, string(data), string(after), "outbox bytes changed")
		afterInfo, err := os.Stat(f.outboxPath)
		require.NoError(t, err)
		require.Equal(t, info.ModTime(), afterInfo.ModTime(), "outbox mtime changed")
		require.Equal(t, row, fanoutRowSnapshot(t, f.member, f.eventID), "fanout row changed")
	}
}

func TestCommitFanoutFailureO3T4UntilExhausted(t *testing.T) {
	f := newFailureFixture(t)
	for round := int64(1); round <= 3; round++ {
		token := f.startAttempt(t, 2)
		before := f.row(t)
		require.NoError(t, f.member.store.commitFanoutFailure(token), "round %d", round)

		entry := f.outboxEntry(t)
		result := entry.FailureResult
		require.NotNil(t, result)
		require.Equal(t, round, int64(entry.Entry.RetryCount))
		require.Equal(t, round, result.RetryCount)
		require.Equal(t, round-1, result.PriorRetryCount)
		require.Equal(t, token.generation, result.Token.AttemptGeneration)
		require.Equal(t, before.Revision, result.ExpectedRowRevision)
		require.EqualValues(t, 2, result.RelayCount)
		require.Equal(t, round, result.Attempts)
		require.Equal(t, result.LastAttemptAt, entry.Entry.LastAttempt)

		after := f.row(t)
		require.Equal(t, round, after.RetryCount)
		require.Equal(t, f.queueID, after.RetryQueueID)
		require.Equal(t, before.Revision+1, after.Revision)
		require.Equal(t, token.generation, after.LastResultGeneration)
		require.Equal(t, "failed", after.Phase)
		require.Equal(t, messaging.FailureReceipt(result.ResultID, sha256Hex(entry.Fields["failure_result"])), after.Receipt)
		if round < 3 {
			require.Equal(t, messaging.OutboxStatusGroupPending, entry.Entry.Status)
			require.Equal(t, string(RecipientQueued), after.State)
			require.Equal(t, string(messaging.AgentMessageIssueSendFailed), after.Issue)
		} else {
			require.Equal(t, messaging.OutboxStatusGroupFailed, entry.Entry.Status)
			require.Equal(t, string(RecipientFailed), after.State)
			require.Equal(t, string(messaging.AgentMessageIssueRetryExhausted), after.Issue)
			require.Equal(t, "retry_exhausted", result.Issue)
		}
	}
}

func TestCommitFanoutFailureStaleTokenWritesNothing(t *testing.T) {
	f := newFailureFixture(t)
	old := f.startAttempt(t, 1)
	tx, err := f.member.store.beginImmediate()
	require.NoError(t, err)
	reserved, err := reserveFanoutAttemptTx(tx, f.key, f.recipient)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())

	unchanged := f.snapshot(t)
	require.ErrorIs(t, f.member.store.commitFanoutFailure(old), errFanoutStaleAttempt, "superseded generation")
	require.ErrorIs(t, f.member.store.commitFanoutFailure(reserved), errFanoutStaleAttempt, "reserved but not started")
	unchanged()
}

func TestCommitFanoutFailureHoldsUnknownBudget(t *testing.T) {
	f := newFailureFixture(t)
	token := f.startAttempt(t, 1)
	_, err := f.member.db.Exec(`UPDATE groupchat_fanout SET retry_count=NULL`)
	require.NoError(t, err)
	unchanged := f.snapshot(t)
	require.ErrorContains(t, f.member.store.commitFanoutFailure(token), "unknown")
	unchanged()
}

func TestCommitFanoutFailureRefusesCrossStoreMismatch(t *testing.T) {
	f := newFailureFixture(t)
	token := f.startAttempt(t, 1)
	_, err := f.member.db.Exec(`UPDATE groupchat_fanout SET retry_count=1`)
	require.NoError(t, err)
	unchanged := f.snapshot(t)
	err = f.member.store.commitFanoutFailure(token)
	require.Error(t, err, "SQLite prior retry_count 1 does not match the JSON entry's 0")
	require.NotErrorIs(t, err, ErrFanoutBookkeepingPending)
	unchanged()
}

func TestCommitFanoutFailureT4FailureLeavesUnconsumedO3(t *testing.T) {
	f := newFailureFixture(t)
	token := f.startAttempt(t, 1)
	_, err := f.member.db.Exec(`CREATE TRIGGER inject_t4_failure BEFORE UPDATE OF last_failure_receipt ON groupchat_fanout
        BEGIN SELECT RAISE(ABORT, 'injected T4 failure'); END`)
	require.NoError(t, err)
	rowBefore := fanoutRowSnapshot(t, f.member, f.eventID)

	err = f.member.store.commitFanoutFailure(token)
	require.ErrorIs(t, err, ErrFanoutBookkeepingPending)
	require.Equal(t, rowBefore, fanoutRowSnapshot(t, f.member, f.eventID), "T4 must roll back")
	entry := f.outboxEntry(t)
	require.NotNil(t, entry.FailureResult, "O3 stays durable for the recovery barrier")
	require.Equal(t, 1, entry.Entry.RetryCount)

	_, err = f.member.db.Exec(`DROP TRIGGER inject_t4_failure`)
	require.NoError(t, err)
	unchanged := f.snapshot(t)
	err = f.member.store.commitFanoutFailure(token)
	// JSON already holds the post-image (retry_count 1) while SQLite still
	// has prior 0, so the second O3 is refused before it could overwrite the
	// unconsumed record; replaying T4 belongs to the recovery barrier.
	require.ErrorContains(t, err, "pre-image")
	require.NotErrorIs(t, err, ErrFanoutBookkeepingPending)
	unchanged()
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// failO3BeforeT4 commits O3 for a started attempt while an injected trigger
// aborts T4, leaving the outbox record unconsumed.
func (f failureFixture) failO3BeforeT4(t *testing.T, token fanoutAttemptToken) {
	t.Helper()
	_, err := f.member.db.Exec(`CREATE TRIGGER inject_t4_failure BEFORE UPDATE OF last_failure_receipt ON groupchat_fanout
        BEGIN SELECT RAISE(ABORT, 'injected T4 failure'); END`)
	require.NoError(t, err)
	require.ErrorIs(t, f.member.store.commitFanoutFailure(token), ErrFanoutBookkeepingPending)
	_, err = f.member.db.Exec(`DROP TRIGGER inject_t4_failure`)
	require.NoError(t, err)
}

func TestRecoverFanoutFailureReplaysT4Once(t *testing.T) {
	f := newFailureFixture(t)
	token := f.startAttempt(t, 2)
	before := f.row(t)
	f.failO3BeforeT4(t, token)
	outboxAfterO3 := f.snapshot(t)

	replayed, err := f.member.store.recoverFanoutFailure(f.key, f.recipient)
	require.NoError(t, err)
	require.True(t, replayed)
	outboxAfterO3Bytes := f.outboxEntry(t)
	after := f.row(t)
	require.EqualValues(t, 1, after.RetryCount)
	require.Equal(t, before.Revision+1, after.Revision)
	require.Equal(t, token.generation, after.LastResultGeneration)
	require.Equal(t, messaging.FailureReceipt(outboxAfterO3Bytes.FailureResult.ResultID, sha256Hex(outboxAfterO3Bytes.Fields["failure_result"])), after.Receipt)
	require.Equal(t, string(messaging.AgentMessageIssueSendFailed), after.Issue)

	unchanged := f.snapshot(t)
	replayed, err = f.member.store.recoverFanoutFailure(f.key, f.recipient)
	require.NoError(t, err)
	require.False(t, replayed, "a consumed receipt is a no-op")
	unchanged()
	_ = outboxAfterO3

	// The replayed receipt authorizes the next O3 to replace the record.
	next := f.startAttempt(t, 1)
	require.NoError(t, f.member.store.commitFanoutFailure(next))
	require.EqualValues(t, 2, f.row(t).RetryCount)
}

func TestRecoverFanoutFailureNoRecordIsNoop(t *testing.T) {
	f := newFailureFixture(t)
	unchanged := f.snapshot(t)
	replayed, err := f.member.store.recoverFanoutFailure(f.key, f.recipient)
	require.NoError(t, err)
	require.False(t, replayed)
	unchanged()

	require.NoError(t, f.member.store.commitFanoutFailure(f.startAttempt(t, 1)))
	unchanged = f.snapshot(t)
	replayed, err = f.member.store.recoverFanoutFailure(f.key, f.recipient)
	require.NoError(t, err)
	require.False(t, replayed, "normal O3+T4 leaves a consumed record")
	unchanged()
}

func TestRecoverFanoutFailureHoldsConflicts(t *testing.T) {
	for name, tamper := range map[string]string{
		"revision moved":     `UPDATE groupchat_fanout SET row_revision=row_revision+1`,
		"new generation":     `UPDATE groupchat_fanout SET attempt_generation=attempt_generation+1`,
		"attempts changed":   `UPDATE groupchat_fanout SET attempts=attempts+1`,
		"budget changed":     `UPDATE groupchat_fanout SET retry_count=1`,
		"not started":        `UPDATE groupchat_fanout SET attempt_phase='reserved'`,
		"watermark advanced": `UPDATE groupchat_fanout SET last_result_generation=attempt_generation`,
	} {
		t.Run(name, func(t *testing.T) {
			f := newFailureFixture(t)
			f.failO3BeforeT4(t, f.startAttempt(t, 1))
			_, err := f.member.db.Exec(tamper)
			require.NoError(t, err)
			unchanged := f.snapshot(t)
			replayed, err := f.member.store.recoverFanoutFailure(f.key, f.recipient)
			require.ErrorIs(t, err, ErrFanoutBookkeepingConflict)
			require.False(t, replayed)
			unchanged()
		})
	}
}

func TestRecoverFanoutFailureReplaysOverOlderReceipt(t *testing.T) {
	f := newFailureFixture(t)
	f.failO3BeforeT4(t, f.startAttempt(t, 1))
	// An earlier consumed receipt on the row does not make the current
	// attempt's exact O3 record a conflict (rev7 §3.3).
	_, err := f.member.db.Exec(`UPDATE groupchat_fanout SET last_failure_receipt='x:y'`)
	require.NoError(t, err)
	replayed, err := f.member.store.recoverFanoutFailure(f.key, f.recipient)
	require.NoError(t, err)
	require.True(t, replayed)
	entry := f.outboxEntry(t)
	require.Equal(t, messaging.FailureReceipt(entry.FailureResult.ResultID, sha256Hex(entry.Fields["failure_result"])), f.row(t).Receipt)
}

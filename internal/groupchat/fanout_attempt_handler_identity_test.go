package groupchat

import (
	"encoding/json"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/internal/messaging"
	"github.com/stretchr/testify/require"
)

func TestNewFanoutAttemptHandlerForConstructionValidation(t *testing.T) {
	member := newMember(t)

	// Nil store fails.
	_, err := NewFanoutAttemptHandlerFor(nil, member.npub)
	require.Error(t, err)
	require.Contains(t, err.Error(), "nil store")

	// Empty npub fails.
	_, err = NewFanoutAttemptHandlerFor(member.store, "")
	require.Error(t, err)

	// Malformed npub fails.
	_, err = NewFanoutAttemptHandlerFor(member.store, "npub1not-a-valid-npub")
	require.Error(t, err)

	// Hex private key or nsec fails.
	_, err = NewFanoutAttemptHandlerFor(member.store, "nsec1vl029mgpspedva04g90vltkh6fvh240eqtv9xxl2xme3zgnqdmeqeqgah3")
	require.Error(t, err)

	// Valid npub succeeds.
	handler, err := NewFanoutAttemptHandlerFor(member.store, member.npub)
	require.NoError(t, err)
	require.NotNil(t, handler)
	require.Equal(t, member.npub, handler.LocalNpub())

	// Hex public key canonicalizes to npub.
	pk, err := common.ParsePublicKey(member.npub)
	require.NoError(t, err)
	handlerHex, err := NewFanoutAttemptHandlerFor(member.store, common.PubKeyToHex(pk))
	require.NoError(t, err)
	require.Equal(t, member.npub, handlerHex.LocalNpub())
}

func TestProcessAlicePresentedBobEntryReturnsConflictNoMutation(t *testing.T) {
	// Bob creates and signs a fully valid group event and fanout row.
	bobFixture := newFailureFixture(t)
	bobEntry := bobFixture.typedEntry(t)

	// Alice is a distinct process identity with a trusted bound handler.
	aliceSK := nostr.Generate()
	aliceNpub := common.EncodeNpub(aliceSK.Public())
	require.NotEqual(t, bobFixture.key.LocalNpub, aliceNpub)

	aliceHandler, err := NewFanoutAttemptHandlerFor(bobFixture.member.store, aliceNpub)
	require.NoError(t, err)

	// Alice presented with Bob's entry fails Conflict BEFORE any barrier replay or IO.
	bobSnapshot := bobFixture.snapshot(t)
	_, err = aliceHandler.Reserve(bobEntry)
	require.ErrorIs(t, err, messaging.ErrGroupAttemptConflict)
	bobSnapshot() // Asserts DB row, outbox bytes, and outbox mtime are completely unchanged.
}

func TestProcessAlicePresentedBobTokenReturnsConflictNoMutation(t *testing.T) {
	bobFixture := newFailureFixture(t)
	bobToken := bobFixture.startAttempt(t, 1)
	token := toGroupAttemptToken(bobToken)

	aliceSK := nostr.Generate()
	aliceNpub := common.EncodeNpub(aliceSK.Public())
	aliceHandler, err := NewFanoutAttemptHandlerFor(bobFixture.member.store, aliceNpub)
	require.NoError(t, err)

	bobSnapshot := bobFixture.snapshot(t)

	// Start refuses Bob's token.
	err = aliceHandler.Start(token, 1, time.Now().Unix())
	require.ErrorIs(t, err, messaging.ErrGroupAttemptConflict)
	bobSnapshot()

	// Accepted refuses Bob's token.
	err = aliceHandler.Accepted(token, time.Now().Unix())
	require.ErrorIs(t, err, messaging.ErrGroupAttemptConflict)
	bobSnapshot()

	// Failed refuses Bob's token.
	exhausted, err := aliceHandler.Failed(token)
	require.ErrorIs(t, err, messaging.ErrGroupAttemptConflict)
	require.False(t, exhausted)
	bobSnapshot()
}

func TestProcessAlicePresentedBobPendingD6ReplayDoesNotConsumeIt(t *testing.T) {
	bobFixture := newFailureFixture(t)
	newQueue := bobFixture.crashD6BeforeT2(t)
	bobEntry := bobFixture.outboxEntry(t).Entry
	require.Equal(t, newQueue, bobEntry.QueueID)
	require.NotNil(t, bobFixture.outboxEntry(t).RecoveryWitness, "Bob has an unconsumed D6 recovery witness")

	aliceSK := nostr.Generate()
	aliceNpub := common.EncodeNpub(aliceSK.Public())
	aliceHandler, err := NewFanoutAttemptHandlerFor(bobFixture.member.store, aliceNpub)
	require.NoError(t, err)

	// Alice attempts to reserve Bob's crashed entry: must fail Conflict and NOT consume witness.
	bobSnapshot := bobFixture.snapshot(t)
	_, err = aliceHandler.Reserve(bobEntry)
	require.ErrorIs(t, err, messaging.ErrGroupAttemptConflict)
	bobSnapshot() // Full DB row, outbox bytes, mtime unchanged!

	// Verify Bob's recovery witness is still present and row is still RecipientFailed.
	afterEntry := bobFixture.outboxEntry(t)
	require.NotNil(t, afterEntry.RecoveryWitness, "Bob's witness must remain unconsumed")
	require.Equal(t, string(RecipientFailed), bobFixture.row(t).State)

	// Bob's matching handler succeeds and replays D6.
	bobHandler, err := NewFanoutAttemptHandlerFor(bobFixture.member.store, bobFixture.key.LocalNpub)
	require.NoError(t, err)
	token, err := bobHandler.Reserve(bobEntry)
	require.NoError(t, err)
	require.Equal(t, newQueue, token.QueueID)
	require.Equal(t, bobFixture.eventID, token.EventID)
	require.Equal(t, "reserved", bobFixture.recovery(t).Phase)
}

func TestBaselineUnboundHandlerConsumesBobReplayContrast(t *testing.T) {
	// Proves that baseline unbound handler did not guard process identity:
	// an unbound handler would consume Bob's authorized pending replay.
	bobFixture := newFailureFixture(t)
	newQueue := bobFixture.crashD6BeforeT2(t)
	bobEntry := bobFixture.outboxEntry(t).Entry

	unboundHandler := NewFanoutAttemptHandler(bobFixture.member.store)
	token, err := unboundHandler.Reserve(bobEntry)
	require.NoError(t, err)
	require.Equal(t, newQueue, token.QueueID)
	require.Equal(t, "reserved", bobFixture.recovery(t).Phase)
}

func TestProcessAlicePresentedBobPendingO3ReplayDoesNotConsumeIt(t *testing.T) {
	bobFixture := newFailureFixture(t)
	token := bobFixture.startAttempt(t, 1)
	bobFixture.failO3BeforeT4(t, token)
	require.EqualValues(t, 0, bobFixture.row(t).RetryCount, "T4 aborted before DB commit")
	require.NotNil(t, bobFixture.outboxEntry(t).FailureResult, "Bob has an unconsumed O3 failure result")

	aliceSK := nostr.Generate()
	aliceNpub := common.EncodeNpub(aliceSK.Public())
	aliceHandler, err := NewFanoutAttemptHandlerFor(bobFixture.member.store, aliceNpub)
	require.NoError(t, err)

	// Alice presented with Bob's entry: must fail Conflict and NOT consume O3.
	bobSnapshot := bobFixture.snapshot(t)
	_, err = aliceHandler.Reserve(bobFixture.typedEntry(t))
	require.ErrorIs(t, err, messaging.ErrGroupAttemptConflict)
	bobSnapshot()

	// Verify Bob's row still has RetryCount == 0.
	require.EqualValues(t, 0, bobFixture.row(t).RetryCount, "Bob's O3 replay must remain unconsumed")

	// Bob's matching handler succeeds and replays O3/T4.
	bobHandler, err := NewFanoutAttemptHandlerFor(bobFixture.member.store, bobFixture.key.LocalNpub)
	require.NoError(t, err)
	nextToken, err := bobHandler.Reserve(bobFixture.typedEntry(t))
	require.NoError(t, err)
	require.EqualValues(t, 2, nextToken.AttemptGeneration)
	require.EqualValues(t, 1, bobFixture.row(t).RetryCount, "Bob's matching handler replayed O3/T4")
}

func TestAliceMatchingEntryWorks(t *testing.T) {
	f := newFailureFixture(t)
	aliceSK := nostr.Generate()
	aliceNpub := common.EncodeNpub(aliceSK.Public())

	recipientSK := nostr.Generate()
	recipient := common.EncodeNpub(recipientSK.Public())
	aliceEvent := testAgentEvent(t, "alice group message", aliceSK, recipientSK, true)
	aliceEventJSON, err := json.Marshal(aliceEvent)
	require.NoError(t, err)

	aliceEntry, _, err := messaging.EnqueueGroupOutboxEntry(string(aliceEventJSON), recipient, nil, 3)
	require.NoError(t, err)

	aliceKey := FanoutKey{
		LocalNpub:    aliceNpub,
		GroupID:      mustOpaque(t),
		EnvelopeType: EnvelopeMessage,
		SendKey:      mustOpaque(t),
	}
	now := time.Now().Unix()
	_, err = f.member.db.Exec(`INSERT INTO groupchat_fanout
        (local_npub,group_id,envelope_type,send_key,recipient_npub,event_id,event_json,queue_id,state,max_retries,retry_count,created_at,updated_at)
        VALUES (?,?,?,?,?,?,?,?,?,3,0,?,?)`,
		aliceKey.LocalNpub, aliceKey.GroupID, aliceKey.EnvelopeType, aliceKey.SendKey, recipient, aliceEntry.ID, string(aliceEventJSON), aliceEntry.QueueID, RecipientQueued, now, now)
	require.NoError(t, err)

	aliceHandler, err := NewFanoutAttemptHandlerFor(f.member.store, aliceNpub)
	require.NoError(t, err)

	token, err := aliceHandler.Reserve(aliceEntry)
	require.NoError(t, err)
	require.Equal(t, aliceEntry.ID, token.EventID)
	require.Equal(t, aliceNpub, token.Key.LocalNpub)
	require.Equal(t, recipient, token.Key.RecipientNpub)

	require.NoError(t, aliceHandler.Start(token, 1, now))
	require.NoError(t, aliceHandler.Accepted(token, now))

	var state string
	require.NoError(t, f.member.db.QueryRow(`SELECT state FROM groupchat_fanout WHERE local_npub=? AND event_id=?`, aliceNpub, aliceEntry.ID).Scan(&state))
	require.Equal(t, string(RecipientRelayAccepted), state)
}

func TestDuplicateEventIDAmbiguityResolvedBySQLPredicate(t *testing.T) {
	// Bob and Alice both have rows in the same DB with the same event_id.
	// UNIQUE(local_npub, event_id) allows this without changing global constraints.
	f := newFailureFixture(t)
	sharedEventID := f.eventID
	bobEntry := f.typedEntry(t)

	aliceSK := nostr.Generate()
	aliceNpub := common.EncodeNpub(aliceSK.Public())

	aliceKey := FanoutKey{
		LocalNpub:    aliceNpub,
		GroupID:      mustOpaque(t),
		EnvelopeType: EnvelopeMessage,
		SendKey:      mustOpaque(t),
	}
	now := time.Now().Unix()
	_, err := f.member.db.Exec(`INSERT INTO groupchat_fanout
        (local_npub,group_id,envelope_type,send_key,recipient_npub,event_id,event_json,queue_id,state,max_retries,retry_count,created_at,updated_at)
        VALUES (?,?,?,?,?,?,?,?,?,3,0,?,?)`,
		aliceKey.LocalNpub, aliceKey.GroupID, aliceKey.EnvelopeType, aliceKey.SendKey, f.recipient, sharedEventID, bobEntry.EventJSON, bobEntry.QueueID, RecipientQueued, now, now)
	require.NoError(t, err)

	// Verify both rows exist with the same event_id.
	var count int
	require.NoError(t, f.member.db.QueryRow(`SELECT COUNT(*) FROM groupchat_fanout WHERE event_id=?`, sharedEventID).Scan(&count))
	require.Equal(t, 2, count, "two rows for different identities share the same event_id")

	// Bob's bound handler selects Bob's row and succeeds without ambiguity:
	bobHandler, err := NewFanoutAttemptHandlerFor(f.member.store, f.key.LocalNpub)
	require.NoError(t, err)
	bobToken, err := bobHandler.Reserve(bobEntry)
	require.NoError(t, err)
	require.Equal(t, f.key.LocalNpub, bobToken.Key.LocalNpub)

	// Alice's bound handler queries with Alice's identity, never touching Bob's row:
	aliceHandler, err := NewFanoutAttemptHandlerFor(f.member.store, aliceNpub)
	require.NoError(t, err)
	_, err = aliceHandler.Reserve(bobEntry)
	require.ErrorIs(t, err, messaging.ErrGroupAttemptConflict)
}

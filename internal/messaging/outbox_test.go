package messaging

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"github.com/iDoris-ai/hyphae/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTempOutbox(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("HOME", tmpDir)
}

func TestGetOutboxPath_Error(t *testing.T) {
	// HOME is valid in test setup, so path should succeed
	setupTempOutbox(t)
	path, err := GetOutboxPath()
	require.NoError(t, err)
	assert.Contains(t, path, "outbox.json")
}

func TestLoadOutbox_New(t *testing.T) {
	setupTempOutbox(t)
	ob, err := LoadOutbox()
	require.NoError(t, err)
	assert.Empty(t, ob.Entries)
}

func TestAddToOutbox(t *testing.T) {
	setupTempOutbox(t)
	ob, err := LoadOutbox()
	require.NoError(t, err)

	event := &nostr.Event{
		Kind:    1,
		Content: "test",
	}
	event.ID = [32]byte{1}

	err = AddToOutbox(ob, event, "npub1test", []string{"wss://relay.aastar.io"})
	require.NoError(t, err)

	ob2, err := LoadOutbox()
	require.NoError(t, err)
	assert.Len(t, ob2.Entries, 1)
	assert.Equal(t, hex.EncodeToString(event.ID[:]), ob2.Entries[0].ID,
		"AddToOutbox must store the ID hex-encoded, not as raw bytes (see specs/m1.5/README.md's UTF-8-corruption known issue)")
	assert.Equal(t, "pending", ob2.Entries[0].Status)
}

// TestAddToOutbox_IDRoundTripsThroughJSONWithoutCorruption is the
// regression test for the specific failure this fix targets: a raw 32-byte
// event.ID stored as a Go string gets silently mangled by json.Marshal the
// moment any of its bytes aren't valid UTF-8 (replaced with U+FFFD), so the
// ID read back from disk stops matching the original event.ID entirely.
// Hex-encoding first means SaveOutbox is marshaling plain ASCII, which
// round-trips through JSON exactly.
func TestAddToOutbox_IDRoundTripsThroughJSONWithoutCorruption(t *testing.T) {
	setupTempOutbox(t)
	ob, err := LoadOutbox()
	require.NoError(t, err)

	event := &nostr.Event{Kind: 1, Content: "test"}
	// A byte sequence that is not valid UTF-8 on its own (0xFF is never a
	// valid UTF-8 leading byte) -- exactly the shape of ID that used to get
	// mangled by json.Marshal before this fix.
	event.ID = [32]byte{0xff, 0xfe, 0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d,
		0x0e, 0x0f, 0x10, 0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18, 0x19, 0x1a, 0x1b, 0x1c, 0x1d}

	require.NoError(t, AddToOutbox(ob, event, "npub1test", nil))

	ob2, err := LoadOutbox()
	require.NoError(t, err)
	require.Len(t, ob2.Entries, 1)

	decoded, err := hex.DecodeString(ob2.Entries[0].ID)
	require.NoError(t, err, "the stored ID must still be valid hex after a JSON round-trip")
	assert.Equal(t, event.ID[:], decoded, "decoding the stored ID must reproduce the exact original event.ID bytes")
}

func TestGetPendingOutbox(t *testing.T) {
	setupTempOutbox(t)
	ob := &types.Outbox{
		Entries: []types.OutboxEntry{
			{ID: "1", Status: "pending", RetryCount: 0, MaxRetries: 10},
			{ID: "2", Status: "sent", RetryCount: 0, MaxRetries: 10},
			{ID: "3", Status: "pending", RetryCount: 10, MaxRetries: 10},
			{ID: "4", Status: "pending", RetryCount: 5, MaxRetries: 10},
		},
	}

	pending := GetPendingOutbox(ob)
	assert.Len(t, pending, 2)
	assert.Equal(t, "1", pending[0].ID)
	assert.Equal(t, "4", pending[1].ID)
}

func TestUpdateOutboxStatus(t *testing.T) {
	setupTempOutbox(t)
	ob := &types.Outbox{
		Entries: []types.OutboxEntry{
			{ID: "1", Status: "pending"},
		},
	}
	err := SaveOutbox(ob)
	require.NoError(t, err)

	err = UpdateOutboxStatus(ob, "1", "sent")
	require.NoError(t, err)

	ob2, _ := LoadOutbox()
	assert.Equal(t, "sent", ob2.Entries[0].Status)
}

func TestIncrementOutboxRetry(t *testing.T) {
	setupTempOutbox(t)
	ob := &types.Outbox{
		Entries: []types.OutboxEntry{
			{ID: "1", Status: "pending", RetryCount: 0},
		},
	}
	err := SaveOutbox(ob)
	require.NoError(t, err)

	err = IncrementOutboxRetry(ob, "1")
	require.NoError(t, err)

	ob2, _ := LoadOutbox()
	assert.Equal(t, 1, ob2.Entries[0].RetryCount)
	assert.NotZero(t, ob2.Entries[0].LastAttempt)
}

func TestRemoveFromOutbox(t *testing.T) {
	setupTempOutbox(t)
	ob := &types.Outbox{
		Entries: []types.OutboxEntry{
			{ID: "1", Status: "pending"},
			{ID: "2", Status: "pending"},
		},
	}
	err := SaveOutbox(ob)
	require.NoError(t, err)

	err = RemoveFromOutbox(ob, "1")
	require.NoError(t, err)

	ob2, _ := LoadOutbox()
	assert.Len(t, ob2.Entries, 1)
	assert.Equal(t, "2", ob2.Entries[0].ID)
}

// TestRemoveFromOutbox_RefusesDuplicateID covers a Codex review finding:
// RemoveFromOutbox is called directly outside AttemptSend too (agent.go's
// normal `agent msg` send path cleans up a stale outbox entry after a
// successful publish, ignoring the error since usually there's nothing to
// clean up) -- AttemptSend's own duplicate-ID guard doesn't protect that
// call site at all. Hardening RemoveFromOutbox itself closes the gap for
// every caller, present or future, not just the ones already known about.
func TestRemoveFromOutbox_RefusesDuplicateID(t *testing.T) {
	setupTempOutbox(t)
	ob := &types.Outbox{Entries: []types.OutboxEntry{
		{ID: "dup", Status: "pending"},
		{ID: "dup", Status: "pending"},
		{ID: "other", Status: "pending"},
	}}
	require.NoError(t, SaveOutbox(ob))

	err := RemoveFromOutbox(ob, "dup")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "2 outbox entries share id")

	ob2, err := LoadOutbox()
	require.NoError(t, err)
	assert.Len(t, ob2.Entries, 3, "refusing must not remove anything, not even the unambiguous entry")
}

// TestAttemptSend_RefusesDuplicateID covers a Codex review finding: the
// original fix (retry --id refusing to proceed) only protected the manual
// CLI path -- the daemon's automatic retry loop calls AttemptSend directly
// with no duplicate-ID awareness of its own. Moving the guard into
// AttemptSend itself protects both callers, since RemoveFromOutbox on
// success would otherwise delete every entry sharing this ID, not just the
// one being sent.
func TestAttemptSend_RefusesDuplicateID(t *testing.T) {
	setupTempOutbox(t)
	ob := &types.Outbox{Entries: []types.OutboxEntry{
		{ID: "dup", Status: "pending", RetryCount: 0, MaxRetries: 10},
		{ID: "dup", Status: "pending", RetryCount: 0, MaxRetries: 10},
	}}
	require.NoError(t, SaveOutbox(ob))

	result, err := AttemptSend(context.Background(), ob, ob.Entries[0], nil, 200*time.Millisecond)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "2 outbox entries share this ID")
	assert.False(t, result.Attempted, "a duplicate-ID refusal never got as far as dialing a relay")
	assert.False(t, result.Sent)

	ob2, err := LoadOutbox()
	require.NoError(t, err)
	assert.Len(t, ob2.Entries, 2, "refusing must not touch either entry")
}

func TestAttemptSend_ParseError(t *testing.T) {
	setupTempOutbox(t)
	ob := &types.Outbox{Entries: []types.OutboxEntry{
		{ID: "1", Status: "pending", EventJSON: "{not valid json", MaxRetries: 10},
	}}
	require.NoError(t, SaveOutbox(ob))

	result, err := AttemptSend(context.Background(), ob, ob.Entries[0], nil, 200*time.Millisecond)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse event")
	assert.False(t, result.Sent)
	assert.False(t, result.Attempted, "a parse failure never got as far as dialing a relay")

	// A parse failure must not mutate the entry at all -- it never got far
	// enough to touch retry count or status.
	ob2, _ := LoadOutbox()
	assert.Equal(t, "pending", ob2.Entries[0].Status)
	assert.Zero(t, ob2.Entries[0].RetryCount)
}

// TestAttemptSend_RelayUnreachable_IncrementsRetry covers the failure path
// when every target relay is unreachable: retry count increments, status
// stays "pending" as long as retries remain.
func TestAttemptSend_RelayUnreachable_IncrementsRetry(t *testing.T) {
	setupTempOutbox(t)
	event := &nostr.Event{Kind: 1, Content: "hi"}
	event.ID = [32]byte{9}
	eventJSONBytes, err := json.Marshal(event)
	require.NoError(t, err)

	entry := types.OutboxEntry{
		ID: string(event.ID[:]), Status: "pending", EventJSON: string(eventJSONBytes),
		RetryCount: 0, MaxRetries: 10,
	}
	ob := &types.Outbox{Entries: []types.OutboxEntry{entry}}
	require.NoError(t, SaveOutbox(ob))

	result, err := AttemptSend(context.Background(), ob, entry, []string{"ws://127.0.0.1:1"}, 200*time.Millisecond)
	require.NoError(t, err)
	assert.True(t, result.Attempted)
	assert.False(t, result.Sent)
	assert.False(t, result.MarkedFailed, "far from MaxRetries yet")

	ob2, _ := LoadOutbox()
	assert.Equal(t, "pending", ob2.Entries[0].Status)
	assert.Equal(t, 1, ob2.Entries[0].RetryCount)
}

// TestAttemptSend_RelayUnreachable_MarksFailedAtMaxRetries covers the
// existing "mark failed once retries are exhausted" behavior, preserved
// exactly from the pre-refactor daemon.go logic (RetryCount >= MaxRetries-1
// before this attempt means this failed attempt pushes it over the edge).
func TestAttemptSend_RelayUnreachable_MarksFailedAtMaxRetries(t *testing.T) {
	setupTempOutbox(t)
	event := &nostr.Event{Kind: 1, Content: "hi"}
	event.ID = [32]byte{10}
	eventJSONBytes, err := json.Marshal(event)
	require.NoError(t, err)

	entry := types.OutboxEntry{
		ID: string(event.ID[:]), Status: "pending", EventJSON: string(eventJSONBytes),
		RetryCount: 9, MaxRetries: 10,
	}
	ob := &types.Outbox{Entries: []types.OutboxEntry{entry}}
	require.NoError(t, SaveOutbox(ob))

	result, err := AttemptSend(context.Background(), ob, entry, []string{"ws://127.0.0.1:1"}, 200*time.Millisecond)
	require.NoError(t, err)
	assert.True(t, result.Attempted)
	assert.False(t, result.Sent)
	assert.True(t, result.MarkedFailed)

	ob2, _ := LoadOutbox()
	assert.Equal(t, "failed", ob2.Entries[0].Status)
	assert.Equal(t, 10, ob2.Entries[0].RetryCount)
}

func TestCleanupOutbox(t *testing.T) {
	setupTempOutbox(t)
	ob := &types.Outbox{
		Entries: []types.OutboxEntry{
			{ID: "1", Status: "pending", LastAttempt: 9999999999},
			{ID: "2", Status: "sent", LastAttempt: 1},
			{ID: "3", Status: "failed", LastAttempt: 9999999999},
		},
	}
	err := SaveOutbox(ob)
	require.NoError(t, err)

	err = CleanupOutbox(ob, 1)
	require.NoError(t, err)

	ob2, _ := LoadOutbox()
	assert.Len(t, ob2.Entries, 2)
}

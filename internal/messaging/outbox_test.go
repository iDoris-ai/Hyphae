package messaging

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"github.com/iDoris-ai/hyphae/internal/identity"
	"github.com/iDoris-ai/hyphae/pkg/crypto"
	"github.com/iDoris-ai/hyphae/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTempOutbox(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
}

func TestOutboxProcessHelper(t *testing.T) {
	id := os.Getenv("HYPHAE_OUTBOX_HELPER_ID")
	if id == "" {
		return
	}
	ob, err := LoadOutbox()
	if err != nil {
		t.Fatal(err)
	}
	event := &nostr.Event{Kind: 1, Content: id}
	event.ID[31] = byte(id[len(id)-1])
	if err := AddToOutbox(ob, event, id, nil); err != nil {
		t.Fatal(err)
	}
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
	assert.NotEmpty(t, ob2.Entries[0].QueueID, "each enqueue gets a stable identity for snapshot-based cleanup")
	assert.Equal(t, "pending", ob2.Entries[0].Status)
}

func TestAddToOutboxWithNilSnapshot(t *testing.T) {
	setupTempOutbox(t)
	event := &nostr.Event{Kind: 1, Content: "test"}
	event.ID[31] = 42
	require.NoError(t, AddToOutbox(nil, event, "npub1test", nil))
	ob, err := LoadOutbox()
	require.NoError(t, err)
	require.Len(t, ob.Entries, 1)
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

func TestAttemptSend_LegacySnapshotCannotMatchNormalizedQueueID(t *testing.T) {
	setupTempOutbox(t)
	eventJSON := `{"kind":1,"content":"same"}`
	entry := types.OutboxEntry{ID: "legacy", EventJSON: eventJSON, Status: "pending", CreatedAt: 42, MaxRetries: 5}
	require.NoError(t, SaveOutbox(&types.Outbox{Entries: []types.OutboxEntry{entry}}))

	current, err := currentOutboxAttempt(entry)
	require.NoError(t, err)
	require.NotEmpty(t, current.entry.QueueID)

	var published bool
	result, err := attemptSend(context.Background(), nil, entry, nil, time.Second,
		func(context.Context, []string, nostr.Event, time.Duration) bool {
			published = true
			return false
		}, func(*nostr.Event, string, string, bool) error { return nil })
	require.ErrorIs(t, err, errOutboxEntrySuperseded)
	assert.True(t, result.Superseded)
	assert.False(t, result.Attempted)
	assert.False(t, published, "a legacy snapshot must not act on a queue item after identity normalization")
}

func TestAttemptSend_FailureUsesLatestRetryCount(t *testing.T) {
	setupTempOutbox(t)
	entry := testOutboxAttemptEntry("latest-count", 1, 10)
	require.NoError(t, SaveOutbox(&types.Outbox{Entries: []types.OutboxEntry{entry}}))
	ob, err := LoadOutbox()
	require.NoError(t, err)

	started, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	done := make(chan SendResult, 1)
	errCh := make(chan error, 1)
	go func() {
		result, err := attemptSend(context.Background(), ob, ob.Entries[0], nil, time.Second,
			func(context.Context, []string, nostr.Event, time.Duration) bool {
				close(started)
				<-release
				return false
			}, func(*nostr.Event, string, string, bool) error { return nil })
		done <- result
		errCh <- err
	}()
	waitForOutboxSignal(t, started)
	_, err = UpdateOutbox(func(latest *types.Outbox) error {
		latest.Entries[0].RetryCount = 9
		return nil
	})
	require.NoError(t, err)
	unblock()
	result := <-done
	require.NoError(t, <-errCh)
	assert.True(t, result.MarkedFailed)
	assert.False(t, result.Queued)

	latest, err := LoadOutbox()
	require.NoError(t, err)
	require.Len(t, latest.Entries, 1)
	assert.Equal(t, 10, latest.Entries[0].RetryCount)
	assert.Equal(t, "failed", latest.Entries[0].Status)
}

func TestAttemptSend_SuccessDoesNotRemoveReenqueuedSameID(t *testing.T) {
	setupTempOutbox(t)
	entry := testOutboxAttemptEntry("requeued", 0, 5)
	require.NoError(t, SaveOutbox(&types.Outbox{Entries: []types.OutboxEntry{entry}}))
	ob, err := LoadOutbox()
	require.NoError(t, err)

	started, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	done := make(chan SendResult, 1)
	errCh := make(chan error, 1)
	go func() {
		result, err := attemptSend(context.Background(), ob, ob.Entries[0], nil, time.Second,
			func(context.Context, []string, nostr.Event, time.Duration) bool {
				close(started)
				<-release
				return true
			}, func(*nostr.Event, string, string, bool) error { return nil })
		done <- result
		errCh <- err
	}()
	waitForOutboxSignal(t, started)
	newEntry := testOutboxAttemptEntry("requeued", 0, 5)
	newEntry.QueueID = "new-queue-id"
	_, err = UpdateOutbox(func(latest *types.Outbox) error {
		latest.Entries[0] = newEntry
		return nil
	})
	require.NoError(t, err)
	unblock()
	result := <-done
	require.NoError(t, <-errCh)
	assert.True(t, result.Sent)
	assert.True(t, result.HistoryStored)
	assert.True(t, result.Superseded)

	latest, err := LoadOutbox()
	require.NoError(t, err)
	require.Len(t, latest.Entries, 1)
	assert.Equal(t, "new-queue-id", latest.Entries[0].QueueID)
}

func TestAttemptSend_FailureAfterConcurrentSuccessDoesNotRestoreQueue(t *testing.T) {
	setupTempOutbox(t)
	entry := testOutboxAttemptEntry("overlap", 0, 5)
	require.NoError(t, SaveOutbox(&types.Outbox{Entries: []types.OutboxEntry{entry}}))
	ob, err := LoadOutbox()
	require.NoError(t, err)
	entry = ob.Entries[0]
	failureSnapshot, err := LoadOutbox()
	require.NoError(t, err)

	successStarted, failureStarted := make(chan struct{}), make(chan struct{})
	releaseSuccess, releaseFailure := make(chan struct{}), make(chan struct{})
	var releaseSuccessOnce, releaseFailureOnce sync.Once
	unblockSuccess := func() { releaseSuccessOnce.Do(func() { close(releaseSuccess) }) }
	unblockFailure := func() { releaseFailureOnce.Do(func() { close(releaseFailure) }) }
	defer unblockSuccess()
	defer unblockFailure()
	successDone := make(chan SendResult, 1)
	failureDone := make(chan SendResult, 1)
	errCh := make(chan error, 2)
	go func() {
		result, err := attemptSend(context.Background(), failureSnapshot, entry, nil, time.Second,
			func(context.Context, []string, nostr.Event, time.Duration) bool {
				close(successStarted)
				<-releaseSuccess
				return true
			}, func(*nostr.Event, string, string, bool) error { return nil })
		successDone <- result
		errCh <- err
	}()
	waitForOutboxSignal(t, successStarted)
	go func() {
		result, err := attemptSend(context.Background(), ob, entry, nil, time.Second,
			func(context.Context, []string, nostr.Event, time.Duration) bool {
				close(failureStarted)
				<-releaseFailure
				return false
			}, func(*nostr.Event, string, string, bool) error { return nil })
		failureDone <- result
		errCh <- err
	}()
	waitForOutboxSignal(t, failureStarted)

	unblockSuccess()
	success := <-successDone
	require.NoError(t, <-errCh)
	assert.True(t, success.Sent)
	assert.True(t, success.HistoryStored)
	unblockFailure()
	failure := <-failureDone
	require.NoError(t, <-errCh)
	assert.False(t, failure.Sent)
	assert.True(t, failure.Superseded)
	assert.False(t, failure.Queued)

	latest, err := LoadOutbox()
	require.NoError(t, err)
	assert.Empty(t, latest.Entries)
}

func waitForOutboxSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for blocked outbox attempt")
	}
}

func TestAttemptSend_HistoryFailureKeepsQueue(t *testing.T) {
	setupTempOutbox(t)
	entry := testOutboxAttemptEntry("history-error", 0, 5)
	require.NoError(t, SaveOutbox(&types.Outbox{Entries: []types.OutboxEntry{entry}}))
	ob, err := LoadOutbox()
	require.NoError(t, err)

	result, err := attemptSend(context.Background(), ob, ob.Entries[0], nil, time.Second,
		func(context.Context, []string, nostr.Event, time.Duration) bool { return true },
		func(*nostr.Event, string, string, bool) error { return errors.New("history unavailable") })
	require.Error(t, err)
	assert.True(t, result.Sent)
	assert.False(t, result.HistoryStored)
	assert.True(t, result.Queued)

	latest, err := LoadOutbox()
	require.NoError(t, err)
	require.Len(t, latest.Entries, 1)
	assert.Equal(t, entry.QueueID, latest.Entries[0].QueueID)
}

func TestAttemptSend_QueuePersistenceFailureIsVisible(t *testing.T) {
	setupTempOutbox(t)
	entry := testOutboxAttemptEntry("queue-error", 0, 5)
	require.NoError(t, SaveOutbox(&types.Outbox{Entries: []types.OutboxEntry{entry}}))
	ob, err := LoadOutbox()
	require.NoError(t, err)

	originalPersist := persistOutbox
	persistOutbox = func(string, *types.Outbox) error { return errors.New("disk unavailable") }
	t.Cleanup(func() { persistOutbox = originalPersist })
	result, err := attemptSend(context.Background(), ob, ob.Entries[0], nil, time.Second,
		func(context.Context, []string, nostr.Event, time.Duration) bool { return true },
		func(*nostr.Event, string, string, bool) error { return nil })
	require.Error(t, err)
	assert.True(t, result.Sent)
	assert.True(t, result.HistoryStored)
	assert.True(t, result.Queued)
	assert.False(t, result.QueueStateUnknown, "pre-rename failures leave the prior queue state intact")

	latest, err := LoadOutbox()
	require.NoError(t, err)
	require.Len(t, latest.Entries, 1)
	assert.Equal(t, entry.QueueID, latest.Entries[0].QueueID)
}

func TestAttemptSend_PostRenameSuccessRemovalReportsUnknown(t *testing.T) {
	setupTempOutbox(t)
	entry := testOutboxAttemptEntry("uncertain-success", 0, 5)
	require.NoError(t, SaveOutbox(&types.Outbox{Entries: []types.OutboxEntry{entry}}))
	ob, err := LoadOutbox()
	require.NoError(t, err)

	originalPersist := persistOutbox
	persistOutbox = func(file string, updated *types.Outbox) error {
		if err := originalPersist(file, updated); err != nil {
			return err
		}
		return &outboxCommitUncertainError{errors.New("directory sync failed after rename")}
	}
	t.Cleanup(func() { persistOutbox = originalPersist })
	result, err := attemptSend(context.Background(), ob, ob.Entries[0], nil, time.Second,
		func(context.Context, []string, nostr.Event, time.Duration) bool { return true },
		func(*nostr.Event, string, string, bool) error { return nil })
	require.Error(t, err)
	assert.True(t, result.Sent)
	assert.True(t, result.HistoryStored)
	assert.True(t, result.QueueStateUnknown)
	assert.False(t, result.Queued)
	assert.False(t, result.MarkedFailed)
	assert.False(t, result.Superseded)

	latest, err := LoadOutbox()
	require.NoError(t, err)
	assert.Empty(t, latest.Entries, "the post-rename state must not be rolled back")
}

func TestAttemptSend_PostRenameFailureIncrementReportsUnknown(t *testing.T) {
	setupTempOutbox(t)
	entry := testOutboxAttemptEntry("uncertain-failure", 4, 5)
	require.NoError(t, SaveOutbox(&types.Outbox{Entries: []types.OutboxEntry{entry}}))
	ob, err := LoadOutbox()
	require.NoError(t, err)

	originalPersist := persistOutbox
	persistOutbox = func(file string, updated *types.Outbox) error {
		if err := originalPersist(file, updated); err != nil {
			return err
		}
		return &outboxCommitUncertainError{errors.New("directory sync failed after rename")}
	}
	t.Cleanup(func() { persistOutbox = originalPersist })
	result, err := attemptSend(context.Background(), ob, ob.Entries[0], nil, time.Second,
		func(context.Context, []string, nostr.Event, time.Duration) bool { return false },
		func(*nostr.Event, string, string, bool) error { return nil })
	require.Error(t, err)
	assert.False(t, result.Sent)
	assert.True(t, result.QueueStateUnknown)
	assert.False(t, result.Queued)
	assert.False(t, result.MarkedFailed)

	latest, err := LoadOutbox()
	require.NoError(t, err)
	require.Len(t, latest.Entries, 1)
	assert.Equal(t, 5, latest.Entries[0].RetryCount)
	assert.Equal(t, "failed", latest.Entries[0].Status)
}

func TestAttemptSend_EncryptedHistoryDoesNotStoreCiphertextAsPlaintext(t *testing.T) {
	resetStore(t)
	t.Cleanup(ResetStoreForTest)
	event := testHistoryEvent(t, mustCompressText(t, "ciphertext"), nostr.Tags{{"enc", "nip44"}, {"z", CompressTag}})
	entry, ob := queueHistoryEvent(t, event)
	// The initial send already recorded local plaintext; a retry must preserve it.
	require.NoError(t, StoreOutgoingMessage(&event, "npub1recipient", "local secret", true))

	var published nostr.Event
	result, err := attemptSend(context.Background(), ob, entry, nil, time.Second,
		func(_ context.Context, _ []string, sent nostr.Event, _ time.Duration) bool {
			published = sent
			return true
		}, StoreOutgoingMessage)
	require.NoError(t, err)
	assert.True(t, result.Sent)
	assert.True(t, result.HistoryStored)
	assert.True(t, reflect.DeepEqual(event, published), "retry must publish the exact stored signed event")

	store, err := GetStore()
	require.NoError(t, err)
	stored, err := store.GetMessage(entry.ID)
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.True(t, stored.IsEncrypted)
	assert.Equal(t, "local secret", stored.Plaintext, "empty retry plaintext must preserve the already stored cleartext")
	assert.Equal(t, event.Content, stored.Content)
}

func TestAttemptSend_NewEncryptedHistoryLeavesPlaintextEmpty(t *testing.T) {
	resetStore(t)
	t.Cleanup(ResetStoreForTest)
	event := testHistoryEvent(t, mustCompressText(t, "ciphertext"), nostr.Tags{{"enc", "nip44"}, {"z", CompressTag}})
	entry, ob := queueHistoryEvent(t, event)
	result, err := attemptSend(context.Background(), ob, entry, nil, time.Second,
		func(context.Context, []string, nostr.Event, time.Duration) bool { return true }, StoreOutgoingMessage)
	require.NoError(t, err)
	assert.True(t, result.HistoryStored)

	store, err := GetStore()
	require.NoError(t, err)
	stored, err := store.GetMessage(entry.ID)
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.True(t, stored.IsEncrypted)
	assert.Empty(t, stored.Plaintext)
	assert.Equal(t, event.Content, stored.Content)
}

func TestAttemptSend_EncryptedHistoryRecoversWithUnlockedKeyStore(t *testing.T) {
	resetStore(t)
	t.Cleanup(ResetStoreForTest)
	ks, event, plaintext := realEncryptedHistoryFixture(t)
	entry, ob := queueHistoryEvent(t, event)

	result, err := attemptSendWithKeyStore(context.Background(), ob, entry, nil, time.Second,
		func(context.Context, []string, nostr.Event, time.Duration) bool { return true }, StoreOutgoingMessage, ks)
	require.NoError(t, err)
	assert.True(t, result.HistoryStored)
	assert.False(t, result.Queued)
	latest, err := LoadOutbox()
	require.NoError(t, err)
	assert.Empty(t, latest.Entries)
	stored, err := mustGetStoredMessage(t, entry.ID)
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.True(t, stored.IsEncrypted)
	assert.Equal(t, plaintext, stored.Plaintext)
	t.Logf("unlocked keystore: sent=%t historyStored=%t queued=%t plaintext=%q", result.Sent, result.HistoryStored, result.Queued, stored.Plaintext)
}

func TestAttemptSend_EncryptedHistoryLockedKeyStoreFallsBackAndClearsQueue(t *testing.T) {
	resetStore(t)
	t.Cleanup(ResetStoreForTest)
	_, event, _ := realEncryptedHistoryFixture(t)
	locked, err := identity.LoadKeyStore()
	require.NoError(t, err)
	require.Nil(t, locked.MasterKey)
	entry, ob := queueHistoryEvent(t, event)

	var logs bytes.Buffer
	oldOutput := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(oldOutput) })
	result, err := attemptSendWithKeyStore(context.Background(), ob, entry, nil, time.Second,
		func(context.Context, []string, nostr.Event, time.Duration) bool { return true }, StoreOutgoingMessage, locked)
	require.NoError(t, err)
	assert.True(t, result.Sent)
	assert.True(t, result.HistoryStored)
	assert.False(t, result.Queued)
	assert.Contains(t, logs.String(), "storing encrypted history with empty plaintext")

	latest, err := LoadOutbox()
	require.NoError(t, err)
	assert.Empty(t, latest.Entries, "locked key recovery falls back to established encrypted-history behavior")
	stored, err := mustGetStoredMessage(t, entry.ID)
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.True(t, stored.IsEncrypted)
	assert.Empty(t, stored.Plaintext)
	t.Logf("locked keystore: sent=%t historyStored=%t queued=%t outboxEntries=%d plaintext=%q warning=%q", result.Sent, result.HistoryStored, result.Queued, len(latest.Entries), stored.Plaintext, logs.String())
}

func TestAttemptSend_EncryptedHistoryFailureUsesRetryLimit(t *testing.T) {
	resetStore(t)
	t.Cleanup(ResetStoreForTest)
	ks, event, _ := realEncryptedHistoryFixture(t)
	entry, _ := queueHistoryEvent(t, event)
	entry.MaxRetries = 3
	require.NoError(t, SaveOutbox(&types.Outbox{Entries: []types.OutboxEntry{entry}}))

	for attempt := 0; attempt < 3; attempt++ {
		latest, err := LoadOutbox()
		require.NoError(t, err)
		require.Len(t, latest.Entries, 1)
		result, err := attemptSendWithKeyStore(context.Background(), latest, latest.Entries[0], nil, time.Second,
			func(context.Context, []string, nostr.Event, time.Duration) bool { return true },
			func(*nostr.Event, string, string, bool) error { return errors.New("history unavailable") }, ks)
		require.Error(t, err)
		assert.True(t, result.Sent)
		assert.Equal(t, attempt < 2, result.Queued)
	}
	latest, err := LoadOutbox()
	require.NoError(t, err)
	require.Len(t, latest.Entries, 1)
	assert.Equal(t, 3, latest.Entries[0].RetryCount)
	assert.NotZero(t, latest.Entries[0].LastAttempt)
	assert.Equal(t, "failed", latest.Entries[0].Status)
	assert.Empty(t, GetPendingOutbox(latest))
}

func realEncryptedHistoryFixture(t *testing.T) (*types.KeyStore, nostr.Event, string) {
	t.Helper()
	ks := &types.KeyStore{Identities: make(map[string]*types.Identity), Contacts: make(map[string]*types.Contact)}
	_, err := identity.CreateIdentityWithPassword(ks, "alice", "fixture-password")
	require.NoError(t, err)
	sender, err := identity.GetSecretKey(ks, "alice")
	require.NoError(t, err)
	recipient := nostr.Generate()
	plaintext := "real NIP-44 recovered plaintext"
	ciphertext, err := crypto.EncryptMessage(plaintext, sender, recipient.Public())
	require.NoError(t, err)
	compressed := mustCompressText(t, ciphertext)
	event := nostr.Event{
		CreatedAt: nostr.Now(), Kind: AgentKind, PubKey: sender.Public(), Content: compressed,
		Tags: nostr.Tags{{"p", recipient.Public().Hex()}, {"c", AgentTag}, {"v", AgentVersion}, {"z", CompressTag}, {"enc", "nip44"}},
	}
	require.NoError(t, event.Sign(sender))
	return ks, event, plaintext
}

func TestAttemptSend_UnencryptedHistoryUsesDecodedOrRawContent(t *testing.T) {
	for _, tc := range []struct {
		name      string
		content   string
		tags      nostr.Tags
		plaintext string
	}{
		{name: "compressed", content: mustCompressText(t, "decoded plaintext"), tags: nostr.Tags{{"z", CompressTag}}, plaintext: "decoded plaintext"},
		{name: "raw", content: "raw plaintext", plaintext: "raw plaintext"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetStore(t)
			t.Cleanup(ResetStoreForTest)
			event := testHistoryEvent(t, tc.content, tc.tags)
			entry, ob := queueHistoryEvent(t, event)
			result, err := attemptSend(context.Background(), ob, entry, nil, time.Second,
				func(context.Context, []string, nostr.Event, time.Duration) bool { return true }, StoreOutgoingMessage)
			require.NoError(t, err)
			assert.True(t, result.HistoryStored)

			store, err := GetStore()
			require.NoError(t, err)
			stored, err := store.GetMessage(entry.ID)
			require.NoError(t, err)
			require.NotNil(t, stored)
			assert.False(t, stored.IsEncrypted)
			assert.Equal(t, tc.plaintext, stored.Plaintext)
		})
	}
}

func TestAttemptSend_FailedPublishDoesNotStoreHistory(t *testing.T) {
	resetStore(t)
	t.Cleanup(ResetStoreForTest)
	event := testHistoryEvent(t, "no acknowledgement", nil)
	entry, ob := queueHistoryEvent(t, event)
	calledStore := false
	result, err := attemptSend(context.Background(), ob, entry, nil, time.Second,
		func(context.Context, []string, nostr.Event, time.Duration) bool { return false },
		func(*nostr.Event, string, string, bool) error {
			calledStore = true
			return StoreOutgoingMessage(&event, "npub1recipient", "", false)
		})
	require.NoError(t, err)
	assert.False(t, result.Sent)
	assert.False(t, calledStore)

	store, err := GetStore()
	require.NoError(t, err)
	stored, err := store.GetMessage(entry.ID)
	require.NoError(t, err)
	assert.Nil(t, stored)
}

func TestAttemptSend_UnknownEncryptionOrCompressionKeepsQueue(t *testing.T) {
	for _, tc := range []struct {
		name string
		tags nostr.Tags
	}{
		{name: "unknown encryption", tags: nostr.Tags{{"enc", "future-cipher"}}},
		{name: "unknown compression", tags: nostr.Tags{{"z", "gzip"}}},
		{name: "corrupt compression", tags: nostr.Tags{{"z", CompressTag}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetStore(t)
			t.Cleanup(ResetStoreForTest)
			event := testHistoryEvent(t, "not valid compressed data", tc.tags)
			entry, ob := queueHistoryEvent(t, event)
			result, err := attemptSend(context.Background(), ob, entry, nil, time.Second,
				func(context.Context, []string, nostr.Event, time.Duration) bool { return true }, StoreOutgoingMessage)
			require.Error(t, err)
			assert.True(t, result.Sent)
			assert.False(t, result.HistoryStored)
			assert.True(t, result.Queued)

			latest, err := LoadOutbox()
			require.NoError(t, err)
			require.Len(t, latest.Entries, 1)
			store, err := GetStore()
			require.NoError(t, err)
			stored, err := store.GetMessage(entry.ID)
			require.NoError(t, err)
			assert.Nil(t, stored)
		})
	}
}

func testHistoryEvent(t *testing.T, content string, tags nostr.Tags) nostr.Event {
	t.Helper()
	secret := nostr.Generate()
	recipient := nostr.Generate().Public()
	tags = append(nostr.Tags{{"p", hex.EncodeToString(recipient[:])}, {"c", AgentTag}, {"v", AgentVersion}}, tags...)
	event := nostr.Event{
		CreatedAt: nostr.Now(),
		Kind:      AgentKind,
		Tags:      tags,
		Content:   content,
		PubKey:    secret.Public(),
	}
	require.NoError(t, event.Sign(secret))
	return event
}

func queueHistoryEvent(t *testing.T, event nostr.Event) (types.OutboxEntry, *types.Outbox) {
	t.Helper()
	data, err := json.Marshal(event)
	require.NoError(t, err)
	entry := types.OutboxEntry{
		QueueID: "history-queue", ID: hex.EncodeToString(event.ID[:]), EventJSON: string(data),
		RecipientNpub: "npub1recipient", Status: "pending", MaxRetries: 5,
	}
	require.NoError(t, SaveOutbox(&types.Outbox{Entries: []types.OutboxEntry{entry}}))
	ob, err := LoadOutbox()
	require.NoError(t, err)
	return ob.Entries[0], ob
}

func mustCompressText(t *testing.T, text string) string {
	t.Helper()
	compressed, err := CompressText(text)
	require.NoError(t, err)
	return compressed
}

func testOutboxAttemptEntry(id string, retryCount, maxRetries int) types.OutboxEntry {
	event := nostr.Event{Kind: 1, Content: "test"}
	event.ID[31] = byte(len(id))
	eventJSON, _ := json.Marshal(event)
	return types.OutboxEntry{
		QueueID: id + "-queue", ID: hex.EncodeToString(event.ID[:]), EventJSON: string(eventJSON),
		Status: "pending", RetryCount: retryCount, MaxRetries: maxRetries,
	}
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

func TestOutboxConcurrentStaleMutationsPreserveAddsAndRemovals(t *testing.T) {
	setupTempOutbox(t)
	const count = 24
	seed := &types.Outbox{Entries: make([]types.OutboxEntry, count)}
	snapshots := make([]*types.Outbox, count)
	for i := range seed.Entries {
		seed.Entries[i] = types.OutboxEntry{ID: fmt.Sprintf("old-%d", i), Status: "failed"}
	}
	require.NoError(t, SaveOutbox(seed))
	for i := range snapshots {
		var err error
		snapshots[i], err = LoadOutbox()
		require.NoError(t, err)
	}

	start := make(chan struct{})
	errs := make(chan error, count)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			if err := RemoveFromOutbox(snapshots[i], fmt.Sprintf("old-%d", i)); err != nil {
				errs <- err
				return
			}
			event := &nostr.Event{Kind: 1, Content: fmt.Sprintf("new-%d", i)}
			event.ID[31] = byte(i + 1)
			if err := AddToOutbox(snapshots[i], event, "recipient", nil); err != nil {
				errs <- err
			}
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	got, err := LoadOutbox()
	require.NoError(t, err)
	require.Len(t, got.Entries, count)
	for _, entry := range got.Entries {
		assert.Contains(t, entry.ID, "00000000000000000000000000000000000000000000000000000000000000")
	}
}

func TestOutboxUpdatesAcrossProcesses(t *testing.T) {
	setupTempOutbox(t)
	const count = 6
	var wg sync.WaitGroup
	errs := make(chan error, count)
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cmd := exec.Command(os.Args[0], "-test.run=^TestOutboxProcessHelper$")
			cmd.Env = append(os.Environ(), "HYPHAE_OUTBOX_HELPER_ID=process-"+fmt.Sprint(i))
			if output, err := cmd.CombinedOutput(); err != nil {
				errs <- fmt.Errorf("child %d: %w: %s", i, err, output)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	ob, err := LoadOutbox()
	require.NoError(t, err)
	require.Len(t, ob.Entries, count)
	queueIDs := make(map[string]struct{}, count)
	for _, entry := range ob.Entries {
		require.NotEmpty(t, entry.QueueID)
		_, exists := queueIDs[entry.QueueID]
		assert.False(t, exists, "each enqueue must get a unique queue id")
		queueIDs[entry.QueueID] = struct{}{}
	}
}

func TestOutboxClearSnapshotPreservesEntryAddedByAnotherProcess(t *testing.T) {
	setupTempOutbox(t)
	seed := &types.Outbox{Entries: []types.OutboxEntry{{
		ID: "failed-before-prompt", QueueID: "queue-before-prompt", Status: "failed", RetryCount: 3,
	}}}
	require.NoError(t, SaveOutbox(seed))
	snapshot, err := LoadOutbox()
	require.NoError(t, err)
	require.Len(t, snapshot.Entries, 1)

	// Model a second process enqueueing while the CLI waits for the user's
	// clear confirmation. The confirmed snapshot must not delete this later
	// entry when the clear operation reloads and mutates the latest file.
	cmd := exec.Command(os.Args[0], "-test.run=^TestOutboxProcessHelper$")
	cmd.Env = append(os.Environ(), "HYPHAE_OUTBOX_HELPER_ID=arrived-during-clear-prompt")
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, string(output))

	updated, removed, err := clearConfirmedOutboxEntries(snapshot.Entries)
	require.NoError(t, err)
	assert.Equal(t, 1, removed)
	require.Len(t, updated.Entries, 1)
	assert.Equal(t, "arrived-during-clear-prompt", updated.Entries[0].RecipientNpub)
	assert.Equal(t, "pending", updated.Entries[0].Status)

	path, err := GetOutboxPath()
	require.NoError(t, err)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var persisted types.Outbox
	require.NoError(t, json.Unmarshal(data, &persisted), "cross-process clear must leave valid complete JSON")
	require.Len(t, persisted.Entries, 1)
	assert.Equal(t, updated.Entries[0].QueueID, persisted.Entries[0].QueueID)
}

func TestUpdateOutboxDoesNotOverwriteCorruptJSON(t *testing.T) {
	setupTempOutbox(t)
	path, err := GetOutboxPath()
	require.NoError(t, err)
	corrupt := []byte(`{"entries":[`)
	require.NoError(t, os.WriteFile(path, corrupt, 0600))

	_, err = UpdateOutbox(func(ob *types.Outbox) error {
		ob.Entries = append(ob.Entries, types.OutboxEntry{ID: "must-not-write"})
		return nil
	})
	require.Error(t, err)
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, corrupt, got)
}

func TestSaveOutboxCleansTemporaryFileAfterRenameFailure(t *testing.T) {
	setupTempOutbox(t)
	path, err := GetOutboxPath()
	require.NoError(t, err)
	require.NoError(t, os.Mkdir(path, 0700))
	require.Error(t, SaveOutbox(&types.Outbox{}), "renaming over a directory should fail")
	temps, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".outbox-*.tmp"))
	require.NoError(t, err)
	assert.Empty(t, temps)
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.True(t, info.IsDir(), "failed replacement must preserve the existing path")
}

func TestClearSnapshotLeavesNewMatchingIDEntry(t *testing.T) {
	setupTempOutbox(t)
	confirmed := types.OutboxEntry{QueueID: "old-key", ID: "same-id", Status: "failed", EventJSON: `{"content":"same"}`}
	require.NoError(t, SaveOutbox(&types.Outbox{Entries: []types.OutboxEntry{confirmed}}))
	_, err := UpdateOutbox(func(ob *types.Outbox) error {
		ob.Entries = []types.OutboxEntry{{QueueID: "new-key", ID: "same-id", Status: "failed", EventJSON: `{"content":"same"}`}}
		return nil
	})
	require.NoError(t, err)
	_, removed, err := clearConfirmedOutboxEntries([]types.OutboxEntry{confirmed})
	require.NoError(t, err)
	assert.Zero(t, removed, "the old snapshot no longer exists")
	latest, err := LoadOutbox()
	require.NoError(t, err)
	require.Len(t, latest.Entries, 1)
	assert.Equal(t, "new-key", latest.Entries[0].QueueID)
}

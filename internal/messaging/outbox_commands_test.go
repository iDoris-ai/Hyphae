package messaging

import (
	"context"
	"io"
	"os"
	"testing"

	"github.com/iDoris-ai/hyphae/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureStdout runs fn with os.Stdout redirected to a pipe and returns
// everything written to it. There's no existing helper for this in the
// codebase (CLI Actions print directly rather than taking an io.Writer), so
// this is the smallest way to assert on `outbox list`'s table output.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w

	fn()

	require.NoError(t, w.Close())
	os.Stdout = orig

	out, err := io.ReadAll(r)
	require.NoError(t, err)
	return string(out)
}

// withStdin temporarily replaces os.Stdin with the given content for the
// duration of fn, for testing the clear --failed confirmation prompt.
func withStdin(t *testing.T, content string, fn func()) {
	t.Helper()
	orig := os.Stdin
	r, w, err := os.Pipe()
	require.NoError(t, err)
	_, err = w.WriteString(content)
	require.NoError(t, err)
	require.NoError(t, w.Close())
	os.Stdin = r

	fn()

	os.Stdin = orig
}

func TestOutboxListCmd_ShowsStatusRetriesAndDuplicateIDWarning(t *testing.T) {
	setupTempOutbox(t)
	ob := &types.Outbox{Entries: []types.OutboxEntry{
		{ID: "a", Status: "pending", RetryCount: 1, MaxRetries: 10, CreatedAt: 1},
		{ID: "b", Status: "pending", RetryCount: 10, MaxRetries: 10, CreatedAt: 1}, // stuck: pending but retries exhausted
		{ID: "c", Status: "sent", RetryCount: 0, MaxRetries: 10, CreatedAt: 1},
		{ID: "dup", Status: "pending", RetryCount: 0, MaxRetries: 10, CreatedAt: 1},
		{ID: "dup", Status: "pending", RetryCount: 0, MaxRetries: 10, CreatedAt: 1},
	}}
	require.NoError(t, SaveOutbox(ob))

	out := captureStdout(t, func() {
		require.NoError(t, outboxListCmd.Run(context.Background(), []string{"list"}))
	})

	assert.Contains(t, out, "pending (stuck)", "an exhausted-retry pending entry must be flagged as stuck")
	assert.Contains(t, out, "⚠️dup", "entries sharing an ID must be flagged")
	assert.Contains(t, out, "sent")
	assert.Contains(t, out, "5 entries")
}

func TestOutboxListCmd_FailedOnlyExcludesHealthyEntries(t *testing.T) {
	setupTempOutbox(t)
	ob := &types.Outbox{Entries: []types.OutboxEntry{
		{ID: "healthy", Status: "pending", RetryCount: 1, MaxRetries: 10, CreatedAt: 1},
		{ID: "stuck", Status: "pending", RetryCount: 10, MaxRetries: 10, CreatedAt: 1},
		{ID: "failed", Status: "failed", RetryCount: 10, MaxRetries: 10, CreatedAt: 1},
		{ID: "sent", Status: "sent", RetryCount: 0, MaxRetries: 10, CreatedAt: 1},
	}}
	require.NoError(t, SaveOutbox(ob))

	out := captureStdout(t, func() {
		require.NoError(t, outboxListCmd.Run(context.Background(), []string{"list", "--failed-only"}))
	})

	assert.NotContains(t, out, "healthy")
	assert.NotContains(t, out, "\tsent\t")
	assert.Contains(t, out, "stuck")
	assert.Contains(t, out, "failed")
}

func TestOutboxListCmd_EmptyOutbox(t *testing.T) {
	setupTempOutbox(t)
	require.NoError(t, SaveOutbox(&types.Outbox{}))

	out := captureStdout(t, func() {
		require.NoError(t, outboxListCmd.Run(context.Background(), []string{"list"}))
	})
	assert.Contains(t, out, "empty")
}

// TestOutboxClearCmd_RequiresConfirmationWithoutYes covers acceptance
// criterion 2: without --yes, a "n" (or anything but y/yes) answer must
// leave the outbox untouched.
func TestOutboxClearCmd_RequiresConfirmationWithoutYes(t *testing.T) {
	setupTempOutbox(t)
	ob := &types.Outbox{Entries: []types.OutboxEntry{
		{ID: "a", Status: "failed", RetryCount: 10, MaxRetries: 10},
	}}
	require.NoError(t, SaveOutbox(ob))

	withStdin(t, "n\n", func() {
		_ = captureStdout(t, func() {
			require.NoError(t, outboxClearCmd.Run(context.Background(), []string{"clear", "--failed"}))
		})
	})

	ob2, err := LoadOutbox()
	require.NoError(t, err)
	assert.Len(t, ob2.Entries, 1, "declining the confirmation must not remove anything")
}

// TestOutboxClearCmd_YesRemovesOnlyEntriesOverThreshold covers acceptance
// criterion 2's other half: --yes clears matching entries and leaves
// healthy ones (below the failure threshold) alone.
func TestOutboxClearCmd_YesRemovesOnlyEntriesOverThreshold(t *testing.T) {
	setupTempOutbox(t)
	ob := &types.Outbox{Entries: []types.OutboxEntry{
		{ID: "healthy", Status: "pending", RetryCount: 1, MaxRetries: 10},
		{ID: "over-threshold", Status: "pending", RetryCount: 6, MaxRetries: 10},
		{ID: "explicitly-failed", Status: "failed", RetryCount: 2, MaxRetries: 10},
	}}
	require.NoError(t, SaveOutbox(ob))

	out := captureStdout(t, func() {
		require.NoError(t, outboxClearCmd.Run(context.Background(), []string{"clear", "--failed", "--yes", "--min-failures", "5"}))
	})
	assert.Contains(t, out, "Removed 2")

	ob2, err := LoadOutbox()
	require.NoError(t, err)
	require.Len(t, ob2.Entries, 1)
	assert.Equal(t, "healthy", ob2.Entries[0].ID)
}

func TestOutboxClearCmd_NothingToClear(t *testing.T) {
	setupTempOutbox(t)
	ob := &types.Outbox{Entries: []types.OutboxEntry{
		{ID: "healthy", Status: "pending", RetryCount: 0, MaxRetries: 10},
	}}
	require.NoError(t, SaveOutbox(ob))

	out := captureStdout(t, func() {
		require.NoError(t, outboxClearCmd.Run(context.Background(), []string{"clear", "--failed", "--yes"}))
	})
	assert.Contains(t, out, "Nothing to clear")

	ob2, err := LoadOutbox()
	require.NoError(t, err)
	assert.Len(t, ob2.Entries, 1)
}

func TestOutboxRetryCmd_UnknownIDErrors(t *testing.T) {
	setupTempOutbox(t)
	require.NoError(t, SaveOutbox(&types.Outbox{}))

	err := outboxRetryCmd.Run(context.Background(), []string{"retry", "--id", "does-not-exist"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does-not-exist")
}

// TestOutboxRetryCmd_DuplicateIDRefusesToRetry covers a real bug found in
// Codex review: AttemptSend's RemoveFromOutbox removes every entry sharing
// an ID, not just the one retried, so retrying one of several colliding
// entries could silently delete unrelated, still-unsent siblings on
// success. Refusing outright is safer than guessing which one "the first"
// is, since the whole point of duplicate IDs (see specs/m1.5/README.md) is
// that they're indistinguishable by ID alone.
func TestOutboxRetryCmd_DuplicateIDRefusesToRetry(t *testing.T) {
	setupTempOutbox(t)
	ob := &types.Outbox{Entries: []types.OutboxEntry{
		{ID: "dup", Status: "pending", RetryCount: 0, MaxRetries: 10},
		{ID: "dup", Status: "pending", RetryCount: 0, MaxRetries: 10},
	}}
	require.NoError(t, SaveOutbox(ob))

	err := outboxRetryCmd.Run(context.Background(), []string{"retry", "--id", displayOutboxID("dup")})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "2 entries share id")

	ob2, err := LoadOutbox()
	require.NoError(t, err)
	assert.Len(t, ob2.Entries, 2, "refusing to retry must not touch either entry")
}

// TestFindOutboxMatches_HexFirstThenLiteralFallback covers a Codex review
// finding: a value that happens to be valid hex but was meant literally
// must still be reachable, not permanently shadowed by the hex-decode path.
func TestFindOutboxMatches_HexFirstThenLiteralFallback(t *testing.T) {
	entries := []types.OutboxEntry{
		{ID: "\xde\xad\xbe\xef", Status: "pending"}, // the bytes "deadbeef" decodes to
		{ID: "deadbeef", Status: "pending"},         // the literal string itself
	}

	// "deadbeef" is valid hex, so it must match the entry whose ID is the
	// *decoded* bytes first (matching what `list` displays for that entry).
	hexMatches := findOutboxMatches(entries[:1], "deadbeef")
	require.Len(t, hexMatches, 1)
	assert.Equal(t, "\xde\xad\xbe\xef", hexMatches[0].ID)

	// With only the literal entry present, the same input must still find
	// it via the literal fallback (decoded bytes match nothing).
	literalMatches := findOutboxMatches(entries[1:], "deadbeef")
	require.Len(t, literalMatches, 1)
	assert.Equal(t, "deadbeef", literalMatches[0].ID)
}

func TestIsFailedOrStuck(t *testing.T) {
	assert.True(t, isFailedOrStuck(types.OutboxEntry{Status: "failed"}))
	assert.True(t, isFailedOrStuck(types.OutboxEntry{Status: "pending", RetryCount: 10, MaxRetries: 10}))
	assert.False(t, isFailedOrStuck(types.OutboxEntry{Status: "pending", RetryCount: 1, MaxRetries: 10}))
	assert.False(t, isFailedOrStuck(types.OutboxEntry{Status: "sent"}))
}

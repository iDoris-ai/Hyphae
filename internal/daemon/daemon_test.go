package daemon

import (
	"context"
	"testing"

	"github.com/AuraAIHQ/agent-speaker/internal/messaging"
	"github.com/AuraAIHQ/agent-speaker/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestProcessOutbox_SkipsDuplicateIDEntriesWithoutMutating covers a Codex
// review finding on specs/m1.5/tasks/08-daemon-outbox-diagnostics.md: the
// original duplicate-ID fix only guarded the manual `storage outbox retry`
// CLI command, but the daemon's automatic retry loop calls
// messaging.AttemptSend directly with no awareness of its own -- a
// successful send would have deleted every entry sharing that ID via
// RemoveFromOutbox, not just the one processed. The guard now lives inside
// AttemptSend itself, so this exercises it through processOutbox (the
// actual daemon code path), not just through AttemptSend directly.
func TestProcessOutbox_SkipsDuplicateIDEntriesWithoutMutating(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	ob := &types.Outbox{Entries: []types.OutboxEntry{
		{ID: "dup", Status: "pending", RetryCount: 0, MaxRetries: 10},
		{ID: "dup", Status: "pending", RetryCount: 0, MaxRetries: 10},
	}}
	require.NoError(t, messaging.SaveOutbox(ob))

	assert.NotPanics(t, func() {
		processOutbox(context.Background(), &types.Identity{Nickname: "test"}, []string{"ws://127.0.0.1:1"})
	})

	ob2, err := messaging.LoadOutbox()
	require.NoError(t, err)
	assert.Len(t, ob2.Entries, 2, "duplicate-ID entries must be left untouched, not deleted or corrupted")
}

func TestIsAutoReplyMessage(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"[auto-reply] bob received your message", true},
		{"[auto-reply] alice received your message: hello", true},
		{"Hello, how are you?", false},
		{"", false},
		{"[auto-reply]", false}, // too short
		{"not [auto-reply] prefix", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, isAutoReplyMessage(tt.input))
		})
	}
}

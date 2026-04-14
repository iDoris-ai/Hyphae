package messaging

import (
	"os"
	"testing"

	"fiatjaf.com/nostr"
	"github.com/AuraAIHQ/agent-speaker/pkg/types"
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
		Kind: 1,
		Content: "test",
	}
	event.ID = [32]byte{1}

	err = AddToOutbox(ob, event, "npub1test", []string{"wss://relay.aastar.io"})
	require.NoError(t, err)

	ob2, err := LoadOutbox()
	require.NoError(t, err)
	assert.Len(t, ob2.Entries, 1)
	assert.Equal(t, string(event.ID[:]), ob2.Entries[0].ID)
	assert.Equal(t, "pending", ob2.Entries[0].Status)
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

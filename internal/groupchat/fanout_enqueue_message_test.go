package groupchat

import (
	"testing"

	"github.com/iDoris-ai/hyphae/internal/messaging"
	"github.com/stretchr/testify/require"
)

func TestEnqueueMessageFanoutPersistsQueuedEntries(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	g := newThreeMemberGroup(t)
	g.activate()
	logicalID := mustOpaque(t)
	report, err := g.alice.store.EnqueueMessageFanout(g.alice.sk, g.draft.Group.ID, logicalID, "persist me", 1234, 7)
	require.NoError(t, err)
	require.Equal(t, logicalID, report.LogicalID)
	require.Len(t, report.Recipients, 2)
	ids := map[string]string{}
	for _, recipient := range report.Recipients {
		require.Equal(t, RecipientQueued, recipient.State)
		require.NotEmpty(t, recipient.QueueID)
		require.NotEmpty(t, recipient.EventID)
		require.Zero(t, recipient.Attempts)
		ids[recipient.EventID] = recipient.QueueID
	}
	ob, err := messaging.LoadOutbox()
	require.NoError(t, err)
	pending := messaging.GetPendingGroupOutbox(ob)
	require.Len(t, pending, 2)
	for _, entry := range pending {
		require.Equal(t, ids[entry.ID], entry.QueueID)
		require.Equal(t, messaging.OutboxStatusGroupPending, entry.Status)
		require.Equal(t, 0, entry.RetryCount)
	}
	again, err := g.alice.store.EnqueueMessageFanout(g.alice.sk, g.draft.Group.ID, logicalID, "persist me", 9999, 7)
	require.NoError(t, err)
	require.Equal(t, report.Recipients[0].QueueID, again.Recipients[0].QueueID)
	require.Equal(t, report.Recipients[1].QueueID, again.Recipients[1].QueueID)
	latest, err := messaging.LoadOutbox()
	require.NoError(t, err)
	require.Len(t, messaging.GetPendingGroupOutbox(latest), 2)
}

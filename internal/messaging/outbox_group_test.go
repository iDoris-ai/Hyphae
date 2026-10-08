package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"github.com/iDoris-ai/hyphae/internal/storage"
	"github.com/iDoris-ai/hyphae/pkg/types"
	"github.com/stretchr/testify/require"
)

func groupOutboxEventJSON(t *testing.T) string {
	t.Helper()
	event := nostr.Event{Kind: 30078, CreatedAt: nostr.Now(), Content: "frozen ciphertext"}
	require.NoError(t, event.Sign(nostr.Generate()))
	data, err := json.Marshal(event)
	require.NoError(t, err)
	return string(data)
}

func TestGroupOutboxEnqueueAndRequeue(t *testing.T) {
	setupTempOutbox(t)
	eventJSON := groupOutboxEventJSON(t)
	relays := []string{"wss://relay.example"}
	first, existed, err := EnqueueGroupOutboxEntry(eventJSON, "recipient", relays, 2)
	require.NoError(t, err)
	require.False(t, existed)
	require.NotEmpty(t, first.QueueID)
	require.Equal(t, OutboxRouteGroup, first.Route)
	require.Equal(t, OutboxStatusGroupPending, first.Status)
	require.Equal(t, eventJSON, first.EventJSON)
	relays[0] = "changed"
	require.Equal(t, "wss://relay.example", first.Relays[0])
	for _, enqueue := range []func(string, string, []string, int) (types.OutboxEntry, bool, error){EnqueueGroupOutboxEntry, RequeueGroupOutboxEntry} {
		entry, existed, err := enqueue(eventJSON, "recipient", nil, 10)
		require.NoError(t, err)
		require.True(t, existed)
		require.Equal(t, first, entry)
	}
	ob, err := LoadOutbox()
	require.NoError(t, err)
	require.Equal(t, []types.OutboxEntry{first}, GetPendingGroupOutbox(ob))
	require.Equal(t, []types.OutboxEntry{first}, GroupOutboxEntriesByEventID(ob, first.ID))
	require.Empty(t, GetPendingOutbox(ob))
	queued, superseded, unknown := inspectAttemptQueue(first)
	require.True(t, queued)
	require.False(t, superseded || unknown)
	result, err := recordAttemptFailure(ob, first, SendResult{})
	require.NoError(t, err)
	require.True(t, result.Queued)
	require.False(t, result.MarkedFailed)
	result, err = recordAttemptFailure(ob, first, SendResult{})
	require.NoError(t, err)
	require.False(t, result.Queued)
	require.True(t, result.MarkedFailed)
	require.Equal(t, OutboxStatusGroupFailed, ob.Entries[0].Status)
	require.True(t, isFailedOrStuck(ob.Entries[0]))
	require.Empty(t, GetPendingGroupOutbox(ob))
	failed, existed, err := EnqueueGroupOutboxEntry(eventJSON, "recipient", nil, 2)
	require.NoError(t, err)
	require.True(t, existed)
	require.Equal(t, ob.Entries[0], failed)
	requeued, existed, err := RequeueGroupOutboxEntry(eventJSON, "recipient", nil, 3)
	require.NoError(t, err)
	require.False(t, existed)
	require.NotEqual(t, first.QueueID, requeued.QueueID)
	require.Equal(t, first.ID, requeued.ID)
	require.Equal(t, eventJSON, requeued.EventJSON)
	require.Zero(t, requeued.RetryCount)
	require.Zero(t, requeued.LastAttempt)
	require.Equal(t, OutboxStatusGroupPending, requeued.Status)
	ob, err = LoadOutbox()
	require.NoError(t, err)
	require.Equal(t, []types.OutboxEntry{requeued}, ob.Entries)
	queued, superseded, unknown = inspectAttemptQueue(first)
	require.False(t, queued || unknown)
	require.True(t, superseded)
}

func TestGroupOutboxConcurrentIdempotency(t *testing.T) {
	setupTempOutbox(t)
	eventJSON := groupOutboxEventJSON(t)
	for _, enqueue := range []func(string, string, []string, int) (types.OutboxEntry, bool, error){EnqueueGroupOutboxEntry, RequeueGroupOutboxEntry} {
		var wg sync.WaitGroup
		entries := make([]types.OutboxEntry, 12)
		errs := make([]error, len(entries))
		for i := range entries {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				entries[i], _, errs[i] = enqueue(eventJSON, "recipient", nil, 2)
			}(i)
		}
		wg.Wait()
		for i := range entries {
			require.NoError(t, errs[i])
			require.Equal(t, entries[0], entries[i])
		}
		ob, err := LoadOutbox()
		require.NoError(t, err)
		require.Len(t, ob.Entries, 1)
		ob.Entries[0].Status = OutboxStatusGroupFailed
		require.NoError(t, SaveOutbox(ob))
	}
}

func TestGroupOutboxRejectsCorruptionAndCollisions(t *testing.T) {
	setupTempOutbox(t)
	eventJSON := groupOutboxEventJSON(t)
	entry, _, err := EnqueueGroupOutboxEntry(eventJSON, "recipient", nil, 2)
	require.NoError(t, err)
	fixtures := []types.OutboxEntry{entry, entry, entry, entry}
	fixtures[0].Status = "pending"
	fixtures[1].Route = ""
	fixtures[2].RecipientNpub = "different recipient"
	fixtures[3].EventJSON += " "
	for _, fixture := range fixtures {
		require.NoError(t, SaveOutbox(&types.Outbox{Entries: []types.OutboxEntry{fixture}}))
		for _, enqueue := range []func(string, string, []string, int) (types.OutboxEntry, bool, error){EnqueueGroupOutboxEntry, RequeueGroupOutboxEntry} {
			_, _, err := enqueue(eventJSON, "recipient", nil, 2)
			require.Error(t, err)
			ob, err := LoadOutbox()
			require.NoError(t, err)
			require.Equal(t, []types.OutboxEntry{fixture}, ob.Entries)
		}
	}
	require.NoError(t, SaveOutbox(&types.Outbox{Entries: []types.OutboxEntry{entry, entry}}))
	_, _, err = RequeueGroupOutboxEntry(eventJSON, "recipient", nil, 2)
	require.Error(t, err)
	ob, err := LoadOutbox()
	require.NoError(t, err)
	require.Len(t, ob.Entries, 2)
}

func TestGroupOutboxValidationAndReadHelpers(t *testing.T) {
	setupTempOutbox(t)
	eventJSON := groupOutboxEventJSON(t)
	for _, enqueue := range []func(string, string, []string, int) (types.OutboxEntry, bool, error){EnqueueGroupOutboxEntry, RequeueGroupOutboxEntry} {
		for _, invalid := range []string{"", "{}", "null", "broken", `{ "id": "invalid" }`} {
			_, _, err := enqueue(invalid, "recipient", nil, 2)
			require.Error(t, err)
		}
		_, _, err := enqueue(eventJSON, "", nil, 2)
		require.Error(t, err)
		_, _, err = enqueue(eventJSON, "recipient", nil, 0)
		require.Error(t, err)
	}
	require.Empty(t, GetPendingGroupOutbox(nil))
	require.Empty(t, GroupOutboxEntriesByEventID(nil, "absent"))
	for _, route := range []string{"", OutboxRouteGroup} {
		for _, status := range []string{"pending", "failed", "sent", OutboxStatusGroupPending, OutboxStatusGroupFailed} {
			entry := types.OutboxEntry{Route: route, Status: status, MaxRetries: 2, RetryCount: 2}
			wantValid := (route == OutboxRouteGroup) == (status == OutboxStatusGroupPending || status == OutboxStatusGroupFailed)
			require.Equal(t, wantValid, validGroupOutboxEntry(entry))
			require.Empty(t, GetPendingGroupOutbox(&types.Outbox{Entries: []types.OutboxEntry{entry}}))
			require.Equal(t, wantValid && status != "sent", isFailedOrStuck(entry))
		}
	}
}

func TestGroupOutboxRequeueCommitFailureKeepsOriginal(t *testing.T) {
	setupTempOutbox(t)
	eventJSON := groupOutboxEventJSON(t)
	entry, _, err := EnqueueGroupOutboxEntry(eventJSON, "recipient", nil, 2)
	require.NoError(t, err)
	entry.Status = OutboxStatusGroupFailed
	require.NoError(t, SaveOutbox(&types.Outbox{Entries: []types.OutboxEntry{entry}}))
	original := persistOutbox
	t.Cleanup(func() { persistOutbox = original })
	persistOutbox = func(string, *types.Outbox) error { return errors.New("write failed") }
	returned, existed, err := RequeueGroupOutboxEntry(eventJSON, "recipient", nil, 2)
	require.Error(t, err)
	require.False(t, existed)
	require.Empty(t, returned)
	ob, err := LoadOutbox()
	require.NoError(t, err)
	require.Equal(t, []types.OutboxEntry{entry}, ob.Entries)
}

func TestGroupOutboxCorruptFailureDoesNotMutate(t *testing.T) {
	setupTempOutbox(t)
	entry := types.OutboxEntry{QueueID: "queue", ID: "event", Route: OutboxRouteGroup, Status: "pending", MaxRetries: 2}
	ob := &types.Outbox{Entries: []types.OutboxEntry{entry}}
	require.NoError(t, SaveOutbox(ob))
	result, err := recordAttemptFailure(ob, entry, SendResult{})
	require.Error(t, err)
	require.False(t, result.Queued || result.MarkedFailed)
	latest, err := LoadOutbox()
	require.NoError(t, err)
	require.Equal(t, []types.OutboxEntry{entry}, latest.Entries)
}

func TestCleanupOutboxKeepsGroupPending(t *testing.T) {
	setupTempOutbox(t)
	ob := &types.Outbox{Entries: []types.OutboxEntry{
		{ID: "new-group", Route: OutboxRouteGroup, Status: OutboxStatusGroupPending},
		{ID: "corrupt-group", Route: OutboxRouteGroup, Status: "failed"},
		{ID: "corrupt-dm", Status: OutboxStatusGroupFailed},
		{ID: "legacy-dm", Status: "pending"},
		{ID: "old-group", Route: OutboxRouteGroup, Status: OutboxStatusGroupFailed},
		{ID: "old-dm", Status: "failed"},
		{ID: "sent-dm", Status: "sent"},
		{ID: "recent-group", Route: OutboxRouteGroup, Status: OutboxStatusGroupFailed, LastAttempt: time.Now().Unix()},
	}}
	require.NoError(t, SaveOutbox(ob))
	require.NoError(t, CleanupOutbox(ob, time.Hour))
	ids := []string{}
	for _, entry := range ob.Entries {
		ids = append(ids, entry.ID)
	}
	require.Equal(t, []string{"new-group", "corrupt-group", "corrupt-dm", "legacy-dm", "recent-group"}, ids)
}

func TestLegacyBinaryIgnoresGroupEntries(t *testing.T) {
	setupTempOutbox(t)
	ResetStoreForTest()
	t.Cleanup(ResetStoreForTest)
	eventJSON := groupOutboxEventJSON(t)
	entry, _, err := EnqueueGroupOutboxEntry(eventJSON, "recipient", nil, 2)
	require.NoError(t, err)
	// Decode the actual disk bytes into the pre-route schema, then run main's
	// original pending selection and attemptSend combination.
	type legacyOutboxEntry struct {
		QueueID       string   `json:"queue_id,omitempty"`
		ID            string   `json:"id"`
		EventJSON     string   `json:"event_json"`
		RecipientNpub string   `json:"recipient_npub"`
		Relays        []string `json:"relays"`
		RetryCount    int      `json:"retry_count"`
		MaxRetries    int      `json:"max_retries"`
		LastAttempt   int64    `json:"last_attempt"`
		CreatedAt     int64    `json:"created_at"`
		Status        string   `json:"status"`
	}
	path, err := GetOutboxPath()
	require.NoError(t, err)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var disk struct {
		Entries []json.RawMessage `json:"entries"`
	}
	require.NoError(t, json.Unmarshal(data, &disk))
	legacy := &types.Outbox{}
	for _, raw := range disk.Entries {
		var value legacyOutboxEntry
		require.NoError(t, json.Unmarshal(raw, &value))
		encoded, err := json.Marshal(value)
		require.NoError(t, err)
		var decoded types.OutboxEntry
		require.NoError(t, json.Unmarshal(encoded, &decoded))
		require.Empty(t, decoded.Route)
		legacy.Entries = append(legacy.Entries, decoded)
	}
	published, stored := 0, 0
	for _, queued := range legacy.Entries {
		if queued.Status != "pending" || queued.RetryCount >= queued.MaxRetries {
			continue
		}
		_, _ = attemptSend(context.Background(), legacy, queued, nil, time.Second,
			func(context.Context, []string, nostr.Event, time.Duration) bool { published++; return true },
			func(event *nostr.Event, recipient, content string, encrypted bool) error {
				stored++
				return StoreOutgoingMessage(event, recipient, content, encrypted)
			})
	}
	require.Zero(t, published)
	require.Zero(t, stored)
	_, err = GetStore()
	require.NoError(t, err)
	var count int
	require.NoError(t, storage.DB.QueryRow("SELECT COUNT(*) FROM messages").Scan(&count))
	require.Zero(t, count)
	ob, err := LoadOutbox()
	require.NoError(t, err)
	require.Equal(t, []types.OutboxEntry{entry}, ob.Entries)
}

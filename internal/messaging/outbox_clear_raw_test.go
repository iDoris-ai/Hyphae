package messaging

import (
	"bytes"
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func exhaustedProtocolGroup(t *testing.T) []byte {
	t.Helper()
	o3 := bytes.TrimSpace(readF2BFixture(t, "p-o3-entry.json"))
	oldFailed := bytes.Replace(o3, []byte(`"retry_count": 1`), []byte(`"retry_count": 10`), -1)
	oldFailed = bytes.Replace(oldFailed, []byte(`"prior_retry_count": 0`), []byte(`"prior_retry_count": 9`), 1)
	oldFailed = bytes.Replace(oldFailed, []byte(`"group_pending"`), []byte(`"group_failed"`), -1)
	oldFailed = bytes.Replace(oldFailed, []byte(`"send_failed"`), []byte(`"retry_exhausted"`), 1)
	if _, err := ParseRawGroupEntry(oldFailed); err != nil {
		t.Fatalf("exhausted protocol fixture: %v", err)
	}
	return oldFailed
}

func TestN1RawEnqueueExhaustClear(t *testing.T) {
	group := exhaustedProtocolGroup(t)
	dm := bytes.Replace(legacyFixtureEntry(t, "legacy-dm-nil-relays.json"), []byte(`"status": "pending"`), []byte(`"status": "failed"`), 1)
	path := stageHomeOutbox(t, rawOutboxDoc(group, dm))
	before, err := ParseRawGroupEntry(group)
	require.NoError(t, err)

	out := captureStdout(t, func() {
		require.NoError(t, outboxClearCmd.Run(context.Background(), []string{"clear", "--failed", "--yes", "--min-failures", "5"}))
	})
	require.Contains(t, out, "Removed 1")
	require.Contains(t, out, "kept 1 group entry")

	ob, err := LoadOutbox()
	require.NoError(t, err)
	require.Len(t, ob.Entries, 1)
	require.Equal(t, before.Entry.ID, ob.Entries[0].ID)
	entry, _, err := ReadGroupEntryLocked(path, before.Entry.ID)
	require.NoError(t, err)
	require.NotNil(t, entry)
	require.Equal(t, before.Raw, entry.Raw)
}

func TestN1RawClearKeepsGroupWhenNoDM(t *testing.T) {
	group := exhaustedProtocolGroup(t)
	path := stageHomeOutbox(t, rawOutboxDoc(group))
	before, err := os.ReadFile(path)
	require.NoError(t, err)

	out := captureStdout(t, func() {
		require.NoError(t, outboxClearCmd.Run(context.Background(), []string{"clear", "--failed", "--yes"}))
	})
	require.Contains(t, out, "Kept 1 group entry")
	require.Contains(t, out, "removed 0 DM entries")

	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, before, after)
}

package groupchat

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGroupMessagesPageWalksHistoryWithoutRepeatOrGap(t *testing.T) {
	g := newThreeMemberGroup(t)
	g.activate()
	group := g.draft.Group.ID
	// seven messages; groups of three share a created_at second, which is
	// where a timestamp-only cursor would skip or repeat messages.
	var want []string
	for i := 0; i < 7; i++ {
		id := fmt.Sprintf("%032x", i+1)
		_, err := g.alice.store.StoreLocalMessage(g.alice.npub, group, id, "m"+id, int64(1000+i/3))
		require.NoError(t, err)
		want = append(want, id)
	}
	page := func(limit int, before, after string) []string {
		t.Helper()
		got, err := g.alice.store.GroupMessagesPage(g.alice.npub, group, limit, before, after)
		require.NoError(t, err)
		out := make([]string, len(got))
		for i, m := range got {
			out[i] = m.ID
		}
		return out
	}

	require.Equal(t, want[4:], page(3, "", ""), "newest page, oldest first")
	require.Equal(t, want[1:4], page(3, want[4], ""), "next older page")
	require.Equal(t, want[:1], page(3, want[1], ""), "last older page is short")
	require.Empty(t, page(3, want[0], ""), "nothing older than the first message")

	require.Equal(t, want[3:6], page(3, "", want[2]), "incremental refresh from a known message")
	require.Equal(t, want[6:], page(3, "", want[5]))
	require.Empty(t, page(3, "", want[6]), "nothing newer than the last message")

	// walking the whole history by pages visits every message once
	var walked []string
	cursor := ""
	for {
		older := page(2, cursor, "")
		if len(older) == 0 {
			break
		}
		walked = append(older, walked...)
		cursor = older[0]
	}
	require.Equal(t, want, walked)

	_, err := g.alice.store.GroupMessagesPage(g.alice.npub, group, 3, "nope", "")
	require.ErrorIs(t, err, ErrCursorNotFound)
	_, err = g.alice.store.GroupMessagesPage(g.alice.npub, group, 3, want[1], want[2])
	require.ErrorIs(t, err, ErrConflictingCursors)
}

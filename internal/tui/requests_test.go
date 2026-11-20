package tui

import (
	"testing"

	"fiatjaf.com/nostr"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/require"

	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/internal/identity"
	"github.com/iDoris-ai/hyphae/internal/storage"
)

// strangerWrote records one message from peer to me as the inbox would.
func strangerWrote(t *testing.T, me, peer, text string) {
	t.Helper()
	db, err := storage.OpenDB()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	id := nostr.Generate().Public().Hex()
	_, err = db.Exec(`INSERT INTO messages (id, event_id, sender_npub, recipient_npub, plaintext, created_at, received_at, is_incoming)
		VALUES (?, ?, ?, ?, ?, 1, 1, 1)`, id, id, peer, me, text)
	require.NoError(t, err)
	_, err = storage.NewRelationStore(db).ClassifyReceive(me, peer, id, false, 1)
	require.NoError(t, err)
}

// pressReq sends key and runs any command it returns, feeding the result back.
func pressReq(m *RequestsModel, key string) {
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
	if cmd != nil {
		m.Update(cmd())
	}
}

func relationState(t *testing.T, me, peer string) string {
	t.Helper()
	db, err := storage.OpenDB()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	p, err := storage.NewRelationStore(db).Get(me, peer)
	require.NoError(t, err)
	if p.Ignored {
		return "ignored"
	}
	return p.Relation.State.String()
}

func contactCount(t *testing.T) int {
	t.Helper()
	ks, err := identity.LoadKeyStore()
	require.NoError(t, err)
	return len(ks.Contacts)
}

// Every action hits the peer under the cursor, and a failed accept's resend
// key targets that same peer, never the next row (#396 review B1).
func TestRequestsActOnCursorPeer(t *testing.T) {
	cleanup := setupTestEnv(t)
	defer cleanup()
	ks, err := identity.LoadKeyStore()
	require.NoError(t, err)
	me := ks.Identities["testuser"].Npub
	for i := 0; i < 4; i++ {
		strangerWrote(t, me, common.EncodeNpub(nostr.Generate().Public()), "你好\x1b[2J\u202e，一起吃火锅吗？\n第二行")
	}
	m, err := NewRequestsModel([]string{"ws://127.0.0.1:1"})
	require.NoError(t, err)
	require.Len(t, m.items, 4)
	view := m.View()
	require.Contains(t, view, "你好[2J，一起吃火锅吗？ 第二行", "flattened; escape and U+202E dropped")
	require.NotContains(t, view, "\x1b[2J")

	// j then i: the second row is ignored, the first is untouched.
	p0, p1 := m.items[0].Npub, m.items[1].Npub
	pressReq(m, "j")
	pressReq(m, "i")
	require.Equal(t, "ignored", relationState(t, me, p1))
	require.Equal(t, "stranger", relationState(t, me, p0))
	require.Len(t, m.items, 3)

	// b, n cancels; b, y blocks the row under the cursor.
	target := m.items[m.cursor].Npub
	pressReq(m, "b")
	require.Contains(t, m.status, "Press y to confirm")
	pressReq(m, "n")
	require.Contains(t, m.status, "cancelled")
	require.Equal(t, "stranger", relationState(t, me, target))
	pressReq(m, "b")
	pressReq(m, "y")
	require.Equal(t, "blocked", relationState(t, me, target))
	require.Len(t, m.items, 2)

	// a with the relay down: that peer is accepted locally and named; a second
	// a would accept the next row, so the resend is r and targets the same peer.
	chosen := m.items[m.cursor].Npub
	pressReq(m, "a")
	require.Equal(t, "friend", relationState(t, me, chosen))
	require.Contains(t, m.status, short(chosen))
	require.Contains(t, m.status, "press r to resend")
	require.Equal(t, chosen, m.unsent)
	require.Equal(t, 1, contactCount(t))
	pressReq(m, "r")
	require.Equal(t, 1, contactCount(t), "the resend accepted no one else")
	require.Equal(t, chosen, m.unsent)
	require.Len(t, m.items, 1)
}

func TestRequestsKeepCursorOnScreen(t *testing.T) {
	require.Equal(t, [2]int{0, 3}, pair(window(3, 0, 0)), "no size yet: draw all")
	require.Equal(t, [2]int{0, 8}, pair(window(20, 0, 24)))
	s, e := window(20, 19, 24)
	require.True(t, s <= 19 && 19 < e, "the last row is drawn")
	s, e = window(20, 10, 24)
	require.True(t, s <= 10 && 10 < e)
}

func pair(a, b int) [2]int { return [2]int{a, b} }
func TestOneLine(t *testing.T) {
	require.Equal(t, "a b", oneLine("a\nb\x07", 10))
	require.Equal(t, "你好你…", oneLine("你好你好", 3), "cuts on characters, not bytes")
	require.Equal(t, "Alice", oneLine("Al\u200bi\u202ece", 10), "format characters dropped")
}

func TestRequestsQuitWaitsForAction(t *testing.T) {
	m := &RequestsModel{busy: true}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	require.Nil(t, cmd, "q does not quit mid-action")
	require.Contains(t, m.status, "Wait for the action")
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	require.NotNil(t, cmd, "ctrl+c still quits")
}

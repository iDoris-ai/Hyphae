package tui

import (
	"strings"
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

func TestRequestsIgnoreBlockAccept(t *testing.T) {
	cleanup := setupTestEnv(t)
	defer cleanup()
	ks, err := identity.LoadKeyStore()
	require.NoError(t, err)
	me := ks.Identities["testuser"].Npub
	carol, dave, erin := common.EncodeNpub(nostr.Generate().Public()), common.EncodeNpub(nostr.Generate().Public()),
		common.EncodeNpub(nostr.Generate().Public())
	for _, p := range []string{carol, dave, erin} {
		strangerWrote(t, me, p, "你好\x1b[2J，一起吃火锅吗？\n第二行")
	}
	m, err := NewRequestsModel([]string{"ws://127.0.0.1:1"})
	require.NoError(t, err)
	require.Len(t, m.items, 3)
	view := m.View()
	require.Contains(t, view, "你好[2J，一起吃火锅吗？ 第二行", "flattened, escape dropped")
	require.NotContains(t, view, "\x1b[2J")

	first := m.items[0].Npub
	pressReq(m, "i")
	require.Len(t, m.items, 2)
	require.Contains(t, m.status, "Ignored")

	pressReq(m, "b")
	require.Contains(t, m.status, "Press y to confirm")
	pressReq(m, "n")
	require.Contains(t, m.status, "cancelled")
	require.Len(t, m.items, 2)
	pressReq(m, "b")
	pressReq(m, "y")
	require.Len(t, m.items, 1)
	require.Contains(t, m.status, "Blocked")

	// The relay is down: accepted locally, and the request is gone.
	pressReq(m, "a")
	require.Contains(t, m.status, "no relay took the friend_accept")
	require.Empty(t, m.items)
	ks, err = identity.LoadKeyStore()
	require.NoError(t, err)
	found := false
	for _, ct := range ks.Contacts {
		found = found || ct.Npub != first && strings.HasPrefix(ct.Nickname, "friend-")
	}
	require.True(t, found, "the accepted stranger is a contact")
}

func TestOneLine(t *testing.T) {
	require.Equal(t, "a b", oneLine("a\nb\x07", 10))
	require.Equal(t, "你好你…", oneLine("你好你好", 3), "cuts on characters, not bytes")
}

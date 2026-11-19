package tui

import (
	"errors"
	"testing"

	"fiatjaf.com/nostr"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/require"

	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/internal/identity"
)

func TestVisibilityCommand(t *testing.T) {
	for _, c := range []struct {
		in       string
		current  bool
		want, ok bool
		err      bool
	}{
		{"/visibility", true, false, true, false},
		{"/visibility", false, true, true, false},
		{"/visibility public", false, true, true, false},
		{"/visibility hidden", true, false, true, false},
		{"/visibility maybe", true, true, true, true},
		{"hello /visibility", true, false, false, false},
		{"/visibilityx", true, false, false, false},
	} {
		want, ok, err := visibilityCommand(c.in, c.current)
		require.Equal(t, c.ok, ok, c.in)
		require.Equal(t, c.err, err != nil, c.in)
		if ok && err == nil {
			require.Equal(t, c.want, want, c.in)
		}
	}
}

func TestVisibilityToggleInChat(t *testing.T) {
	cleanup := setupTestEnv(t)
	defer cleanup()
	ks, err := identity.LoadKeyStore()
	require.NoError(t, err)
	peer := common.EncodeNpub(nostr.Generate().Public())
	require.NoError(t, identity.AddContact(ks, "peer", peer))
	model, err := NewChatModel("peer", "ws://127.0.0.1:1")
	require.NoError(t, err)
	defer model.Close()
	require.True(t, model.visible, "public by default")
	require.Contains(t, model.View(), "Card: public")

	model.input.SetValue("/visibility")
	_, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	require.NotNil(t, cmd)
	require.Empty(t, model.input.Value(), "the command is not sent as a message")
	require.False(t, model.outboxSending)

	model.Update(visibilityMsg{visible: false})
	require.Contains(t, model.View(), "Card: hidden")
	model.Update(visibilityMsg{visible: false, err: errors.New("no relay accepted the change")})
	require.Contains(t, model.View(), "Card: no relay accepted")
}

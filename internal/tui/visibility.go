package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/iDoris-ai/hyphae/internal/friend"
	"github.com/iDoris-ai/hyphae/internal/identity"
	"github.com/iDoris-ai/hyphae/internal/storage"
)

// visibilityMsg carries the result of a /visibility switch.
type visibilityMsg struct {
	visible bool
	err     error
}

// loadVisibility reads the stored card setting for the status line; a fresh
// identity is public by default.
func loadVisibility(npub string) bool {
	db, err := storage.OpenDB()
	if err != nil {
		return true
	}
	defer func() { _ = db.Close() }()
	s, err := storage.LoadCardSetting(db, npub)
	return err != nil || s.Visible
}

func visibilityLine(visible bool) string {
	if visible {
		return "Card: public — people on your relay can find you by tag • /visibility to hide"
	}
	return "Card: hidden — not findable by tag • /visibility to show"
}

// visibilityCommand parses /visibility [public|hidden]. With no argument it
// toggles. ok is false when content is not a /visibility command.
func visibilityCommand(content string, current bool) (want bool, ok bool, err error) {
	fields := strings.Fields(content)
	if len(fields) == 0 || fields[0] != "/visibility" {
		return false, false, nil
	}
	switch {
	case len(fields) == 1:
		return !current, true, nil
	case len(fields) == 2 && fields[1] == "public":
		return true, true, nil
	case len(fields) == 2 && fields[1] == "hidden":
		return false, true, nil
	}
	return current, true, fmt.Errorf("usage: /visibility [public|hidden]")
}

func (m *ChatModel) setVisibility(visible bool) tea.Cmd {
	return func() tea.Msg {
		ks, err := identity.LoadKeyStore()
		if err != nil {
			return visibilityMsg{visible: !visible, err: err}
		}
		sk, err := identity.GetSecretKey(ks, m.myIdentity.Nickname)
		if err != nil {
			return visibilityMsg{visible: !visible, err: err}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := friend.SetVisible(ctx, m.myIdentity, sk, m.relays, visible); err != nil {
			// Show what is stored, whatever step failed.
			return visibilityMsg{visible: loadVisibility(m.myIdentity.Npub), err: err}
		}
		return visibilityMsg{visible: visible}
	}
}

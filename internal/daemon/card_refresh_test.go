package daemon

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/iDoris-ai/hyphae/internal/friend"
	"github.com/iDoris-ai/hyphae/internal/storage"
	"github.com/iDoris-ai/hyphae/pkg/types"
)

func TestCardRefresherAnnouncesFirstCard(t *testing.T) {
	var log bytes.Buffer
	var notes []string
	result := friend.RefreshResult{}
	var err error
	r := &cardRefresher{
		id: &types.Identity{Nickname: "alice"}, notify: true, logger: newDaemonLogger(&log),
		desktop: func(title, msg string) error { notes = append(notes, msg); return nil },
		nowUnix: func() int64 { return 1 },
		refreshF: func(context.Context, *types.Identity, []string, int64) (friend.RefreshResult, error) {
			return result, err
		},
	}

	r.run(context.Background()) // nothing due
	require.Empty(t, log.String())

	result = friend.RefreshResult{Published: true, FirstTime: true,
		Card: storage.CardSetting{Visible: true, Tags: []string{"友好"}, ExpiresAt: 100}}
	r.run(context.Background())
	require.Contains(t, log.String(), "You are public (the default)")
	require.Len(t, notes, 1)
	require.Contains(t, notes[0], "hyphae friend visibility hidden --as alice")

	log.Reset()
	result.FirstTime = false
	r.run(context.Background())
	require.Contains(t, log.String(), "Directory card published")
	require.NotContains(t, log.String(), "You are public")
	require.Len(t, notes, 1, "only the first card is announced")

	log.Reset()
	result = friend.RefreshResult{Published: true, Card: storage.CardSetting{Visible: false}}
	r.run(context.Background())
	require.Contains(t, log.String(), "Hide resent")

	log.Reset()
	err = errors.New("boom")
	r.run(context.Background())
	require.Contains(t, log.String(), "refresh failed: boom")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	log.Reset()
	r.run(ctx)
	require.Empty(t, log.String(), "a stopping daemon does not publish")
}

func TestCardRefresherNoDesktopWhenNotifyOff(t *testing.T) {
	var log bytes.Buffer
	called := false
	r := &cardRefresher{
		id: &types.Identity{Nickname: "alice"}, logger: newDaemonLogger(&log),
		desktop: func(string, string) error { called = true; return nil },
		nowUnix: func() int64 { return 1 },
		refreshF: func(context.Context, *types.Identity, []string, int64) (friend.RefreshResult, error) {
			return friend.RefreshResult{Published: true, FirstTime: true, Card: storage.CardSetting{Visible: true}}, nil
		},
	}
	r.run(context.Background())
	require.False(t, called)
	require.Contains(t, log.String(), "You are public")
}

func TestNewCardRefresherUsesKeystoreSecret(t *testing.T) {
	r := newCardRefresher(&types.Identity{Nickname: "ghost"}, &types.KeyStore{Identities: map[string]*types.Identity{}}, nil, false, newDaemonLogger(&bytes.Buffer{}))
	_, err := r.refreshF(context.Background(), r.id, nil, 1)
	require.Error(t, err, "unknown identity has no secret")
}

package messaging

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"fiatjaf.com/nostr"
	"github.com/stretchr/testify/require"

	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/internal/identity"
	"github.com/iDoris-ai/hyphae/internal/relay"
	"github.com/iDoris-ai/hyphae/internal/social/contact"
	"github.com/iDoris-ai/hyphae/internal/storage"
)

func startGateRelay(t *testing.T) string {
	t.Helper()
	handler, cleanup, err := relay.New(relay.Config{DataDir: t.TempDir()})
	require.NoError(t, err)
	srv := httptest.NewServer(handler)
	t.Cleanup(func() { srv.Close(); cleanup() })
	return "ws" + strings.TrimPrefix(srv.URL, "http")
}

func strangerNpub() string {
	return common.EncodeNpub(nostr.Generate().Public())
}

func sendTo(t *testing.T, relayURL, to string) error {
	t.Helper()
	var err error
	captureStdout(t, func() {
		err = runAgentMsgCLICommand(context.Background(),
			[]string{"msg", "--from", "alice", "--to", to, "--content", "hi", "--relay", relayURL, "--encrypt=false"})
	})
	return err
}

func requireCode(t *testing.T, err error, code string) *common.ExitError {
	t.Helper()
	var ee *common.ExitError
	require.True(t, errors.As(err, &ee), "err = %v", err)
	require.Equal(t, code, ee.Code)
	return ee
}

func TestAgentMsgStrangerMessageLimit(t *testing.T) {
	setupAgentMsgCLI(t)
	url := startGateRelay(t)
	carol := strangerNpub()
	require.NoError(t, sendTo(t, url, carol))
	require.NoError(t, sendTo(t, url, carol))

	ob, err := LoadOutbox()
	require.NoError(t, err)
	before := len(ob.Entries)
	ee := requireCode(t, sendTo(t, url, carol), common.ErrCodeStrangerMessageLimit)
	require.Equal(t, map[string]any{"peer": carol, "sent": contact.PerStrangerLimit, "limit": contact.PerStrangerLimit}, ee.Data)
	ob, err = LoadOutbox()
	require.NoError(t, err)
	require.Len(t, ob.Entries, before, "a refused message is never queued")

	// Friends are not limited: bob is alice's own identity.
	for i := 0; i < 3; i++ {
		require.NoError(t, sendTo(t, url, "bob"))
	}
}

func TestAgentMsgStrangerDailyLimit(t *testing.T) {
	setupAgentMsgCLI(t)
	url := startGateRelay(t)
	for i := 0; i < contact.DailyLimit; i++ {
		require.NoError(t, sendTo(t, url, strangerNpub()))
	}
	ee := requireCode(t, sendTo(t, url, strangerNpub()), common.ErrCodeStrangerDailyLimit)
	data := ee.Data.(map[string]any)
	require.Equal(t, contact.DailyLimit, data["used"])
	require.Positive(t, data["resets_at"])
}

func TestAgentMsgBlockedPeer(t *testing.T) {
	setupAgentMsgCLI(t)
	url := startGateRelay(t)
	dave := strangerNpub()
	ks, err := identity.LoadKeyStore()
	require.NoError(t, err)
	require.NoError(t, InitStorage())
	_, err = storage.NewRelationStore(storage.DB).Update(ks.Identities["alice"].Npub, dave, 1,
		func(p storage.PeerRelation) (storage.PeerRelation, error) {
			p.Relation = contact.Block(p.Relation)
			return p, nil
		})
	require.NoError(t, err)
	_ = requireCode(t, sendTo(t, url, dave), common.ErrCodePeerBlocked)
}

func TestSendRefusalPassesOtherErrors(t *testing.T) {
	boom := errors.New("boom")
	require.ErrorIs(t, storage.SendRefusal(boom, "x"), boom)
	require.NoError(t, storage.SendRefusal(nil, "x"))
}

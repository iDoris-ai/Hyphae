package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// Every spelling of one key is the same peer: hex, upper-case hex or a padded
// npub cannot get past a block or the per-stranger limit.
func TestAgentMsgGateCanonicalizesRecipient(t *testing.T) {
	setupAgentMsgCLI(t)
	url := startGateRelay(t)
	sk := nostr.Generate()
	npub, hex := common.EncodeNpub(sk.Public()), sk.Public().Hex()
	require.NoError(t, sendTo(t, url, npub))
	require.NoError(t, sendTo(t, url, hex))
	for _, to := range []string{strings.ToUpper(hex), " " + npub, hex} {
		_ = requireCode(t, sendTo(t, url, to), common.ErrCodeStrangerMessageLimit)
	}

	dave := nostr.Generate().Public()
	ks, err := identity.LoadKeyStore()
	require.NoError(t, err)
	require.NoError(t, InitStorage())
	_, err = storage.NewRelationStore(storage.DB).Update(ks.Identities["alice"].Npub, common.EncodeNpub(dave), 1,
		func(p storage.PeerRelation) (storage.PeerRelation, error) {
			p.Relation = contact.Block(p.Relation)
			return p, nil
		})
	require.NoError(t, err)
	for _, to := range []string{dave.Hex(), strings.ToUpper(dave.Hex()), " " + common.EncodeNpub(dave)} {
		_ = requireCode(t, sendTo(t, url, to), common.ErrCodePeerBlocked)
	}

	// A friend addressed by hex is still a friend (bob is alice's identity).
	bob := ks.Identities["bob"].Npub
	bobPK, err := common.ParsePublicKey(bob)
	require.NoError(t, err)
	for i := 0; i < 3; i++ {
		require.NoError(t, sendTo(t, url, bobPK.Hex()))
	}
}

func TestAgentInboxAppliesStrangerRules(t *testing.T) {
	_, recipientPK := setupAgentInbox(t)
	stranger, friend := nostr.Generate(), nostr.Generate()
	ks, err := identity.LoadKeyStore()
	require.NoError(t, err)
	require.NoError(t, identity.AddContact(ks, "friend", common.EncodeNpub(friend.Public())))
	now := nostr.Now()
	events := []nostr.Event{
		makeInboxEvent(t, recipientPK, stranger, now-3, "s1", nil),
		makeInboxEvent(t, recipientPK, stranger, now-2, "s2", nil),
		makeInboxEvent(t, recipientPK, stranger, now-1, "s3", nil),
		makeInboxEvent(t, recipientPK, friend, now, "f1", nil),
	}
	url := startInboxRelay(t, inboxRelayPlan{events: events})
	list := func() []agentInboxEntry {
		out := captureStdout(t, func() {
			require.NoError(t, runAgentInboxCLI(context.Background(), []string{"--json", "--as", "alice", "--limit", "10", "--relay", url}))
		})
		var resp struct {
			Data []agentInboxEntry `json:"data"`
		}
		require.NoError(t, json.Unmarshal([]byte(out), &resp))
		return resp.Data
	}
	first := list()
	require.Len(t, first, 3, "two stranger messages and the friend's; the third stranger message is held back")
	seqs := map[int]bool{}
	for _, e := range first {
		if e.Content == "f1" {
			require.Equal(t, "friend", e.Relation)
			require.Zero(t, e.StrangerSeq)
			continue
		}
		require.Equal(t, "stranger", e.Relation)
		seqs[e.StrangerSeq] = true
	}
	require.Equal(t, map[int]bool{1: true, 2: true}, seqs)
	require.Equal(t, first, list(), "re-fetching does not count the same events again")
}

func TestStrangerFilteredEmit(t *testing.T) {
	setupAgentMsgCLI(t)
	ks, err := identity.LoadKeyStore()
	require.NoError(t, err)
	var got []AgentInboxWatchUpdate
	emit := strangerFilteredEmit(ks, ks.Identities["alice"].Npub, func(u AgentInboxWatchUpdate) { got = append(got, u) })
	sender := strangerNpub()
	for i := 0; i < 3; i++ {
		emit(AgentInboxWatchUpdate{Message: &ReceivedAgentMessage{EventID: fmt.Sprintf("%064x", i), SenderNpub: sender}})
	}
	emit(AgentInboxWatchUpdate{RelayURL: "ws://x"}) // non-message updates pass through
	require.Len(t, got, 3)
	require.Equal(t, 1, got[0].Message.StrangerSeq)
	require.Equal(t, 2, got[1].Message.StrangerSeq)
	require.Nil(t, got[2].Message)
}

// When the stranger check fails, agent inbox lists the sender as a stranger,
// never as a friend.
func TestAgentInboxClassifyErrorFailsClosed(t *testing.T) {
	_, recipientPK := setupAgentInbox(t)
	stranger := nostr.Generate()
	url := startInboxRelay(t, inboxRelayPlan{events: []nostr.Event{
		makeInboxEvent(t, recipientPK, stranger, nostr.Now(), "s1", nil),
	}})
	require.NoError(t, InitStorage())
	_, err := storage.DB.Exec(`CREATE TRIGGER fail_receipt BEFORE INSERT ON receive_disposition
		BEGIN SELECT RAISE(ABORT, 'injected'); END`)
	require.NoError(t, err)
	var runErr error
	captureStdout(t, func() {
		runErr = runAgentInboxCLI(context.Background(), []string{"--json", "--as", "alice", "--limit", "10", "--relay", url})
	})
	var ee *common.ExitError
	require.True(t, errors.As(runErr, &ee), "err = %v", runErr)
	entries, ok := ee.Data.([]agentInboxEntry)
	require.True(t, ok, "data = %#v", ee.Data)
	require.Len(t, entries, 1)
	require.Equal(t, "stranger", entries[0].Relation)
}

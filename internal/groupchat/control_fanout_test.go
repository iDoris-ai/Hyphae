package groupchat

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"fiatjaf.com/nostr"
	"github.com/iDoris-ai/hyphae/internal/messaging"
	"github.com/stretchr/testify/require"
)

func TestPrepareControlFanout(t *testing.T) {
	t.Run("one frozen event", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		g := newThreeMemberGroup(t)
		invite := g.invites[g.bob.npub]
		report, err := g.alice.store.PrepareControlFanout(g.alice.sk, invite, 1234, 7)
		require.NoError(t, err)
		require.Len(t, report.Recipients, 1)
		got := report.Recipients[0]
		require.Equal(t, g.bob.npub, got.RecipientNpub)
		require.NotEmpty(t, got.EventID)
		require.NotEmpty(t, got.QueueID)
		require.Equal(t, RecipientQueued, got.State)

		var rows int
		require.NoError(t, g.alice.db.QueryRow(`SELECT COUNT(*) FROM groupchat_fanout`).Scan(&rows))
		require.Equal(t, 1, rows)
		var raw string
		require.NoError(t, g.alice.db.QueryRow(`SELECT event_json FROM groupchat_fanout WHERE event_id = ?`, got.EventID).Scan(&raw))
		var event nostr.Event
		require.NoError(t, json.Unmarshal([]byte(raw), &event))
		require.Equal(t, got.EventID, event.ID.Hex())
		require.True(t, event.VerifySignature())
		verified, err := VerifyIncoming(event, g.bob.sk)
		require.NoError(t, err)
		encoded, err := Encode(invite)
		require.NoError(t, err)
		require.Equal(t, encoded, verified.Plaintext())
		require.EqualValues(t, 1234, verified.CreatedAt())

		ob, err := messaging.LoadOutbox()
		require.NoError(t, err)
		pending := messaging.GetPendingGroupOutbox(ob)
		require.Len(t, pending, 1)
		require.Equal(t, got.EventID, pending[0].ID)
		require.Equal(t, got.QueueID, pending[0].QueueID)
		require.Equal(t, messaging.OutboxRouteGroup, pending[0].Route)
		path, err := messaging.GetOutboxPath()
		require.NoError(t, err)
		file, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Contains(t, string(file), `"fanout_protocol":1`)
		require.Equal(t, 1, bytes.Count(file, []byte(`"fanout_protocol"`)))

		again, err := g.alice.store.PrepareControlFanout(g.alice.sk, invite, 9999, 7)
		require.NoError(t, err)
		require.Equal(t, got.EventID, again.Recipients[0].EventID)
		require.Equal(t, got.QueueID, again.Recipients[0].QueueID)
		after, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, string(file), string(after))
		var rawAgain string
		require.NoError(t, g.alice.db.QueryRow(`SELECT event_json FROM groupchat_fanout WHERE event_id = ?`, got.EventID).Scan(&rawAgain))
		require.Equal(t, raw, rawAgain)
		require.Equal(t, 1, bytes.Count(after, []byte(`"fanout_protocol"`)))
	})

	t.Run("negative time", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		g := newThreeMemberGroup(t)
		invite := g.invites[g.bob.npub]
		_, err := g.alice.store.PrepareControlFanout(g.alice.sk, invite, -1, 7)
		require.Error(t, err)
		var rows int
		require.NoError(t, g.alice.db.QueryRow(`SELECT COUNT(*) FROM groupchat_fanout`).Scan(&rows))
		require.Zero(t, rows)
		ob, err := messaging.LoadOutbox()
		require.NoError(t, err)
		require.Empty(t, ob.Entries)
	})

	t.Run("bad version", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		g := newThreeMemberGroup(t)
		invite := g.invites[g.bob.npub]
		invite.Version = 2
		_, err := g.alice.store.PrepareControlFanout(g.alice.sk, invite, 1234, 7)
		require.Error(t, err)
		var rows int
		require.NoError(t, g.alice.db.QueryRow(`SELECT COUNT(*) FROM groupchat_fanout`).Scan(&rows))
		require.Zero(t, rows)
		ob, err := messaging.LoadOutbox()
		require.NoError(t, err)
		require.Empty(t, ob.Entries)
		path, err := messaging.GetOutboxPath()
		require.NoError(t, err)
		_, err = os.Stat(path)
		require.True(t, os.IsNotExist(err))
	})

	t.Run("empty sender", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		g := newThreeMemberGroup(t)
		_, err := g.alice.store.PrepareControlFanout(nostr.SecretKey{}, g.invites[g.bob.npub], 1234, 7)
		require.ErrorContains(t, err, "invalid control fanout parameters")
		requireZeroControlRows(t, g)
	})

	t.Run("zero budget", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		g := newThreeMemberGroup(t)
		_, err := g.alice.store.PrepareControlFanout(g.alice.sk, g.invites[g.bob.npub], 1234, 0)
		require.ErrorContains(t, err, "invalid control fanout parameters")
		requireZeroControlRows(t, g)
	})

	t.Run("non invite", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		g := newThreeMemberGroup(t)
		invite := g.invites[g.bob.npub]
		accept := Envelope{Type: EnvelopeAccept, Version: Version, GroupID: invite.GroupID, CreatorNpub: invite.CreatorNpub, InviteID: invite.InviteID, RosterHash: invite.RosterHash, InviteeNpub: invite.InviteeNpub}
		_, err := g.alice.store.PrepareControlFanout(g.alice.sk, accept, 1234, 7)
		require.ErrorIs(t, err, ErrProtocolMismatch)
		requireZeroControlRows(t, g)
	})

	t.Run("non creator", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		g := newThreeMemberGroup(t)
		_, err := g.alice.store.PrepareControlFanout(g.bob.sk, g.invites[g.bob.npub], 1234, 7)
		require.ErrorIs(t, err, ErrProtocolMismatch)
		requireZeroControlRows(t, g)
	})

	t.Run("roster hash", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		g := newThreeMemberGroup(t)
		invite := g.invites[g.bob.npub]
		invite.Name = "other"
		hash, members, err := CanonicalRosterHash(invite.GroupID, invite.CreatorNpub, invite.Name, invite.Members)
		require.NoError(t, err)
		invite.RosterHash = hash
		invite.Members = members
		_, err = g.alice.store.PrepareControlFanout(g.alice.sk, invite, 1234, 7)
		require.ErrorIs(t, err, ErrProtocolMismatch)
		requireZeroControlRows(t, g)
	})

	t.Run("budget mismatch", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		g := newThreeMemberGroup(t)
		invite := g.invites[g.bob.npub]
		first, err := g.alice.store.PrepareControlFanout(g.alice.sk, invite, 1234, 7)
		require.NoError(t, err)
		_, err = g.alice.store.PrepareControlFanout(g.alice.sk, invite, 1234, 8)
		require.ErrorIs(t, err, ErrProtocolMismatch)
		var budget int
		require.NoError(t, g.alice.db.QueryRow(`SELECT max_retries FROM groupchat_fanout WHERE event_id = ?`, first.Recipients[0].EventID).Scan(&budget))
		require.Equal(t, 7, budget)
	})

	t.Run("stored event id", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		g := newThreeMemberGroup(t)
		invite := g.invites[g.bob.npub]
		first, err := g.alice.store.PrepareControlFanout(g.alice.sk, invite, 1234, 7)
		require.NoError(t, err)
		_, err = g.alice.db.Exec(`UPDATE groupchat_fanout SET event_id = ? WHERE event_id = ?`, "ab", first.Recipients[0].EventID)
		require.NoError(t, err)
		_, err = g.alice.store.PrepareControlFanout(g.alice.sk, invite, 1234, 7)
		require.ErrorIs(t, err, ErrProtocolMismatch)
	})

	t.Run("requeue after exhaust", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		g := newThreeMemberGroup(t)
		invite := g.invites[g.bob.npub]
		first, err := g.alice.store.PrepareControlFanout(g.alice.sk, invite, 1234, 1)
		require.NoError(t, err)
		got := first.Recipients[0]
		key := FanoutKey{LocalNpub: g.alice.npub, GroupID: invite.GroupID, EnvelopeType: EnvelopeInvite, SendKey: invite.InviteID}
		exhaustRecipient(t, g.alice, key, got.RecipientNpub, 1)
		again, err := g.alice.store.RequeueFailed(key, got.RecipientNpub)
		require.NoError(t, err)
		require.Equal(t, got.EventID, again.Recipients[0].EventID)
		require.NotEqual(t, got.QueueID, again.Recipients[0].QueueID)
		require.Equal(t, RecipientQueued, again.Recipients[0].State)
	})

	t.Run("no cli flag and no dial", func(t *testing.T) {
		found := false
		for _, cmd := range Cmd.Commands {
			if cmd.Name == "send" {
				found = true
			}
		}
		require.True(t, found)
		src, err := os.ReadFile("control_fanout.go")
		require.NoError(t, err)
		text := string(src)
		require.NotContains(t, text, "cli.")
		require.NotContains(t, text, "Dial")
		require.NotContains(t, text, "websocket")
		require.NotContains(t, text, "AttemptSend")
		require.NotContains(t, text, "Publish")
	})
}

func requireZeroControlRows(t *testing.T, g *threeMemberGroup) {
	t.Helper()
	var rows int
	require.NoError(t, g.alice.db.QueryRow(`SELECT COUNT(*) FROM groupchat_fanout`).Scan(&rows))
	require.Zero(t, rows)
	ob, err := messaging.LoadOutbox()
	require.NoError(t, err)
	require.Empty(t, ob.Entries)
}

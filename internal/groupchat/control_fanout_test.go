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

	t.Run("no cli flag and no dial", func(t *testing.T) {
		require.Len(t, Cmd.Commands, 1)
		require.Equal(t, "send", Cmd.Commands[0].Name)
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

package groupchat

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLogicalIDConflictAndUnknownMemberRejected(t *testing.T) {
	g := newThreeMemberGroup(t)
	g.activate()
	logicalID := mustOpaque(t)
	first := buildIncoming(t, g.alice, g.bob, Envelope{Type: EnvelopeMessage, Version: Version,
		GroupID: g.draft.Group.ID, LogicalID: logicalID, Body: "original"})
	inserted, err := g.bob.store.ReceiveMessage(first)
	require.NoError(t, err)
	require.True(t, inserted)
	conflict := buildIncoming(t, g.alice, g.bob, Envelope{Type: EnvelopeMessage, Version: Version,
		GroupID: g.draft.Group.ID, LogicalID: logicalID, Body: "altered"})
	_, err = g.bob.store.ReceiveMessage(conflict)
	require.ErrorIs(t, err, ErrLogicalIDConflict)

	stranger := newMember(t)
	unknown := buildIncoming(t, stranger, g.bob, Envelope{Type: EnvelopeMessage, Version: Version,
		GroupID: g.draft.Group.ID, LogicalID: mustOpaque(t), Body: "untrusted sender"})
	_, err = g.bob.store.ReceiveMessage(unknown)
	require.ErrorIs(t, err, ErrProtocolMismatch)
}

func TestStoreLocalMessageIdempotencyAndConflict(t *testing.T) {
	g := newThreeMemberGroup(t)
	g.activate()

	logicalID := mustOpaque(t)
	body := "hello groupchat"

	// First local store succeeds.
	msg1, err := g.alice.store.StoreLocalMessage(g.alice.npub, g.draft.Group.ID, logicalID, body, 1000)
	require.NoError(t, err)
	require.Equal(t, logicalID, msg1.ID)
	require.Equal(t, body, msg1.Plaintext)

	// Same logical ID + same body: idempotent, succeeds and does not duplicate in DB.
	msg2, err := g.alice.store.StoreLocalMessage(g.alice.npub, g.draft.Group.ID, logicalID, body, 1000)
	require.NoError(t, err)
	require.Equal(t, logicalID, msg2.ID)
	require.Equal(t, body, msg2.Plaintext)

	messages, err := g.alice.store.GroupMessages(g.alice.npub, g.draft.Group.ID, 10)
	require.NoError(t, err)
	require.Len(t, messages, 1)
	require.Equal(t, logicalID, messages[0].ID)

	// Same logical ID + altered body: returns ErrLogicalIDConflict.
	_, err = g.alice.store.StoreLocalMessage(g.alice.npub, g.draft.Group.ID, logicalID, "altered body", 1000)
	require.ErrorIs(t, err, ErrLogicalIDConflict)
}

func TestStoreMessageTxRollback(t *testing.T) {
	for _, transaction := range []string{"immediate", "sql"} {
		for _, writer := range []string{"message", "local message"} {
			t.Run(transaction+"/"+writer, func(t *testing.T) {
				g := newThreeMemberGroup(t)
				g.activate()
				store := g.alice.store
				groupID := g.draft.Group.ID
				logicalID := mustOpaque(t)
				_, err := g.alice.db.Exec(`UPDATE groupchat_groups SET updated_at = 1
					WHERE local_npub = ? AND group_id = ?`, g.alice.npub, groupID)
				require.NoError(t, err)

				var tx interface {
					queryExecer
					Rollback() error
				}
				if transaction == "immediate" {
					tx, err = store.beginImmediate()
				} else {
					tx, err = g.alice.db.Begin()
				}
				require.NoError(t, err)
				defer tx.Rollback()

				if writer == "local message" {
					message, err := store.storeLocalMessageTx(tx, g.alice.npub, groupID, logicalID, "rollback", 1000)
					require.NoError(t, err)
					require.Equal(t, logicalID, message.ID)
					require.Equal(t, g.alice.npub, message.Sender)
					require.Equal(t, "rollback", message.Plaintext)
					require.Equal(t, int64(1000), message.CreatedAt)
					require.True(t, message.IsEncrypted)
				} else {
					inserted, err := store.storeMessageTx(tx, g.alice.npub, groupID, logicalID,
						g.bob.npub, "rollback", 1000, mustOpaque(t)+mustOpaque(t), false)
					require.NoError(t, err)
					require.True(t, inserted)
				}
				var count int
				require.NoError(t, tx.QueryRow(`SELECT COUNT(*) FROM groupchat_messages`).Scan(&count))
				require.Equal(t, 1, count, "message must exist in the caller's transaction")
				require.NoError(t, tx.Rollback())
				require.NoError(t, g.alice.db.QueryRow(`SELECT COUNT(*) FROM groupchat_messages`).Scan(&count))
				require.Zero(t, count, "rollback must leave no message row")
				var updatedAt int64
				require.NoError(t, g.alice.db.QueryRow(`SELECT updated_at FROM groupchat_groups
					WHERE local_npub = ? AND group_id = ?`, g.alice.npub, groupID).Scan(&updatedAt))
				require.Equal(t, int64(1), updatedAt, "rollback must also restore group metadata")
			})
		}
	}
}

func TestStoreMessageTxReplayRollback(t *testing.T) {
	g := newThreeMemberGroup(t)
	g.activate()
	logicalID := mustOpaque(t)
	_, err := g.alice.store.StoreLocalMessage(g.alice.npub, g.draft.Group.ID, logicalID, "original", 1000)
	require.NoError(t, err)
	tx, err := g.alice.store.beginImmediate()
	require.NoError(t, err)
	defer tx.Rollback()
	eventID := mustOpaque(t) + mustOpaque(t)
	inserted, err := g.alice.store.storeMessageTx(tx, g.alice.npub, g.draft.Group.ID, logicalID,
		g.alice.npub, "original", 1000, eventID, false)
	require.NoError(t, err)
	require.False(t, inserted)
	var storedEventID string
	require.NoError(t, tx.QueryRow(`SELECT COALESCE(event_id, '') FROM groupchat_messages`).Scan(&storedEventID))
	require.Equal(t, eventID, storedEventID, "replay must backfill within the caller's transaction")
	require.NoError(t, tx.Rollback())
	messages, err := g.alice.store.GroupMessages(g.alice.npub, g.draft.Group.ID, 10)
	require.NoError(t, err)
	require.Len(t, messages, 1)
	require.Empty(t, messages[0].EventID, "rollback must undo the replay's event ID backfill")
}

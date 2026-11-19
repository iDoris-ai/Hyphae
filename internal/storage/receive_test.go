package storage

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/iDoris-ai/hyphae/internal/social/contact"
)

// A block also hides messages classified before it; unblocking shows them
// again with the stored receipt.
func TestBlockDropsEarlierReceipts(t *testing.T) {
	s := newRelationStore(t)
	r, err := s.ClassifyReceive("me", "carol", "E1", false, 1)
	require.NoError(t, err)
	require.Equal(t, contact.Request, r.Disposition)
	_, err = s.Update("me", "carol", 2, func(p PeerRelation) (PeerRelation, error) {
		p.Relation = contact.Block(p.Relation)
		return p, nil
	})
	require.NoError(t, err)
	for _, id := range []string{"E1", "E2"} {
		r, err = s.ClassifyReceive("me", "carol", id, false, 3)
		require.NoError(t, err)
		require.Equal(t, contact.Drop, r.Disposition, id)
	}
	_, err = s.Update("me", "carol", 4, func(p PeerRelation) (PeerRelation, error) {
		p.Relation, err = contact.Unblock(p.Relation, 4)
		return p, err
	})
	require.NoError(t, err)
	r, err = s.ClassifyReceive("me", "carol", "E1", false, 5)
	require.NoError(t, err)
	require.Equal(t, contact.Request, r.Disposition, "the stored receipt comes back after unblock")
}

// PreviewSends counts each would-be initiation against the next, and records
// nothing.
func TestPreviewSendsBatch(t *testing.T) {
	s := newRelationStore(t)
	for i := 0; i < contact.DailyLimit-1; i++ {
		_, err := s.ReserveSend("me", "old"+string(rune('a'+i)), false, relNow)
		require.NoError(t, err)
	}
	none := func(string) bool { return false }
	require.NoError(t, s.PreviewSends("me", []string{"p1"}, none, relNow))
	err := s.PreviewSends("me", []string{"p1", "p2"}, none, relNow)
	require.Error(t, err, "the second new stranger would exceed the daily limit")
	q, err := s.Quota("me", relNow)
	require.NoError(t, err)
	require.Equal(t, contact.DailyLimit-1, q.Used, "previewing records nothing")
	require.NoError(t, s.PreviewSends("me", []string{"olda", "p1"}, none, relNow), "a known stranger is not a new initiation")
}

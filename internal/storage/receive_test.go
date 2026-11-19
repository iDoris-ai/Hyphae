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

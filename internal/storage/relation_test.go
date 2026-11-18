package storage

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/iDoris-ai/hyphae/internal/social/contact"
)

const relNow = int64(1_700_000_000)

func newRelationStore(t *testing.T) *RelationStore {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	db, err := OpenDB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return NewRelationStore(db)
}

func TestReserveSendStrangerLimits(t *testing.T) {
	s := newRelationStore(t)
	d, err := s.ReserveSend("me", "bob", false, relNow)
	require.NoError(t, err)
	require.True(t, d.Initiation)
	d, err = s.ReserveSend("me", "bob", false, relNow+1)
	require.NoError(t, err)
	require.False(t, d.Initiation)
	_, err = s.ReserveSend("me", "bob", false, relNow+2)
	require.ErrorIs(t, err, contact.ErrStrangerMessageLimit)

	p, err := s.Get("me", "bob")
	require.NoError(t, err)
	require.Equal(t, contact.Relation{State: contact.Stranger, Sent: 2}, p.Relation)
	require.Equal(t, relNow, p.FirstContactAt)

	q, err := s.Quota("me", relNow+3)
	require.NoError(t, err)
	require.Equal(t, 1, q.Used)
	require.Equal(t, relNow+contact.Window, q.ResetsAt)
}

func TestReserveSendDailyLimitAndWindow(t *testing.T) {
	s := newRelationStore(t)
	for i := 0; i < contact.DailyLimit; i++ {
		_, err := s.ReserveSend("me", fmt.Sprintf("p%d", i), false, relNow+int64(i))
		require.NoError(t, err)
	}
	_, err := s.ReserveSend("me", "late", false, relNow+100)
	var dl *contact.DailyLimitError
	require.ErrorAs(t, err, &dl)
	require.Equal(t, relNow+contact.Window, dl.ResetsAt)

	// The refused attempt left no trace.
	p, err := s.Get("me", "late")
	require.NoError(t, err)
	require.Zero(t, p.Relation.Sent)

	// Another identity has its own quota.
	_, err = s.ReserveSend("other", "late", false, relNow+100)
	require.NoError(t, err)

	// When the first initiation leaves the window, one slot opens.
	_, err = s.ReserveSend("me", "late", false, relNow+contact.Window)
	require.NoError(t, err)
	_, err = s.ReserveSend("me", "later", false, relNow+contact.Window)
	require.ErrorAs(t, err, &dl)
}

func TestContactsAreFriendsUnlessBlocked(t *testing.T) {
	s := newRelationStore(t)
	for i := 0; i < 5; i++ {
		d, err := s.ReserveSend("me", "alice", true, relNow)
		require.NoError(t, err)
		require.False(t, d.Initiation)
	}
	p, err := s.Get("me", "alice")
	require.NoError(t, err)
	require.Zero(t, p.Relation.Sent, "friend sends are not counted")

	rc, err := s.ClassifyReceive("me", "alice", "e1", true, relNow)
	require.NoError(t, err)
	require.Equal(t, Receipt{Disposition: contact.Show}, rc)

	// A contact is a friend only through the keystore; nothing is stored, so
	// removing the contact later makes them a stranger again.
	var rows int
	require.NoError(t, s.db.QueryRow(`SELECT COUNT(*) FROM peer_relation`).Scan(&rows))
	require.Zero(t, rows)

	_, err = s.Update("me", "alice", relNow, func(p PeerRelation) (PeerRelation, error) {
		p.Relation = contact.Block(p.Relation)
		return p, nil
	})
	require.NoError(t, err)
	_, err = s.ReserveSend("me", "alice", true, relNow)
	require.ErrorIs(t, err, contact.ErrPeerBlocked, "block wins over the contact list")
	rc, err = s.ClassifyReceive("me", "alice", "e2", true, relNow)
	require.NoError(t, err)
	require.Equal(t, contact.Drop, rc.Disposition)
}

func TestClassifyReceive(t *testing.T) {
	s := newRelationStore(t)
	want := []contact.Disposition{contact.Request, contact.Show, contact.Suppress, contact.Suppress}
	for i, w := range want {
		rc, err := s.ClassifyReceive("me", "carol", fmt.Sprintf("ev%d", i), false, relNow+int64(i))
		require.NoError(t, err)
		require.Equal(t, Receipt{Disposition: w, Stranger: true, Seq: i + 1}, rc, "message %d", i+1)
	}
	// Redelivery of the first event returns the stored decision and does not count.
	rc, err := s.ClassifyReceive("me", "carol", "ev0", false, relNow+9)
	require.NoError(t, err)
	require.Equal(t, Receipt{Disposition: contact.Request, Stranger: true, Seq: 1}, rc)
	p, err := s.Get("me", "carol")
	require.NoError(t, err)
	require.Equal(t, 4, p.Relation.Received)

	// The same event ID for another identity is a separate receipt.
	rc, err = s.ClassifyReceive("other", "carol", "ev0", false, relNow)
	require.NoError(t, err)
	require.Equal(t, contact.Request, rc.Disposition)

	// A reply to carol is not an initiation.
	d, err := s.ReserveSend("me", "carol", false, relNow+10)
	require.NoError(t, err)
	require.False(t, d.Initiation)

	// A stored decision outlives a later accept: an old suppressed event stays suppressed.
	_, err = s.Update("me", "carol", relNow+11, func(p PeerRelation) (PeerRelation, error) {
		r, err := contact.Accept(p.Relation)
		p.Relation = r
		return p, err
	})
	require.NoError(t, err)
	rc, err = s.ClassifyReceive("me", "carol", "ev2", false, relNow+12)
	require.NoError(t, err)
	require.Equal(t, contact.Suppress, rc.Disposition)
	rc, err = s.ClassifyReceive("me", "carol", "ev9", false, relNow+12)
	require.NoError(t, err)
	require.Equal(t, Receipt{Disposition: contact.Show}, rc, "new events from a friend are shown")
}

func TestUpdateAcceptIgnoreAndRollback(t *testing.T) {
	s := newRelationStore(t)
	_, err := s.ClassifyReceive("me", "dave", "d1", false, relNow)
	require.NoError(t, err)

	p, err := s.Update("me", "dave", relNow+1, func(p PeerRelation) (PeerRelation, error) {
		p.Ignored = true
		return p, nil
	})
	require.NoError(t, err)
	require.True(t, p.Ignored)

	boom := errors.New("boom")
	_, err = s.Update("me", "dave", relNow+2, func(p PeerRelation) (PeerRelation, error) {
		p.Relation.Received = 99
		return p, boom
	})
	require.ErrorIs(t, err, boom)
	p, err = s.Get("me", "dave")
	require.NoError(t, err)
	require.Equal(t, 1, p.Relation.Received, "a failed update writes nothing")

	_, err = s.Update("me", "dave", relNow+3, func(p PeerRelation) (PeerRelation, error) {
		r, err := contact.Accept(p.Relation)
		p.Relation = r
		return p, err
	})
	require.NoError(t, err)

	p, err = s.Get("me", "dave")
	require.NoError(t, err)
	require.Equal(t, contact.Friend, p.Relation.State)
	require.Equal(t, relNow, p.FirstContactAt)
	require.Equal(t, relNow+3, p.UpdatedAt)
}

// Concurrent senders (CLI and daemon) must not both pass the same check.
func TestReserveSendIsAtomic(t *testing.T) {
	s := newRelationStore(t)
	const workers = 8
	var (
		wg sync.WaitGroup
		mu sync.Mutex
		ok int
	)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.ReserveSend("me", "eve", false, relNow); err == nil {
				mu.Lock()
				ok++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	require.Equal(t, contact.PerStrangerLimit, ok)
	q, err := s.Quota("me", relNow)
	require.NoError(t, err)
	require.Equal(t, 1, q.Used, "one initiation, not one per racing sender")
}

func TestUnknownStateIsAnError(t *testing.T) {
	s := newRelationStore(t)
	_, err := s.db.Exec(`PRAGMA ignore_check_constraints = ON`)
	require.NoError(t, err)
	_, err = s.db.Exec(`INSERT INTO peer_relation (identity_npub, peer_npub, state, first_contact_at, updated_at)
		VALUES ('me', 'x', 'weird', 1, 1)`)
	require.NoError(t, err)
	_, err = s.Get("me", "x")
	require.Error(t, err)
}

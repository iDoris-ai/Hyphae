package friend

import (
	"database/sql"
	"errors"
	"time"

	"github.com/iDoris-ai/hyphae/internal/identity"
	"github.com/iDoris-ai/hyphae/internal/social/contact"
	"github.com/iDoris-ai/hyphae/internal/storage"
	"github.com/iDoris-ai/hyphae/pkg/types"
)

// The stranger actions shared by the `friend` CLI and the TUI. Peers are
// canonical npubs; callers map the errors to their own output.

// ErrSelf: the action targets one of the caller's own identity.
var ErrSelf = errors.New("that is you")

// Pending is one stranger who wrote and waits for accept, ignore or block.
type Pending struct {
	Npub         string `json:"npub"`
	FirstMessage string `json:"first_message"`
	Received     int    `json:"received"`
	Suppressed   int    `json:"suppressed"`
	FirstSeenAt  int64  `json:"first_seen_at"`
}

// ListPending returns me's strangers who wrote and were not ignored, blocked
// or added as a contact.
func ListPending(db *sql.DB, ks *types.KeyStore, me string) ([]Pending, error) {
	rows, err := storage.NewRelationStore(db).List(me)
	if err != nil {
		return nil, err
	}
	out := []Pending{}
	for _, p := range rows {
		if p.Relation.State != contact.Stranger || p.Relation.Received == 0 || p.Ignored || isContactNpub(ks, p.Peer) {
			continue
		}
		first, _, err := storage.FirstIncoming(db, me, p.Peer)
		if err != nil {
			return nil, err
		}
		out = append(out, Pending{Npub: p.Peer, FirstMessage: first, Received: p.Relation.Received,
			Suppressed: max(0, p.Relation.Received-contact.PerStrangerLimit), FirstSeenAt: p.FirstContactAt})
	}
	return out, nil
}

func update(db *sql.DB, me, peer string, fn func(storage.PeerRelation, int64) (storage.PeerRelation, error)) (storage.PeerRelation, error) {
	if peer == me {
		return storage.PeerRelation{}, ErrSelf
	}
	now := time.Now().Unix()
	return storage.NewRelationStore(db).Update(me, peer, now, func(p storage.PeerRelation) (storage.PeerRelation, error) {
		return fn(p, now)
	})
}

// Ignore clears a stranger's request; they stay a stranger.
func Ignore(db *sql.DB, me, peer string) (storage.PeerRelation, error) {
	return update(db, me, peer, func(p storage.PeerRelation, _ int64) (storage.PeerRelation, error) {
		if p.Relation.State != contact.Stranger {
			return p, contact.ErrNotStranger
		}
		p.Ignored = true
		return p, nil
	})
}

// Block blocks peer from any state and removes them from contacts; it
// returns the removed contact nicknames.
func Block(db *sql.DB, ks *types.KeyStore, me, peer string) (storage.PeerRelation, []string, error) {
	p, err := update(db, me, peer, func(p storage.PeerRelation, _ int64) (storage.PeerRelation, error) {
		p.Relation = contact.Block(p.Relation)
		return p, nil
	})
	if err != nil {
		return p, nil, err
	}
	removed, err := identity.RemoveContactsByNpub(ks, peer)
	if removed == nil {
		removed = []string{}
	}
	return p, removed, err
}

// Unblock makes a blocked peer a stranger again with the counts they had.
func Unblock(db *sql.DB, me, peer string) (storage.PeerRelation, error) {
	return update(db, me, peer, func(p storage.PeerRelation, now int64) (storage.PeerRelation, error) {
		var err error
		p.Relation, err = contact.Unblock(p.Relation, now)
		return p, err
	})
}

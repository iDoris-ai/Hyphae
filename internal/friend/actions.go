package friend

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"fiatjaf.com/nostr"

	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/internal/identity"
	"github.com/iDoris-ai/hyphae/internal/messaging"
	"github.com/iDoris-ai/hyphae/internal/social/contact"
	"github.com/iDoris-ai/hyphae/internal/storage"
	"github.com/iDoris-ai/hyphae/pkg/types"
)

// The stranger actions shared by the `friend` CLI and the TUI. Peers are
// canonical npubs; callers map the errors to their own output.

// ErrSelf: the action targets one of the caller's own identity.
var ErrSelf = errors.New("that is you")

// ErrNicknameTaken: the chosen contact nickname belongs to someone else.
var ErrNicknameTaken = errors.New("contact nickname already exists")

// ErrNotPublished: accepted locally, but no relay took the friend_accept.
var ErrNotPublished = errors.New("accepted locally, but no relay took the friend_accept; run accept again to resend")

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

// AcceptResult is what Accept did.
type AcceptResult struct {
	Nickname    string
	Relays      []relayResult
	PublishedTo int
}

// Accept makes a stranger who wrote to me a friend, adds them as a contact
// (nick, or a name derived from the npub) and publishes a friend_accept
// answering their first message. It is safe to repeat: on a friend it only
// resends, so a run that returned ErrNotPublished can be retried.
func Accept(ctx context.Context, db *sql.DB, ks *types.KeyStore, me *types.Identity, sk nostr.SecretKey,
	relays []string, peer, nick string) (AcceptResult, error) {
	var res AcceptResult
	if peer == me.Npub {
		return res, ErrSelf
	}
	peerPK, err := common.ParsePublicKey(peer)
	if err != nil {
		return res, err
	}
	if ct, taken := ks.Contacts[nick]; nick != "" && taken && ct.Npub != peer {
		// Checked before the relation changes, so nothing is left half done.
		return res, fmt.Errorf("%w: %q", ErrNicknameTaken, nick)
	}
	_, answers, err := storage.FirstIncoming(db, me.Npub, peer)
	if err != nil {
		return res, err
	}
	if answers == "" {
		return res, contact.ErrNothingToAccept
	}
	if _, err := storage.NewRelationStore(db).Update(me.Npub, peer, time.Now().Unix(),
		func(p storage.PeerRelation) (storage.PeerRelation, error) {
			if p.Relation.State == contact.Friend {
				return p, nil
			}
			r, err := contact.Accept(p.Relation)
			p.Relation = r
			return p, err
		}); err != nil {
		return res, err
	}
	if res.Nickname, err = ensureContact(ks, peer, nick); err != nil {
		return res, err
	}
	ev, err := messaging.BuildFriendAccept(sk, peerPK, answers, nostr.Now())
	if err != nil {
		return res, err
	}
	results, okURLs, _ := publish(ctx, relays, []nostr.Event{*ev})
	res.Relays, res.PublishedTo = results, len(okURLs)
	if res.PublishedTo == 0 {
		return res, ErrNotPublished
	}
	return res, nil
}

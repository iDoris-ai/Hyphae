// Package contact holds the pure rules for messaging people who are not yet
// friends: two messages per direction until one side accepts, and at most ten
// new strangers per rolling 24 hours (docs/search/discover-friends.md).
//
// Nothing here touches storage, relays or clocks. Callers pass the stored
// Relation, the stored initiation times and a trusted local now, then persist
// the result themselves.
package contact

import (
	"errors"
	"fmt"
)

const (
	// PerStrangerLimit is how many messages each side may send while strangers.
	PerStrangerLimit = 2
	// DailyLimit is how many new strangers one identity may start talking to
	// within Window.
	DailyLimit = 10
	// Window is the rolling window for DailyLimit, in seconds.
	Window = 24 * 60 * 60
)

// State is the local relationship with one peer.
type State int

const (
	// Stranger is neither a contact nor blocked. It is the zero value, so a
	// peer with no stored row is a stranger.
	Stranger State = iota
	// Friend lifts every limit. Existing contacts are friends.
	Friend
	// Blocked drops everything from the peer and forbids sending to it.
	Blocked
)

func (s State) String() string {
	switch s {
	case Stranger:
		return "stranger"
	case Friend:
		return "friend"
	case Blocked:
		return "blocked"
	}
	return fmt.Sprintf("State(%d)", int(s))
}

// Relation is what one identity stores about one peer. Sent and Received count
// messages in each direction while strangers; Received counts distinct event
// IDs, so the caller must deduplicate before calling Received.
type Relation struct {
	State    State
	Sent     int
	Received int
	// UnblockedAt is the local time of the last Unblock. A friend_accept only
	// counts when it answers a message sent after it, so an accept from before
	// a block cannot be replayed.
	UnblockedAt int64
}

var (
	// ErrPeerBlocked: the caller blocked this peer; unblock first.
	ErrPeerBlocked = errors.New("peer is blocked")
	// ErrStrangerMessageLimit: already sent PerStrangerLimit messages.
	ErrStrangerMessageLimit = errors.New("stranger message limit reached")
	// ErrNothingToAccept: the peer never sent anything to accept.
	ErrNothingToAccept = errors.New("peer has not sent a message")
	// ErrNotStranger: the action only applies to a stranger.
	ErrNotStranger = errors.New("peer is not a stranger")
)

// DailyLimitError reports that DailyLimit is used up. ResetsAt is when the
// oldest initiation in the window leaves it.
type DailyLimitError struct {
	Used     int
	ResetsAt int64
}

func (e *DailyLimitError) Error() string {
	return fmt.Sprintf("stranger daily limit reached (%d/%d, resets at %d)", e.Used, DailyLimit, e.ResetsAt)
}

// Quota summarises DailyLimit at now. Initiations dated in the future (the
// clock went back) stay in the window, so turning the clock back cannot free
// quota.
type Quota struct {
	Used      int
	Remaining int
	// ResetsAt is when the oldest counted initiation leaves the window; zero
	// when nothing is counted.
	ResetsAt int64
}

// QuotaAt counts the initiations that fall inside the window ending at now.
func QuotaAt(initiations []int64, now int64) Quota {
	var q Quota
	for _, at := range initiations {
		if at <= now-Window {
			continue
		}
		q.Used++
		if q.ResetsAt == 0 || at+Window < q.ResetsAt {
			q.ResetsAt = at + Window
		}
	}
	q.Remaining = DailyLimit - q.Used
	if q.Remaining < 0 {
		q.Remaining = 0
	}
	return q
}

// SendDecision tells the caller what to record once the message is sent.
type SendDecision struct {
	// Initiation is true when this message starts a conversation with a new
	// stranger: record now as an initiation.
	Initiation bool
	// CountSent is true when Sent must be incremented (only while strangers).
	CountSent bool
}

// CheckSend decides whether one new message may go to the peer. A retry of a
// message that already passed must not be checked again.
func CheckSend(r Relation, initiations []int64, now int64) (SendDecision, error) {
	switch r.State {
	case Friend:
		return SendDecision{}, nil
	case Blocked:
		return SendDecision{}, ErrPeerBlocked
	case Stranger:
	default:
		return SendDecision{}, fmt.Errorf("unknown state %v", r.State)
	}
	if r.Sent >= PerStrangerLimit {
		return SendDecision{}, ErrStrangerMessageLimit
	}
	d := SendDecision{CountSent: true}
	if r.Sent == 0 && r.Received == 0 {
		q := QuotaAt(initiations, now)
		if q.Remaining == 0 {
			return SendDecision{}, &DailyLimitError{Used: q.Used, ResetsAt: q.ResetsAt}
		}
		d.Initiation = true
	}
	return d, nil
}

// ApplySend returns the relation after a message allowed by d was sent.
func ApplySend(r Relation, d SendDecision) Relation {
	if d.CountSent {
		r.Sent++
	}
	return r
}

// Disposition is what the receiving side does with one incoming message.
type Disposition int

const (
	// Show displays the message normally.
	Show Disposition = iota
	// Request displays it as a stranger request offering accept/ignore/block.
	Request
	// Suppress stores it for the record but neither shows nor notifies.
	Suppress
	// Drop discards it.
	Drop
)

func (d Disposition) String() string {
	return [...]string{"show", "request", "suppress", "drop"}[d]
}

// CheckReceive decides how to treat one new (deduplicated) incoming message,
// given the relation before it arrived. Order is the local arrival order;
// the sender's created_at plays no part.
func CheckReceive(r Relation) Disposition {
	switch r.State {
	case Friend:
		return Show
	case Blocked:
		return Drop
	}
	switch {
	case r.Received >= PerStrangerLimit:
		return Suppress
	case r.Received == 0 && r.Sent == 0:
		return Request
	default:
		return Show
	}
}

// ApplyReceive returns the relation after one message was received. Blocked
// peers are not counted; suppressed messages still are, so the count keeps
// showing how far past the limit a peer went.
func ApplyReceive(r Relation) Relation {
	if r.State == Stranger {
		r.Received++
	}
	return r
}

// Accept is the local user accepting the peer as a friend. Only a stranger who
// has sent something can be accepted.
func Accept(r Relation) (Relation, error) {
	switch {
	case r.State != Stranger:
		return r, ErrNotStranger
	case r.Received == 0:
		return r, ErrNothingToAccept
	}
	return Relation{State: Friend}, nil
}

// AcceptedByPeer applies a friend_accept from the peer. found must be true
// only when the accept's "answers" names a message this identity sent to that
// peer and has on record, and sentAt is the local time that message was sent.
// Signature and recipient checks happen before this. A blocked peer is
// refused, and so is an accept answering a message sent before the last
// unblock, so an old accept cannot be replayed.
func AcceptedByPeer(r Relation, found bool, sentAt int64) (Relation, error) {
	switch {
	case r.State == Friend:
		return r, nil
	case r.State != Stranger:
		return r, ErrNotStranger
	case !found || r.Sent == 0 || sentAt <= r.UnblockedAt:
		return r, ErrNothingToAccept
	}
	return Relation{State: Friend}, nil
}

// Block blocks the peer from any state, keeping the stranger counts so that a
// later unblock does not hand out fresh quota. Blocking a friend leaves zero
// counts, so after unblock that peer is a stranger with full quota.
func Block(r Relation) Relation {
	r.State = Blocked
	return r
}

// ErrNotBlocked: Unblock on a peer that is not blocked.
var ErrNotBlocked = errors.New("peer is not blocked")

// Unblock returns a blocked peer to stranger with the counts it had, and
// records now as UnblockedAt.
func Unblock(r Relation, now int64) (Relation, error) {
	if r.State != Blocked {
		return r, ErrNotBlocked
	}
	r.State = Stranger
	r.UnblockedAt = now
	return r, nil
}

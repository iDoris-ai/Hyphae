package referral

import (
	"errors"
	"fmt"
	"slices"

	"github.com/iDoris-ai/hyphae/internal/social"
)

// State is the lifecycle position of one request.
type State int

const (
	Requested State = iota
	Forwarded
	Accepted
	Settled
	Declined
	Expired
)

// Flow is the state machine for one request (not concurrency-safe).
type Flow struct {
	req              Request
	reqID, requester string
	chain            []string // recipients in order; last is the current holder
	forwarder        string   // author of the last forward: the payee
	fwdID, acceptID  string
	created          int64 // request created_at
	acceptAt         int64 // trusted time the accept was observed
	hops             int
	state            State
	seen             map[string]struct{} // applied event IDs
}

// NewFlow starts a flow from a referral_request. firstHolder is the request's
// recipient (routing is not in the body); it must not be the requester.
func NewFlow(env social.Envelope, firstHolder string, now int64) (*Flow, error) {
	r, err := ParseRequest(env)
	if err == nil && (!hexOK(firstHolder) || firstHolder == env.Author) {
		err = errors.New("invalid first holder")
	}
	if err != nil {
		return nil, err
	}
	if env.CreatedAt > now+social.MaxClockSkew {
		return nil, ErrTooEarly
	}
	return &Flow{
		req: r, reqID: env.ID, requester: env.Author, chain: []string{firstHolder},
		created: env.CreatedAt, hops: *r.HopsLeft, seen: map[string]struct{}{env.ID: {}},
	}, nil
}

// State reports the state as of now. Expiry comes from the clock, not from
// message timestamps, so a far-future event cannot kill a flow. Accepted
// flows never expire.
func (f *Flow) State(now int64) State {
	if f.state < Accepted && now > f.req.ExpiresAt {
		return Expired
	}
	return f.state
}

// ErrTooEarly means created_at is ahead of the local clock by more than
// social.MaxClockSkew. The event is deferred: offer it again later.
var ErrTooEarly = errors.New("event is dated in the future")

// ErrNotNow means the event is well formed but not allowed in the flow's
// current state. A relay does not guarantee order (a receipt may arrive
// before the accept it cites), so the caller should keep such events (bounded
// per flow) and offer them again after the next event is applied.
var ErrNotNow = errors.New("event not allowed in the current state")

func notNow(why string) error { return fmt.Errorf("%w: %s", ErrNotNow, why) }

// Apply feeds one verified event in. to is the recipient of a referral_forward
// (ignored otherwise). now is the trusted local time at which the caller first
// observed the event (persist it and pass the same value when rebuilding from
// a log): created_at is chosen by the sender, so only now can expire a flow,
// and a backdated accept cannot revive an expired one.
//
// Redelivering an applied event ID is a no-op. Nothing else is remembered;
// see ErrNotNow and ErrTooEarly. Terminal states reject everything.
func (f *Flow) Apply(env social.Envelope, to string, now int64) error {
	if _, dup := f.seen[env.ID]; dup {
		return nil
	}
	if f.state >= Settled {
		return errors.New("terminal")
	}
	if f.state < Accepted && now > f.req.ExpiresAt {
		f.state = Expired
		return errors.New("expired")
	}
	h, err := social.ParseHeader(env)
	if err != nil {
		return err
	}
	if env.CreatedAt > now+social.MaxClockSkew {
		return ErrTooEarly
	}
	if err = f.step(env, h.Type, to, now); err == nil {
		f.seen[env.ID] = struct{}{}
	}
	return err
}

func (f *Flow) step(env social.Envelope, typ, to string, now int64) error {
	// Timestamps of different authors are not comparable, so order comes from
	// the state machine, not from created_at. After acceptance the debt
	// stands, so a receipt may land past expires_at; otherwise the debtor
	// could escape by waiting out the expiry.
	if env.CreatedAt < f.created || (f.state < Accepted && env.CreatedAt > f.req.ExpiresAt) {
		return errors.New("event predates the request or is after expires_at")
	}
	switch typ {
	case "referral_forward":
		return f.forward(env, to)
	case "referral_accept", "referral_decline":
		return f.reply(env, typ, now)
	case "receipt":
		return f.receipt(env)
	}
	return errors.New("unexpected type")
}

func (f *Flow) forward(env social.Envelope, to string) error {
	b, err := ParseForward(env)
	if err != nil {
		return err
	}
	if f.state > Forwarded || env.Author != f.chain[len(f.chain)-1] {
		return notNow("only the current holder may forward, and only while open")
	}
	if b.Request != f.reqID || b.Intent != f.req.Intent {
		return errors.New("forward does not match the request")
	}
	if f.hops < 1 || *b.HopsLeft != f.hops-1 {
		return errors.New("hops_left must drop by exactly 1, and not forward at 0")
	}
	// Also rejects anyone already in the chain (loops): stricter than the
	// draft, which names only the requester and the forwarder themselves.
	if !hexOK(to) || to == f.requester || to == env.Author || slices.Contains(f.chain, to) {
		return errors.New("invalid forward recipient")
	}
	f.chain, f.forwarder, f.fwdID, f.hops, f.state = append(f.chain, to), env.Author, env.ID, f.hops-1, Forwarded
	return nil
}

func (f *Flow) reply(env social.Envelope, typ string, now int64) error {
	b, err := ParseReply(env, typ)
	if err != nil {
		return err
	}
	// Must answer the latest forward, not an older one in the chain.
	if f.state != Forwarded || env.Author != f.chain[len(f.chain)-1] || b.Forward != f.fwdID {
		return notNow("only the recipient of the last forward may answer it")
	}
	if typ == "referral_decline" {
		f.state = Declined
	} else {
		// The grace period runs from when the accept was observed, not from
		// the target's own timestamp.
		f.state, f.acceptID, f.acceptAt = Accepted, env.ID, now
	}
	return nil
}

func (f *Flow) receipt(env social.Envelope) error {
	b, err := ParseReceipt(env)
	if err != nil {
		return err
	}
	// Nothing is owed before the target accepts, so no receipt counts earlier.
	if f.state != Accepted || env.Author != f.requester || b.To != f.forwarder || b.Amount != f.req.Offer || b.Ref != f.acceptID {
		return notNow("receipt must be requester -> last forwarder, for the offer, citing the accept")
	}
	f.state = Settled // a second receipt for this ref hits the terminal check
	return nil
}

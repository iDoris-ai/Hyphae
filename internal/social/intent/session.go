package intent

import (
	"errors"
	"fmt"

	"github.com/iDoris-ai/hyphae/internal/social"
)

// State is a session's position in the handshake.
type State int

// Session states. Matched, Declined, Withdrawn and Expired are terminal.
const (
	Open State = iota
	Responded
	Disclosing
	Matched
	Declined
	Withdrawn
	Expired
)

// Terminal reports whether no further message can change the state.
func (s State) Terminal() bool { return s >= Matched }

// Errors returned by Apply.
var (
	ErrTerminal = errors.New("intent: session is in a terminal state")
	ErrExpired  = errors.New("intent: message is after expires_at")
	ErrNotParty = errors.New("intent: author is not a party to this session")
	// ErrWrongIntent means the message references a different intent.
	ErrWrongIntent = errors.New("intent: message does not reference this intent")
	// ErrRejected means the message is not allowed in the current state. It
	// may become applicable after other messages arrive.
	ErrRejected = errors.New("intent: message not allowed now")
	// ErrTooEarly means created_at is ahead of the local clock by more than
	// social.MaxClockSkew. The message is deferred: offer it again later.
	ErrTooEarly = errors.New("intent: message is dated in the future")
)

// Key identifies a session: one intent event ID and one responder pubkey.
type Key struct{ Intent, Responder string }

// Session is the handshake between intent author A (side 0) and one responder
// B (side 1), fed in delivery order. Routing A's private messages to the right
// session is the caller's job: an envelope does not name its recipient.
type Session struct {
	key      Key
	author   string
	expires  int64 // intent expires_at
	state    State
	round    [2]int // highest round each side has sent (B's counts once engaged)
	accepted [2]bool
	applied  map[string]bool
}

// NewSession starts an Open session for a parsed intent and a responder. The
// intent author cannot respond to their own intent. now is the trusted local
// time; an intent dated ahead of it by more than social.MaxClockSkew is
// deferred with ErrTooEarly.
func NewSession(intent Message, responder string, now int64) (*Session, error) {
	b, ok := intent.Body.(*Intent)
	e := intent.Envelope
	switch {
	case !ok || !social.ValidHex32(responder):
		return nil, errors.New("intent: session needs an intent message and a hex responder key")
	case e.CreatedAt > now+social.MaxClockSkew:
		return nil, ErrTooEarly
	case responder == e.Author:
		return nil, fmt.Errorf("%w: author cannot respond to their own intent", ErrNotParty)
	}
	// The public intent counts as A's round 0, so after respond (B's round 0)
	// either side may open round 1.
	return &Session{key: Key{e.ID, responder}, author: e.Author, expires: b.ExpiresAt, applied: map[string]bool{e.ID: true}}, nil
}

// Key returns the session's identity.
func (s *Session) Key() Key { return s.key }

// State returns the state as of the last applied message.
func (s *Session) State() State { return s.state }

// StateAt is State, but reports Expired once now is past expires_at.
func (s *Session) StateAt(now int64) State {
	if !s.state.Terminal() && now > s.expires {
		return Expired
	}
	return s.state
}

// Apply advances the session with m. now is the trusted local time at which
// the caller first observed m (persist it and pass the same value when
// rebuilding from a log): created_at is chosen by the sender, so only now can
// expire a session, and a backdated message cannot revive an expired one.
//
// Redelivering an applied event ID is a no-op. Nothing else is remembered:
// ErrRejected means "not allowed in the current state" and ErrTooEarly means
// "dated ahead of the clock"; a relay does not guarantee order, so the caller
// should keep such messages (bounded per session) and offer them again after
// the next message is applied. A message that is wrong in itself (other
// intent, outsider, dated after expires_at) fails the same way every time.
func (s *Session) Apply(m Message, now int64) error {
	e := m.Envelope
	if s.applied[e.ID] {
		return nil
	}
	if s.state.Terminal() {
		return ErrTerminal
	}
	if now > s.expires {
		s.state = Expired
		return ErrExpired
	}
	// No floor on created_at: the responder's clock is not comparable with the
	// intent author's, and an intent up to MaxClockSkew ahead is accepted, so
	// an honest respond may be dated before the intent it answers.
	if m.Ref != s.key.Intent {
		return ErrWrongIntent
	}
	side := 0
	if e.Author == s.key.Responder {
		side = 1
	} else if e.Author != s.author {
		return ErrNotParty
	}
	if e.CreatedAt > now+social.MaxClockSkew {
		return ErrTooEarly
	}
	// Exactly expires_at is still valid. A later sender timestamp rejects
	// that one message; only the trusted clock above expires the session.
	if e.CreatedAt > s.expires {
		return ErrExpired
	}
	other, engaged, ok := 1-side, s.state == Responded || s.state == Disclosing, false
	switch b := m.Body.(type) {
	case *Respond: // only B, only once
		if ok = side == 1 && s.state == Open; ok {
			s.state = Responded // B's round 0; s.round[1] is already 0
		}
	case *Disclose:
		// Round n+1 needs both sides to have sent round n. Stricter than the
		// draft: once either side has accepted, disclosure is frozen.
		ok = engaged && !s.accepted[0] && !s.accepted[1] && b.Round <= MaxRound
		if ok = ok && b.Round == s.round[side]+1 && s.round[other] >= s.round[side]; ok {
			s.round[side], s.state = b.Round, Disclosing
		}
	case *Accept: // stricter than the draft: not before respond, once per side
		if ok = engaged && !s.accepted[side]; ok {
			s.accepted[side] = true
			if s.accepted[other] {
				s.state = Matched
			}
		}
	case *Decline: // stricter than the draft: not before respond
		if ok = engaged; ok {
			s.state = Declined
		}
	case *Withdraw: // only the intent author
		if ok = side == 0; ok {
			s.state = Withdrawn
		}
	}
	if !ok {
		return fmt.Errorf("%w: %s from side %d in state %d", ErrRejected, m.Type, side, s.state)
	}
	s.applied[e.ID] = true
	return nil
}

package intent

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/iDoris-ai/hyphae/internal/social"
)

// harness drives one session with auto-numbered event IDs.
type harness struct {
	t *testing.T
	s *Session
	n int
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	m, err := Parse(env(1, pkA, t0, intentBody(nil)))
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewSession(m, pkB, t0)
	if err != nil {
		t.Fatal(err)
	}
	return &harness{t: t, s: s, n: 1}
}

func (h *harness) msg(author string, at int64, content string) Message {
	h.t.Helper()
	h.n++
	m, err := Parse(env(h.n, author, at, content))
	if err != nil {
		h.t.Fatalf("parse %s: %v", content, err)
	}
	return m
}

// ok applies a message that must be accepted and checks the resulting state.
func (h *harness) ok(author, content string, want State) Message {
	h.t.Helper()
	m := h.msg(author, t0+10, content)
	if err := h.s.apply(m); err != nil {
		h.t.Fatalf("apply %s by %s: %v", content, author[:1], err)
	}
	if got := h.s.State(); got != want {
		h.t.Fatalf("after %s: state %d, want %d", content, got, want)
	}
	return m
}

// bad applies a message that must be rejected with target (nil = any error)
// and leave the state unchanged.
func (h *harness) bad(author string, at int64, content string, target error) {
	h.t.Helper()
	before := h.s.State()
	// The clock stays within the intent's lifetime so that a late created_at
	// exercises the sender-timestamp rules rather than clock expiry.
	err := h.s.Apply(h.msg(author, at, content), min(at, expires))
	if err == nil || (target != nil && !errors.Is(err, target)) {
		h.t.Fatalf("apply %s by %s: err %v, want %v", content, author[:1], err, target)
	}
	if got := h.s.State(); got != before {
		h.t.Fatalf("rejected %s changed state %d -> %d", content, before, got)
	}
}

func respond() string       { return ref("respond", `,"fields":{"k":"v"}`) }
func disclose(n int) string { return ref("disclose", fmt.Sprintf(`,"round":%d,"fields":{"k":"v"}`, n)) }

var (
	accept   = ref("accept", "")
	decline  = ref("decline", "")
	withdraw = ref("withdraw", "")
)

func TestNewSession(t *testing.T) {
	m, _ := Parse(env(1, pkA, t0, intentBody(nil)))
	s, err := NewSession(m, pkB, t0)
	if err != nil || s.Key() != (Key{intentID, pkB}) || s.State() != Open {
		t.Fatalf("got %+v, %v", s, err)
	}
	if _, err := NewSession(m, pkA, t0); !errors.Is(err, ErrNotParty) {
		t.Errorf("author as responder: %v", err)
	}
	if _, err := NewSession(m, strings.Repeat("B", 64), t0); err == nil {
		t.Error("non-hex responder accepted")
	}
	acc, _ := Parse(env(2, pkB, t0, accept))
	if _, err := NewSession(acc, pkB, t0); err == nil {
		t.Error("session from a non-intent accepted")
	}
}

func TestRespond(t *testing.T) {
	h := newHarness(t)
	h.bad(pkA, t0+10, respond(), ErrRejected) // author cannot respond to own intent
	h.bad(pkC, t0+10, respond(), ErrNotParty)
	h.ok(pkB, respond(), Responded)
	h.bad(pkB, t0+10, respond(), ErrRejected) // only once
}

func TestOnlyPartiesAndRightIntent(t *testing.T) {
	h := newHarness(t)
	h.ok(pkB, respond(), Responded)
	for _, c := range []string{disclose(1), accept, decline, withdraw} {
		h.bad(pkC, t0+10, c, ErrNotParty)
	}
	other := strings.Replace(accept, intentID, id(99), 1)
	h.bad(pkA, t0+10, other, nil)
	h.ok(pkA, accept, Responded) // the parties still can
}

func TestDisclosureAlternates(t *testing.T) {
	h := newHarness(t)
	h.bad(pkA, t0+10, disclose(1), ErrRejected) // before respond
	h.ok(pkB, respond(), Responded)
	h.ok(pkA, disclose(1), Disclosing)
	h.bad(pkA, t0+10, disclose(2), ErrRejected) // B has not sent round 1
	h.bad(pkA, t0+10, disclose(1), ErrRejected) // repeat of own round
	h.bad(pkB, t0+10, disclose(2), ErrRejected) // skipping round 1
	h.ok(pkB, disclose(1), Disclosing)
	h.ok(pkB, disclose(2), Disclosing) // A has sent round 1
	h.bad(pkB, t0+10, disclose(3), ErrRejected)
	h.ok(pkA, disclose(2), Disclosing)
	h.ok(pkA, disclose(3), Disclosing)
	h.ok(pkB, disclose(3), Disclosing)
	h.ok(pkB, disclose(4), Disclosing)
	h.ok(pkA, disclose(4), Disclosing)
	if a, b := h.s.round[0], h.s.round[1]; a != MaxRound || b != MaxRound {
		t.Fatalf("rounds %d/%d", a, b)
	}
	// Round 5 never parses; a hand-built one is still refused by the session.
	m := h.msg(pkB, t0+10, disclose(4))
	m.Envelope.ID, m.Body = id(500), &Disclose{Ref: Ref{Intent: intentID}, Round: 5, Fields: map[string]string{"k": "v"}}
	if err := h.s.apply(m); !errors.Is(err, ErrRejected) {
		t.Fatalf("round 5: %v", err)
	}
	h.ok(pkA, accept, Disclosing)
	h.ok(pkB, accept, Matched)
}

func TestResponderMayOpenRoundOne(t *testing.T) {
	// The intent is A's round 0, so B may go to round 1 straight after respond.
	h := newHarness(t)
	h.ok(pkB, respond(), Responded)
	h.ok(pkB, disclose(1), Disclosing)
	h.bad(pkB, t0+10, disclose(2), ErrRejected)
}

func TestMatchNeedsBothAccepts(t *testing.T) {
	h := newHarness(t)
	h.bad(pkA, t0+10, accept, ErrRejected) // before respond
	h.ok(pkB, respond(), Responded)
	h.ok(pkB, accept, Responded)
	h.bad(pkB, t0+10, accept, ErrRejected)      // second accept, new event
	h.bad(pkA, t0+10, disclose(1), ErrRejected) // frozen after an accept
	h.ok(pkA, accept, Matched)
	h.bad(pkA, t0+10, decline, ErrTerminal)
}

func TestDecline(t *testing.T) {
	h := newHarness(t)
	h.bad(pkB, t0+10, decline, ErrRejected) // before respond
	h.ok(pkB, respond(), Responded)
	h.ok(pkA, accept, Responded)
	h.ok(pkB, decline, Declined) // either side, even after the other accepted
	h = newHarness(t)
	h.ok(pkB, respond(), Responded)
	h.ok(pkA, decline, Declined)
}

func TestWithdraw(t *testing.T) {
	h := newHarness(t)
	h.ok(pkA, withdraw, Withdrawn) // allowed while Open
	h = newHarness(t)
	h.ok(pkB, respond(), Responded)
	h.bad(pkB, t0+10, withdraw, ErrRejected)
	h.ok(pkA, disclose(1), Disclosing)
	h.ok(pkA, withdraw, Withdrawn)
}

func TestTerminalStatesRejectEverything(t *testing.T) {
	setups := map[State]func(h *harness){
		Matched:   func(h *harness) { h.ok(pkB, accept, Responded); h.ok(pkA, accept, Matched) },
		Declined:  func(h *harness) { h.ok(pkA, decline, Declined) },
		Withdrawn: func(h *harness) { h.ok(pkA, withdraw, Withdrawn) },
		Expired: func(h *harness) {
			if err := h.s.apply(h.msg(pkB, expires+1, accept)); !errors.Is(err, ErrExpired) {
				t.Fatalf("late accept: %v", err)
			}
		},
	}
	for want, setup := range setups {
		h := newHarness(t)
		h.ok(pkB, respond(), Responded)
		setup(h)
		if h.s.State() != want || !want.Terminal() {
			t.Fatalf("state %d, want terminal %d", h.s.State(), want)
		}
		for _, c := range []string{respond(), disclose(1), accept, decline, withdraw} {
			for _, who := range []string{pkA, pkB} {
				if err := h.s.apply(h.msg(who, t0+10, c)); !errors.Is(err, ErrTerminal) {
					t.Errorf("state %d: %s by %s: %v", want, c, who[:1], err)
				}
			}
		}
		if h.s.State() != want {
			t.Errorf("state moved from %d to %d", want, h.s.State())
		}
	}
}

func TestExpiry(t *testing.T) {
	h := newHarness(t)
	m := h.msg(pkB, expires, respond()) // exactly expires_at is still valid
	if err := h.s.apply(m); err != nil || h.s.State() != Responded {
		t.Fatalf("at expires_at: %v, %d", err, h.s.State())
	}
	if h.s.StateAt(expires) != Responded || h.s.StateAt(expires+1) != Expired {
		t.Fatal("StateAt does not report expiry by clock")
	}
	// An outsider's late event must not expire the session.
	h.bad(pkC, expires+100, accept, ErrNotParty)
	if err := h.s.apply(h.msg(pkA, expires+1, accept)); !errors.Is(err, ErrExpired) || h.s.State() != Expired {
		t.Fatalf("late accept: %v, %d", err, h.s.State())
	}
	h2 := newHarness(t)
	h2.ok(pkA, withdraw, Withdrawn)
	if h2.s.StateAt(expires+1) != Withdrawn {
		t.Fatal("StateAt overrode a terminal state")
	}
}

func TestResponderClockBehindIntentIsAccepted(t *testing.T) {
	// A's clock runs MaxClockSkew ahead of B's. B's respond is dated before
	// the intent it answers and must still apply: clocks of different authors
	// are not comparable.
	h := newHarness(t)
	m := h.msg(pkB, t0-social.MaxClockSkew+1, respond())
	if err := h.s.Apply(m, t0-social.MaxClockSkew+1); err != nil || h.s.State() != Responded {
		t.Fatalf("respond dated before the intent: %v, state %d", err, h.s.State())
	}
}

func TestWrongIntentReference(t *testing.T) {
	h := newHarness(t)
	other := strings.Replace(respond(), intentID, id(77), 1)
	if err := h.s.apply(h.msg(pkB, t0+1, other)); !errors.Is(err, ErrWrongIntent) || h.s.State() != Open {
		t.Fatalf("respond to another intent: %v, state %d", err, h.s.State())
	}
}

func TestRedeliveryIsIdempotent(t *testing.T) {
	h := newHarness(t)
	intent, _ := Parse(env(1, pkA, t0, intentBody(nil)))
	if err := h.s.apply(intent); err != nil || h.s.State() != Open {
		t.Fatalf("intent redelivered: %v", err)
	}
	r := h.ok(pkB, respond(), Responded)
	d := h.ok(pkA, disclose(1), Disclosing)
	for i := 0; i < 2; i++ {
		if err := h.s.apply(r); err != nil {
			t.Fatalf("respond redelivered: %v", err)
		}
		if err := h.s.apply(d); err != nil {
			t.Fatalf("disclose redelivered: %v", err)
		}
	}
	if h.s.State() != Disclosing || h.s.round[0] != 1 || h.s.round[1] != 0 {
		t.Fatalf("redelivery changed state: %d %v", h.s.State(), h.s.round)
	}
	h.ok(pkB, accept, Disclosing)
	last := h.ok(pkA, accept, Matched)
	if err := h.s.apply(last); err != nil { // redelivery after terminal too
		t.Fatalf("final accept redelivered: %v", err)
	}
	// A rejected event is not remembered: it is rejected again.
	h2 := newHarness(t)
	bad := h2.msg(pkB, t0+10, accept)
	for i := 0; i < 2; i++ {
		if err := h2.s.apply(bad); err == nil {
			t.Fatal("rejected event accepted on redelivery")
		}
	}
}

// apply feeds m with the local clock equal to its created_at.
func (s *Session) apply(m Message) error { return s.Apply(m, m.Envelope.CreatedAt) }

func TestClockExpiryCannotBeUndoneByBackdating(t *testing.T) {
	h := newHarness(t)
	h.ok(pkB, respond(), Responded)
	h.ok(pkA, accept, Responded)
	late := h.msg(pkB, t0+30, accept) // backdated: created well before expiry
	if err := h.s.Apply(late, expires+1); !errors.Is(err, ErrExpired) || h.s.State() != Expired {
		t.Fatalf("backdated accept after expiry: %v, state %d", err, h.s.State())
	}
	if err := h.s.Apply(h.msg(pkB, t0+31, accept), t0+40); !errors.Is(err, ErrTerminal) {
		t.Fatalf("expiry was not persisted: %v", err)
	}
}

func TestFutureDatedMessageIsDeferred(t *testing.T) {
	h := newHarness(t)
	m := h.msg(pkB, t0+social.MaxClockSkew+1, respond())
	if err := h.s.Apply(m, t0); !errors.Is(err, ErrTooEarly) || h.s.State() != Open {
		t.Fatalf("future-dated respond: %v, state %d", err, h.s.State())
	}
	if err := h.s.Apply(m, t0+1); err != nil || h.s.State() != Responded {
		t.Fatalf("deferred message must apply once the clock catches up: %v", err)
	}
	h2 := newHarness(t)
	if err := h2.s.Apply(h2.msg(pkB, t0+social.MaxClockSkew, respond()), t0); err != nil {
		t.Fatalf("skew at the limit rejected: %v", err)
	}
}

func TestOutOfOrderMessageCanBeRetried(t *testing.T) {
	// A's accept reaches the observer before B's respond. It is not allowed
	// yet, is not remembered, and applies when offered again.
	h := newHarness(t)
	early := h.msg(pkA, t0+20, accept)
	if err := h.s.apply(early); !errors.Is(err, ErrRejected) || h.s.State() != Open {
		t.Fatalf("accept before respond: %v, state %d", err, h.s.State())
	}
	h.ok(pkB, respond(), Responded)
	if err := h.s.apply(early); err != nil {
		t.Fatalf("retry after the prerequisite arrived: %v", err)
	}
	h.ok(pkB, accept, Matched)
}

func TestLateSenderTimestampDoesNotExpireSession(t *testing.T) {
	h := newHarness(t)
	late := h.msg(pkB, expires+1, respond()) // within clock skew of now, past expires_at
	if err := h.s.Apply(late, expires-100); !errors.Is(err, ErrExpired) || h.s.State() != Open {
		t.Fatalf("late-dated respond: %v, state %d", err, h.s.State())
	}
	if err := h.s.Apply(h.msg(pkB, expires-99, respond()), expires-99); err != nil || h.s.State() != Responded {
		t.Fatalf("session must still be usable: %v, state %d", err, h.s.State())
	}
}

func TestNewSessionDefersFutureIntent(t *testing.T) {
	m, err := Parse(env(1, pkA, t0, intentBody(nil)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewSession(m, pkB, t0-social.MaxClockSkew-1); !errors.Is(err, ErrTooEarly) {
		t.Fatalf("future-dated intent: %v", err)
	}
	if _, err := NewSession(m, pkB, t0-social.MaxClockSkew); err != nil {
		t.Fatalf("skew at the limit: %v", err)
	}
}

func TestNullFieldValueRejected(t *testing.T) {
	_, err := Parse(env(7, pkB, t0, ref("respond", `,"fields":{"age":null}`)))
	if err == nil {
		t.Fatal("null field value must not parse")
	}
}

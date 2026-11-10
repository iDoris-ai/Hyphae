package referral

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/iDoris-ai/hyphae/internal/social"
)

func hx(n int) string { return fmt.Sprintf("%064x", n) }

var (
	req, f1, f2, tgt = hx(1), hx(2), hx(3), hx(4) // requester, forwarders, target
	intent           = hx(99)
	reqID            = hx(10)
)

const (
	t0      = 100
	expires = 1000
)

func ev(id int, author string, at int64, body string) social.Envelope {
	return social.Envelope{ID: hx(id), Author: author, CreatedAt: at, Content: json.RawMessage(body)}
}

func reqBody(offer string, hops string, exp int64) string {
	return fmt.Sprintf(`{"v":1,"type":"referral_request","intent":%q,"offer":%s,"hops_left":%s,"expires_at":%d}`, intent, offer, hops, exp)
}

func fwdBody(hops int) string {
	return fmt.Sprintf(`{"v":1,"type":"referral_forward","request":%q,"intent":%q,"hops_left":%d}`, reqID, intent, hops)
}

func replyBody(typ string, fwd string) string {
	return fmt.Sprintf(`{"v":1,"type":%q,"forward":%q}`, typ, fwd)
}

func rcptBody(to string, amount string, ref string) string {
	return fmt.Sprintf(`{"v":1,"type":"receipt","to":%q,"amount":%s,"ref":%q}`, to, amount, ref)
}

// newFlow opens a request (offer 10, 2 hops) from req to f1.
func newFlow(t *testing.T) *Flow {
	t.Helper()
	f, err := NewFlow(ev(10, req, t0, reqBody("10", "2", expires)), f1, t0)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func mustApply(t *testing.T, f *Flow, e social.Envelope, to string) {
	t.Helper()
	if err := f.Apply(e, to, e.CreatedAt); err != nil {
		t.Fatalf("apply %s: %v", e.ID[:4], err)
	}
}

var badID = 900000

func mustFail(t *testing.T, f *Flow, e social.Envelope, to string, why string) {
	t.Helper()
	// Every rejected event gets its own ID (a real ID is a content hash), and
	// the clock stays within the request's lifetime so that a late created_at
	// exercises the sender-timestamp rules rather than clock expiry.
	badID++
	e.ID = hx(badID)
	if err := f.Apply(e, to, min(e.CreatedAt, expires)); err == nil {
		t.Fatalf("expected rejection: %s", why)
	}
}

// forwarded: f1 -> f2 (hops 1) -> tgt (hops 0); last forwarder f2, forward id 21.
func forwarded(t *testing.T) *Flow {
	f := newFlow(t)
	mustApply(t, f, ev(20, f1, 110, fwdBody(1)), f2)
	mustApply(t, f, ev(21, f2, 120, fwdBody(0)), tgt)
	return f
}

// accepted: forwarded + tgt accepts with event 30 at t=130.
func accepted(t *testing.T) *Flow {
	f := forwarded(t)
	mustApply(t, f, ev(30, tgt, 130, replyBody("referral_accept", hx(21))), "")
	return f
}

func TestParseRequest(t *testing.T) {
	cases := []struct {
		name, offer, hops string
		exp               int64
		ok                bool
	}{
		{"valid", "10", "3", expires, true},
		{"hops zero", "10", "0", expires, true},
		{"max offer", fmt.Sprint(MaxPoints), "1", expires, true},
		{"zero offer", "0", "1", expires, false},
		{"negative offer", "-5", "1", expires, false},
		{"offer over bound", fmt.Sprint(MaxPoints + 1), "1", expires, false},
		{"int64 overflow offer", "9223372036854775808", "1", expires, false},
		{"huge float offer", "1e30", "1", expires, false},
		{"fractional offer", "1.5", "1", expires, false},
		{"hops too many", "10", "4", expires, false},
		{"hops negative", "10", "-1", expires, false},
		{"hops missing", "10", "null", expires, false},
		{"expires == created", "10", "1", t0, false},
		{"expires in past", "10", "1", t0 - 1, false},
		{"expires beyond horizon", "10", "1", t0 + MaxHorizon + 1, false},
		{"expires at horizon", "10", "1", t0 + MaxHorizon, true},
	}
	for _, c := range cases {
		_, err := ParseRequest(ev(10, req, t0, reqBody(c.offer, c.hops, c.exp)))
		if (err == nil) != c.ok {
			t.Errorf("%s: err=%v, want ok=%v", c.name, err, c.ok)
		}
	}
}

func TestParseRejectsMalformed(t *testing.T) {
	bad := map[string]social.Envelope{
		"bad intent hex":   ev(10, req, t0, `{"v":1,"type":"referral_request","intent":"zz","offer":1,"hops_left":1,"expires_at":200}`),
		"unknown field":    ev(10, req, t0, `{"v":1,"type":"referral_request","intent":"`+intent+`","offer":1,"hops_left":1,"expires_at":200,"x":1}`),
		"duplicate key":    ev(10, req, t0, `{"v":1,"type":"referral_request","intent":"`+intent+`","offer":1,"offer":2,"hops_left":1,"expires_at":200}`),
		"wrong type":       ev(10, req, t0, fwdBody(1)),
		"wrong version":    ev(10, req, t0, `{"v":2,"type":"referral_request"}`),
		"uppercase author": {ID: hx(10), Author: "ABC", CreatedAt: t0, Content: json.RawMessage(reqBody("1", "1", 200))},
	}
	for name, e := range bad {
		if _, err := ParseRequest(e); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := ParseForward(ev(20, f1, 110, fwdBody(1))); err != nil {
		t.Errorf("valid forward: %v", err)
	}
	if _, err := ParseForward(ev(20, f1, 110, `{"v":1,"type":"referral_forward","request":"`+reqID+`","intent":"`+intent+`"}`)); err == nil {
		t.Error("forward without hops_left accepted")
	}
	if _, err := ParseForward(ev(20, f1, 110, fwdBody(4))); err == nil {
		t.Error("forward with hops_left 4 accepted")
	}
	if _, err := ParseReply(ev(30, tgt, 130, replyBody("referral_accept", hx(21))), "referral_accept"); err != nil {
		t.Errorf("valid accept: %v", err)
	}
	if _, err := ParseReply(ev(30, tgt, 130, replyBody("referral_accept", "nothex")), "referral_accept"); err == nil {
		t.Error("accept with bad forward ref accepted")
	}
	if _, err := ParseReply(ev(30, tgt, 130, replyBody("referral_decline", hx(21))), "referral_accept"); err == nil {
		t.Error("decline parsed as accept")
	}
	if _, err := ParseReceipt(ev(40, req, 140, rcptBody(f2, "10", hx(30)))); err != nil {
		t.Errorf("valid receipt: %v", err)
	}
	for _, amt := range []string{"0", "-1", "1000001", "9223372036854775808"} {
		if _, err := ParseReceipt(ev(40, req, 140, rcptBody(f2, amt, hx(30)))); err == nil {
			t.Errorf("receipt amount %s accepted", amt)
		}
	}
}

func TestNewFlowFirstHolder(t *testing.T) {
	e := ev(10, req, t0, reqBody("10", "2", expires))
	if _, err := NewFlow(e, req, t0); err == nil {
		t.Error("request routed to the requester accepted")
	}
	if _, err := NewFlow(e, "bad", t0); err == nil {
		t.Error("invalid first holder accepted")
	}
}

func TestHappyPath(t *testing.T) {
	f := newFlow(t)
	if f.State(t0) != Requested {
		t.Fatal("want Requested")
	}
	mustApply(t, f, ev(20, f1, 110, fwdBody(1)), f2)
	mustApply(t, f, ev(21, f2, 120, fwdBody(0)), tgt)
	if f.State(120) != Forwarded {
		t.Fatal("want Forwarded")
	}
	mustApply(t, f, ev(30, tgt, 130, replyBody("referral_accept", hx(21))), "")
	if f.State(130) != Accepted {
		t.Fatal("want Accepted")
	}
	mustApply(t, f, ev(40, req, 140, rcptBody(f2, "10", hx(30))), "")
	if f.State(140) != Settled {
		t.Fatal("want Settled")
	}
}

func TestForwardRules(t *testing.T) {
	f := newFlow(t)
	mustFail(t, f, ev(20, f2, 110, fwdBody(1)), tgt, "non-holder forward")
	mustFail(t, f, ev(20, req, 110, fwdBody(1)), tgt, "requester forwarding own request")
	mustFail(t, f, ev(20, f1, 110, fwdBody(2)), f2, "hops_left not decremented")
	mustFail(t, f, ev(20, f1, 110, fwdBody(0)), f2, "hops_left decremented by 2")
	mustFail(t, f, ev(20, f1, 110, fwdBody(1)), req, "forward back to the requester")
	mustFail(t, f, ev(20, f1, 110, fwdBody(1)), f1, "forward to self")
	mustFail(t, f, ev(20, f1, 110, fwdBody(1)), "bad", "invalid recipient")
	mustFail(t, f, ev(20, f1, 110, fwdBody(1)), "", "missing recipient")
	other := `{"v":1,"type":"referral_forward","request":"` + hx(77) + `","intent":"` + intent + `","hops_left":1}`
	mustFail(t, f, ev(20, f1, 110, other), f2, "forward of a different request")
	mustApply(t, f, ev(20, f1, 110, fwdBody(1)), f2)
	mustFail(t, f, ev(22, f1, 115, fwdBody(0)), tgt, "previous holder forwarding again")
	mustFail(t, f, ev(22, f2, 115, fwdBody(0)), f1, "forward back into the chain (loop)")
	mustFail(t, f, ev(22, f2, 115, fwdBody(0)), req, "forward to the requester later in the chain")
	mustApply(t, f, ev(21, f2, 120, fwdBody(0)), tgt)
	mustFail(t, f, ev(23, tgt, 125, fwdBody(0)), hx(5), "forward at hops 0")
}

func TestZeroHopsRequestCannotBeForwarded(t *testing.T) {
	f, err := NewFlow(ev(10, req, t0, reqBody("10", "0", expires)), f1, t0)
	if err != nil {
		t.Fatal(err)
	}
	mustFail(t, f, ev(20, f1, 110, fwdBody(0)), f2, "forward at hops_left 0")
	mustFail(t, f, ev(20, f1, 110, fwdBody(-1)), f2, "negative hops_left")
}

func TestAcceptDeclineRules(t *testing.T) {
	f := newFlow(t)
	mustFail(t, f, ev(30, f1, 110, replyBody("referral_accept", hx(21))), "", "accept before any forward")
	mustApply(t, f, ev(20, f1, 110, fwdBody(1)), f2)
	mustFail(t, f, ev(30, f1, 115, replyBody("referral_accept", hx(20))), "", "forwarder accepting own forward")
	mustFail(t, f, ev(30, tgt, 115, replyBody("referral_accept", hx(20))), "", "stranger accepting")
	mustFail(t, f, ev(30, req, 115, replyBody("referral_accept", hx(20))), "", "requester accepting")
	mustFail(t, f, ev(30, f2, 115, replyBody("referral_accept", hx(55))), "", "accept citing an unknown forward")
	mustApply(t, f, ev(21, f2, 120, fwdBody(0)), tgt)
	mustFail(t, f, ev(30, f2, 125, replyBody("referral_accept", hx(20))), "", "older holder answering stale forward")
	mustFail(t, f, ev(30, tgt, 125, replyBody("referral_accept", hx(20))), "", "accept citing an older forward")
	mustFail(t, f, ev(30, f2, 125, replyBody("referral_decline", hx(21))), "", "decline by non-recipient")
	mustApply(t, f, ev(31, tgt, 126, replyBody("referral_decline", hx(21))), "")
	if f.State(126) != Declined {
		t.Fatal("want Declined")
	}
	mustFail(t, f, ev(32, tgt, 127, replyBody("referral_accept", hx(21))), "", "accept after decline")
	mustFail(t, f, ev(33, f2, 127, fwdBody(0)), hx(5), "forward after decline")
	mustFail(t, f, ev(34, req, 128, rcptBody(f2, "10", hx(31))), "", "receipt after decline")
}

func TestForwarderCannotCollectWithoutAccept(t *testing.T) {
	f := newFlow(t)
	mustApply(t, f, ev(20, f1, 110, fwdBody(1)), f2)
	// Forwarder self-authored receipt, and a requester receipt citing the forward.
	mustFail(t, f, ev(40, f1, 115, rcptBody(f1, "10", hx(20))), "", "forwarder-authored receipt")
	mustFail(t, f, ev(41, req, 115, rcptBody(f1, "10", hx(20))), "", "receipt citing a forward before accept")
	mustFail(t, f, ev(42, req, 115, rcptBody(f1, "10", hx(30))), "", "receipt before accept")
	// Even once accepted, only the requester's receipt settles it.
	mustApply(t, f, ev(21, f2, 120, fwdBody(0)), tgt)
	mustApply(t, f, ev(30, tgt, 130, replyBody("referral_accept", hx(21))), "")
	mustFail(t, f, ev(43, f2, 135, rcptBody(f2, "10", hx(30))), "", "payee-authored receipt")
	mustFail(t, f, ev(44, tgt, 135, rcptBody(f2, "10", hx(30))), "", "target-authored receipt")
	if f.State(135) != Accepted {
		t.Fatal("forged receipts must leave the flow Accepted")
	}
}

func TestReceiptRules(t *testing.T) {
	f := accepted(t)
	mustFail(t, f, ev(40, req, 140, rcptBody(f2, "9", hx(30))), "", "amount below offer")
	mustFail(t, f, ev(40, req, 140, rcptBody(f2, "11", hx(30))), "", "amount above offer")
	mustFail(t, f, ev(40, req, 140, rcptBody(f2, "9223372036854775807", hx(30))), "", "huge amount")
	mustFail(t, f, ev(40, req, 140, rcptBody(f2, "9223372036854775808", hx(30))), "", "overflowing amount")
	mustFail(t, f, ev(40, req, 140, rcptBody(f1, "10", hx(30))), "", "paying an earlier forwarder")
	mustFail(t, f, ev(40, req, 140, rcptBody(tgt, "10", hx(30))), "", "paying the target")
	mustFail(t, f, ev(40, req, 140, rcptBody(f2, "10", hx(21))), "", "ref is the forward, not the accept")
	mustFail(t, f, ev(40, req, 140, rcptBody(f2, "10", hx(31))), "", "ref to unknown event")
	mustFail(t, f, ev(40, f1, 140, rcptBody(f2, "10", hx(30))), "", "receipt by a non-requester")
	if f.State(140) != Accepted {
		t.Fatal("want Accepted after rejected receipts")
	}
	good := ev(41, req, 140, rcptBody(f2, "10", hx(30)))
	mustApply(t, f, good, "")
	if f.State(140) != Settled {
		t.Fatal("want Settled")
	}
	// Redelivery is a no-op; a second distinct receipt for the same ref is not counted.
	mustApply(t, f, good, "")
	mustFail(t, f, ev(42, req, 150, rcptBody(f2, "10", hx(30))), "", "duplicate receipt for one ref")
}

func TestIdempotentRedelivery(t *testing.T) {
	f := newFlow(t)
	fw := ev(20, f1, 110, fwdBody(1))
	mustApply(t, f, fw, f2)
	mustApply(t, f, fw, f2)  // same ID again
	mustApply(t, f, fw, tgt) // same ID, even with a different recipient: no-op
	mustApply(t, f, ev(10, req, t0, reqBody("10", "2", expires)), "")
	mustApply(t, f, ev(21, f2, 120, fwdBody(0)), tgt) // chain is still f1 -> f2
}

func TestTerminalStatesRejectEverything(t *testing.T) {
	f := accepted(t)
	mustApply(t, f, ev(40, req, 140, rcptBody(f2, "10", hx(30))), "")
	mustFail(t, f, ev(50, tgt, 150, replyBody("referral_decline", hx(21))), "", "decline after settle")
	mustFail(t, f, ev(51, f2, 150, fwdBody(0)), hx(5), "forward after settle")
	mustFail(t, f, ev(52, tgt, 150, replyBody("referral_accept", hx(21))), "", "second accept after settle")
}

func TestExpiry(t *testing.T) {
	f := newFlow(t)
	if f.State(expires) != Requested || f.State(expires+1) != Expired {
		t.Fatal("expiry must be strictly after expires_at")
	}
	mustFail(t, f, ev(20, f1, expires+1, fwdBody(1)), f2, "forward after expires_at")
	mustApply(t, f, ev(20, f1, expires, fwdBody(1)), f2) // at the boundary is fine
	mustFail(t, f, ev(21, f2, expires+1, fwdBody(0)), tgt, "second forward after expires_at")
	f2w := newFlow(t)
	mustApply(t, f2w, ev(20, f1, 110, fwdBody(1)), f2)
	mustFail(t, f2w, ev(30, f2, expires+5, replyBody("referral_accept", hx(20))), "", "accept after expires_at")
	if f2w.State(expires+5) != Expired {
		t.Fatal("an unanswered forwarded flow must report Expired")
	}
	// A far-future event must not poison the flow's state.
	mustFail(t, f2w, ev(31, hx(8), 1<<40, fwdBody(0)), tgt, "far-future event")
	if f2w.State(500) != Forwarded {
		t.Fatal("rejected event changed state")
	}
	// Accepted flows do not expire: the debt stands and a late receipt settles it.
	g := accepted(t)
	if g.State(expires+500) != Accepted {
		t.Fatal("accepted flow must not expire")
	}
	mustApply(t, g, ev(40, req, expires+500, rcptBody(f2, "10", hx(30))), "")
	if g.State(expires+500) != Settled {
		t.Fatal("late receipt must settle")
	}
}

func TestOrdering(t *testing.T) {
	f := newFlow(t)
	mustFail(t, f, ev(20, f1, t0-1, fwdBody(1)), f2, "event predating the request")
	mustApply(t, f, ev(20, f1, t0, fwdBody(1)), f2) // same second is fine
}

// flowN builds an independent flow (distinct request ID) from req via f1 to
// tgt, offering 10, and advances it to the wanted state.
func flowN(t *testing.T, n int, upTo State) *Flow {
	t.Helper()
	b := n * 100
	f, err := NewFlow(ev(b, req, t0, reqBody("10", "1", expires)), f1, t0)
	if err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"v":1,"type":"referral_forward","request":%q,"intent":%q,"hops_left":0}`, hx(b), intent)
	if upTo >= Forwarded {
		mustApply(t, f, ev(b+1, f1, 110, body), tgt)
	}
	if upTo >= Accepted {
		mustApply(t, f, ev(b+2, tgt, 130, replyBody("referral_accept", hx(b+1))), "")
	}
	if upTo >= Settled {
		mustApply(t, f, ev(b+3, req, 140, rcptBody(f1, "10", hx(b+2))), "")
	}
	return f
}

func TestClockExpiryCannotBeUndoneByBackdating(t *testing.T) {
	f := flowN(t, 1, Forwarded)
	late := ev(190, tgt, 120, replyBody("referral_accept", hx(101))) // backdated accept
	if err := f.Apply(late, "", expires+1); err == nil || f.State(expires+1) != Expired {
		t.Fatalf("backdated accept after expiry: %v, state %d", err, f.State(expires+1))
	}
	if f.State(200) != Expired {
		t.Error("expiry was not persisted")
	}
	// An accepted flow is exempt: the receipt may land after expires_at.
	g := flowN(t, 2, Accepted)
	if err := g.Apply(ev(290, req, expires+5, rcptBody(f1, "10", hx(202))), "", expires+5); err != nil || g.State(expires+5) != Settled {
		t.Fatalf("receipt after expiry on an accepted flow: %v", err)
	}
}

func TestFutureDatedAcceptIsDeferred(t *testing.T) {
	f := flowN(t, 1, Forwarded)
	a := ev(190, tgt, 130+social.MaxClockSkew+1, replyBody("referral_accept", hx(101)))
	if err := f.Apply(a, "", 130); !errors.Is(err, ErrTooEarly) || f.State(130) != Forwarded {
		t.Fatalf("future-dated accept: %v, state %d", err, f.State(130))
	}
	if err := f.Apply(a, "", 131); err != nil || f.State(131) != Accepted {
		t.Fatalf("deferred accept must apply once the clock catches up: %v", err)
	}
}

func TestOutOfOrderReceiptCanBeRetried(t *testing.T) {
	// The receipt reaches the observer before the accept it cites.
	f := flowN(t, 1, Forwarded)
	rcpt := ev(190, req, 135, rcptBody(f1, "10", hx(102)))
	if err := f.Apply(rcpt, "", 140); !errors.Is(err, ErrNotNow) || f.State(140) != Forwarded {
		t.Fatalf("receipt before accept: %v, state %d", err, f.State(140))
	}
	mustApply(t, f, ev(102, tgt, 130, replyBody("referral_accept", hx(101))), "")
	if err := f.Apply(rcpt, "", 141); err != nil || f.State(141) != Settled {
		t.Fatalf("retry after the accept arrived: %v, state %d", err, f.State(141))
	}
}

func TestSenderTimestampsDoNotOrderAuthors(t *testing.T) {
	// The target's clock runs ahead (within skew); the requester's receipt is
	// dated earlier than the accept and must still settle.
	f := flowN(t, 1, Forwarded)
	if err := f.Apply(ev(102, tgt, 500+social.MaxClockSkew, replyBody("referral_accept", hx(101))), "", 500); err != nil {
		t.Fatal(err)
	}
	if err := f.Apply(ev(103, req, 501, rcptBody(f1, "10", hx(102))), "", 501); err != nil || f.State(501) != Settled {
		t.Fatalf("receipt dated before the accept: %v", err)
	}
	g := flowN(t, 2, Forwarded)
	if err := g.Apply(ev(290, tgt, t0-1, replyBody("referral_accept", hx(201))), "", 500); err == nil {
		t.Fatal("an event dated before the request itself must be rejected")
	}
}

func TestNewFlowDefersFutureRequest(t *testing.T) {
	e := ev(10, req, t0, reqBody("10", "2", expires))
	if _, err := NewFlow(e, f1, t0-social.MaxClockSkew-1); !errors.Is(err, ErrTooEarly) {
		t.Fatalf("future-dated request: %v", err)
	}
}

package referral

import "testing"

func TestLedgerDebt(t *testing.T) {
	acc := flowN(t, 1, Accepted) // accepted at 130
	flows := []*Flow{acc}
	for now, want := range map[int64]int64{140: 0, 190: 0, 191: 10} {
		if d := Debt(flows, req, now, 60); d != want {
			t.Errorf("debt at %d = %d, want %d", now, d, want)
		}
	}
	if d := Debt(flows, f1, 1000, 60); d != 0 {
		t.Errorf("debt charged to a non-requester: %d", d)
	}
	if d := Debt([]*Flow{flowN(t, 2, Forwarded), flowN(t, 3, Requested)}, req, 1000, 0); d != 0 {
		t.Errorf("unaccepted flows are not debt: %d", d)
	}
	if d := Debt([]*Flow{flowN(t, 4, Settled)}, req, 1000, 0); d != 0 {
		t.Errorf("settled flow still counts as debt: %d", d)
	}
}

func TestLedgerCountsEachRequestOnce(t *testing.T) {
	s, a := flowN(t, 1, Settled), flowN(t, 2, Accepted)
	twin := flowN(t, 1, Settled) // same request rebuilt as a second object
	flows := []*Flow{s, s, twin, nil, a, a}
	if b := Balances(flows); b[req] != -10 || b[f1] != 10 {
		t.Errorf("balances %v, want req -10 and f1 +10", b)
	}
	if d := Debt(flows, req, 1000, 0); d != 10 {
		t.Errorf("debt %d, want 10", d)
	}
	if e := Exposure(flows, req, 500); e != 20 {
		t.Errorf("exposure %d, want 20", e)
	}
}

func TestExposure(t *testing.T) {
	flows := []*Flow{flowN(t, 1, Requested), flowN(t, 2, Forwarded), flowN(t, 3, Accepted), flowN(t, 4, Settled)}
	if e := Exposure(flows, req, 500); e != 40 {
		t.Errorf("open + forwarded + accepted + settled = %d, want 40", e)
	}
	// Past expiry the two open requests lapse; the accepted and paid ones stay.
	if e := Exposure(flows, req, expires+1); e != 20 {
		t.Errorf("after expiry = %d, want 20", e)
	}
	if e := Exposure(flows, f1, 500); e != 0 {
		t.Errorf("a net earner has exposure %d, want 0", e)
	}
	dec := flowN(t, 5, Forwarded)
	mustApply(t, dec, ev(590, tgt, 130, replyBody("referral_decline", hx(501))), "")
	if e := Exposure([]*Flow{dec}, req, 500); e != 0 {
		t.Errorf("declined request counts %d, want 0", e)
	}
}

func TestCanRequest(t *testing.T) {
	valid := func(k string) bool { return k == req }
	// Paying receipts must not reset the limit: ten settled flows of 10 use it up.
	var paid []*Flow
	for i := 1; i <= 9; i++ {
		paid = append(paid, flowN(t, i, Settled))
	}
	if !CanRequest(paid, req, 10, 2000, 0, valid) {
		t.Error("90 paid + 10 offer = 100 must fit the default limit")
	}
	if CanRequest(paid, req, 11, 2000, 0, valid) {
		t.Error("90 paid + 11 offer exceeds the default limit")
	}
	if CanRequest(paid, req, 5, 2000, 50, valid) {
		t.Error("custom limit 50 must block 90 exposure")
	}
	// Open requests count before anyone accepts, so they cannot be stacked.
	var open []*Flow
	for i := 1; i <= 10; i++ {
		open = append(open, flowN(t, i, Requested))
	}
	if CanRequest(open, req, 1, 500, 0, valid) {
		t.Error("100 in open requests leaves no room")
	}
	if !CanRequest(open, req, 1, expires+1, 0, valid) {
		t.Error("lapsed open requests must free the limit")
	}
	if !CanRequest(nil, req, 5, 1000, 0, valid) {
		t.Error("clean identity must be allowed")
	}
	if CanRequest(nil, f1, 5, 1000, 0, valid) || CanRequest(nil, req, 5, 1000, 0, nil) {
		t.Error("invalid identity or nil predicate must be denied")
	}
	for _, off := range []int64{0, -1, MaxPoints + 1, 1<<63 - 1} {
		if CanRequest(nil, req, off, 1000, 1<<62, valid) {
			t.Errorf("offer %d must be denied", off)
		}
	}
}

func TestAddSat(t *testing.T) {
	const max, min = int64(1<<63 - 1), int64(-1 << 63)
	for _, c := range [][3]int64{{1, 2, 3}, {max, 1, max}, {max - 1, 1, max}, {min, -1, min}, {min + 1, -1, min}, {max, min, -1}} {
		if got := addSat(c[0], c[1]); got != c[2] {
			t.Errorf("addSat(%d, %d) = %d, want %d", c[0], c[1], got, c[2])
		}
	}
}

func TestStaleCopyCannotHideObligation(t *testing.T) {
	stale, current := flowN(t, 1, Requested), flowN(t, 1, Accepted)
	for _, flows := range [][]*Flow{{stale, current}, {current, stale}} {
		// Past expires_at the stale copy alone would look Expired.
		if e := Exposure(flows, req, expires+1); e != 10 {
			t.Errorf("exposure %d, want 10", e)
		}
		if d := Debt(flows, req, expires+1, 0); d != 10 {
			t.Errorf("debt %d, want 10", d)
		}
	}
	paid := flowN(t, 2, Settled)
	if b := Balances([]*Flow{flowN(t, 2, Forwarded), paid}); b[req] != -10 {
		t.Errorf("balances %v, want req -10", b)
	}
}

func TestGraceRunsFromObservedAccept(t *testing.T) {
	// Request, forward and accept are dated long ago but only observed now:
	// the debt is not instantly overdue.
	f := flowN(t, 1, Forwarded)
	if err := f.Apply(ev(102, tgt, 120, replyBody("referral_accept", hx(101))), "", 900); err != nil {
		t.Fatal(err)
	}
	if d := Debt([]*Flow{f}, req, 950, 60); d != 0 {
		t.Errorf("inside grace of the observation time: debt %d", d)
	}
	if d := Debt([]*Flow{f}, req, 961, 60); d != 10 {
		t.Errorf("past grace: debt %d, want 10", d)
	}
}

func TestBalancesFollowTheFlow(t *testing.T) {
	// Two forwarders: only the last one is paid, and only once it is settled.
	f := forwarded(t) // req -> f1 -> f2 -> tgt, offer 10
	if got := Balances([]*Flow{f}); len(got) != 0 {
		t.Fatalf("balances before accept %v, want none", got)
	}
	mustApply(t, f, ev(30, tgt, 130, replyBody("referral_accept", hx(21))), "")
	if got := Balances([]*Flow{f}); len(got) != 0 {
		t.Fatalf("balances before the receipt %v, want none", got)
	}
	good := ev(40, req, 140, rcptBody(f2, "10", hx(30)))
	mustApply(t, f, good, "")
	mustApply(t, f, good, "") // redelivery
	mustFail(t, f, ev(42, req, 150, rcptBody(f2, "10", hx(30))), "", "duplicate receipt for one ref")
	b := Balances([]*Flow{f})
	if b[f2] != 10 || b[req] != -10 || b[f1] != 0 || b[tgt] != 0 {
		t.Fatalf("balances %v: only the last forwarder is paid, once", b)
	}
}

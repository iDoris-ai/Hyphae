package contact

import (
	"errors"
	"testing"
)

const now = int64(1_700_000_000)

func TestCheckSendStranger(t *testing.T) {
	full := make([]int64, DailyLimit)
	for i := range full {
		full[i] = now - int64(i)*60
	}
	cases := []struct {
		name     string
		r        Relation
		inits    []int64
		wantErr  error
		wantInit bool
	}{
		{"first message is an initiation", Relation{}, nil, nil, true},
		{"second message is not", Relation{Sent: 1}, nil, nil, false},
		{"third message refused", Relation{Sent: 2}, nil, ErrStrangerMessageLimit, false},
		{"reply to a stranger is not an initiation", Relation{Received: 1}, full, nil, false},
		{"reply limit still applies", Relation{Sent: 2, Received: 2}, nil, ErrStrangerMessageLimit, false},
		{"eleventh new stranger refused", Relation{}, full, &DailyLimitError{}, false},
		{"second message to an old stranger is fine at the limit", Relation{Sent: 1}, full, nil, false},
		{"friend is unlimited", Relation{State: Friend, Sent: 9}, full, nil, false},
		{"blocked is refused", Relation{State: Blocked}, nil, ErrPeerBlocked, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d, err := CheckSend(c.r, c.inits, now)
			var dl *DailyLimitError
			switch {
			case c.wantErr == nil && err != nil:
				t.Fatalf("err = %v", err)
			case errors.As(c.wantErr, &dl):
				if !errors.As(err, &dl) {
					t.Fatalf("err = %v, want DailyLimitError", err)
				}
			case c.wantErr != nil && !errors.Is(err, c.wantErr):
				t.Fatalf("err = %v, want %v", err, c.wantErr)
			}
			if err == nil && d.Initiation != c.wantInit {
				t.Fatalf("Initiation = %v, want %v", d.Initiation, c.wantInit)
			}
		})
	}
}

func TestDailyWindowRolls(t *testing.T) {
	inits := make([]int64, DailyLimit)
	for i := range inits {
		inits[i] = now - Window + 100 + int64(i) // oldest leaves at now+100
	}
	_, err := CheckSend(Relation{}, inits, now)
	var dl *DailyLimitError
	if !errors.As(err, &dl) || dl.Used != DailyLimit || dl.ResetsAt != now+100 {
		t.Fatalf("err = %v (%+v), want limit resetting at now+100", err, dl)
	}
	if _, err := CheckSend(Relation{}, inits, now+99); err == nil {
		t.Fatal("one second before reset must still refuse")
	}
	d, err := CheckSend(Relation{}, inits, now+100)
	if err != nil || !d.Initiation {
		t.Fatalf("at reset: d = %+v, err = %v", d, err)
	}
}

func TestQuota(t *testing.T) {
	q := QuotaAt([]int64{now - Window, now - Window + 1, now + 500}, now)
	// now-Window has left; the future one (clock went back) still counts.
	if q.Used != 2 || q.Remaining != DailyLimit-2 || q.ResetsAt != now+1 {
		t.Fatalf("quota = %+v", q)
	}
	if q := QuotaAt(nil, now); q.Used != 0 || q.Remaining != DailyLimit || q.ResetsAt != 0 {
		t.Fatalf("empty quota = %+v", q)
	}
	many := make([]int64, DailyLimit+3)
	for i := range many {
		many[i] = now
	}
	if q := QuotaAt(many, now); q.Remaining != 0 || q.Used != DailyLimit+3 {
		t.Fatalf("over-full quota = %+v", q)
	}
}

func TestSendSequence(t *testing.T) {
	var r Relation
	var inits []int64
	for i := 0; i < PerStrangerLimit; i++ {
		d, err := CheckSend(r, inits, now)
		if err != nil {
			t.Fatalf("message %d: %v", i+1, err)
		}
		if d.Initiation {
			inits = append(inits, now)
		}
		r = ApplySend(r, d)
	}
	if len(inits) != 1 || r.Sent != PerStrangerLimit {
		t.Fatalf("inits = %v, r = %+v", inits, r)
	}
	if _, err := CheckSend(r, inits, now); !errors.Is(err, ErrStrangerMessageLimit) {
		t.Fatalf("third: %v", err)
	}
	f := ApplySend(Relation{State: Friend}, SendDecision{})
	if f.Sent != 0 {
		t.Fatalf("friend send counted: %+v", f)
	}
}

func TestCheckReceive(t *testing.T) {
	cases := []struct {
		r    Relation
		want Disposition
	}{
		{Relation{}, Request},
		{Relation{Received: 1}, Show},
		{Relation{Received: 2}, Suppress},
		{Relation{Received: 7}, Suppress},
		{Relation{Sent: 1}, Show}, // reply to my first message is not a request
		{Relation{Sent: 1, Received: 2}, Suppress},
		{Relation{State: Friend, Received: 50}, Show},
		{Relation{State: Blocked}, Drop},
	}
	for _, c := range cases {
		if got := CheckReceive(c.r); got != c.want {
			t.Errorf("CheckReceive(%+v) = %v, want %v", c.r, got, c.want)
		}
	}
	if r := ApplyReceive(Relation{Received: 2}); r.Received != 3 {
		t.Errorf("suppressed message not counted: %+v", r)
	}
	if r := ApplyReceive(Relation{State: Blocked}); r.Received != 0 {
		t.Errorf("blocked message counted: %+v", r)
	}
	if r := ApplyReceive(Relation{State: Friend}); r.Received != 0 {
		t.Errorf("friend message counted: %+v", r)
	}
}

func TestAccept(t *testing.T) {
	if r, err := Accept(Relation{Received: 1}); err != nil || r != (Relation{State: Friend}) {
		t.Fatalf("accept = %+v, %v", r, err)
	}
	if _, err := Accept(Relation{Sent: 1}); !errors.Is(err, ErrNothingToAccept) {
		t.Fatalf("accept without incoming: %v", err)
	}
	if _, err := Accept(Relation{State: Blocked, Received: 1}); !errors.Is(err, ErrNotStranger) {
		t.Fatalf("accept blocked: %v", err)
	}
	if _, err := Accept(Relation{State: Friend}); !errors.Is(err, ErrNotStranger) {
		t.Fatalf("accept friend: %v", err)
	}
}

func TestAcceptedByPeer(t *testing.T) {
	sent := Relation{Sent: 1}
	if r, err := AcceptedByPeer(sent, true, now); err != nil || r.State != Friend {
		t.Fatalf("valid accept = %+v, %v", r, err)
	}
	if _, err := AcceptedByPeer(sent, false, 0); !errors.Is(err, ErrNothingToAccept) {
		t.Fatalf("accept not answering mine: %v", err)
	}
	if _, err := AcceptedByPeer(Relation{}, true, now); !errors.Is(err, ErrNothingToAccept) {
		t.Fatalf("accept with nothing sent: %v", err)
	}
	if _, err := AcceptedByPeer(Relation{State: Blocked, Sent: 1}, true, now); !errors.Is(err, ErrNotStranger) {
		t.Fatalf("accept while blocked: %v", err)
	}
	if r, err := AcceptedByPeer(Relation{State: Friend}, false, 0); err != nil || r.State != Friend {
		t.Fatalf("duplicate accept on friend = %+v, %v", r, err)
	}
}

func TestBlockUnblockKeepsCountsAndStopsReplay(t *testing.T) {
	r := Relation{Sent: 2, Received: 2}
	b := Block(r)
	if b.State != Blocked || b.Sent != 2 || b.Received != 2 {
		t.Fatalf("block = %+v", b)
	}
	if _, err := CheckSend(b, nil, now); !errors.Is(err, ErrPeerBlocked) {
		t.Fatalf("send while blocked: %v", err)
	}
	u, err := Unblock(b, now)
	if err != nil || u.State != Stranger || u.Sent != 2 || u.UnblockedAt != now {
		t.Fatalf("unblock = %+v, %v", u, err)
	}
	if _, err := CheckSend(u, nil, now); !errors.Is(err, ErrStrangerMessageLimit) {
		t.Fatalf("unblock must not refill quota: %v", err)
	}
	// An accept answering a message sent before the unblock is a replay.
	if _, err := AcceptedByPeer(u, true, now-1); !errors.Is(err, ErrNothingToAccept) {
		t.Fatalf("replayed accept: %v", err)
	}
	if _, err := AcceptedByPeer(u, true, now); !errors.Is(err, ErrNothingToAccept) {
		t.Fatalf("accept in the unblock second: %v", err)
	}
	if r, err := AcceptedByPeer(u, true, now+1); err != nil || r.State != Friend {
		t.Fatalf("fresh accept = %+v, %v", r, err)
	}
	if _, err := Unblock(u, now); !errors.Is(err, ErrNotBlocked) {
		t.Fatalf("unblock stranger: %v", err)
	}
}

func TestStrings(t *testing.T) {
	if Stranger.String() != "stranger" || Friend.String() != "friend" || Blocked.String() != "blocked" || State(9).String() != "State(9)" {
		t.Fatal("State.String")
	}
	if Show.String() != "show" || Drop.String() != "drop" {
		t.Fatal("Disposition.String")
	}
	if (&DailyLimitError{Used: 10, ResetsAt: 5}).Error() == "" {
		t.Fatal("DailyLimitError.Error")
	}
	if _, err := CheckSend(Relation{State: State(9)}, nil, now); err == nil {
		t.Fatal("unknown state must error")
	}
}

package trust

import (
	"encoding/json"
	"fmt"
	"maps"
	"math/rand"
	"reflect"
	"strings"
	"testing"

	"github.com/iDoris-ai/hyphae/internal/social"
)

// pk returns a deterministic public key for identity n.
// testNow is a trusted clock later than every test event.
const testNow = int64(1) << 40

func pk(n int) string { return fmt.Sprintf("%064x", n) }

var evSeq = 1 << 40

// id returns a fresh event ID; later calls sort after earlier ones.
func id() string { evSeq++; return fmt.Sprintf("%064x", evSeq) }

func envOf(eid, author string, at int64, content string) social.Envelope {
	return social.Envelope{ID: eid, Author: author, CreatedAt: at, Content: json.RawMessage(content)}
}

func inv(from, to int, at int64) social.Envelope {
	return invID(id(), from, to, at)
}

func invID(eid string, from, to int, at int64) social.Envelope {
	return envOf(eid, pk(from), at, fmt.Sprintf(`{"v":1,"type":"invite","invitee":%q}`, pk(to)))
}

// acc is identity who accepting the invite event invite.
func acc(who int, invite social.Envelope, at int64) social.Envelope {
	return accID(id(), who, invite.ID, at)
}

func accID(eid string, who int, inviteID string, at int64) social.Envelope {
	return envOf(eid, pk(who), at, fmt.Sprintf(`{"v":1,"type":"invite_accept","invite":%q}`, inviteID))
}

// link is from inviting to, and to accepting.
func link(from, to int, at int64) []social.Envelope {
	i := inv(from, to, at)
	return []social.Envelope{i, acc(to, i, at)}
}

func resolve(t *testing.T, cfg Config, envs ...social.Envelope) *Chain {
	t.Helper()
	c, err := ResolveInvites(cfg, envs, testNow)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func roots(ns ...int) Config {
	var c Config
	for _, n := range ns {
		c.Roots = append(c.Roots, pk(n))
	}
	return c
}

func cat(groups ...[]social.Envelope) []social.Envelope {
	var out []social.Envelope
	for _, g := range groups {
		out = append(out, g...)
	}
	return out
}

func wantMember(t *testing.T, c *Chain, n, inviter, depth int) {
	t.Helper()
	m, ok := c.Member(pk(n))
	if !ok || m.Inviter != pk(inviter) || m.Depth != depth {
		t.Errorf("identity %d: %+v %v, want inviter %d depth %d", n, m, ok, inviter, depth)
	}
}

func TestParseInvite(t *testing.T) {
	got, err := ParseInvite(inv(1, 2, 10))
	if err != nil || got.Inviter != pk(1) || got.Invitee != pk(2) || got.CreatedAt != 10 {
		t.Fatalf("got %+v, %v", got, err)
	}
	bad := map[string]string{
		"wrong type":    `{"v":1,"type":"rating","invitee":"` + pk(2) + `"}`,
		"version":       `{"v":2,"type":"invite","invitee":"` + pk(2) + `"}`,
		"missing":       `{"v":1,"type":"invite"}`,
		"upper hex":     `{"v":1,"type":"invite","invitee":"` + strings.ToUpper(pk(0xab)) + `"}`,
		"short":         `{"v":1,"type":"invite","invitee":"abc"}`,
		"unknown field": `{"v":1,"type":"invite","invitee":"` + pk(2) + `","x":1}`,
		"duplicate key": `{"v":1,"type":"invite","invitee":"` + pk(2) + `","invitee":"` + pk(3) + `"}`,
		"self":          `{"v":1,"type":"invite","invitee":"` + pk(1) + `"}`,
	}
	for name, body := range bad {
		if _, err := ParseInvite(envOf(id(), pk(1), 10, body)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestParseAccept(t *testing.T) {
	ref := pk(0xfeed)
	got, err := ParseAccept(accID(pk(0xa), 2, ref, 10))
	if err != nil || got.Invitee != pk(2) || got.Invite != ref || got.ID != pk(0xa) || got.CreatedAt != 10 {
		t.Fatalf("got %+v, %v", got, err)
	}
	bad := map[string]string{
		"wrong type":    `{"v":1,"type":"invite","invite":"` + ref + `"}`,
		"version":       `{"v":2,"type":"invite_accept","invite":"` + ref + `"}`,
		"missing":       `{"v":1,"type":"invite_accept"}`,
		"upper hex":     `{"v":1,"type":"invite_accept","invite":"` + strings.ToUpper(pk(0xab)) + `"}`,
		"short":         `{"v":1,"type":"invite_accept","invite":"abc"}`,
		"unknown field": `{"v":1,"type":"invite_accept","invite":"` + ref + `","invitee":"` + pk(3) + `"}`,
		"duplicate key": `{"v":1,"type":"invite_accept","invite":"` + ref + `","invite":"` + pk(3) + `"}`,
		"not object":    `["invite_accept"]`,
	}
	for name, body := range bad {
		if _, err := ParseAccept(envOf(id(), pk(2), 10, body)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestChainBasic(t *testing.T) {
	c := resolve(t, roots(1), cat(link(1, 2, 10), link(2, 3, 20))...)
	wantMember(t, c, 2, 1, 1)
	wantMember(t, c, 3, 2, 2)
	if m, ok := c.Member(pk(1)); !ok || m != (Member{}) {
		t.Errorf("root: %+v %v", m, ok)
	}
	if c.Valid(pk(4)) {
		t.Error("uninvited identity is valid")
	}
}

func TestBadRoot(t *testing.T) {
	if _, err := ResolveInvites(Config{Roots: []string{"nope"}}, nil, testNow); err == nil {
		t.Fatal("bad root accepted")
	}
}

func TestInviteWithoutAcceptGivesNoParent(t *testing.T) {
	c := resolve(t, roots(1), inv(1, 2, 10))
	if c.Valid(pk(2)) {
		t.Error("an unaccepted invite admitted its invitee")
	}
}

func TestNoTimestampCausality(t *testing.T) {
	// 2 invites 3 long before 2 itself is invited; structure alone decides.
	i23 := inv(2, 3, 5)
	c := resolve(t, roots(1), cat(link(1, 2, 100), []social.Envelope{i23, acc(3, i23, 6)})...)
	wantMember(t, c, 3, 2, 2)
}

func TestAttackerInviteWithoutAcceptChangesNothing(t *testing.T) {
	// Attacker 9 (valid) invites victim 2 earlier than 2's real inviter did,
	// but 2 never accepts it.
	base := cat(link(1, 9, 1), link(1, 2, 50), link(2, 3, 60))
	want := resolve(t, roots(1), base...)
	got := resolve(t, roots(1), append(base, inv(9, 2, 2), inv(9, 3, 2))...)
	if !reflect.DeepEqual(got.members, want.members) {
		t.Errorf("unaccepted invites changed the chain:\n got %v\nwant %v", got.members, want.members)
	}
	wantMember(t, got, 2, 1, 1)
}

func TestBackdatingAttack(t *testing.T) {
	// Honest chain 1 -> 2 -> 3 -> ... -> 9, so 9 sits exactly at MaxDepth 8.
	// Attacker 20 is valid at depth 5 (1 -> 16 -> 17 -> 18 -> 19 -> 20).
	var base []social.Envelope
	for i := 1; i <= 8; i++ {
		base = append(base, link(i, i+1, int64(100+i))...)
	}
	base = append(base, cat(link(1, 16, 1), link(16, 17, 2), link(17, 18, 3), link(18, 19, 4), link(19, 20, 5))...)
	// Identity 30 was invited by 3 "before" 3 joined; the old rules voided it.
	i330 := inv(3, 30, 1)
	base = append(base, i330, acc(30, i330, 1))
	want := resolve(t, roots(1), base...)
	wantMember(t, want, 2, 1, 1)
	wantMember(t, want, 9, 8, 8)
	wantMember(t, want, 30, 3, 3)

	// The attacker signs backdated invites to 2, 3 and 9, predating everything.
	attack := append(base, inv(20, 2, 1), inv(20, 3, 1), inv(20, 9, 1))
	got := resolve(t, roots(1), attack...)
	if !reflect.DeepEqual(got.members, want.members) {
		t.Errorf("backdated invites changed the chain:\n got %v\nwant %v", got.members, want.members)
	}
}

func TestAcceptOfForeignInviteIgnored(t *testing.T) {
	// 3 accepts the invite 1 addressed to 2.
	i12 := inv(1, 2, 10)
	c := resolve(t, roots(1), i12, acc(3, i12, 11))
	if c.Valid(pk(3)) || c.Valid(pk(2)) {
		t.Error("accept of someone else's invite counted")
	}
	// It does not shadow 3's own, later accept either.
	c = resolve(t, roots(1), cat([]social.Envelope{i12, acc(3, i12, 1)}, link(1, 3, 50))...)
	wantMember(t, c, 3, 1, 1)
}

func TestAcceptOfUnknownInviteIgnored(t *testing.T) {
	c := resolve(t, roots(1), accID(id(), 2, pk(0xdead), 1))
	if c.Valid(pk(2)) {
		t.Error("accept of unknown invite counted")
	}
	// An accept of an unknown or malformed invite does not shadow a real one.
	junk := envOf(id(), pk(1), 1, `{"v":1,"type":"invite","invitee":"bad"}`)
	c = resolve(t, roots(1), cat([]social.Envelope{accID(id(), 2, pk(0xdead), 1), junk, acc(2, junk, 1)}, link(1, 2, 50))...)
	wantMember(t, c, 2, 1, 1)
}

func TestLowestAcceptWins(t *testing.T) {
	i1, i2 := inv(1, 3, 10), inv(2, 3, 5)
	c := resolve(t, roots(1, 2), i1, i2, acc(3, i1, 20), acc(3, i2, 30))
	wantMember(t, c, 3, 1, 1)
	// Same created_at: the lower accept event ID wins, whatever the invites say.
	lo, hi := pk(0xa1), pk(0xb1)
	c = resolve(t, roots(1, 2), i1, i2, accID(hi, 3, i1.ID, 20), accID(lo, 3, i2.ID, 20))
	wantMember(t, c, 3, 2, 1)
}

func TestRootAcceptsIgnored(t *testing.T) {
	i31, i32 := inv(3, 1, 1), inv(3, 2, 1)
	c := resolve(t, roots(1, 2), cat(link(1, 3, 10), []social.Envelope{i31, acc(1, i31, 2), i32, acc(2, i32, 2)})...)
	for _, r := range []int{1, 2} {
		if m, ok := c.Member(pk(r)); !ok || m != (Member{}) {
			t.Errorf("root %d: %+v %v", r, m, ok)
		}
	}
	wantMember(t, c, 3, 1, 1)
}

func TestSelfInviteInvalid(t *testing.T) {
	self := envOf(id(), pk(2), 1, `{"v":1,"type":"invite","invitee":"`+pk(2)+`"}`)
	c := resolve(t, roots(1), self, acc(2, self, 1))
	if c.Valid(pk(2)) {
		t.Error("self-invite admitted")
	}
	// A self-invite by a root uses no quota.
	rootSelf := envOf(id(), pk(1), 1, `{"v":1,"type":"invite","invitee":"`+pk(1)+`"}`)
	c = resolve(t, Config{Roots: []string{pk(1)}, Quota: 1}, cat([]social.Envelope{rootSelf}, link(1, 2, 5))...)
	wantMember(t, c, 2, 1, 1)
}

func TestSybilRingWithoutRoot(t *testing.T) {
	envs := link(1, 2, 10)
	for i := 100; i < 110; i++ {
		envs = append(envs, link(i, i+1, 10)...)
	}
	c := resolve(t, roots(1), envs...)
	for i := 100; i <= 110; i++ {
		if c.Valid(pk(i)) {
			t.Errorf("sybil %d valid", i)
		}
	}
	wantMember(t, c, 2, 1, 1)
}

func TestCycles(t *testing.T) {
	// Two: 5 accepts 6's invite, 6 accepts 5's.
	c := resolve(t, roots(1), cat(link(5, 6, 1), link(6, 5, 1))...)
	if len(c.members) != 1 {
		t.Errorf("2-cycle admitted: %v", c.members)
	}
	// Three, with a root inviting into the cycle: its members already picked
	// their parents inside the cycle, so the root's invites change nothing.
	envs := cat(link(5, 6, 1), link(6, 7, 1), link(7, 5, 1), []social.Envelope{inv(1, 5, 1), inv(1, 6, 1)})
	c = resolve(t, roots(1), envs...)
	if len(c.members) != 1 {
		t.Errorf("3-cycle admitted: %v", c.members)
	}
	// A cycle hanging under the chain: 2 is valid, 3 accepts 2, and 2 then
	// accepts 3's invite but at a later time, so 2 keeps root 1 as parent.
	c = resolve(t, roots(1), cat(link(1, 2, 10), link(2, 3, 20), link(3, 2, 30))...)
	wantMember(t, c, 2, 1, 1)
	wantMember(t, c, 3, 2, 2)
	// If 2's lowest accept is the one inside the cycle, 2 and 3 are invalid,
	// and so is 4 below them.
	i12 := inv(1, 2, 10)
	c = resolve(t, roots(1), cat([]social.Envelope{i12, acc(2, i12, 40)}, link(2, 3, 20), link(3, 2, 30), link(3, 4, 50))...)
	if c.Valid(pk(2)) || c.Valid(pk(3)) || c.Valid(pk(4)) {
		t.Errorf("attached cycle admitted: %v", c.members)
	}
}

func TestQuota(t *testing.T) {
	// Root 1 invites 10..17; later invitees have earlier times, so 16 and 17
	// hold the first two slots.
	var invs, accs []social.Envelope
	for i := 0; i < 8; i++ {
		e := inv(1, 10+i, int64(100-i))
		invs, accs = append(invs, e), append(accs, acc(10+i, e, 200))
	}
	// Without accepts nothing is admitted, and nothing is freed.
	if c := resolve(t, roots(1), invs...); len(c.members) != 1 {
		t.Errorf("no accepts: %v", c.members)
	}
	c := resolve(t, roots(1), cat(invs, accs)...)
	for i := 0; i < 8; i++ {
		if want := i >= 2; c.Valid(pk(10+i)) != want {
			t.Errorf("invitee %d valid=%v, want %v", 10+i, !want, want)
		}
	}
	// Unaccepted invites still hold their slots: only 10 and 11 accept, and
	// they are past the quota.
	c = resolve(t, roots(1), cat(invs, accs[:2])...)
	if len(c.members) != 1 {
		t.Errorf("unaccepted invites freed slots: %v", c.members)
	}
	c = resolve(t, Config{Roots: []string{pk(1)}, Quota: 2}, cat(invs, accs)...)
	if len(c.members) != 3 || !c.Valid(pk(17)) || !c.Valid(pk(16)) {
		t.Errorf("custom quota: %v", c.members)
	}
}

func TestQuotaNoFallback(t *testing.T) {
	// 1 fills its quota of one with an invite to 3; its invite to 2 is past
	// the quota. 2 first accepts that one, then a counted invite from root 4:
	// 2's choice is the first accept, which does not count, so 2 is invalid.
	over := inv(1, 2, 20)
	other := inv(4, 2, 30)
	cfg := Config{Roots: []string{pk(1), pk(4)}, Quota: 1}
	envs := cat(link(1, 3, 10), []social.Envelope{over, other, acc(2, over, 21), acc(2, other, 31)})
	if c := resolve(t, cfg, envs...); c.Valid(pk(2)) {
		t.Error("fell back to a later accept")
	}
}

func TestQuotaTieBreaksByID(t *testing.T) {
	a, b := pk(0xa1), pk(0xb1) // used as event IDs: a sorts first
	ia, ib := invID(b, 1, 3, 10), invID(a, 1, 2, 10)
	c := resolve(t, Config{Roots: []string{pk(1)}, Quota: 1}, ia, ib, acc(3, ia, 11), acc(2, ib, 11))
	if !c.Valid(pk(2)) || c.Valid(pk(3)) {
		t.Error("same-time invites not ordered by event ID")
	}
}

func TestQuotaSpentOnRootsAndMembers(t *testing.T) {
	// Root 1 spends one slot on root 2 and one on 3, who picks root 2 instead.
	i13 := inv(1, 3, 2)
	envs := cat([]social.Envelope{inv(1, 2, 1), i13}, link(2, 3, 1))
	for i := 0; i < 5; i++ {
		envs = append(envs, link(1, 20+i, int64(10+i))...)
	}
	c := resolve(t, roots(1, 2), envs...)
	wantMember(t, c, 3, 2, 1)
	if c.Valid(pk(24)) || !c.Valid(pk(23)) {
		t.Error("invites to a root and to another's member must still use slots")
	}
}

func TestMaxDepth(t *testing.T) {
	var envs []social.Envelope
	for i := 1; i <= 10; i++ {
		envs = append(envs, link(i, i+1, int64(i))...)
	}
	c := resolve(t, roots(1), envs...)
	wantMember(t, c, 9, 8, 8)
	if c.Valid(pk(10)) || c.Valid(pk(11)) {
		t.Error("identity beyond max depth admitted")
	}
	c = resolve(t, Config{Roots: []string{pk(1)}, MaxDepth: 2}, envs...)
	if !c.Valid(pk(3)) || c.Valid(pk(4)) {
		t.Error("custom max depth ignored")
	}
	// A long chain off the cap resolves without blowing up.
	envs = nil
	for i := 1; i <= 2000; i++ {
		envs = append(envs, link(i, i+1, 1)...)
	}
	if c := resolve(t, roots(1), envs...); len(c.members) != 9 {
		t.Errorf("long chain: %d members", len(c.members))
	}
}

func TestDuplicatesAndJunkIgnored(t *testing.T) {
	l := link(1, 2, 10)
	junk := envOf(id(), pk(1), 1, `{"v":1,"type":"invite","invitee":"bad"}`)
	other := envOf(id(), pk(1), 1, `{"v":1,"type":"rating"}`)
	c := resolve(t, Config{Roots: []string{pk(1)}, Quota: 2}, cat(l, l, l, []social.Envelope{junk, other}, link(1, 3, 20))...)
	wantMember(t, c, 2, 1, 1)
	wantMember(t, c, 3, 1, 1)
}

func TestResolveOrderIndependent(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	var envs, invs []social.Envelope
	// A random forest: each identity is linked from a lower one, so chains
	// of many depths form and quotas overflow.
	for to := 2; to < 80; to++ {
		envs = append(envs, link(rng.Intn(to), to, int64(1+rng.Intn(15)))...) // many same-second ties
	}
	// Noise: random invites, accepts by their invitee or by someone else,
	// and cycles.
	for i := 0; i < 300; i++ {
		from, to := rng.Intn(80), rng.Intn(80)
		if from != to {
			e := inv(from, to, int64(1+rng.Intn(15)))
			invs, envs = append(invs, e), append(envs, e)
		}
	}
	for i := 0; i < 200; i++ {
		e := invs[rng.Intn(len(invs))]
		who := e.Content[len(e.Content)-66 : len(e.Content)-2] // the invitee key
		if rng.Intn(5) == 0 {
			who = []byte(pk(rng.Intn(80)))
		}
		envs = append(envs, envOf(id(), string(who), int64(1+rng.Intn(15)),
			fmt.Sprintf(`{"v":1,"type":"invite_accept","invite":%q}`, e.ID)))
	}
	for i := 0; i < 100; i++ { // redeliveries
		envs = append(envs, envs[rng.Intn(len(envs))])
	}
	cfg := Config{Roots: []string{pk(0), pk(1)}, Quota: 4, MaxDepth: 4}
	want := resolve(t, cfg, envs...)
	if len(want.members) < 10 || len(want.members) > 55 {
		t.Fatalf("fixture not discriminating: %d members", len(want.members))
	}
	depths := map[int]bool{}
	for m := range maps.Values(want.members) {
		depths[m.Depth] = true
	}
	if len(depths) < 3 {
		t.Fatalf("fixture too shallow: depths %v", depths)
	}
	t.Logf("%d envelopes, %d members, depths %v", len(envs), len(want.members), depths)
	for i := 0; i < 60; i++ {
		rng.Shuffle(len(envs), func(a, b int) { envs[a], envs[b] = envs[b], envs[a] })
		if got := resolve(t, cfg, envs...); !reflect.DeepEqual(got.members, want.members) {
			t.Fatalf("permutation %d changed the result", i)
		}
	}
}

func TestFutureDatedEventsWaitForTheClock(t *testing.T) {
	cfg := Config{Roots: []string{pk(1)}}
	i := inv(1, 2, 1000)
	a := acc(2, i, 1000+social.MaxClockSkew+1)
	c, err := ResolveInvites(cfg, []social.Envelope{i, a}, 1000)
	if err != nil || c.Valid(pk(2)) {
		t.Fatalf("future-dated accept admitted early: %v", err)
	}
	if c, _ = ResolveInvites(cfg, []social.Envelope{i, a}, 1001); !c.Valid(pk(2)) {
		t.Fatal("accept must count once the clock reaches it")
	}
	// A far-future invite does not take a quota slot yet.
	far := inv(1, 3, 1<<50)
	near := link(1, 4, 10)
	c, _ = ResolveInvites(Config{Roots: []string{pk(1)}, Quota: 1}, append([]social.Envelope{far}, near...), 1000)
	if !c.Valid(pk(4)) {
		t.Fatal("far-future invite must not consume quota")
	}
}

package referral

import "math"

// DefaultDebtLimit caps an identity's exposure: what it owes plus what it has
// committed to pay.
const DefaultDebtLimit = 100

// unique drops nil entries and repeated representations of one request (same
// request event ID), so no obligation or settlement is counted twice. Of
// several copies it keeps the one furthest along by progress, so a stale copy
// cannot hide an accepted or settled obligation, whatever the input order.
// Progress uses only per-copy fields: a shallow copy of a Flow shares its seen
// map, so the number of applied events cannot tell such copies apart. The
// caller should still hold one canonical Flow per request.
func unique(flows []*Flow) []*Flow {
	at := map[string]int{}
	var out []*Flow
	for _, f := range flows {
		if f == nil {
			continue
		}
		if i, dup := at[f.reqID]; !dup {
			at[f.reqID] = len(out)
			out = append(out, f)
		} else if progress(f) > progress(out[i]) {
			out[i] = f
		}
	}
	return out
}

// progress ranks how far a flow has gone: by state, obligations above dead
// ends (one history never holds both, but if copies diverge the one that
// carries debt wins), then by chain length among open flows.
func progress(f *Flow) int {
	rank := map[State]int{Requested: 0, Forwarded: 1, Declined: 2, Expired: 2, Accepted: 3, Settled: 4}[f.state]
	return rank<<16 + len(f.chain)
}

// Balances derives received - paid from settled flows (a per-viewer balance).
func Balances(flows []*Flow) map[string]int64 {
	m := map[string]int64{}
	for _, f := range unique(flows) {
		if f.state == Settled {
			m[f.forwarder] = addSat(m[f.forwarder], f.req.Offer)
			m[f.requester] = addSat(m[f.requester], -f.req.Offer)
		}
	}
	return m
}

// Debt sums who's Accepted flows unpaid more than grace seconds after the
// accept was observed (trusted time, not the target's created_at):
// the overdue part of what who owes. It is a reporting figure; admission uses
// Exposure.
func Debt(flows []*Flow, who string, now, grace int64) int64 {
	var d int64
	for _, f := range unique(flows) {
		if f.state == Accepted && f.requester == who && now-f.acceptAt > grace {
			d = addSat(d, f.req.Offer)
		}
	}
	return d
}

// Exposure is everything who owes or may come to owe as of now: a negative
// settled balance, every accepted-but-unpaid offer (overdue or not), and the
// offers of its requests that are still open. Paying a receipt therefore
// moves an amount from "unpaid" to "negative balance" without freeing limit;
// only points earned as a referrer do.
func Exposure(flows []*Flow, who string, now int64) int64 {
	var e int64
	if b := Balances(flows)[who]; b == math.MinInt64 {
		e = math.MaxInt64 // -b would wrap
	} else if b < 0 {
		e = -b
	}
	for _, f := range unique(flows) {
		if s := f.State(now); f.requester == who && s <= Accepted {
			e = addSat(e, f.req.Offer)
		}
	}
	return e
}

// CanRequest reports whether who may open a request offering offer. valid is
// the caller's invite-chain predicate (nil denies). The new offer counts
// against the limit; limit <= 0 means the default.
func CanRequest(flows []*Flow, who string, offer, now, limit int64, valid func(string) bool) bool {
	if limit <= 0 {
		limit = DefaultDebtLimit
	}
	if valid == nil || !valid(who) || !pointsOK(offer) {
		return false
	}
	return addSat(Exposure(flows, who, now), offer) <= limit
}

// addSat adds without wrapping: sums saturate at the int64 bounds.
func addSat(a, b int64) int64 {
	if b > 0 && a > math.MaxInt64-b {
		return math.MaxInt64
	}
	if b < 0 && a < math.MinInt64-b {
		return math.MinInt64
	}
	return a + b
}

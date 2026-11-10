// Package referral implements section 3 of docs/search/protocol-draft.md:
// body parsing, the per-request state machine and a points ledger. It is pure
// logic over verified events; recipient routing is not in any body, so callers
// supply it explicitly.
package referral

import (
	"errors"
	"fmt"

	"github.com/iDoris-ai/hyphae/internal/social"
)

const (
	// MaxPoints bounds an offer or amount; far beyond any sane referral, and
	// keeps ledger sums far from int64 overflow.
	MaxPoints = 1_000_000
	// MaxHops is the largest hops_left a request may start with.
	MaxHops = 3
	// MaxHorizon bounds expires_at (30 days as for intents; draft is silent).
	MaxHorizon = 30 * 24 * 3600
)

// Request is a referral_request body (HopsLeft is a pointer: missing != 0).
type Request struct {
	social.Header
	Intent    string `json:"intent"`
	Offer     int64  `json:"offer"`
	HopsLeft  *int   `json:"hops_left"`
	ExpiresAt int64  `json:"expires_at"`
}

// Forward is a referral_forward body; HopsLeft is the value after this hop.
type Forward struct {
	social.Header
	Request  string `json:"request"`
	Intent   string `json:"intent"`
	HopsLeft *int   `json:"hops_left"`
}

// Reply is a referral_accept or referral_decline body.
type Reply struct {
	social.Header
	Forward string `json:"forward"`
}

// Receipt is a receipt body.
type Receipt struct {
	social.Header
	To     string `json:"to"`
	Amount int64  `json:"amount"`
	Ref    string `json:"ref"`
}

// decode checks type, strictly unmarshals into v, then runs the field check ok.
func decode(env social.Envelope, typ string, v any, ok func() bool) error {
	h, err := social.ParseHeader(env)
	if err != nil {
		return err
	}
	if h.Type != typ {
		return fmt.Errorf("content: type %q, want %q", h.Type, typ)
	}
	if err := social.StrictUnmarshal(env.Content, v); err != nil {
		return err
	}
	if !ok() {
		return errors.New("content: field out of range or malformed")
	}
	return nil
}

func hexOK(vals ...string) bool {
	for _, v := range vals {
		if !social.ValidHex32(v) {
			return false
		}
	}
	return true
}

func hopsOK(h *int) bool { return h != nil && *h >= 0 && *h <= MaxHops }

func pointsOK(n int64) bool { return n >= 1 && n <= MaxPoints }

// ParseRequest validates offer, hops_left and expires_at bounds as well.
func ParseRequest(env social.Envelope) (r Request, err error) {
	err = decode(env, "referral_request", &r, func() bool {
		return hexOK(r.Intent) && pointsOK(r.Offer) && hopsOK(r.HopsLeft) &&
			r.ExpiresAt > env.CreatedAt && r.ExpiresAt-env.CreatedAt <= MaxHorizon
	})
	return
}

func ParseForward(env social.Envelope) (f Forward, err error) {
	err = decode(env, "referral_forward", &f, func() bool {
		return hexOK(f.Request, f.Intent) && hopsOK(f.HopsLeft)
	})
	return
}

// ParseForward, ParseReply (typ: accept or decline) and ParseReceipt validate
// the other bodies; any other type fails the strict decode.
func ParseReply(env social.Envelope, typ string) (r Reply, err error) {
	err = decode(env, typ, &r, func() bool { return hexOK(r.Forward) })
	return
}

func ParseReceipt(env social.Envelope) (r Receipt, err error) {
	err = decode(env, "receipt", &r, func() bool { return hexOK(r.To, r.Ref) && pointsOK(r.Amount) })
	return
}

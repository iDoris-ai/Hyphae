package trust

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/iDoris-ai/hyphae/internal/social"
)

// Bounds on attestations and on the work Verify does per call.
const (
	MaxPresented  = 64
	MaxValueRunes = 128
	MaxAttestLife = 366 * 24 * 3600 // seconds between created_at and expires_at
)

// Attestation is a parsed attest message. Issuer is the envelope author.
type Attestation struct {
	ID, Issuer, Subject, Claim, Value string
	CreatedAt, ExpiresAt              int64
}

// ParseAttest parses and validates an attest body. It does not decide whether
// the issuer is trusted or the attestation is still current; Verify does.
func ParseAttest(e social.Envelope) (Attestation, error) {
	var b struct {
		V         int    `json:"v"`
		Type      string `json:"type"`
		Subject   string `json:"subject"`
		Claim     string `json:"claim"`
		Value     string `json:"value"`
		ExpiresAt int64  `json:"expires_at"`
	}
	if err := parseBody(e, "attest", &b); err != nil {
		return Attestation{}, err
	}
	switch n := utf8.RuneCountInString(b.Value); {
	case !social.ValidHex32(b.Subject):
		return Attestation{}, errors.New("attest: subject must be 64 lowercase hex characters")
	case b.Subject == e.Author:
		return Attestation{}, errors.New("attest: self-attestation")
	case !social.ValidKey(b.Claim):
		return Attestation{}, errors.New("attest: invalid claim")
	case n < 1 || n > MaxValueRunes || strings.TrimSpace(b.Value) == "":
		return Attestation{}, fmt.Errorf("attest: value must be 1-%d non-blank characters", MaxValueRunes)
	case b.ExpiresAt <= e.CreatedAt:
		return Attestation{}, errors.New("attest: expires_at must be later than created_at")
	case b.ExpiresAt-e.CreatedAt > MaxAttestLife:
		return Attestation{}, errors.New("attest: expires_at more than 366 days after created_at")
	}
	return Attestation{ID: e.ID, Issuer: e.Author, Subject: b.Subject, Claim: b.Claim, Value: b.Value,
		CreatedAt: e.CreatedAt, ExpiresAt: b.ExpiresAt}, nil
}

// Verify checks the attestation events presenter chose to show a counterparty
// (selective disclosure). An attestation is accepted iff it parses, is about
// presenter, its issuer is trusted (a nil trusted rejects everything),
// now <= expires_at, and created_at is at most social.MaxClockSkew ahead of
// now. now must be the caller's trusted clock, since created_at is
// sender-chosen. Duplicate event IDs count once. Per (issuer, claim) only the
// latest accepted attestation survives (created_at, then higher event ID);
// different issuers' values for one claim are all returned, ordered by claim,
// issuer, event ID, and the caller decides how to weigh conflicts. More than
// MaxPresented envelopes is an error for the whole call. rejected is keyed by
// event ID; a superseded attestation is reported there too.
//
// presented must already be signature- and ID-verified envelopes: the issuer
// is taken from e.Author, and of two envelopes with one ID the first wins.
// Supersession is not revocation: the presenter picks what to show, so it can
// withhold a newer attestation, and an expired or future-dated newer one does
// not displace a still-valid older one. MaxAttestLife bounds how long a stale
// attestation can be shown.
func Verify(presented []social.Envelope, presenter string, trusted func(issuer string) bool, now int64) (accepted []Attestation, rejected map[string]error, err error) {
	if len(presented) > MaxPresented {
		return nil, nil, fmt.Errorf("attest: %d envelopes presented, limit %d", len(presented), MaxPresented)
	}
	rejected = map[string]error{}
	seen := map[string]bool{}
	latest := map[[2]string]Attestation{} // (issuer, claim) -> newest
	superseded := errors.New("attest: superseded by a later attestation")
	for _, e := range presented {
		if seen[e.ID] {
			continue
		}
		seen[e.ID] = true
		a, perr := ParseAttest(e)
		switch {
		case perr != nil:
			rejected[e.ID] = perr
		case a.Subject != presenter:
			rejected[e.ID] = errors.New("attest: subject is not the presenter")
		case trusted == nil || !trusted(a.Issuer):
			rejected[e.ID] = errors.New("attest: issuer not trusted")
		case now > a.ExpiresAt:
			rejected[e.ID] = errors.New("attest: expired")
		case a.CreatedAt > now+social.MaxClockSkew:
			rejected[e.ID] = errors.New("attest: created_at ahead of local clock")
		default:
			k := [2]string{a.Issuer, a.Claim}
			old, ok := latest[k]
			if ok && order(a.CreatedAt, a.ID, old.CreatedAt, old.ID) < 0 {
				rejected[a.ID] = superseded
				continue
			}
			if ok {
				rejected[old.ID] = superseded
			}
			latest[k] = a
		}
	}
	for _, a := range latest {
		accepted = append(accepted, a)
	}
	slices.SortFunc(accepted, func(x, y Attestation) int {
		return cmp.Or(strings.Compare(x.Claim, y.Claim), strings.Compare(x.Issuer, y.Issuer), strings.Compare(x.ID, y.ID))
	})
	return accepted, rejected, nil
}

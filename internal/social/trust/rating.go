package trust

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/iDoris-ai/hyphae/internal/social"
)

// Defaults for ScoreConfig, and bounds on the optional free-form tags.
const (
	DefaultPrior, DefaultPriorWeight = 3.0, 5.0
	MaxTags, MaxTagLen               = 8, 64
)

// Rating is a parsed rating message. Rater is the envelope author. Match
// holds the two accept event IDs in ascending order.
type Rating struct {
	ID, Rater, Subject string
	Match              [2]string
	Score              int
	Tags               []string
	CreatedAt          int64
}

// ParseRating parses and validates a rating body. It does not check the
// match itself; Reputation does that against caller-supplied match facts.
func ParseRating(e social.Envelope) (Rating, error) {
	var b struct {
		V       int      `json:"v"`
		Type    string   `json:"type"`
		Subject string   `json:"subject"`
		Match   []string `json:"match"`
		Score   int      `json:"score"`
		Tags    []string `json:"tags"`
	}
	if err := parseBody(e, "rating", &b); err != nil {
		return Rating{}, err
	}
	switch {
	case !social.ValidHex32(b.Subject):
		return Rating{}, errors.New("rating: subject must be 64 lowercase hex characters")
	case b.Subject == e.Author:
		return Rating{}, errors.New("rating: self-rating")
	case len(b.Match) != 2 || !social.ValidHex32(b.Match[0]) || !social.ValidHex32(b.Match[1]) || b.Match[0] == b.Match[1]:
		return Rating{}, errors.New("rating: match must be two distinct event IDs")
	case b.Score < 1 || b.Score > 5:
		return Rating{}, fmt.Errorf("rating: score %d out of range 1-5", b.Score)
	case len(b.Tags) > MaxTags:
		return Rating{}, fmt.Errorf("rating: more than %d tags", MaxTags)
	case slices.ContainsFunc(b.Tags, func(t string) bool { return t == "" || len(t) > MaxTagLen }):
		return Rating{}, fmt.Errorf("rating: tag length must be 1-%d bytes", MaxTagLen)
	}
	return Rating{ID: e.ID, Rater: e.Author, Subject: b.Subject, Match: pair(b.Match[0], b.Match[1]),
		Score: b.Score, Tags: b.Tags, CreatedAt: e.CreatedAt}, nil
}

func pair(a, b string) [2]string {
	if b < a {
		a, b = b, a
	}
	return [2]string{a, b}
}

// Match is a fact the caller has established, typically with the intent
// package: these two parties both accepted, with these two accept event IDs.
// Order within each array does not matter.
type Match struct {
	Accepts, Parties [2]string
}

// ScoreConfig holds the prior. Zero fields mean the defaults, so a zero prior
// or zero weight cannot be requested. The values are trusted as given: a
// negative or NaN weight is not rejected, so validate them before this config
// ever comes from user input.
type ScoreConfig struct {
	Prior, PriorWeight float64
}

// Score is one subject's reputation.
type Score struct {
	Value  float64
	Raters int
}

// Reputation scores every subject with at least one counted rating. A
// subject missing from the result has the prior and zero raters.
//
//   - A rating counts only if its match is known, rater and subject are
//     exactly that match's two parties, and the rater is valid in chain.
//   - Per rater and match only the latest rating counts (created_at, then
//     the greater event ID), so flooding one match yields one rating.
//   - A rater's ratings of one subject across matches collapse to their mean.
//   - Value = (prior*weight + sum of per-rater means) / (weight + raters).
//
// Unparseable envelopes are ignored, and so are events dated more than
// social.MaxClockSkew ahead of now (the trusted local time): a far-future
// rating would otherwise stay "latest" and suppress later corrections.
func Reputation(cfg ScoreConfig, chain *Chain, matches []Match, envs []social.Envelope, now int64) map[string]Score {
	cfg.Prior = cmp.Or(cfg.Prior, DefaultPrior)
	cfg.PriorWeight = cmp.Or(cfg.PriorWeight, DefaultPriorWeight)
	facts := map[[2][2]string]bool{}
	for _, m := range matches {
		facts[[2][2]string{pair(m.Accepts[0], m.Accepts[1]), pair(m.Parties[0], m.Parties[1])}] = true
	}
	type slot struct{ rater, a, b string }
	latest := map[slot]Rating{}
	for _, e := range envs {
		r, err := ParseRating(e)
		if err != nil || e.CreatedAt > now+social.MaxClockSkew || !chain.Valid(r.Rater) || !facts[[2][2]string{r.Match, pair(r.Rater, r.Subject)}] {
			continue
		}
		k := slot{r.Rater, r.Match[0], r.Match[1]}
		if old, ok := latest[k]; !ok || order(old.CreatedAt, old.ID, r.CreatedAt, r.ID) < 0 {
			latest[k] = r
		}
	}
	type edge struct{ rater, subject string }
	sum, n := map[edge]int{}, map[edge]int{}
	for _, r := range latest {
		k := edge{r.Rater, r.Subject}
		sum[k] += r.Score
		n[k]++
	}
	// Add per-rater means in a fixed order so the float result is stable.
	keys := slices.SortedFunc(maps.Keys(sum), func(a, b edge) int { return strings.Compare(a.rater, b.rater) })
	total, raters := map[string]float64{}, map[string]int{}
	for _, k := range keys {
		total[k.subject] += float64(sum[k]) / float64(n[k])
		raters[k.subject]++
	}
	out := map[string]Score{}
	for subj, t := range total {
		w := cfg.PriorWeight + float64(raters[subj])
		out[subj] = Score{Value: (cfg.Prior*cfg.PriorWeight + t) / w, Raters: raters[subj]}
	}
	return out
}

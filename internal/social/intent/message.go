// Package intent implements direction 1 of the social protocol: intents, the
// respond/disclose/accept/decline handshake and its session state machine (see
// docs/search/protocol-draft.md). Pure logic over verified social.Envelopes.
package intent

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/iDoris-ai/hyphae/internal/social"
)

// Limits from the draft. Character limits count runes, not bytes.
const (
	MaxCategory   = 32
	MaxSummary    = 280
	MaxArea       = 64
	MaxParty      = 20
	MaxLifetime   = 30 * 24 * 60 * 60
	MaxFields     = 16
	MaxFieldKey   = 32
	MaxFieldVal   = 512
	MaxReason     = 280 // the draft gives no bound; reuse the summary bound
	MaxRound      = 4   // "at most 5 rounds" read as rounds 0..4, respond being 0
	categoryChars = "abcdefghijklmnopqrstuvwxyz0123456789-"
)

// Intent is the public announcement that opens sessions.
type Intent struct {
	social.Header
	Category  string                   `json:"category"`
	Summary   string                   `json:"summary"`
	Window    struct{ From, To int64 } `json:"window"`
	Area      string                   `json:"area"`
	Party     struct{ Min, Max int }   `json:"party"`
	ExpiresAt int64                    `json:"expires_at"`
}

// The other bodies. Respond is the responder's round 0; disclose starts at 1.
type (
	// Ref is the header plus intent reference every other body carries.
	Ref struct {
		social.Header
		Intent string `json:"intent"`
	}
	Respond struct {
		Ref
		Fields map[string]string `json:"fields"`
	}
	Disclose struct {
		Ref
		Round  int               `json:"round"`
		Fields map[string]string `json:"fields"`
	}
	Decline struct {
		Ref
		Reason string `json:"reason,omitempty"`
	}
	Accept   struct{ Ref }
	Withdraw struct{ Ref }
)

// Message is a parsed, validated envelope. Body is one of *Intent, *Respond,
// *Disclose, *Accept, *Decline or *Withdraw; Ref is the referenced intent ID.
type Message struct {
	Envelope social.Envelope
	Type     string
	Body     any
	Ref      string
}

type body interface {
	validate(createdAt int64) error
	target() string
}

var bodies = map[string]func() body{
	"intent":   func() body { return &Intent{} },
	"respond":  func() body { return &Respond{} },
	"disclose": func() body { return &Disclose{} },
	"accept":   func() body { return &Accept{} },
	"decline":  func() body { return &Decline{} },
	"withdraw": func() body { return &Withdraw{} },
}

// Parse validates e and decodes its body into a typed message, enforcing every
// field constraint of the draft.
func Parse(e social.Envelope) (Message, error) {
	h, err := social.ParseHeader(e)
	if err != nil {
		return Message{}, err
	}
	mk, ok := bodies[h.Type]
	if !ok {
		return Message{}, fmt.Errorf("intent: unknown type %q", h.Type)
	}
	b := mk()
	if err = social.StrictUnmarshal(e.Content, b); err == nil {
		err = b.validate(e.CreatedAt)
	}
	if err != nil {
		return Message{}, fmt.Errorf("intent: %s: %w", h.Type, err)
	}
	return Message{Envelope: e, Type: h.Type, Body: b, Ref: b.target()}, nil
}

func (b *Intent) target() string { return "" }
func (r *Ref) target() string    { return r.Intent }

func (b *Intent) validate(at int64) error {
	c, w, p := b.Category, b.Window, b.Party
	return errors.Join(
		// Trim leaves something only if a byte outside the charset is present.
		errIf(len(c) < 1 || len(c) > MaxCategory || strings.Trim(c, categoryChars) != "",
			"category must be 1..%d characters of a-z, 0-9 and -", MaxCategory),
		errIf(!text(b.Summary, MaxSummary), "summary must be 1..%d characters", MaxSummary),
		// Stricter than the draft (only from < to): times must be positive.
		errIf(w.From <= 0 || w.From >= w.To, "window must satisfy 0 < from < to"),
		errIf(!text(b.Area, MaxArea), "area must be 1..%d characters", MaxArea),
		errIf(p.Min < 1 || p.Min > p.Max || p.Max > MaxParty, "party must satisfy 1 <= min <= max <= %d", MaxParty),
		errIf(b.ExpiresAt <= at || b.ExpiresAt > at+MaxLifetime, "expires_at must be after created_at, by at most 30 days"),
	)
}

func (r *Ref) validate(int64) error {
	return errIf(!social.ValidHex32(r.Intent), "intent reference must be 64 lowercase hex characters")
}

func (b *Respond) validate(int64) error { return errors.Join(b.Ref.validate(0), validFields(b.Fields)) }

func (b *Disclose) validate(int64) error {
	return errors.Join(b.Ref.validate(0), validFields(b.Fields),
		errIf(b.Round < 1 || b.Round > MaxRound, "round must be 1..%d", MaxRound))
}

func (b *Decline) validate(int64) error {
	return errors.Join(b.Ref.validate(0), errIf(!runes(b.Reason, 0, MaxReason), "reason exceeds %d characters", MaxReason))
}

// validFields also rejects an empty map: a disclosure must give something.
func validFields(f map[string]string) error {
	bad := len(f) == 0 || len(f) > MaxFields
	for k, v := range f {
		bad = bad || !social.ValidKey(k) || !runes(v, 0, MaxFieldVal)
	}
	return errIf(bad, "fields must have 1..%d entries, keys of 1..%d and values of at most %d characters",
		MaxFields, MaxFieldKey, MaxFieldVal)
}

// text rejects blank strings too: the draft's minimum of 1 means content.
func text(s string, max int) bool { return strings.TrimSpace(s) != "" && runes(s, 1, max) }

func runes(s string, min, max int) bool {
	n := utf8.RuneCountInString(s)
	return n >= min && n <= max
}

func errIf(bad bool, format string, args ...any) error {
	if bad {
		return fmt.Errorf(format, args...)
	}
	return nil
}

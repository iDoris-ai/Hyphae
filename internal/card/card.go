// Package card builds and reads directory cards: the public, searchable entry
// a person publishes when visible (docs/search/discover-friends.md). A card is
// a kind-30078 addressable event with c=d=hyphae-card, so the newest card
// from an author replaces the older one; hiding publishes a tombstone card.
package card

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"fiatjaf.com/nostr"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"

	"github.com/iDoris-ai/hyphae/internal/social"
	"github.com/iDoris-ai/hyphae/internal/wireevent"
)

const (
	// DefaultTag is used when a person goes public without choosing tags.
	DefaultTag = "友好"
	// MaxTags is the most tags one card may carry.
	MaxTags = 10
	// MaxTagRunes, MaxNameRunes and MaxAboutRunes bound each text field.
	MaxTagRunes   = 32
	MaxNameRunes  = 64
	MaxAboutRunes = 256
	// Lifetime is how long a card stays valid; the daemon republishes it
	// before then.
	Lifetime = 30 * 24 * 60 * 60
)

// Card is the public part of a person's directory entry.
type Card struct {
	Name  string
	Tags  []string
	About string
}

// MaxClockSkew is how far in the future a card's created_at may be.
const MaxClockSkew = 10 * 60

// canonical is stable only for the Unicode tables of the Go build that runs
// it: a tag accepted by one build can fail Validate on a build with newer
// tables. Peers on mixed Go versions are expected to be rare.
func canonical(s string) string {
	// Folding can produce a form that is no longer NFKC; normalize again.
	return norm.NFKC.String(cases.Fold().String(norm.NFKC.String(s)))
}

// NormalizeTag returns the canonical form of one tag: trimmed, NFKC, case
// folded. It rejects empty or over-long tags, tags containing whitespace,
// commas, '#', control or format characters or invalid UTF-8, and tags whose
// canonical form is not stable (folding Cherokee flips case each time).
func NormalizeTag(raw string) (string, error) {
	if !utf8.ValidString(raw) {
		return "", errors.New("tag is not valid UTF-8")
	}
	t := canonical(strings.TrimSpace(raw))
	if t == "" {
		return "", errors.New("tag is empty")
	}
	if n := utf8.RuneCountInString(t); n > MaxTagRunes {
		return "", fmt.Errorf("tag has %d characters, limit %d", n, MaxTagRunes)
	}
	for _, r := range t {
		if unicode.IsSpace(r) || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) ||
			r == ',' || r == '#' || r == utf8.RuneError {
			return "", fmt.Errorf("tag contains %q", r)
		}
	}
	if canonical(t) != t {
		return "", errors.New("tag has no stable canonical form")
	}
	return t, nil
}

// NormalizeTags normalizes every tag, drops duplicates keeping first order,
// and enforces MaxTags. An empty input stays empty; callers add DefaultTag.
func NormalizeTags(raw []string) ([]string, error) {
	out := make([]string, 0, len(raw))
	for _, r := range raw {
		t, err := NormalizeTag(r)
		if err != nil {
			return nil, fmt.Errorf("tag %q: %w", r, err)
		}
		if !slices.Contains(out, t) {
			out = append(out, t)
		}
	}
	if len(out) > MaxTags {
		return nil, fmt.Errorf("%d tags, limit %d", len(out), MaxTags)
	}
	return out, nil
}

// Validate checks a card whose tags are already normalized.
func (c Card) Validate() error {
	if err := checkText("name", c.Name, 1, MaxNameRunes); err != nil {
		return err
	}
	if err := checkText("about", c.About, 0, MaxAboutRunes); err != nil {
		return err
	}
	if len(c.Tags) == 0 {
		return errors.New("card needs at least one tag")
	}
	norm, err := NormalizeTags(c.Tags)
	if err != nil {
		return err
	}
	if !slices.Equal(norm, c.Tags) {
		return errors.New("card tags are not normalized or contain duplicates")
	}
	return nil
}

func checkText(field, s string, minRunes, maxRunes int) error {
	if !utf8.ValidString(s) {
		return fmt.Errorf("%s is not valid UTF-8", field)
	}
	if strings.TrimSpace(s) != s {
		return fmt.Errorf("%s has leading or trailing space", field)
	}
	n := utf8.RuneCountInString(s)
	if n < minRunes || n > maxRunes {
		return fmt.Errorf("%s has %d characters, want %d..%d", field, n, minRunes, maxRunes)
	}
	runes := []rune(s)
	visible := false
	for i, r := range runes {
		if unicode.In(r, unicode.L, unicode.N, unicode.S, unicode.P) {
			visible = true
		}
		if unicode.IsControl(r) || (unicode.Is(unicode.Cf, r) && !allowedFormat(runes, i)) {
			return fmt.Errorf("%s contains a control or format character", field)
		}
	}
	if n > 0 && !visible {
		return fmt.Errorf("%s has no visible character", field)
	}
	return nil
}

// allowedFormat reports whether the format character runes[i] does its
// real job, so it cannot hide inside Latin text: ZWJ joins two emoji, ZWNJ
// separates two letters of a script that uses it (Persian, Indic), and
// LRM/RLM sit next to a right-to-left letter. Any other format character
// (U+202E reverses text, U+200B hides) is rejected.
func allowedFormat(runes []rune, i int) bool {
	if i == 0 || i == len(runes)-1 {
		return false
	}
	prev, next := runes[i-1], runes[i+1]
	switch runes[i] {
	case '\u200d':
		return isEmoji(prev) && unicode.Is(unicode.So, next)
	case '\u200c':
		return joiningLetter(prev) && joiningLetter(next)
	case '\u200e', '\u200f':
		return rtl(prev) || rtl(next)
	}
	return false
}

// isEmoji is the part of an emoji a ZWJ may follow: the symbol itself, the
// emoji presentation selector, or a skin tone modifier.
func isEmoji(r rune) bool {
	return unicode.Is(unicode.So, r) || r == '\ufe0f' || (r >= 0x1f3fb && r <= 0x1f3ff)
}

func joiningLetter(r rune) bool {
	return unicode.In(r, unicode.L, unicode.M) && !unicode.In(r, unicode.Latin, unicode.Cyrillic, unicode.Greek)
}

func rtl(r rune) bool {
	return unicode.In(r, unicode.Arabic, unicode.Hebrew)
}

type body struct {
	V      int      `json:"v"`
	Name   string   `json:"name,omitempty"`
	Tags   []string `json:"tags,omitempty"`
	About  string   `json:"about,omitempty"`
	Hidden bool     `json:"hidden,omitempty"`
}

func baseTags(now nostr.Timestamp) nostr.Tags {
	return nostr.Tags{
		{"d", wireevent.CardCategory},
		{"c", wireevent.CardCategory},
		{"v", wireevent.CardVersion},
		{"expiration", strconv.FormatInt(int64(now)+Lifetime, 10)},
	}
}

// Event returns the unsigned card event for pubkey at now.
func (c Card) Event(pubkey nostr.PubKey, now nostr.Timestamp) (*nostr.Event, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	content, err := json.Marshal(body{V: 1, Name: c.Name, Tags: c.Tags, About: c.About})
	if err != nil {
		return nil, err
	}
	tags := baseTags(now)
	for _, t := range c.Tags {
		tags = append(tags, nostr.Tag{"t", t})
	}
	return &nostr.Event{Kind: wireevent.Kind30078, CreatedAt: now, Tags: tags, Content: string(content), PubKey: pubkey}, nil
}

// Tombstone returns the unsigned event that replaces a card when its owner
// hides. It carries no t tags, so tag searches stop matching it.
func Tombstone(pubkey nostr.PubKey, now nostr.Timestamp) *nostr.Event {
	return &nostr.Event{Kind: wireevent.Kind30078, CreatedAt: now, Tags: baseTags(now),
		Content: `{"v":1,"hidden":true}`, PubKey: pubkey}
}

// ErrHidden reports a tombstone: the author is hidden.
var ErrHidden = errors.New("card owner is hidden")

// Parse reads a card event whose signature the caller has already verified.
// It returns ErrHidden for a tombstone and an error for anything malformed or
// expired at now (the caller's trusted local time).
func Parse(ev *nostr.Event, now nostr.Timestamp) (Card, error) {
	if ev == nil || ev.Kind != wireevent.Kind30078 {
		return Card{}, errors.New("not a kind 30078 event")
	}
	class, err := wireevent.Classify30078(ev.Tags)
	if err != nil {
		return Card{}, err
	}
	if class != wireevent.ClassCard {
		return Card{}, errors.New("not a card event")
	}
	exp, tTags, err := cardTags(ev.Tags)
	if err != nil {
		return Card{}, err
	}
	if exp <= int64(now) {
		return Card{}, errors.New("card has expired")
	}
	// A future-dated card would outrank the author's later tombstone, so
	// hiding would not work; bound both ends by the reader's clock.
	if int64(ev.CreatedAt) > int64(now)+MaxClockSkew {
		return Card{}, errors.New("card is dated in the future")
	}
	// A comparison, not exp-created_at: a negative created_at must not
	// overflow past the bound.
	if int64(ev.CreatedAt) < exp-Lifetime {
		return Card{}, errors.New("card lifetime exceeds limit")
	}
	var b body
	if err := social.StrictUnmarshal(json.RawMessage(ev.Content), &b); err != nil {
		return Card{}, err
	}
	if b.V != 1 {
		return Card{}, fmt.Errorf("unsupported card version %d", b.V)
	}
	if b.Hidden {
		if b.Name != "" || len(b.Tags) != 0 || b.About != "" || len(tTags) != 0 {
			return Card{}, errors.New("tombstone carries card fields")
		}
		return Card{}, ErrHidden
	}
	c := Card{Name: b.Name, Tags: b.Tags, About: b.About}
	if err := c.Validate(); err != nil {
		return Card{}, err
	}
	if !slices.Equal(tTags, c.Tags) {
		return Card{}, errors.New("t tags do not match content tags")
	}
	return c, nil
}

// cardTags returns the single expiration value and the t tag values in order.
func cardTags(tags nostr.Tags) (int64, []string, error) {
	var exp int64
	seen := false
	var ts []string
	for _, tag := range tags {
		if len(tag) == 0 {
			continue
		}
		switch tag[0] {
		case "expiration":
			if seen || len(tag) != 2 {
				return 0, nil, errors.New("card needs exactly one expiration tag")
			}
			v, err := strconv.ParseInt(tag[1], 10, 64)
			if err != nil || v <= 0 {
				return 0, nil, errors.New("card expiration is not a positive integer")
			}
			exp, seen = v, true
		case "t":
			if len(tag) != 2 {
				return 0, nil, errors.New("malformed t tag")
			}
			ts = append(ts, tag[1])
		}
	}
	if !seen {
		return 0, nil, errors.New("card needs exactly one expiration tag")
	}
	return exp, ts, nil
}

// Filter returns the relay filter for cards carrying any of the given tags.
// The tags are normalized first; the c tag keeps other applications' kind
// 30078 events from using up the limit.
func Filter(tags []string, limit int) (nostr.Filter, error) {
	norm, err := NormalizeTags(tags)
	if err != nil {
		return nostr.Filter{}, err
	}
	if len(norm) == 0 {
		return nostr.Filter{}, errors.New("search needs at least one tag")
	}
	return nostr.Filter{
		Kinds: []nostr.Kind{wireevent.Kind30078},
		Tags:  nostr.TagMap{"c": {wireevent.CardCategory}, "t": norm},
		Limit: limit,
	}, nil
}

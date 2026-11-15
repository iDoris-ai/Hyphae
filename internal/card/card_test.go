package card

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"fiatjaf.com/nostr"

	"github.com/iDoris-ai/hyphae/internal/wireevent"
)

const now = nostr.Timestamp(1_700_000_000)

var pk = nostr.MustPubKeyFromHex(strings.Repeat("ab", 32))

func TestNormalizeTag(t *testing.T) {
	ok := map[string]string{
		"友好":                             "友好",
		"  友好 ":                          "友好",
		"Hotpot":                         "hotpot",
		"ＨＯＴＰＯＴ":                         "hotpot",  // full width folds to ASCII (NFKC)
		"Straße":                         "strasse", // case folding, not ToLower
		"ﬁ":                              "fi",
		"火锅-夜宵":                          "火锅-夜宵",
		strings.Repeat("好", MaxTagRunes): strings.Repeat("好", MaxTagRunes),
	}
	for in, want := range ok {
		if got, err := NormalizeTag(in); err != nil || got != want {
			t.Errorf("NormalizeTag(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	bad := []string{"", "   ", "a b", "a,b", "#tag", "a\tb", "a\x00", "\xff", strings.Repeat("好", MaxTagRunes+1), "a　b",
		"Ꭰ", "ꭰ", // Cherokee folds back and forth, so it has no stable form
		"a\u200bb", "a\u202eb", "a\u00adb", "a\ufeffb", "a\u200db"} // format characters
	for _, in := range bad {
		if got, err := NormalizeTag(in); err == nil {
			t.Errorf("NormalizeTag(%q) = %q, want error", in, got)
		}
	}
}

// Every tag NormalizeTag accepts is a fixed point, so Card.Event never rejects
// a tag the CLI accepted.
func TestNormalizeTagIdempotent(t *testing.T) {
	for r := rune(1); r <= 0x2FFFF; r++ {
		got, err := NormalizeTag(string(r))
		if err != nil {
			continue
		}
		if again, err := NormalizeTag(got); err != nil || again != got {
			t.Fatalf("U+%04X: %q -> %q, %v", r, got, again, err)
		}
	}
}

func TestNormalizeTags(t *testing.T) {
	got, err := NormalizeTags([]string{"火锅", "Hotpot", "HOTPOT", "友好"})
	if err != nil || strings.Join(got, ",") != "火锅,hotpot,友好" {
		t.Fatalf("got %v, %v", got, err)
	}
	if got, err := NormalizeTags(nil); err != nil || len(got) != 0 {
		t.Fatalf("empty: %v, %v", got, err)
	}
	many := make([]string, MaxTags+1)
	for i := range many {
		many[i] = "t" + strconv.Itoa(i)
	}
	if _, err := NormalizeTags(many); err == nil {
		t.Fatal("too many tags accepted")
	}
	dup := append(many[:MaxTags:MaxTags], "T0")
	if got, err := NormalizeTags(dup); err != nil || len(got) != MaxTags {
		t.Fatalf("duplicates must not count against the limit: %v, %v", got, err)
	}
	if _, err := NormalizeTags([]string{"ok", "a b"}); err == nil {
		t.Fatal("bad tag accepted")
	}
}

func TestValidate(t *testing.T) {
	good := Card{Name: "小王", Tags: []string{DefaultTag}}
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	family := Card{Name: "\U0001F468\u200d\U0001F469\u200d\U0001F467", Tags: []string{DefaultTag}}
	if err := family.Validate(); err != nil {
		t.Fatalf("emoji joined by ZWJ: %v", err)
	}
	bad := []Card{
		{Name: "", Tags: []string{DefaultTag}},
		{Name: " 小王", Tags: []string{DefaultTag}},
		{Name: strings.Repeat("名", MaxNameRunes+1), Tags: []string{DefaultTag}},
		{Name: "a\nb", Tags: []string{DefaultTag}},
		{Name: "\xff", Tags: []string{DefaultTag}},
		{Name: "a\u202eb", Tags: []string{DefaultTag}},
		{Name: "小王", Tags: []string{DefaultTag}, About: "a\u200bb"},
		{Name: "小王"},
		{Name: "小王", Tags: []string{"Hotpot"}},           // not normalized
		{Name: "小王", Tags: []string{"hotpot", "hotpot"}}, // duplicate
		{Name: "小王", Tags: []string{"a b"}},
		{Name: "小王", Tags: []string{DefaultTag}, About: strings.Repeat("x", MaxAboutRunes+1)},
	}
	for _, c := range bad {
		if err := c.Validate(); err == nil {
			t.Errorf("Validate(%+v) = nil", c)
		}
	}
}

func TestEventRoundTrip(t *testing.T) {
	c := Card{Name: "小王", Tags: []string{"友好", "火锅"}, About: "周末吃火锅"}
	ev, err := c.Event(pk, now)
	if err != nil {
		t.Fatal(err)
	}
	if class, err := wireevent.Classify30078(ev.Tags); err != nil || class != wireevent.ClassCard {
		t.Fatalf("class = %v, %v", class, err)
	}
	if got := ev.Tags.Find("expiration"); got[1] != strconv.FormatInt(int64(now)+Lifetime, 10) {
		t.Fatalf("expiration = %v", got)
	}
	back, err := Parse(ev, now)
	if err != nil || back.Name != c.Name || back.About != c.About || strings.Join(back.Tags, ",") != "友好,火锅" {
		t.Fatalf("Parse = %+v, %v", back, err)
	}
	if _, err := Parse(ev, now+Lifetime); err == nil {
		t.Fatal("card valid at its expiration second")
	}
	if _, err := Parse(ev, now+Lifetime-1); err != nil {
		t.Fatalf("card one second before expiry: %v", err)
	}
	if _, err := (Card{Name: "x"}).Event(pk, now); err == nil {
		t.Fatal("Event accepted a card without tags")
	}
}

func TestTombstone(t *testing.T) {
	ev := Tombstone(pk, now)
	if _, err := Parse(ev, now); !errors.Is(err, ErrHidden) {
		t.Fatalf("tombstone: %v", err)
	}
	if ev.Tags.Find("t") != nil {
		t.Fatal("tombstone has t tags")
	}
	ev.Tags = append(ev.Tags, nostr.Tag{"t", "友好"})
	if _, err := Parse(ev, now); err == nil || errors.Is(err, ErrHidden) {
		t.Fatalf("tombstone with t tag: %v", err)
	}
	ev = Tombstone(pk, now)
	ev.Content = `{"v":1,"hidden":true,"name":"x"}`
	if _, err := Parse(ev, now); err == nil || errors.Is(err, ErrHidden) {
		t.Fatalf("tombstone with name: %v", err)
	}
}

func TestParseRejects(t *testing.T) {
	good := func() *nostr.Event {
		ev, err := Card{Name: "小王", Tags: []string{"友好", "火锅"}}.Event(pk, now)
		if err != nil {
			t.Fatal(err)
		}
		return ev
	}
	setTag := func(ev *nostr.Event, name string, vals ...string) {
		var out nostr.Tags
		for _, tag := range ev.Tags {
			if tag[0] != name {
				out = append(out, tag)
			}
		}
		for _, v := range vals {
			out = append(out, nostr.Tag{name, v})
		}
		ev.Tags = out
	}
	cases := map[string]func(*nostr.Event){
		"wrong kind": func(ev *nostr.Event) { ev.Kind = 1 },
		"profile class": func(ev *nostr.Event) {
			setTag(ev, "c", wireevent.ProfileCategory)
			setTag(ev, "d", wireevent.ProfileDTag)
			setTag(ev, "v")
		},
		"no expiration": func(ev *nostr.Event) { setTag(ev, "expiration") },
		"two expirations": func(ev *nostr.Event) {
			setTag(ev, "expiration", strconv.FormatInt(int64(now)+10, 10), strconv.FormatInt(int64(now)+20, 10))
		},
		"expiration not a number": func(ev *nostr.Event) { setTag(ev, "expiration", "soon") },
		"expiration zero":         func(ev *nostr.Event) { setTag(ev, "expiration", "0") },
		"lifetime too long": func(ev *nostr.Event) {
			setTag(ev, "expiration", strconv.FormatInt(int64(now)+Lifetime+1, 10))
		},
		"missing t tag":    func(ev *nostr.Event) { setTag(ev, "t", "友好") },
		"extra t tag":      func(ev *nostr.Event) { setTag(ev, "t", "友好", "火锅", "夜宵") },
		"t tags reordered": func(ev *nostr.Event) { setTag(ev, "t", "火锅", "友好") },
		"malformed t tag":  func(ev *nostr.Event) { ev.Tags = append(ev.Tags, nostr.Tag{"t", "a", "b"}) },
		"unknown field":    func(ev *nostr.Event) { ev.Content = `{"v":1,"name":"小王","tags":["友好","火锅"],"x":1}` },
		"version 2":        func(ev *nostr.Event) { ev.Content = `{"v":2,"name":"小王","tags":["友好","火锅"]}` },
		"null name":        func(ev *nostr.Event) { ev.Content = `{"v":1,"name":null,"tags":["友好","火锅"]}` },
		"unnormalized tag": func(ev *nostr.Event) {
			ev.Content = `{"v":1,"name":"小王","tags":["友好","Hotpot"]}`
			setTag(ev, "t", "友好", "Hotpot")
		},
		"not json":            func(ev *nostr.Event) { ev.Content = "hello" },
		"duplicate json keys": func(ev *nostr.Event) { ev.Content = `{"v":1,"name":"a","name":"b","tags":["友好","火锅"]}` },
	}
	for name, mutate := range cases {
		ev := good()
		mutate(ev)
		if _, err := Parse(ev, now); err == nil {
			t.Errorf("%s: Parse accepted", name)
		}
	}
	if _, err := Parse(nil, now); err == nil {
		t.Error("nil event accepted")
	}
}

// A card dated in the future would outrank its author's later tombstone.
func TestParseFutureDated(t *testing.T) {
	c := Card{Name: "小王", Tags: []string{DefaultTag}}
	ev, err := c.Event(pk, now+MaxClockSkew)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Parse(ev, now); err != nil {
		t.Fatalf("within clock skew: %v", err)
	}
	ev, _ = c.Event(pk, now+MaxClockSkew+1)
	if _, err := Parse(ev, now); err == nil {
		t.Fatal("card beyond clock skew accepted")
	}
	ev, _ = c.Event(pk, 4_102_444_800) // year 2100
	if _, err := Parse(ev, now); err == nil {
		t.Fatal("year-2100 card accepted")
	}
}

func TestFilter(t *testing.T) {
	f, err := Filter([]string{"友好", "Hotpot", "hotpot"}, 20)
	if err != nil {
		t.Fatal(err)
	}
	if f.Limit != 20 || len(f.Kinds) != 1 || f.Kinds[0] != wireevent.Kind30078 {
		t.Fatalf("filter = %+v", f)
	}
	if strings.Join(f.Tags["c"], ",") != wireevent.CardCategory || strings.Join(f.Tags["t"], ",") != "友好,hotpot" {
		t.Fatalf("tags = %v", f.Tags)
	}
	if _, err := Filter(nil, 20); err == nil {
		t.Fatal("empty search accepted")
	}
	if _, err := Filter([]string{"a b"}, 20); err == nil {
		t.Fatal("bad tag accepted")
	}
}

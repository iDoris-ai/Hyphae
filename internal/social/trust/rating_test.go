package trust

import (
	"fmt"
	"math"
	"testing"

	"github.com/iDoris-ai/hyphae/internal/social"
)

// fixture tracks matches between identities and builds ratings for them.
type fixture struct {
	matches []Match
}

// match records a match between a and b and returns its accept event IDs.
func (f *fixture) match(a, b int) [2]string {
	acc := [2]string{id(), id()}
	f.matches = append(f.matches, Match{Accepts: acc, Parties: [2]string{pk(a), pk(b)}})
	return acc
}

func rateBody(subject int, m [2]string, score any, extra string) string {
	return fmt.Sprintf(`{"v":1,"type":"rating","subject":%q,"match":[%q,%q],"score":%v%s}`,
		pk(subject), m[0], m[1], score, extra)
}

func rate(rater, subject int, m [2]string, score int, at int64) social.Envelope {
	return envOf(id(), pk(rater), at, rateBody(subject, m, score, ""))
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// open returns a chain in which identities 1..9 are valid.
func open(t *testing.T) *Chain {
	var envs []social.Envelope
	for i := 2; i <= 9; i++ {
		envs = append(envs, link(1, i, int64(i))...)
	}
	return resolve(t, roots(1), envs...)
}

func TestParseRating(t *testing.T) {
	m := [2]string{pk(0xb0), pk(0xa0)}
	r, err := ParseRating(envOf(id(), pk(1), 5, rateBody(2, m, 4, `,"tags":["on-time","kind"]`)))
	if err != nil || r.Score != 4 || r.Subject != pk(2) || r.Match != [2]string{pk(0xa0), pk(0xb0)} || len(r.Tags) != 2 {
		t.Fatalf("got %+v, %v", r, err)
	}
	long := fmt.Sprintf(`,"tags":["%0*d"]`, MaxTagLen+1, 0)
	bad := map[string]string{
		"self":          rateBody(1, m, 4, ""),
		"score 0":       rateBody(2, m, 0, ""),
		"score 6":       rateBody(2, m, 6, ""),
		"score float":   rateBody(2, m, 3.5, ""),
		"same accepts":  rateBody(2, [2]string{m[0], m[0]}, 4, ""),
		"bad accept":    rateBody(2, [2]string{m[0], "x"}, 4, ""),
		"one accept":    fmt.Sprintf(`{"v":1,"type":"rating","subject":%q,"match":[%q],"score":4}`, pk(2), m[0]),
		"three accepts": fmt.Sprintf(`{"v":1,"type":"rating","subject":%q,"match":[%q,%q,%q],"score":4}`, pk(2), m[0], m[1], pk(9)),
		"bad subject":   `{"v":1,"type":"rating","subject":"zz","match":["` + m[0] + `","` + m[1] + `"],"score":4}`,
		"too many tags": rateBody(2, m, 4, `,"tags":["a","b","c","d","e","f","g","h","i"]`),
		"empty tag":     rateBody(2, m, 4, `,"tags":[""]`),
		"long tag":      rateBody(2, m, 4, long),
		"unknown field": rateBody(2, m, 4, `,"comment":"x"`),
		"wrong type":    `{"v":1,"type":"invite","invitee":"` + pk(2) + `"}`,
	}
	for name, body := range bad {
		if _, err := ParseRating(envOf(id(), pk(1), 5, body)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestReputationBasic(t *testing.T) {
	var f fixture
	m := f.match(2, 3)
	got := Reputation(ScoreConfig{}, open(t), f.matches, []social.Envelope{rate(2, 3, m, 5, 10), rate(3, 2, m, 1, 10)}, testNow)
	if s := got[pk(3)]; s.Raters != 1 || !near(s.Value, (3*5+5)/6.0) {
		t.Errorf("subject 3: %+v", s)
	}
	if s := got[pk(2)]; s.Raters != 1 || !near(s.Value, (3*5+1)/6.0) {
		t.Errorf("subject 2: %+v", s)
	}
	if _, ok := got[pk(4)]; ok {
		t.Error("unrated subject present")
	}
	got = Reputation(ScoreConfig{Prior: 1, PriorWeight: 1}, open(t), f.matches, []social.Envelope{rate(2, 3, m, 5, 10)}, testNow)
	if s := got[pk(3)]; !near(s.Value, 3) {
		t.Errorf("custom prior: %+v", s)
	}
}

func TestReputationMatchOrderInsensitive(t *testing.T) {
	m := [2]string{pk(0xa0), pk(0xb0)}
	facts := []Match{{Accepts: [2]string{m[1], m[0]}, Parties: [2]string{pk(3), pk(2)}}}
	if got := Reputation(ScoreConfig{}, open(t), facts, []social.Envelope{rate(2, 3, m, 5, 10)}, testNow); got[pk(3)].Raters != 1 {
		t.Error("rating rejected for reordered match facts")
	}
}

func TestReputationRejectsWrongParties(t *testing.T) {
	var f fixture
	m := f.match(2, 3)
	other := f.match(4, 5)
	envs := []social.Envelope{
		rate(4, 3, m, 1, 10),                             // rater not a party
		rate(2, 4, m, 1, 10),                             // subject not a party
		rate(2, 3, [2]string{pk(0xee), pk(0xef)}, 1, 10), // unknown match
		rate(2, 3, other, 1, 10),                         // match between other people
		rate(2, 3, [2]string{m[0], other[1]}, 1, 10),     // spliced accept IDs
	}
	if got := Reputation(ScoreConfig{}, open(t), f.matches, envs, testNow); len(got) != 0 {
		t.Errorf("invalid ratings counted: %+v", got)
	}
}

func TestReputationRequiresValidRater(t *testing.T) {
	var f fixture
	m := f.match(2, 50) // 50 has no invite
	envs := []social.Envelope{rate(50, 2, m, 1, 10), rate(2, 50, m, 4, 10)}
	got := Reputation(ScoreConfig{}, open(t), f.matches, envs, testNow)
	if _, ok := got[pk(2)]; ok {
		t.Error("rating by an uninvited rater counted")
	}
	if got[pk(50)].Raters != 1 {
		t.Error("valid rater's rating of an uninvited subject dropped")
	}
}

func TestReputationSybilRing(t *testing.T) {
	// Twenty uninvited keys match and five-star each other pairwise.
	var f fixture
	var envs []social.Envelope
	for a := 100; a < 120; a++ {
		for b := a + 1; b < 120; b++ {
			m := f.match(a, b)
			envs = append(envs, rate(a, b, m, 5, 10), rate(b, a, m, 5, 10))
		}
	}
	// One valid identity joins the ring and boosts a sybil.
	m := f.match(2, 100)
	envs = append(envs, rate(2, 100, m, 5, 10))
	got := Reputation(ScoreConfig{}, open(t), f.matches, envs, testNow)
	if len(got) != 1 || got[pk(100)].Raters != 1 {
		t.Errorf("sybil ratings counted: %d subjects, %+v", len(got), got[pk(100)])
	}
}

func TestReputationLatestPerMatch(t *testing.T) {
	var f fixture
	m := f.match(2, 3)
	got := Reputation(ScoreConfig{}, open(t), f.matches, []social.Envelope{rate(2, 3, m, 5, 20), rate(2, 3, m, 1, 10)}, testNow)
	if s := got[pk(3)]; s.Raters != 1 || !near(s.Value, 20/6.0) {
		t.Errorf("latest did not win: %+v", s)
	}
	lo := envOf(pk(0xa1), pk(2), 10, rateBody(3, m, 1, ""))
	hi := envOf(pk(0xb1), pk(2), 10, rateBody(3, m, 5, ""))
	for _, envs := range [][]social.Envelope{{lo, hi}, {hi, lo}} {
		if s := Reputation(ScoreConfig{}, open(t), f.matches, envs, testNow)[pk(3)]; !near(s.Value, 20/6.0) {
			t.Errorf("tie not broken by greater event ID: %+v", s)
		}
	}
}

func TestReputationFlooding(t *testing.T) {
	var f fixture
	var envs []social.Envelope
	// One rater floods one match, then farms fifty more matches with the subject.
	m := f.match(2, 3)
	for i := 0; i < 100; i++ {
		envs = append(envs, rate(2, 3, m, 5, int64(i)))
	}
	for i := 0; i < 50; i++ {
		envs = append(envs, rate(2, 3, f.match(2, 3), 5, 10))
	}
	// An honest rater gives one low score.
	envs = append(envs, rate(4, 3, f.match(3, 4), 1, 10))
	s := Reputation(ScoreConfig{}, open(t), f.matches, envs, testNow)[pk(3)]
	if s.Raters != 2 || !near(s.Value, (15+5+1)/7.0) {
		t.Errorf("flooding not capped: %+v", s)
	}
}

func TestReputationMeanAcrossMatches(t *testing.T) {
	var f fixture
	envs := []social.Envelope{
		rate(2, 3, f.match(2, 3), 5, 10),
		rate(2, 3, f.match(3, 2), 5, 10),
		rate(2, 3, f.match(2, 3), 2, 10),
	}
	if s := Reputation(ScoreConfig{}, open(t), f.matches, envs, testNow)[pk(3)]; s.Raters != 1 || !near(s.Value, (15+4)/6.0) {
		t.Errorf("per-rater mean: %+v", s)
	}
}

func TestFutureDatedRatingCannotPinTheScore(t *testing.T) {
	var f fixture
	m := f.match(2, 3)
	// A rating dated far in the future would otherwise stay "latest" forever.
	envs := []social.Envelope{rate(2, 3, m, 1, 1<<50), rate(2, 3, m, 5, 20)}
	s := Reputation(ScoreConfig{}, open(t), f.matches, envs, 1000)[pk(3)]
	if s.Raters != 1 || !near(s.Value, (15+5)/6.0) {
		t.Fatalf("got %+v, want the rating dated 20 to count", s)
	}
}

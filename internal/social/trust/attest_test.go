package trust

import (
	"fmt"
	"strings"
	"testing"

	"github.com/iDoris-ai/hyphae/internal/social"
)

const (
	aNow = int64(1_000_000)
	aAt  = aNow - 1000
)

// Identities: 1 and 2 are issuers, 5 is the presenter, 6 someone else.
func attestBody(subject int, claim, value string, exp int64) string {
	return fmt.Sprintf(`{"v":1,"type":"attest","subject":%q,"claim":%q,"value":%q,"expires_at":%d}`, pk(subject), claim, value, exp)
}

func attestEnv(eid string, issuer, subject int, claim, value string, at, exp int64) social.Envelope {
	return envOf(eid, pk(issuer), at, attestBody(subject, claim, value, exp))
}

func att(issuer, subject int, claim, value string, at int64) social.Envelope {
	return attestEnv(id(), issuer, subject, claim, value, at, at+86400)
}

func trust(ids ...int) func(string) bool {
	return func(s string) bool {
		for _, i := range ids {
			if s == pk(i) {
				return true
			}
		}
		return false
	}
}

func verify(t *testing.T, envs []social.Envelope, tr func(string) bool) ([]Attestation, map[string]error) {
	t.Helper()
	acc, rej, err := Verify(envs, pk(5), tr, aNow)
	if err != nil {
		t.Fatal(err)
	}
	return acc, rej
}

func TestParseAttest(t *testing.T) {
	ok := func(body string) social.Envelope { return envOf(id(), pk(1), aAt, body) }
	good := attestBody(5, "age_range", "25-30", aAt+100)
	if a, err := ParseAttest(ok(good)); err != nil || a.Issuer != pk(1) || a.Subject != pk(5) || a.Claim != "age_range" || a.Value != "25-30" || a.ExpiresAt != aAt+100 {
		t.Fatalf("good attest: %+v %v", a, err)
	}
	long := strings.Repeat("好", 128)
	if _, err := ParseAttest(ok(attestBody(5, "c", long, aAt+1))); err != nil {
		t.Errorf("128 runes (384 bytes) must pass: %v", err)
	}
	if _, err := ParseAttest(ok(attestBody(5, "c", "x", aAt+MaxAttestLife))); err != nil {
		t.Errorf("exactly 366 days must pass: %v", err)
	}
	bad := map[string]string{
		"self":           attestBody(1, "c", "x", aAt+1),
		"subject upper":  strings.Replace(attestBody(0xab, "c", "x", aAt+1), pk(0xab), strings.ToUpper(pk(0xab)), 1),
		"subject short":  `{"v":1,"type":"attest","subject":"ab","claim":"c","value":"x","expires_at":2000000}`,
		"claim empty":    attestBody(5, "", "x", aAt+1),
		"claim upper":    attestBody(5, "Age", "x", aAt+1),
		"claim long":     attestBody(5, strings.Repeat("a", 33), "x", aAt+1),
		"value empty":    attestBody(5, "c", "", aAt+1),
		"value blank":    attestBody(5, "c", " \t ", aAt+1),
		"value 129":      attestBody(5, "c", strings.Repeat("好", 129), aAt+1),
		"exp == created": attestBody(5, "c", "x", aAt),
		"exp < created":  attestBody(5, "c", "x", aAt-1),
		"exp > 366d":     attestBody(5, "c", "x", aAt+MaxAttestLife+1),
		"exp float":      strings.Replace(good, fmt.Sprint(aAt+100), "1.5e6", 1),
		"null value":     strings.Replace(good, `"25-30"`, "null", 1),
		"upper key":      strings.Replace(good, `"claim"`, `"Claim"`, 1),
		"null body":      "null",
		"unknown field":  strings.Replace(good, `"v":1`, `"v":1,"extra":"x"`, 1),
		"wrong type":     strings.Replace(good, `"attest"`, `"rating"`, 1),
		"wrong version":  strings.Replace(good, `"v":1`, `"v":2`, 1),
	}
	for name, body := range bad {
		if _, err := ParseAttest(ok(body)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestVerifyAccepts(t *testing.T) {
	e := att(1, 5, "age_range", "25-30", aAt)
	acc, rej := verify(t, []social.Envelope{e}, trust(1))
	if len(acc) != 1 || acc[0].ID != e.ID || acc[0].Value != "25-30" || len(rej) != 0 {
		t.Fatalf("acc=%+v rej=%v", acc, rej)
	}
	// Boundary: now == expires_at still passes; created_at exactly at the skew limit passes.
	b1 := attestEnv(id(), 1, 5, "a", "x", aAt, aNow)
	b2 := attestEnv(id(), 1, 5, "b", "x", aNow+social.MaxClockSkew, aNow+social.MaxClockSkew+10)
	if acc, rej := verify(t, []social.Envelope{b1, b2}, trust(1)); len(acc) != 2 {
		t.Errorf("boundaries: acc=%+v rej=%v", acc, rej)
	}
}

func TestVerifyRejects(t *testing.T) {
	cases := map[string]struct {
		env social.Envelope
		tr  func(string) bool
	}{
		"about someone else": {att(1, 6, "c", "x", aAt), trust(1)},
		"untrusted issuer":   {att(2, 5, "c", "x", aAt), trust(1)},
		"nil predicate":      {att(1, 5, "c", "x", aAt), nil},
		"self-attestation":   {att(5, 5, "c", "x", aAt), trust(5)},
		"expired":            {attestEnv(id(), 1, 5, "c", "x", aAt, aNow-1), trust(1)},
		"future-dated":       {attestEnv(id(), 1, 5, "c", "x", aNow+social.MaxClockSkew+1, aNow+10000), trust(1)},
		"non-attest":         {envOf(id(), pk(1), aAt, fmt.Sprintf(`{"v":1,"type":"invite","invitee":%q}`, pk(5))), trust(1)},
		"null body":          {envOf(id(), pk(1), aAt, "null"), trust(1)},
		"uppercase key":      {envOf(id(), pk(1), aAt, strings.Replace(attestBody(5, "c", "x", aAt+9), `"value"`, `"Value"`, 1)), trust(1)},
	}
	for name, c := range cases {
		acc, rej := verify(t, []social.Envelope{c.env}, c.tr)
		if len(acc) != 0 || rej[c.env.ID] == nil {
			t.Errorf("%s: acc=%+v rej=%v", name, acc, rej)
		}
	}
}

func TestVerifyMixedBatch(t *testing.T) {
	good := att(1, 5, "age_range", "25-30", aAt)
	junk := envOf(id(), pk(1), aAt, `{"v":1,"type":"rating"}`)
	other := att(1, 6, "age_range", "99", aAt)
	acc, rej := verify(t, []social.Envelope{junk, good, other}, trust(1))
	if len(acc) != 1 || acc[0].ID != good.ID || rej[junk.ID] == nil || rej[other.ID] == nil {
		t.Fatalf("acc=%+v rej=%v", acc, rej)
	}
}

func TestVerifyDuplicateEventID(t *testing.T) {
	e := att(1, 5, "c", "x", aAt)
	acc, rej := verify(t, []social.Envelope{e, e, e}, trust(1))
	if len(acc) != 1 || len(rej) != 0 {
		t.Fatalf("acc=%+v rej=%v", acc, rej)
	}
}

func TestVerifySameIssuerSupersession(t *testing.T) {
	older := att(1, 5, "age_range", "25-30", aAt)
	newer := att(1, 5, "age_range", "31-35", aAt+10)
	for _, order := range [][]social.Envelope{{older, newer}, {newer, older}} {
		acc, rej := verify(t, order, trust(1))
		if len(acc) != 1 || acc[0].ID != newer.ID || rej[older.ID] == nil {
			t.Fatalf("acc=%+v rej=%v", acc, rej)
		}
	}
	// created_at tie: the higher event ID wins regardless of order.
	lo := attestEnv(fmt.Sprintf("%064x", 1), 1, 5, "c", "lo", aAt, aNow+100)
	hi := attestEnv(fmt.Sprintf("%064x", 2), 1, 5, "c", "hi", aAt, aNow+100)
	for _, order := range [][]social.Envelope{{lo, hi}, {hi, lo}} {
		acc, rej := verify(t, order, trust(1))
		if len(acc) != 1 || acc[0].ID != hi.ID || rej[lo.ID] == nil {
			t.Fatalf("tie: acc=%+v rej=%v", acc, rej)
		}
	}
	// A rejected newer attestation (expired) must not supersede a valid older one.
	dead := attestEnv(id(), 1, 5, "age_range", "dead", aAt+20, aNow-1)
	if acc, _ := verify(t, []social.Envelope{older, dead}, trust(1)); len(acc) != 1 || acc[0].ID != older.ID {
		t.Errorf("expired newer superseded older: %+v", acc)
	}
	// Different claims from the same issuer do not supersede each other.
	other := att(1, 5, "city", "Paris", aAt)
	if acc, _ := verify(t, []social.Envelope{older, other}, trust(1)); len(acc) != 2 {
		t.Errorf("different claims: %+v", acc)
	}
}

func TestVerifyConflictingIssuersAndOrder(t *testing.T) {
	a := att(2, 5, "age_range", "40-45", aAt+5)
	b := att(1, 5, "age_range", "25-30", aAt)
	c := att(2, 5, "city", "Paris", aAt)
	acc, rej := verify(t, []social.Envelope{a, b, c}, trust(1, 2))
	if len(rej) != 0 || len(acc) != 3 {
		t.Fatalf("acc=%+v rej=%v", acc, rej)
	}
	want := []string{b.ID, a.ID, c.ID} // age_range/issuer1, age_range/issuer2, city/issuer2
	for i, w := range want {
		if acc[i].ID != w {
			t.Fatalf("order[%d]=%s, want %s", i, acc[i].ID, w)
		}
	}
	// Claim sorts before issuer: issuer order alone would put city/issuer1 first.
	city1 := att(1, 5, "city", "Paris", aAt)
	age2 := att(2, 5, "age_range", "40-45", aAt)
	acc, _ = verify(t, []social.Envelope{city1, age2}, trust(1, 2))
	if len(acc) != 2 || acc[0].ID != age2.ID || acc[1].ID != city1.ID {
		t.Fatalf("claim-first order: %+v", acc)
	}
}

func TestVerifyBound(t *testing.T) {
	var envs []social.Envelope
	for i := 0; i < MaxPresented; i++ {
		envs = append(envs, att(1, 5, fmt.Sprintf("c%d", i), "x", aAt))
	}
	if acc, _ := verify(t, envs, trust(1)); len(acc) != MaxPresented {
		t.Fatalf("64 envelopes: accepted %d", len(acc))
	}
	envs = append(envs, att(1, 5, "extra", "x", aAt))
	acc, rej, err := Verify(envs, pk(5), trust(1), aNow)
	if err == nil || acc != nil || rej != nil {
		t.Fatalf("65 envelopes: acc=%v rej=%v err=%v", acc, rej, err)
	}
	// Duplicates count toward the bound.
	e := att(1, 5, "c", "x", aAt)
	dups := make([]social.Envelope, MaxPresented+1)
	for i := range dups {
		dups[i] = e
	}
	if _, _, err := Verify(dups, pk(5), trust(1), aNow); err == nil {
		t.Error("65 duplicates accepted")
	}
}

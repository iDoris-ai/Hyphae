package intent

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/iDoris-ai/hyphae/internal/social"
)

var (
	pkA = strings.Repeat("a", 64) // intent author
	pkB = strings.Repeat("b", 64) // responder
	pkC = strings.Repeat("c", 64) // outsider
)

const (
	t0      = int64(1_700_000_000)
	expires = t0 + 3600
)

var intentID = id(1)

func id(n int) string { return fmt.Sprintf("%064x", n) }

func env(n int, author string, at int64, content string) social.Envelope {
	return social.Envelope{ID: id(n), Author: author, CreatedAt: at, Content: json.RawMessage(content)}
}

// intentBody builds an intent body with one field overridden by kv (raw JSON
// "key":value pairs replacing the defaults of the same key).
func intentBody(over map[string]string) string {
	f := map[string]string{
		"category": `"meal"`, "summary": `"hotpot tonight"`, "window": `{"from":1,"to":2}`,
		"area": `"Chaoyang"`, "party": `{"min":2,"max":4}`, "expires_at": fmt.Sprint(expires),
	}
	for k, v := range over {
		if v == "" {
			delete(f, k)
		} else {
			f[k] = v
		}
	}
	parts := []string{`"v":1`, `"type":"intent"`}
	for k, v := range f {
		parts = append(parts, fmt.Sprintf("%q:%s", k, v))
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func q(s string) string { b, _ := json.Marshal(s); return string(b) }

func fieldsJSON(n, keyLen, valLen int) string {
	m := map[string]string{}
	for i := 0; i < n; i++ {
		k := fmt.Sprintf("%d", i) + strings.Repeat("k", keyLen-len(fmt.Sprint(i)))
		m[k] = strings.Repeat("值", valLen)
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func ref(typ, extra string) string {
	return fmt.Sprintf(`{"v":1,"type":%q,"intent":%q%s}`, typ, intentID, extra)
}

func TestParseIntent(t *testing.T) {
	good := map[string]map[string]string{
		"defaults":            nil,
		"category charset":    {"category": `"night-out-2"`},
		"category 32":         {"category": q(strings.Repeat("a", 32))},
		"summary 280 runes":   {"summary": q(strings.Repeat("火", 280))},
		"area 64 runes":       {"area": q(strings.Repeat("区", 64))},
		"party min=max=1":     {"party": `{"min":1,"max":1}`},
		"party max 20":        {"party": `{"min":1,"max":20}`},
		"expires 30 days":     {"expires_at": fmt.Sprint(t0 + MaxLifetime)},
		"expires next second": {"expires_at": fmt.Sprint(t0 + 1)},
	}
	for name, over := range good {
		m, err := Parse(env(1, pkA, t0, intentBody(over)))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if b, ok := m.Body.(*Intent); !ok || m.Type != "intent" || m.Ref != "" || b.Category == "" {
			t.Errorf("%s: got %+v", name, m)
		}
	}
	bad := map[string]map[string]string{
		"category empty":       {"category": `""`},
		"category 33":          {"category": q(strings.Repeat("a", 33))},
		"category uppercase":   {"category": `"Meal"`},
		"category space":       {"category": `"a b"`},
		"category underscore":  {"category": `"a_b"`},
		"category non-ascii":   {"category": `"饭"`},
		"category missing":     {"category": ""},
		"summary empty":        {"summary": `""`},
		"summary blank":        {"summary": `"   "`},
		"summary 281 runes":    {"summary": q(strings.Repeat("火", 281))},
		"window from=to":       {"window": `{"from":5,"to":5}`},
		"window from>to":       {"window": `{"from":6,"to":5}`},
		"window from 0":        {"window": `{"from":0,"to":5}`},
		"window missing":       {"window": ""},
		"window extra field":   {"window": `{"from":1,"to":2,"tz":"x"}`},
		"area empty":           {"area": `""`},
		"area 65 runes":        {"area": q(strings.Repeat("区", 65))},
		"party min 0":          {"party": `{"min":0,"max":2}`},
		"party min>max":        {"party": `{"min":3,"max":2}`},
		"party max 21":         {"party": `{"min":1,"max":21}`},
		"party missing":        {"party": ""},
		"expires at event":     {"expires_at": fmt.Sprint(t0)},
		"expires before event": {"expires_at": fmt.Sprint(t0 - 1)},
		"expires 30 days + 1":  {"expires_at": fmt.Sprint(t0 + MaxLifetime + 1)},
		"expires missing":      {"expires_at": ""},
		"unknown field":        {"geo": `"39.9,116.4"`},
		"string party":         {"party": `"2-4"`},
	}
	for name, over := range bad {
		if _, err := Parse(env(1, pkA, t0, intentBody(over))); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestParseReplies(t *testing.T) {
	good := map[string]string{
		"respond": ref("respond", `,"fields":{"age":"30"}`),
		// 16 maximal CJK fields would exceed the 16 KiB body cap, so the
		// count and the per-value limit are tested separately.
		"respond 16 fields":     ref("respond", `,"fields":`+fieldsJSON(16, 32, 8)),
		"respond 512-rune val":  ref("respond", `,"fields":`+fieldsJSON(1, 32, 512)),
		"respond empty value":   ref("respond", `,"fields":{"k":""}`),
		"disclose round 1":      ref("disclose", `,"round":1,"fields":{"k":"v"}`),
		"disclose round 4":      ref("disclose", `,"round":4,"fields":{"k":"v"}`),
		"accept":                ref("accept", ""),
		"decline":               ref("decline", ""),
		"decline reason 280":    ref("decline", `,"reason":`+q(strings.Repeat("不", 280))),
		"withdraw":              ref("withdraw", ""),
		"respond max key runes": ref("respond", `,"fields":{`+q(strings.Repeat("k", 32))+`:"v"}`),
	}
	for name, c := range good {
		m, err := Parse(env(2, pkB, t0, c))
		if err != nil {
			t.Errorf("%s: %v", name, err)
		} else if m.Ref != intentID {
			t.Errorf("%s: ref %q", name, m.Ref)
		}
	}
	bad := map[string]string{
		"respond no fields":       ref("respond", ""),
		"respond empty fields":    ref("respond", `,"fields":{}`),
		"respond null fields":     ref("respond", `,"fields":null`),
		"respond 17 fields":       ref("respond", `,"fields":`+fieldsJSON(17, 2, 1)),
		"respond key 33 runes":    ref("respond", `,"fields":{`+q(strings.Repeat("k", 33))+`:"v"}`),
		"respond empty key":       ref("respond", `,"fields":{"":"v"}`),
		"respond value 513 runes": ref("respond", `,"fields":{"k":`+q(strings.Repeat("值", 513))+`}`),
		"respond non-string val":  ref("respond", `,"fields":{"k":1}`),
		"respond with round":      ref("respond", `,"round":0,"fields":{"k":"v"}`),
		"respond dup field key":   ref("respond", `,"fields":{"k":"v","k":"w"}`),
		"disclose round 0":        ref("disclose", `,"round":0,"fields":{"k":"v"}`),
		"disclose round 5":        ref("disclose", `,"round":5,"fields":{"k":"v"}`),
		"disclose no round":       ref("disclose", `,"fields":{"k":"v"}`),
		"disclose no fields":      ref("disclose", `,"round":1`),
		"decline reason 281":      ref("decline", `,"reason":`+q(strings.Repeat("不", 281))),
		"accept extra field":      ref("accept", `,"fields":{"k":"v"}`),
		"withdraw extra field":    ref("withdraw", `,"reason":"x"`),
		"accept ref uppercase":    `{"v":1,"type":"accept","intent":"` + strings.Repeat("A", 64) + `"}`,
		"accept ref short":        `{"v":1,"type":"accept","intent":"abc"}`,
		"withdraw ref missing":    `{"v":1,"type":"withdraw"}`,
		"unknown type":            `{"v":1,"type":"poke","intent":"` + intentID + `"}`,
		"version 2":               `{"v":2,"type":"accept","intent":"` + intentID + `"}`,
	}
	for name, c := range bad {
		if _, err := Parse(env(2, pkB, t0, c)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

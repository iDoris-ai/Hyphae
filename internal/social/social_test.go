package social

import (
	"encoding/json"
	"strings"
	"testing"
)

var (
	hexA = strings.Repeat("a", 64)
	hexB = strings.Repeat("b", 64)
)

func env(content string) Envelope {
	return Envelope{ID: hexA, Author: hexB, CreatedAt: 1, Content: json.RawMessage(content)}
}

func TestValidHex32(t *testing.T) {
	for s, want := range map[string]bool{
		hexA:                     true,
		strings.Repeat("A", 64):  false,
		strings.Repeat("a", 63):  false,
		strings.Repeat("g", 64):  false,
		"":                       false,
		strings.Repeat("0", 64):  true,
		strings.Repeat("a", 65):  false,
		strings.Repeat("a", 128): false,
	} {
		if got := ValidHex32(s); got != want {
			t.Errorf("ValidHex32(%q) = %v, want %v", s, got, want)
		}
	}
}

func TestParseHeader(t *testing.T) {
	if _, err := ParseHeader(env(`{"v":1,"type":"a","x":"\ud83d\ude00 ok"}`)); err != nil {
		t.Fatalf("paired surrogate escape rejected: %v", err)
	}
	h, err := ParseHeader(env(`{"v":1,"type":"intent","x":[1,{"y":2}]}`))
	if err != nil || h.Type != "intent" || h.V != 1 {
		t.Fatalf("got %+v, %v", h, err)
	}
	bad := map[string]Envelope{
		"version":     env(`{"v":2,"type":"intent"}`),
		"no type":     env(`{"v":1}`),
		"dup key":     env(`{"v":1,"type":"a","type":"b"}`),
		"nested dup":  env(`{"v":1,"type":"a","x":{"k":1,"k":2}}`),
		"case dup":    env(`{"v":1,"type":"a","Type":"b"}`),
		"fold key":    env(`{"v":1,"type":"a","x":{"\u212a":2}}`),
		"empty key":   env(`{"v":1,"type":"a","":1}`),
		"null value":  env(`{"v":1,"type":"a","x":null}`),
		"lone surr":   env(`{"v":1,"type":"a","x":"\ud800"}`),
		"bad utf8":    env("{\"v\":1,\"type\":\"a\",\"x\":\"\xff\"}"),
		"u+fffd":      env(`{"v":1,"type":"a","x":"\ufffd"}`),
		"nested null": env(`{"v":1,"type":"a","x":{"k":[null]}}`),
		"long key":    env(`{"v":1,"type":"a","` + strings.Repeat("k", 33) + `":1}`),
		"dash key":    env(`{"v":1,"type":"a","a-b":1}`),
		"array":       env(`[1]`),
		"trailing":    env(`{"v":1,"type":"a"} {}`),
		"empty":       env(``),
		"truncated":   env(`{"v":1,"type":"a"`),
		"too big":     env(`{"v":1,"type":"a","p":"` + strings.Repeat("x", MaxContentBytes) + `"}`),
		"too deep":    env(`{"v":1,"type":"a","d":` + strings.Repeat("[", MaxDepth) + strings.Repeat("]", MaxDepth) + `}`),
		"bad id":      {ID: "zz", Author: hexB, CreatedAt: 1, Content: json.RawMessage(`{"v":1,"type":"a"}`)},
		"bad author":  {ID: hexA, Author: "zz", CreatedAt: 1, Content: json.RawMessage(`{"v":1,"type":"a"}`)},
		"no time":     {ID: hexA, Author: hexB, Content: json.RawMessage(`{"v":1,"type":"a"}`)},
		"scalar body": env(`1`),
	}
	for name, e := range bad {
		if _, err := ParseHeader(e); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestParseHeaderAcceptsDepthAtLimit(t *testing.T) {
	// The object itself is level 1, so MaxDepth-1 nested arrays are allowed.
	n := MaxDepth - 1
	body := `{"v":1,"type":"a","d":` + strings.Repeat("[", n) + strings.Repeat("]", n) + `}`
	if _, err := ParseHeader(env(body)); err != nil {
		t.Fatalf("depth at limit rejected: %v", err)
	}
}

func TestStrictUnmarshal(t *testing.T) {
	type msg struct {
		Header
		Name string `json:"name"`
	}
	var m msg
	if err := StrictUnmarshal(json.RawMessage(`{"v":1,"type":"t","name":"n"}`), &m); err != nil || m.Name != "n" {
		t.Fatalf("got %+v, %v", m, err)
	}
	for name, body := range map[string]string{
		"unknown field": `{"v":1,"type":"t","name":"n","extra":1}`,
		"dup key":       `{"v":1,"type":"t","name":"n","name":"m"}`,
		"case dup key":  `{"v":1,"type":"t","name":"n","Name":"m"}`,
		"case only key": `{"v":1,"type":"t","NAME":"n"}`,
		"trailing":      `{"v":1,"type":"t","name":"n"}x`,
		"wrong type":    `{"v":1,"type":"t","name":5}`,
	} {
		var m msg
		if err := StrictUnmarshal(json.RawMessage(body), &m); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

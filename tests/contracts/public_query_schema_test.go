package contracts

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

const (
	publicQueryProfile = "hyphae-public-query-fixtures/1"
	publicQueryMaxBody = 32768
)

type publicQueryRecipe struct {
	Prefix string `json:"prefix"`
	Fill   string `json:"fill"`
	Repeat int    `json:"repeat"`
	Suffix string `json:"suffix"`
}

type publicQueryCase struct {
	ID           string             `json:"id"`
	Body         *string            `json:"body"`
	Recipe       *publicQueryRecipe `json:"recipe"`
	ExpectedByte *int               `json:"expected_bytes"`
	Context      *string            `json:"query_context"`
	Expected     string             `json:"expected"`
}

type publicQueryFixtures struct {
	Profile string            `json:"profile"`
	Cases   []publicQueryCase `json:"cases"`
}

type publicQueryFormat struct {
	name string
	max  int
}

func (f publicQueryFormat) Validate(v any) error {
	s, ok := v.(string)
	if !ok || !utf8.ValidString(s) || len(s) == 0 || len(s) > f.max {
		return fmt.Errorf("must be valid UTF-8 between 1 and %d bytes", f.max)
	}
	return nil
}

type publicQueryNoNetworkLoader struct{}

func (publicQueryNoNetworkLoader) Load(url string) (any, error) {
	return nil, fmt.Errorf("external schema loading is disabled: %s", url)
}

func publicQuerySchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	for _, format := range []publicQueryFormat{{"hyphae-utf8-1-128", 128}, {"hyphae-utf8-1-1024", 1024}, {"hyphae-utf8-1-4096", 4096}, {"hyphae-utf8-1-256", 256}} {
		f := format
		compiler.RegisterFormat(&jsonschema.Format{Name: f.name, Validate: f.Validate})
	}
	compiler.UseLoader(publicQueryNoNetworkLoader{})
	data, err := os.ReadFile("testdata/public-query.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc any
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&doc); err != nil {
		t.Fatal(err)
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		t.Fatal("schema file must contain exactly one JSON value")
	}
	const id = "https://hyphae.invalid/schemas/public-query-v1.schema.json"
	if err := compiler.AddResource(id, doc); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile(id)
	if err != nil {
		t.Fatal(err)
	}
	return schema
}

func publicQueryDecode(raw []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("trailing JSON content")
	}
	return v, nil
}

func publicQueryObject(v any) map[string]any { m, _ := v.(map[string]any); return m }
func publicQueryInt(v any) (int64, bool) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	i, err := n.Int64()
	return i, err == nil
}
func publicQueryLife(issued, expires any) bool {
	i, ok1 := publicQueryInt(issued)
	e, ok2 := publicQueryInt(expires)
	return ok1 && ok2 && i >= 0 && e > i && e-i <= 86400
}
func publicQueryDuplicateCapabilities(v any) bool {
	items, ok := v.([]any)
	if !ok {
		return false
	}
	seen := map[string]bool{}
	for _, item := range items {
		m := publicQueryObject(item)
		id, _ := m["id"].(string)
		version, _ := m["version"].(string)
		key := id + "\x00" + version
		if seen[key] {
			return true
		}
		seen[key] = true
	}
	return false
}

func publicQuerySemantic(root map[string]any, queryContext *string, schema *jsonschema.Schema) bool {
	typ, _ := root["type"].(string)
	issued := root["issued_at"]
	switch typ {
	case "declaration":
		if !publicQueryLife(issued, root["expires_at"]) {
			return false
		}
		payload := publicQueryObject(root["payload"])
		if payload["action"] == "upsert" {
			profile := publicQueryObject(payload["profile"])
			if profile["mode"] == "structured" && publicQueryDuplicateCapabilities(profile["capabilities"]) {
				return false
			}
		}
	case "notification", "query":
		if !publicQueryLife(issued, root["expires_at"]) {
			return false
		}
	case "response":
		payload := publicQueryObject(root["payload"])
		if queryContext == nil {
			return false
		}
		ctxValue, err := publicQueryDecode([]byte(*queryContext))
		if err != nil {
			return false
		}
		ctx := publicQueryObject(ctxValue)
		if err := schema.Validate(ctxValue); err != nil || !publicQuerySemantic(ctx, nil, schema) {
			return false
		}
		if ctx["type"] != "query" {
			return false
		}
		ctxPayload := publicQueryObject(ctx["payload"])
		if root["request_id"] != ctx["request_id"] {
			return false
		}
		if payload["outcome"] != "ok" {
			return true
		}
		if !publicQueryLife(payload["declaration_issued_at"], payload["declaration_expires_at"]) {
			return false
		}
		ctxScope, _ := ctxPayload["scope"].(string)
		if payload["scope"] != ctxScope {
			return false
		}
		if ctxScope == "capability" {
			want := ctxPayload
			got := publicQueryObject(payload["data"])
			if got["id"] != want["id"] || got["version"] != want["version"] {
				return false
			}
		}
		if ctxScope == "capabilities" {
			data := publicQueryObject(payload["data"])
			if publicQueryDuplicateCapabilities(data["capabilities"]) {
				return false
			}
		}
		if ctxScope == "profile" {
			data := publicQueryObject(payload["data"])
			if data["mode"] == "structured" && publicQueryDuplicateCapabilities(data["capabilities"]) {
				return false
			}
		}
	default:
		return false
	}
	return true
}

func publicQueryExpand(c publicQueryCase) ([]byte, error) {
	if (c.Body == nil) == (c.Recipe == nil) {
		return nil, fmt.Errorf("exactly one body or recipe required")
	}
	if c.Body != nil {
		return []byte(*c.Body), nil
	}
	if c.Recipe.Repeat < 0 || len(c.Recipe.Fill) == 0 {
		return nil, fmt.Errorf("invalid recipe")
	}
	return []byte(c.Recipe.Prefix + strings.Repeat(c.Recipe.Fill, c.Recipe.Repeat) + c.Recipe.Suffix), nil
}

func TestPublicQuerySharedSchemaFixtures(t *testing.T) {
	schema := publicQuerySchema(t)
	data, err := os.ReadFile("testdata/public-query-fixtures.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures publicQueryFixtures
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&fixtures); err != nil {
		t.Fatal(err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		t.Fatal("fixture file must contain exactly one JSON value")
	}
	if fixtures.Profile != publicQueryProfile || len(fixtures.Cases) < 50 || len(fixtures.Cases) > 70 {
		t.Fatalf("unexpected fixture profile/count: %q/%d", fixtures.Profile, len(fixtures.Cases))
	}
	ids := map[string]bool{}
	coverage := map[string]map[string]bool{}
	for _, tc := range fixtures.Cases {
		if tc.ID == "" || ids[tc.ID] {
			t.Fatalf("empty or duplicate fixture id %q", tc.ID)
		}
		ids[tc.ID] = true
		if tc.Body != nil && tc.Recipe != nil {
			t.Fatalf("%s: body and recipe are mutually exclusive", tc.ID)
		}
		if tc.Expected != "accepted" && tc.Expected != "body_limit" && tc.Expected != "schema" && tc.Expected != "payload_semantic" {
			t.Fatalf("%s: unknown expected stage %q", tc.ID, tc.Expected)
		}
		raw, err := publicQueryExpand(tc)
		if err != nil {
			t.Fatalf("%s: %v", tc.ID, err)
		}
		if tc.ExpectedByte != nil && len(raw) != *tc.ExpectedByte {
			t.Fatalf("%s: expanded bytes %d, expected %d", tc.ID, len(raw), *tc.ExpectedByte)
		}
		got := "accepted"
		bodyValue, bodyErr := publicQueryDecode(raw)
		if bodyErr == nil {
			if root := publicQueryObject(bodyValue); root != nil {
				typ, _ := root["type"].(string)
				if coverage[typ] == nil {
					coverage[typ] = map[string]bool{}
				}
				coverage[typ][tc.Expected] = true
			}
		}
		if len(raw) > publicQueryMaxBody {
			got = "body_limit"
		} else {
			value, err := publicQueryDecode(raw)
			if err != nil {
				got = "schema"
			} else if err := schema.Validate(value); err != nil {
				got = "schema"
			} else if !publicQuerySemantic(publicQueryObject(value), tc.Context, schema) {
				got = "payload_semantic"
			}
		}
		if got != tc.Expected {
			t.Errorf("%s: got %s, want %s", tc.ID, got, tc.Expected)
		}
	}
	for _, typ := range []string{"declaration", "notification", "query", "response"} {
		if !coverage[typ]["accepted"] || len(coverage[typ]) < 2 {
			t.Errorf("fixture coverage for %s needs an accepted and rejected case", typ)
		}
	}
}

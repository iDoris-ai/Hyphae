package contracts

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"fiatjaf.com/nostr"
)

const (
	eventTransportFixtureProfile = "hyphae-event-transport-fixtures/1"
	eventTransportKind           = 8787
	eventTransportMaxJSON        = 65536
	eventTransportMaxPublic      = 32768
	eventTransportMaxPrivate     = 45056
	eventTransportMaxInteger     = uint64(9007199254740991)
)

type eventTransportRecipe struct {
	Prefix string `json:"prefix"`
	Fill   string `json:"fill"`
	Repeat int    `json:"repeat"`
	Suffix string `json:"suffix"`
}

type eventTransportCase struct {
	ID              string                `json:"id"`
	EventJSON       *string               `json:"event_json"`
	EventJSONBase64 *string               `json:"event_json_base64"`
	EventJSONRecipe *eventTransportRecipe `json:"event_json_recipe"`
	LocalPubKey     string                `json:"local_pubkey"`
	ExpectedBytes   *int                  `json:"expected_bytes"`
	ExpectedStage   string                `json:"expected_stage"`
}

type eventTransportFixtures struct {
	Profile string               `json:"profile"`
	Cases   []eventTransportCase `json:"cases"`
}

type eventTransportParsed struct {
	id        string
	pubkey    string
	createdAt uint64
	kind      uint64
	tags      [][]string
	content   string
	sig       string
}

func eventTransportScanJSONValue(dec *json.Decoder) (any, error) {
	token, err := dec.Token()
	if err != nil {
		return nil, err
	}
	delim, isDelim := token.(json.Delim)
	if !isDelim {
		return token, nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for dec.More() {
			keyToken, err := dec.Token()
			if err != nil {
				return nil, err
			}
			key, ok := keyToken.(string)
			if !ok || seen[key] {
				return nil, fmt.Errorf("duplicate or invalid JSON object key")
			}
			seen[key] = true
			if _, err := eventTransportScanJSONValue(dec); err != nil {
				return nil, err
			}
		}
		closeToken, err := dec.Token()
		if err != nil || closeToken != json.Delim('}') {
			return nil, fmt.Errorf("invalid JSON object")
		}
	case '[':
		for dec.More() {
			if _, err := eventTransportScanJSONValue(dec); err != nil {
				return nil, err
			}
		}
		closeToken, err := dec.Token()
		if err != nil || closeToken != json.Delim(']') {
			return nil, fmt.Errorf("invalid JSON array")
		}
	default:
		return nil, fmt.Errorf("invalid JSON delimiter")
	}
	return token, nil
}

func eventTransportHasOneJSONValue(raw []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	root, err := eventTransportScanJSONValue(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("trailing JSON content")
	}
	return root, nil
}

func eventTransportString(raw json.RawMessage) (string, bool) {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "", false
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", false
	}
	return value, true
}

func eventTransportUnsignedInteger(raw json.RawMessage) (uint64, bool) {
	value := string(bytes.TrimSpace(raw))
	if value == "" || len(value) > 16 || (len(value) > 1 && value[0] == '0') {
		return 0, false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return 0, false
		}
	}
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil || parsed > eventTransportMaxInteger {
		return 0, false
	}
	return parsed, true
}

func eventTransportLowerHex(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for _, char := range value {
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f')) {
			return false
		}
	}
	return true
}

func eventTransportParse(raw []byte) (eventTransportParsed, string) {
	var event eventTransportParsed
	if len(raw) > eventTransportMaxJSON {
		return event, "raw_limit"
	}
	if !utf8.Valid(raw) {
		return event, "json_invalid"
	}
	root, err := eventTransportHasOneJSONValue(raw)
	if err != nil {
		return event, "json_invalid"
	}
	if _, ok := root.(json.Delim); !ok || root.(json.Delim) != '{' {
		return event, "event_invalid"
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		return event, "event_invalid"
	}
	allowed := map[string]bool{"id": true, "pubkey": true, "created_at": true, "kind": true, "tags": true, "content": true, "sig": true}
	if len(object) != len(allowed) {
		return event, "event_invalid"
	}
	for key := range object {
		if !allowed[key] {
			return event, "event_invalid"
		}
	}
	for key := range allowed {
		value, exists := object[key]
		if !exists || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return event, "event_invalid"
		}
	}
	var ok bool
	if event.id, ok = eventTransportString(object["id"]); !ok || !eventTransportLowerHex(event.id, 64) {
		return event, "event_invalid"
	}
	if event.pubkey, ok = eventTransportString(object["pubkey"]); !ok || !eventTransportLowerHex(event.pubkey, 64) {
		return event, "event_invalid"
	}
	if event.sig, ok = eventTransportString(object["sig"]); !ok || !eventTransportLowerHex(event.sig, 128) {
		return event, "event_invalid"
	}
	if event.createdAt, ok = eventTransportUnsignedInteger(object["created_at"]); !ok {
		return event, "event_invalid"
	}
	if event.kind, ok = eventTransportUnsignedInteger(object["kind"]); !ok {
		return event, "event_invalid"
	}
	if err := json.Unmarshal(object["tags"], &event.tags); err != nil || event.tags == nil || len(event.tags) > 8 {
		return event, "event_invalid"
	}
	for _, tag := range event.tags {
		if len(tag) < 2 || len(tag) > 3 {
			return event, "event_invalid"
		}
		for _, value := range tag {
			if !utf8.ValidString(value) || len(value) > 256 {
				return event, "event_invalid"
			}
		}
	}
	if event.content, ok = eventTransportString(object["content"]); !ok {
		return event, "event_invalid"
	}
	return event, ""
}

func eventTransportRoute(event eventTransportParsed, localPubKey string) string {
	if event.kind != eventTransportKind {
		return "legacy_or_unsupported"
	}
	route := map[string]string{}
	for _, tag := range event.tags {
		if len(tag) != 2 {
			return "route_invalid"
		}
		key := tag[0]
		switch key {
		case "c", "b", "x", "p", "e":
		default:
			return "route_invalid"
		}
		if _, exists := route[key]; exists {
			return "route_invalid"
		}
		route[key] = tag[1]
	}
	for _, key := range []string{"c", "b", "x"} {
		if route[key] == "" {
			return "route_invalid"
		}
	}
	if route["c"] != "hyphae-behavior/1" {
		return "legacy_or_unsupported"
	}
	_, behaviorOK := map[string]bool{"register": true, "publish": true, "inquire": true}[route["b"]]
	encodingOK := route["x"] == "json" || route["x"] == "nip44"
	if !behaviorOK || !encodingOK {
		return "legacy_or_unsupported"
	}
	p, hasP := route["p"]
	e, hasE := route["e"]
	if route["b"] == "register" || route["b"] == "publish" {
		if route["x"] != "json" || hasP || hasE {
			return "route_invalid"
		}
	} else {
		if route["x"] != "nip44" || !hasP {
			return "route_invalid"
		}
		if !eventTransportLowerHex(p, 64) || p != localPubKey {
			return "route_invalid"
		}
		if hasE && !eventTransportLowerHex(e, 64) {
			return "route_invalid"
		}
	}
	return ""
}

func eventTransportContent(event eventTransportParsed, routeEncoding string) string {
	if routeEncoding == "json" {
		if len(event.content) > eventTransportMaxPublic {
			return "content_limit"
		}
		if !utf8.ValidString(event.content) {
			return "content_invalid"
		}
		return ""
	}
	if len(event.content) > eventTransportMaxPrivate {
		return "content_limit"
	}
	for i := 0; i < len(event.content); i++ {
		if event.content[i] > 0x7f {
			return "content_invalid"
		}
	}
	decoded, err := base64.StdEncoding.Strict().DecodeString(event.content)
	if err != nil || base64.StdEncoding.EncodeToString(decoded) != event.content || len(decoded) < 99 || decoded[0] != 2 {
		return "content_invalid"
	}
	return ""
}

func eventTransportVerify(event eventTransportParsed) bool {
	idBytes, err := hex.DecodeString(event.id)
	if err != nil || len(idBytes) != 32 {
		return false
	}
	pubBytes, err := hex.DecodeString(event.pubkey)
	if err != nil || len(pubBytes) != 32 {
		return false
	}
	sigBytes, err := hex.DecodeString(event.sig)
	if err != nil || len(sigBytes) != 64 {
		return false
	}
	var sdkEvent nostr.Event
	copy(sdkEvent.ID[:], idBytes)
	copy(sdkEvent.PubKey[:], pubBytes)
	copy(sdkEvent.Sig[:], sigBytes)
	sdkEvent.CreatedAt = nostr.Timestamp(event.createdAt)
	sdkEvent.Kind = nostr.Kind(event.kind)
	sdkEvent.Content = event.content
	for _, tag := range event.tags {
		sdkEvent.Tags = append(sdkEvent.Tags, nostr.Tag(tag))
	}
	return sdkEvent.CheckID() && sdkEvent.VerifySignature()
}

func eventTransportValidate(raw []byte, localPubKey string) string {
	event, stage := eventTransportParse(raw)
	if stage != "" {
		return stage
	}
	if event.kind != eventTransportKind {
		return "legacy_or_unsupported"
	}
	stage = eventTransportRoute(event, localPubKey)
	if stage != "" {
		return stage
	}
	var encoding string
	for _, tag := range event.tags {
		if tag[0] == "x" {
			encoding = tag[1]
		}
	}
	if stage = eventTransportContent(event, encoding); stage != "" {
		return stage
	}
	if !eventTransportVerify(event) {
		return "signature_invalid"
	}
	return "outer_valid"
}

func eventTransportExpand(c eventTransportCase) ([]byte, error) {
	sources := 0
	if c.EventJSON != nil {
		sources++
	}
	if c.EventJSONBase64 != nil {
		sources++
	}
	if c.EventJSONRecipe != nil {
		sources++
	}
	if sources != 1 {
		return nil, fmt.Errorf("exactly one event_json, event_json_base64, or event_json_recipe required")
	}
	if c.EventJSON != nil {
		return []byte(*c.EventJSON), nil
	}
	if c.EventJSONBase64 != nil {
		decoded, err := base64.StdEncoding.Strict().DecodeString(*c.EventJSONBase64)
		if err != nil || base64.StdEncoding.EncodeToString(decoded) != *c.EventJSONBase64 {
			return nil, fmt.Errorf("invalid fixture event_json_base64")
		}
		return decoded, nil
	}
	r := c.EventJSONRecipe
	if r.Repeat < 0 || r.Fill == "" || !utf8.ValidString(r.Prefix+r.Fill+r.Suffix) {
		return nil, fmt.Errorf("invalid event JSON recipe")
	}
	return []byte(r.Prefix + strings.Repeat(r.Fill, r.Repeat) + r.Suffix), nil
}

func TestEventTransportSharedFixtures(t *testing.T) {
	data, err := os.ReadFile("testdata/event-transport-fixtures.json")
	if err != nil {
		t.Fatal(err)
	}
	if !utf8.Valid(data) {
		t.Fatal("fixture file must be UTF-8")
	}
	if _, err := eventTransportHasOneJSONValue(data); err != nil {
		t.Fatalf("invalid or duplicate-key fixture JSON: %v", err)
	}
	var fixtures eventTransportFixtures
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&fixtures); err != nil {
		t.Fatal(err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		t.Fatal("fixture file must contain exactly one JSON value")
	}
	if fixtures.Profile != eventTransportFixtureProfile || len(fixtures.Cases) < 45 || len(fixtures.Cases) > 70 {
		t.Fatalf("unexpected event transport fixture profile/count: %q/%d", fixtures.Profile, len(fixtures.Cases))
	}
	ids := map[string]bool{}
	stages := map[string]bool{"outer_valid": true, "raw_limit": true, "json_invalid": true, "event_invalid": true, "legacy_or_unsupported": true, "route_invalid": true, "content_limit": true, "content_invalid": true, "signature_invalid": true}
	for _, tc := range fixtures.Cases {
		if tc.ID == "" || ids[tc.ID] {
			t.Fatalf("empty or duplicate fixture id %q", tc.ID)
		}
		ids[tc.ID] = true
		if !stages[tc.ExpectedStage] {
			t.Fatalf("%s: unknown expected stage %q", tc.ID, tc.ExpectedStage)
		}
		if !eventTransportLowerHex(tc.LocalPubKey, 64) {
			t.Fatalf("%s: invalid local_pubkey fixture metadata", tc.ID)
		}
		raw, err := eventTransportExpand(tc)
		if err != nil {
			t.Fatalf("%s: %v", tc.ID, err)
		}
		if tc.ExpectedBytes != nil && len(raw) != *tc.ExpectedBytes {
			t.Fatalf("%s: expanded bytes %d, expected %d", tc.ID, len(raw), *tc.ExpectedBytes)
		}
		actual := eventTransportValidate(raw, tc.LocalPubKey)
		if actual != tc.ExpectedStage {
			t.Errorf("%s: got %s, want %s", tc.ID, actual, tc.ExpectedStage)
		}
	}
	if !stages["outer_valid"] || len(ids) != len(fixtures.Cases) {
		t.Fatal("fixture cases did not all execute")
	}
}

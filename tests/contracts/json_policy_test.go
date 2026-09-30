package contracts

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

type policyFixture struct {
	Profile string       `json:"profile"`
	Cases   []policyCase `json:"cases"`
}

type policyCase struct {
	ID            string          `json:"id"`
	Input         json.RawMessage `json:"input"`
	InputBase64   json.RawMessage `json:"input_base64"`
	Recipe        json.RawMessage `json:"recipe"`
	Expected      string          `json:"expected"`
	ExpectedBytes *int            `json:"expected_bytes"`
}

type inputRecipe struct {
	Prefix string `json:"prefix"`
	Fill   string `json:"fill"`
	Repeat int    `json:"repeat"`
	Suffix string `json:"suffix"`
}

const fixtureProfile = "hyphae-json-input-candidate/1"

var expectedResults = map[string]bool{
	policyAccept: true, policySize: true, policyUnicode: true, policyInvalid: true,
	policyRoot: true, policyDuplicate: true, policyIntegerForm: true,
	policyIntegerSize: true, policyDepth: true,
}

func TestJSONPolicyFixtures(t *testing.T) {
	path := filepath.Join("testdata", "json-policy-v1.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var fixture policyFixture
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		t.Fatalf("fixture must contain exactly one JSON value: %v", err)
	}
	if fixture.Profile != fixtureProfile {
		t.Fatalf("fixture profile = %q, want %q", fixture.Profile, fixtureProfile)
	}
	if len(fixture.Cases) == 0 {
		t.Fatal("fixture has no cases")
	}
	seenIDs := make(map[string]bool, len(fixture.Cases))
	for _, testCase := range fixture.Cases {
		if testCase.ID == "" || seenIDs[testCase.ID] {
			t.Fatalf("fixture case ID %q is empty or duplicated", testCase.ID)
		}
		seenIDs[testCase.ID] = true
		if !expectedResults[testCase.Expected] {
			t.Fatalf("case %q has unknown expected result %q", testCase.ID, testCase.Expected)
		}
		t.Run(testCase.ID, func(t *testing.T) {
			input, err := testCase.bytes()
			if err != nil {
				t.Fatal(err)
			}
			if testCase.ExpectedBytes != nil && len(input) != *testCase.ExpectedBytes {
				t.Fatalf("expanded input has %d bytes, want %d", len(input), *testCase.ExpectedBytes)
			}
			got := validatePolicyV1(input)
			if got != testCase.Expected {
				t.Fatalf("validate = %q, want %q (input bytes=%d)", got, testCase.Expected, len(input))
			}
		})
	}
}

func (c policyCase) bytes() ([]byte, error) {
	count := 0
	if len(c.Input) != 0 {
		count++
	}
	if len(c.InputBase64) != 0 {
		count++
	}
	if len(c.Recipe) != 0 {
		count++
	}
	if count != 1 {
		return nil, fmt.Errorf("case %q must set exactly one input source", c.ID)
	}
	switch {
	case len(c.Input) != 0:
		var input string
		if err := json.Unmarshal(c.Input, &input); err != nil {
			return nil, fmt.Errorf("case %q input must be a string: %w", c.ID, err)
		}
		return []byte(input), nil
	case len(c.InputBase64) != 0:
		var encoded string
		if err := json.Unmarshal(c.InputBase64, &encoded); err != nil {
			return nil, fmt.Errorf("case %q input_base64 must be a string: %w", c.ID, err)
		}
		return base64.StdEncoding.DecodeString(encoded)
	default:
		var recipe *inputRecipe
		decoder := json.NewDecoder(bytes.NewReader(c.Recipe))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&recipe); err != nil {
			return nil, fmt.Errorf("case %q recipe must be an object: %w", c.ID, err)
		}
		if recipe == nil {
			return nil, fmt.Errorf("case %q recipe must be an object", c.ID)
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			return nil, fmt.Errorf("case %q recipe must contain one value: %v", c.ID, err)
		}
		if recipe.Repeat < 0 {
			return nil, fmt.Errorf("case %q has negative recipe repeat", c.ID)
		}
		var output bytes.Buffer
		output.WriteString(recipe.Prefix)
		for i := 0; i < recipe.Repeat; i++ {
			output.WriteString(recipe.Fill)
		}
		output.WriteString(recipe.Suffix)
		return output.Bytes(), nil
	}
}

const maxPolicyBytes = 32768
const maxPolicyDepth = 16

const (
	policyAccept      = "accept"
	policySize        = "JSON_SIZE"
	policyUnicode     = "JSON_UNICODE"
	policyInvalid     = "JSON_INVALID"
	policyRoot        = "JSON_ROOT"
	policyDuplicate   = "JSON_DUPLICATE_KEY"
	policyIntegerForm = "JSON_INTEGER_FORMAT"
	policyIntegerSize = "JSON_INTEGER_RANGE"
	policyDepth       = "JSON_DEPTH"
)

var maxSafeInteger = big.NewInt(9007199254740991)

// validatePolicyV1 is an executable test reference for this candidate profile.
// It intentionally is not exported or used by any production input path.
func validatePolicyV1(input []byte) string {
	if len(input) > maxPolicyBytes {
		return policySize
	}
	if !utf8.Valid(input) || !validStringUnicode(input) {
		return policyUnicode
	}
	if !json.Valid(input) {
		return policyInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.UseNumber()
	root, err := decoder.Token()
	if err != nil {
		return policyInvalid
	}
	if root != json.Delim('{') {
		return policyRoot
	}
	if code := walkJSONToken(decoder, root, 0); code != "" {
		return code
	}
	return policyAccept
}

// validStringUnicode checks Unicode before encoding/json can replace an
// unpaired UTF-16 surrogate with U+FFFD. Raw input was UTF-8 validated first.
func validStringUnicode(input []byte) bool {
	for i := 0; i < len(input); {
		if input[i] != '"' {
			i++
			continue
		}
		i++
		for i < len(input) {
			if input[i] == '"' {
				i++
				break
			}
			if input[i] != '\\' {
				r, size := utf8.DecodeRune(input[i:])
				if isNoncharacter(r) {
					return false
				}
				i += size
				continue
			}
			if i+1 >= len(input) {
				return true // syntax check will report a truncated escape
			}
			if input[i+1] != 'u' {
				i += 2
				continue
			}
			if i+6 > len(input) {
				return true // malformed/truncated hex is JSON syntax, not Unicode
			}
			unit, ok := parseHexUnit(input[i+2 : i+6])
			if !ok {
				i += 6
				continue
			}
			switch {
			case 0xD800 <= unit && unit <= 0xDBFF:
				if i+12 > len(input) || input[i+6] != '\\' || input[i+7] != 'u' {
					return false
				}
				low, valid := parseHexUnit(input[i+8 : i+12])
				if !valid || low < 0xDC00 || low > 0xDFFF {
					return false
				}
				r := rune(0x10000 + (int(unit-0xD800) << 10) + int(low-0xDC00))
				if isNoncharacter(r) {
					return false
				}
				i += 12
			case 0xDC00 <= unit && unit <= 0xDFFF:
				return false
			default:
				if isNoncharacter(rune(unit)) {
					return false
				}
				i += 6
			}
		}
	}
	return true
}

func isNoncharacter(r rune) bool {
	return 0xFDD0 <= r && r <= 0xFDEF || r&0xFFFF == 0xFFFE || r&0xFFFF == 0xFFFF
}

func parseHexUnit(hex []byte) (uint16, bool) {
	var value uint16
	for _, b := range hex {
		value <<= 4
		switch {
		case '0' <= b && b <= '9':
			value |= uint16(b - '0')
		case 'a' <= b && b <= 'f':
			value |= uint16(b-'a') + 10
		case 'A' <= b && b <= 'F':
			value |= uint16(b-'A') + 10
		default:
			return 0, false
		}
	}
	return value, true
}

func walkJSONToken(decoder *json.Decoder, token json.Token, parentDepth int) string {
	switch value := token.(type) {
	case json.Delim:
		if value != '{' && value != '[' {
			return policyInvalid
		}
		depth := parentDepth + 1
		if depth > maxPolicyDepth {
			return policyDepth
		}
		if value == '{' {
			seen := make(map[string]struct{})
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return policyInvalid
				}
				key, ok := keyToken.(string)
				if !ok {
					return policyInvalid
				}
				if _, exists := seen[key]; exists {
					return policyDuplicate
				}
				seen[key] = struct{}{}
				child, err := decoder.Token()
				if err != nil {
					return policyInvalid
				}
				if code := walkJSONToken(decoder, child, depth); code != "" {
					return code
				}
			}
		} else {
			for decoder.More() {
				child, err := decoder.Token()
				if err != nil {
					return policyInvalid
				}
				if code := walkJSONToken(decoder, child, depth); code != "" {
					return code
				}
			}
		}
		closing, err := decoder.Token()
		wantClosing := json.Delim(']')
		if value == '{' {
			wantClosing = '}'
		}
		if err != nil || closing != wantClosing {
			return policyInvalid
		}
	case json.Number:
		text := value.String()
		if strings.ContainsAny(text, ".eE") || text == "-0" {
			return policyIntegerForm
		}
		digits := strings.TrimPrefix(text, "-")
		integer, ok := new(big.Int).SetString(digits, 10)
		if !ok {
			return policyInvalid
		}
		if integer.Cmp(maxSafeInteger) > 0 {
			return policyIntegerSize
		}
	}
	return ""
}

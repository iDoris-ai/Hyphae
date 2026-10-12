package messaging

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"unicode/utf8"
)

func strictJSONString(raw []byte) (string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) < 2 || trimmed[0] != '"' || trimmed[len(trimmed)-1] != '"' {
		return "", fmt.Errorf("expected a JSON string")
	}
	if !utf8.Valid(trimmed) {
		return "", fmt.Errorf("invalid UTF-8 in JSON string")
	}
	if err := checkJSONSurrogates(trimmed[1 : len(trimmed)-1]); err != nil {
		return "", err
	}
	var value string
	if err := json.Unmarshal(trimmed, &value); err != nil {
		return "", fmt.Errorf("invalid JSON string")
	}
	return value, nil
}

func checkJSONSurrogates(inner []byte) error {
	for i := 0; i < len(inner); {
		if inner[i] != '\\' {
			i++
			continue
		}
		if i+1 >= len(inner) {
			return fmt.Errorf("trailing backslash in JSON string")
		}
		switch inner[i+1] {
		case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
			i += 2
		case 'u':
			hi, ok := hex4(inner, i+2)
			if !ok {
				return fmt.Errorf("invalid \\u escape")
			}
			switch {
			case hi >= 0xDC00 && hi <= 0xDFFF:
				return fmt.Errorf("unpaired low surrogate escape")
			case hi >= 0xD800 && hi <= 0xDBFF:
				if i+12 > len(inner) || inner[i+6] != '\\' || inner[i+7] != 'u' {
					return fmt.Errorf("unpaired high surrogate escape")
				}
				lo, ok := hex4(inner, i+8)
				if !ok || lo < 0xDC00 || lo > 0xDFFF {
					return fmt.Errorf("unpaired high surrogate escape")
				}
				i += 12
			default:
				i += 6
			}
		default:
			return fmt.Errorf("invalid escape sequence")
		}
	}
	return nil
}

func hex4(b []byte, at int) (int, bool) {
	if at+4 > len(b) {
		return 0, false
	}
	value, err := strconv.ParseUint(string(b[at:at+4]), 16, 32)
	return int(value), err == nil
}

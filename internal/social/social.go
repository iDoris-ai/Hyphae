// Package social holds the primitives shared by the social protocol packages
// (intent, trust, referral). See docs/search/protocol-draft.md.
//
// Everything here is pure logic over already-verified events. Nothing in this
// tree talks to a relay, assigns a Nostr kind, or grants execution authority:
// the wire format is not frozen (docs/agent/t01-contract-gates.md).
package social

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

const (
	// Version is the only content version this code accepts.
	Version = 1
	// MaxContentBytes bounds a message body before any parsing happens.
	MaxContentBytes = 16 << 10
	// MaxDepth bounds JSON nesting.
	MaxDepth = 16
	// MaxClockSkew is how far (seconds) an event's sender-chosen created_at
	// may run ahead of the local clock before the state machines defer it.
	MaxClockSkew = 300
)

// Envelope is the projection of a Nostr event whose signature and ID the
// caller has already verified. Author is the signing key; message bodies
// never carry or override it.
type Envelope struct {
	ID        string
	Author    string
	CreatedAt int64
	Content   json.RawMessage
}

// Header is the part every message body shares.
type Header struct {
	V    int    `json:"v"`
	Type string `json:"type"`
}

// ValidHex32 reports whether s is 64 lowercase hex characters, the encoding
// used for public keys and event IDs.
func ValidHex32(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// ValidKey reports whether s may be used as a JSON object key anywhere in a
// message body: 1 to 32 characters of a-z, 0-9 and underscore. Restricting
// keys this way removes the case-folding that encoding/json applies when it
// matches field names ("Offer" would otherwise fill the "offer" field), so
// every implementation agrees on which keys a body contains.
func ValidKey(s string) bool {
	if len(s) == 0 || len(s) > 32 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '_' {
			return false
		}
	}
	return true
}

// Validate checks the envelope fields that do not depend on the body.
func (e Envelope) Validate() error {
	if !ValidHex32(e.ID) {
		return errors.New("envelope: id must be 64 lowercase hex characters")
	}
	if !ValidHex32(e.Author) {
		return errors.New("envelope: author must be 64 lowercase hex characters")
	}
	if e.CreatedAt <= 0 {
		return errors.New("envelope: created_at must be positive")
	}
	return nil
}

// ParseHeader validates the envelope and returns the body's version and type.
// It rejects oversized, ambiguous and unknown-version bodies.
func ParseHeader(e Envelope) (Header, error) {
	if err := e.Validate(); err != nil {
		return Header{}, err
	}
	if err := checkShape(e.Content); err != nil {
		return Header{}, err
	}
	var h Header
	if err := json.Unmarshal(e.Content, &h); err != nil {
		return Header{}, fmt.Errorf("content: %w", err)
	}
	if h.V != Version {
		return Header{}, fmt.Errorf("content: unsupported version %d", h.V)
	}
	if h.Type == "" {
		return Header{}, errors.New("content: missing type")
	}
	return h, nil
}

// StrictUnmarshal decodes a message body into v. It rejects bodies that are
// too large, nested too deeply, not a single JSON object, or that contain
// duplicate or invalid keys (see ValidKey), null values, unknown fields or
// trailing data.
func StrictUnmarshal(raw json.RawMessage, v any) error {
	if err := checkShape(raw); err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("content: %w", err)
	}
	return nil
}

func checkShape(raw json.RawMessage) error {
	if len(raw) == 0 {
		return errors.New("content: empty")
	}
	if len(raw) > MaxContentBytes {
		return fmt.Errorf("content: %d bytes exceeds limit %d", len(raw), MaxContentBytes)
	}
	if b := bytes.TrimLeft(raw, " \t\r\n"); len(b) == 0 || b[0] != '{' {
		return errors.New("content: must be a JSON object")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := walk(dec, 0); err != nil {
		return fmt.Errorf("content: %w", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return errors.New("content: trailing data after JSON object")
	}
	return nil
}

// walk consumes one JSON value, rejecting invalid or duplicate object keys,
// null, strings that are not valid Unicode (encoding/json silently turns an
// unpaired surrogate escape or a bad byte into U+FFFD, where other decoders
// fail, so any U+FFFD is refused) and nesting
// deeper than MaxDepth.
func walk(dec *json.Decoder, depth int) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if tok == nil {
		// No field in the protocol is nullable, and encoding/json would turn
		// null into a zero value that passes later checks.
		return errors.New("null is not allowed")
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		if str, isStr := tok.(string); isStr && strings.ContainsRune(str, utf8.RuneError) {
			return errors.New("string is not valid Unicode")
		}
		return nil
	}
	if depth >= MaxDepth {
		return fmt.Errorf("nesting exceeds %d levels", MaxDepth)
	}
	seen := map[string]struct{}{}
	for dec.More() {
		if delim == '{' {
			kt, err := dec.Token()
			if err != nil {
				return err
			}
			key, _ := kt.(string)
			if !ValidKey(key) {
				return fmt.Errorf("invalid key %q", key)
			}
			if _, dup := seen[key]; dup {
				return fmt.Errorf("duplicate key %q", key)
			}
			seen[key] = struct{}{}
		}
		if err := walk(dec, depth+1); err != nil {
			return err
		}
	}
	_, err = dec.Token()
	return err
}

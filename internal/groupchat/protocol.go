// Package groupchat contains the local group-chat protocol and durable state.
// It intentionally has no CLI, TUI, daemon, or relay-watcher integration.
package groupchat

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/iDoris-ai/hyphae/internal/common"
)

const (
	// ReservedPrefix is reserved for typed group envelopes. Ordinary chat JSON
	// that does not start with this exact prefix remains ordinary chat text.
	ReservedPrefix = "hyphae.group/"
	MagicV1        = ReservedPrefix + "v1\n"
	Version        = 1
	MaxEnvelope    = 32 << 10
	MaxBodyRunes   = 500
	MaxMembers     = 16
)

var (
	ErrNotEnvelope        = errors.New("not a group envelope")
	ErrUnsupportedVersion = errors.New("unsupported group envelope version")
)

type EnvelopeType string

const (
	EnvelopeInvite   EnvelopeType = "invite"
	EnvelopeAccept   EnvelopeType = "accept"
	EnvelopeDecline  EnvelopeType = "decline"
	EnvelopeActivate EnvelopeType = "activate"
	EnvelopeCancel   EnvelopeType = "cancel"
	EnvelopeMessage  EnvelopeType = "message"
)

// Envelope is a strict typed payload. Fields not applicable to Type must be
// empty; in particular, message envelopes never carry a roster.
type Envelope struct {
	Type        EnvelopeType `json:"type"`
	Version     int          `json:"version"`
	GroupID     string       `json:"group_id,omitempty"`
	CreatorNpub string       `json:"creator_npub,omitempty"`
	InviteID    string       `json:"invite_id,omitempty"`
	RosterHash  string       `json:"roster_hash,omitempty"`
	InviteeNpub string       `json:"invitee_npub,omitempty"`
	Name        string       `json:"name,omitempty"`
	Members     []string     `json:"members,omitempty"`
	LogicalID   string       `json:"logical_id,omitempty"`
	Body        string       `json:"body,omitempty"`
}

type canonicalRoster struct {
	Version int      `json:"version"`
	GroupID string   `json:"group_id"`
	Creator string   `json:"creator_npub"`
	Name    string   `json:"name"`
	Members []string `json:"members"`
}

// NewOpaqueID returns a cryptographically random, non-name-derived ID.
func NewOpaqueID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate opaque ID: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}

// CanonicalRosterHash validates and hashes the immutable initial roster.
func CanonicalRosterHash(groupID, creator, name string, members []string) (string, []string, error) {
	if !validOpaqueID(groupID) {
		return "", nil, errors.New("invalid group ID")
	}
	creator, err := canonicalNpub(creator)
	if err != nil {
		return "", nil, fmt.Errorf("invalid creator: %w", err)
	}
	if !validProtocolText(name) || strings.TrimSpace(name) == "" || utf8.RuneCountInString(name) > 80 {
		return "", nil, errors.New("group name must contain 1 to 80 runes")
	}
	if len(members) < 2 || len(members) > MaxMembers {
		return "", nil, fmt.Errorf("roster must contain 2 to %d members", MaxMembers)
	}

	canonicalMembers := make([]string, 0, len(members))
	seen := make(map[string]struct{}, len(members))
	for _, member := range members {
		npub, err := canonicalNpub(member)
		if err != nil {
			return "", nil, fmt.Errorf("invalid roster member: %w", err)
		}
		if _, ok := seen[npub]; ok {
			return "", nil, errors.New("roster contains duplicate member")
		}
		seen[npub] = struct{}{}
		canonicalMembers = append(canonicalMembers, npub)
	}
	if _, ok := seen[creator]; !ok {
		return "", nil, errors.New("creator must be in roster")
	}
	sort.Strings(canonicalMembers)

	material := canonicalRoster{Version: Version, GroupID: groupID, Creator: creator, Name: name, Members: canonicalMembers}
	encoded, err := json.Marshal(material)
	if err != nil {
		return "", nil, err
	}
	hash := sha256.Sum256(encoded)
	return hex.EncodeToString(hash[:]), canonicalMembers, nil
}

// Encode serializes a validated envelope under the reserved typed magic.
func Encode(envelope Envelope) (string, error) {
	if err := ValidateEnvelope(envelope); err != nil {
		return "", err
	}
	data, err := json.Marshal(envelope)
	if err != nil {
		return "", fmt.Errorf("encode group envelope: %w", err)
	}
	encoded := MagicV1 + string(data)
	if len(encoded) > MaxEnvelope {
		return "", fmt.Errorf("group envelope exceeds %d bytes", MaxEnvelope)
	}
	return encoded, nil
}

// Decode distinguishes normal chat text from reserved group content. Once the
// reserved namespace is present, malformed or unknown versions fail closed.
func Decode(content string) (Envelope, error) {
	if !strings.HasPrefix(content, ReservedPrefix) {
		return Envelope{}, ErrNotEnvelope
	}
	if !utf8.ValidString(content) {
		return Envelope{}, errors.New("group envelope must be valid UTF-8")
	}
	if len(content) > MaxEnvelope {
		return Envelope{}, fmt.Errorf("group envelope exceeds %d bytes", MaxEnvelope)
	}
	version, payload, ok := strings.Cut(strings.TrimPrefix(content, ReservedPrefix), "\n")
	if !ok {
		return Envelope{}, errors.New("malformed group envelope magic")
	}
	if version != "v1" {
		return Envelope{}, fmt.Errorf("%w: %q", ErrUnsupportedVersion, version)
	}
	if err := rejectDuplicateJSONKeys([]byte(payload)); err != nil {
		return Envelope{}, fmt.Errorf("decode group envelope: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewBufferString(payload))
	decoder.DisallowUnknownFields()
	var envelope Envelope
	if err := decoder.Decode(&envelope); err != nil {
		return Envelope{}, fmt.Errorf("decode group envelope: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return Envelope{}, errors.New("group envelope must contain exactly one JSON value")
	}
	if err := ValidateEnvelope(envelope); err != nil {
		return Envelope{}, err
	}
	return envelope, nil
}

// ValidateEnvelope enforces type-specific shape, strict bounds, and roster
// binding. Unknown JSON fields are rejected by Decode before this validation.
func ValidateEnvelope(e Envelope) error {
	if e.Version != Version {
		return fmt.Errorf("unsupported group envelope payload version %d", e.Version)
	}
	if !validOpaqueID(e.GroupID) {
		return errors.New("invalid group ID")
	}
	switch e.Type {
	case EnvelopeInvite, EnvelopeActivate:
		if err := validateCreatorAndHash(e); err != nil {
			return err
		}
		if !validOpaqueID(e.InviteID) {
			return errors.New("invalid invite ID")
		}
		invitee, err := canonicalNpub(e.InviteeNpub)
		if err != nil {
			return errors.New("invalid invitee npub")
		}
		if invitee != e.InviteeNpub {
			return errors.New("invitee npub is not canonical")
		}
		if !validProtocolText(e.Name) || strings.TrimSpace(e.Name) == "" || utf8.RuneCountInString(e.Name) > 80 {
			return errors.New("group name must contain 1 to 80 runes")
		}
		hash, members, err := CanonicalRosterHash(e.GroupID, e.CreatorNpub, e.Name, e.Members)
		if err != nil {
			return err
		}
		if hash != e.RosterHash || !sameStrings(members, e.Members) {
			return errors.New("roster is not canonical or hash does not match")
		}
		if !contains(members, invitee) || invitee == e.CreatorNpub {
			return errors.New("invitee must be a non-creator roster member")
		}
		if e.LogicalID != "" || e.Body != "" {
			return errors.New("invite envelope contains message fields")
		}
	case EnvelopeAccept, EnvelopeDecline, EnvelopeCancel:
		if err := validateCreatorAndHash(e); err != nil {
			return err
		}
		if !validOpaqueID(e.InviteID) {
			return errors.New("invalid invite ID")
		}
		invitee, err := canonicalNpub(e.InviteeNpub)
		if err != nil {
			return fmt.Errorf("invalid invitee npub: %w", err)
		}
		if invitee != e.InviteeNpub {
			return errors.New("invitee npub is not canonical")
		}
		if e.Name != "" || e.Members != nil || e.LogicalID != "" || e.Body != "" {
			return errors.New("control envelope contains unrelated fields")
		}
	case EnvelopeMessage:
		if e.CreatorNpub != "" || e.RosterHash != "" {
			return errors.New("group message must not contain creator or roster metadata")
		}
		if !validOpaqueID(e.LogicalID) {
			return errors.New("invalid logical message ID")
		}
		if !validProtocolText(e.Body) || utf8.RuneCountInString(e.Body) == 0 || utf8.RuneCountInString(e.Body) > MaxBodyRunes {
			return fmt.Errorf("group message body must contain 1 to %d runes", MaxBodyRunes)
		}
		if e.InviteID != "" || e.InviteeNpub != "" || e.Name != "" || e.Members != nil {
			return errors.New("group message must not contain roster or invitation metadata")
		}
	default:
		return fmt.Errorf("unknown group envelope type %q", e.Type)
	}
	return nil
}

func validateCreatorAndHash(e Envelope) error {
	creator, err := canonicalNpub(e.CreatorNpub)
	if err != nil {
		return fmt.Errorf("invalid creator npub: %w", err)
	}
	if creator != e.CreatorNpub {
		return errors.New("creator npub is not canonical")
	}
	if len(e.RosterHash) != sha256.Size*2 || !isLowerHex(e.RosterHash) {
		return errors.New("invalid roster hash")
	}
	return nil
}

func canonicalNpub(value string) (string, error) {
	pk, err := common.ParsePublicKey(value)
	if err != nil {
		return "", err
	}
	return common.EncodeNpub(pk), nil
}

func validProtocolText(value string) bool {
	return utf8.ValidString(value) && !strings.ContainsRune(value, '\x00')
}

func validOpaqueID(value string) bool {
	if len(value) != 32 || !isLowerHex(value) {
		return false
	}
	return true
}

func validEventID(value string) bool { return len(value) == 64 && isLowerHex(value) }

func isLowerHex(value string) bool {
	if value == "" || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func rejectDuplicateJSONKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := scanJSONValue(decoder); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return errors.New("group envelope must contain exactly one JSON value")
		}
		return err
	}
	return nil
}

func scanJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, isDelim := token.(json.Delim)
	if !isDelim {
		return nil
	}
	switch delim {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("group envelope JSON object has a non-string key")
			}
			if _, exists := seen[key]; exists {
				return fmt.Errorf("duplicate group envelope field %q", key)
			}
			seen[key] = struct{}{}
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return errors.New("malformed group envelope JSON object")
		}
	case '[':
		for decoder.More() {
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return errors.New("malformed group envelope JSON array")
		}
	default:
		return errors.New("unexpected group envelope JSON delimiter")
	}
	return nil
}

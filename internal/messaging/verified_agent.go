package messaging

import (
	"encoding/hex"
	"errors"
	"strings"

	"fiatjaf.com/nostr"
	"github.com/iDoris-ai/hyphae/internal/common"
)

const reservedGroupPayloadPrefix = "hyphae.group/"

// AgentMessageRoute classifies the already-decrypted payload without parsing
// any group protocol. ReservedGroup includes unknown or malformed versions so
// callers cannot silently display those payloads as ordinary direct messages.
type AgentMessageRoute uint8

const (
	AgentRouteInvalid AgentMessageRoute = iota
	AgentRouteDirect
	AgentRouteReservedGroup
)

// VerifiedAgentMessage is an opaque, signature-verified, single-recipient
// message. It retains bounded plaintext and signed metadata, but neither the
// event/ciphertext nor a secret key. Only VerifyAgentMessage can set verified.
type VerifiedAgentMessage struct {
	verified      bool
	eventID       string
	senderNpub    string
	recipientNpub string
	kind          int
	createdAt     int64
	plaintext     string
	encrypted     bool
	route         AgentMessageRoute
}

// VerifyAgentMessage verifies and decodes one event for exactly one recipient.
// It does not classify the plaintext as a group envelope; the reserved prefix
// is only marked for a later domain-specific decoder.
func VerifyAgentMessage(event nostr.Event, recipientSecret nostr.SecretKey) (VerifiedAgentMessage, error) {
	if recipientSecret == (nostr.SecretKey{}) {
		return VerifiedAgentMessage{}, errors.New("recipient secret key is required")
	}
	if event.Kind != AgentKind {
		return VerifiedAgentMessage{}, errors.New("unexpected Agent message event kind")
	}
	if !event.CheckID() || !event.VerifySignature() {
		return VerifiedAgentMessage{}, errors.New("invalid Agent message event ID or signature")
	}
	if err := ValidateAgentMessageEvent(&event); err != nil {
		return VerifiedAgentMessage{}, err
	}

	recipientHex := common.PubKeyToHex(recipientSecret.Public())
	pCount := 0
	matched := false
	for _, tag := range event.Tags {
		if len(tag) == 0 || tag[0] != "p" {
			continue
		}
		pCount++
		if len(tag) == 2 && tag[1] == recipientHex {
			matched = true
		}
	}
	if pCount != 1 || !matched {
		return VerifiedAgentMessage{}, errors.New("Agent message must target exactly this recipient")
	}

	plaintext, encrypted, err := DecodeMessageContent(&event, recipientSecret)
	if err != nil {
		return VerifiedAgentMessage{}, err
	}
	route := AgentRouteDirect
	if strings.HasPrefix(plaintext, reservedGroupPayloadPrefix) {
		route = AgentRouteReservedGroup
	}
	return VerifiedAgentMessage{
		verified: true, eventID: event.ID.Hex(),
		senderNpub: common.EncodeNpub(event.PubKey), recipientNpub: common.EncodeNpub(recipientSecret.Public()),
		kind: int(event.Kind), createdAt: int64(event.CreatedAt), plaintext: plaintext,
		encrypted: encrypted, route: route,
	}, nil
}

// Valid reports whether this value was constructed from a fully validated
// event. The zero value and internally inconsistent values are invalid.
func (m VerifiedAgentMessage) Valid() bool {
	if !m.verified || m.kind != AgentKind || len(m.eventID) != 64 || len(m.plaintext) > maxIncomingBodyBytes || strings.ToLower(m.eventID) != m.eventID {
		return false
	}
	if _, err := hex.DecodeString(m.eventID); err != nil {
		return false
	}
	for _, value := range []string{m.senderNpub, m.recipientNpub} {
		publicKey, err := common.ParsePublicKey(value)
		if err != nil || common.EncodeNpub(publicKey) != value {
			return false
		}
	}
	expectedRoute := AgentRouteDirect
	if strings.HasPrefix(m.plaintext, reservedGroupPayloadPrefix) {
		expectedRoute = AgentRouteReservedGroup
	}
	return m.route == expectedRoute
}

func (m VerifiedAgentMessage) EventID() string       { return m.eventID }
func (m VerifiedAgentMessage) SenderNpub() string    { return m.senderNpub }
func (m VerifiedAgentMessage) RecipientNpub() string { return m.recipientNpub }
func (m VerifiedAgentMessage) Kind() int             { return m.kind }
func (m VerifiedAgentMessage) CreatedAt() int64      { return m.createdAt }
func (m VerifiedAgentMessage) Plaintext() string     { return m.plaintext }
func (m VerifiedAgentMessage) IsEncrypted() bool     { return m.encrypted }

// Route returns AgentRouteInvalid for a zero or otherwise invalid value.
func (m VerifiedAgentMessage) Route() AgentMessageRoute {
	if !m.Valid() {
		return AgentRouteInvalid
	}
	return m.route
}

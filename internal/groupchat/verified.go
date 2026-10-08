package groupchat

import (
	"errors"

	"fiatjaf.com/nostr"
	"github.com/iDoris-ai/hyphae/internal/messaging"
)

// VerifiedIncoming is an opaque, signature-verified, single-recipient Agent
// message decrypted for RecipientNpub. Its constructor never retains the
// recipient secret key or ciphertext. Use VerifyIncoming to create one.
type VerifiedIncoming struct {
	verified      bool
	eventID       string
	senderNpub    string
	recipientNpub string
	kind          int
	createdAt     int64
	plaintext     string
	isEncrypted   bool
}

// Only a validated reserved payload may cross into group state. This type
// conversion does not parse the group envelope or authorize its transition.
func FromVerifiedAgentMessage(message messaging.VerifiedAgentMessage) (VerifiedIncoming, error) {
	if !message.Valid() {
		return VerifiedIncoming{}, errors.New("verified Agent message is required")
	}
	if message.Route() != messaging.AgentRouteReservedGroup {
		return VerifiedIncoming{}, errors.New("reserved group Agent message is required")
	}
	if !message.IsEncrypted() {
		return VerifiedIncoming{}, errors.New("group protocol requires NIP-44 encryption")
	}
	return VerifiedIncoming{
		verified:      true,
		eventID:       message.EventID(),
		senderNpub:    message.SenderNpub(),
		recipientNpub: message.RecipientNpub(),
		kind:          message.Kind(),
		createdAt:     message.CreatedAt(),
		plaintext:     message.Plaintext(),
		isEncrypted:   message.IsEncrypted(),
	}, nil
}

// VerifyIncoming remains the group-facing convenience API. Shared event
// authentication/classification/decryption happens once in messaging before
// this adapter passes the opaque result to group-specific state guards.
func VerifyIncoming(event nostr.Event, recipientSecret nostr.SecretKey) (VerifiedIncoming, error) {
	message, err := messaging.VerifyAgentMessage(event, recipientSecret)
	if err != nil {
		return VerifiedIncoming{}, err
	}
	return FromVerifiedAgentMessage(message)
}

func (v VerifiedIncoming) EventID() string       { return v.eventID }
func (v VerifiedIncoming) SenderNpub() string    { return v.senderNpub }
func (v VerifiedIncoming) RecipientNpub() string { return v.recipientNpub }
func (v VerifiedIncoming) Kind() int             { return v.kind }
func (v VerifiedIncoming) CreatedAt() int64      { return v.createdAt }
func (v VerifiedIncoming) Plaintext() string     { return v.plaintext }
func (v VerifiedIncoming) IsEncrypted() bool     { return v.isEncrypted }

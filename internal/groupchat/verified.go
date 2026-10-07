package groupchat

import (
	"errors"
	"fmt"

	"fiatjaf.com/nostr"
	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/internal/messaging"
	"github.com/iDoris-ai/hyphae/internal/wireevent"
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

// VerifyIncoming validates identity/routing metadata, classifies kind 30078
// through the shared wireevent classifier (via messaging.Validate...), and
// uses the shared bounded decoder/decryptor. Group core methods accept only
// this value so they derive sender and recipient from the signed event, never
// from JSON fields.
func VerifyIncoming(event nostr.Event, recipientSecret nostr.SecretKey) (VerifiedIncoming, error) {
	if event.Kind != wireevent.Kind30078 {
		return VerifiedIncoming{}, errors.New("unexpected Agent event kind")
	}
	if !event.CheckID() || !event.VerifySignature() {
		return VerifiedIncoming{}, errors.New("invalid Agent event ID or signature")
	}
	if err := messaging.ValidateAgentMessageEvent(&event); err != nil {
		return VerifiedIncoming{}, err
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
		return VerifiedIncoming{}, errors.New("Agent event must target exactly this recipient")
	}

	plaintext, encrypted, err := messaging.DecodeMessageContent(&event, recipientSecret)
	if err != nil {
		return VerifiedIncoming{}, fmt.Errorf("decode Agent event: %w", err)
	}
	if !encrypted {
		return VerifiedIncoming{}, errors.New("group protocol requires NIP-44 encryption")
	}
	return VerifiedIncoming{
		verified:      true,
		eventID:       event.ID.Hex(),
		senderNpub:    common.EncodeNpub(event.PubKey),
		recipientNpub: common.EncodeNpub(recipientSecret.Public()),
		kind:          int(event.Kind),
		createdAt:     int64(event.CreatedAt),
		plaintext:     plaintext,
		isEncrypted:   encrypted,
	}, nil
}

func (v VerifiedIncoming) EventID() string       { return v.eventID }
func (v VerifiedIncoming) SenderNpub() string    { return v.senderNpub }
func (v VerifiedIncoming) RecipientNpub() string { return v.recipientNpub }
func (v VerifiedIncoming) Kind() int             { return v.kind }
func (v VerifiedIncoming) CreatedAt() int64      { return v.createdAt }
func (v VerifiedIncoming) Plaintext() string     { return v.plaintext }
func (v VerifiedIncoming) IsEncrypted() bool     { return v.isEncrypted }

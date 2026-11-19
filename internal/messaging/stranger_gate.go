package messaging

import (
	"time"

	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/internal/social/contact"
	"github.com/iDoris-ai/hyphae/internal/storage"
	"github.com/iDoris-ai/hyphae/pkg/types"
)

// ReserveStrangerSend applies the stranger rules (docs/search/discover-friends.md)
// to one new message from sender to recipient and records it when allowed.
// Call it once per new logical message, after everything that can fail before
// queueing and right before the message is queued; outbox retries must not
// call it again. The sender's own identities never touch the database.
// Contacts are friends unless a stored block says otherwise, so re-adding a
// blocked peer as a contact does not lift the block.
func ReserveStrangerSend(ks *types.KeyStore, senderNpub, recipient string) error {
	// Key the rules on the canonical npub: hex, upper case or padded spellings
	// of one key are the same person.
	pk, err := common.ParsePublicKey(recipient)
	if err != nil {
		return err
	}
	recipientNpub := common.EncodeNpub(pk)
	if isOwnIdentity(ks, recipientNpub) {
		return nil
	}
	if err := InitStorage(); err != nil {
		return err
	}
	_, err = storage.NewRelationStore(storage.DB).
		ReserveSend(senderNpub, recipientNpub, isContact(ks, recipientNpub), time.Now().Unix())
	return storage.SendRefusal(err, recipientNpub)
}

func isContact(ks *types.KeyStore, npub string) bool {
	for _, c := range ks.Contacts {
		if c.Npub == npub {
			return true
		}
	}
	return false
}

func isOwnIdentity(ks *types.KeyStore, npub string) bool {
	for _, id := range ks.Identities {
		if id.Npub == npub {
			return true
		}
	}
	return false
}

// ClassifyIncoming decides how to treat one incoming message from sender to
// recipient (Show, Request, Suppress or Drop) and counts it once per event ID.
// The recipient's own identities are always shown and never touch the
// database; contacts are shown unless a stored block says otherwise.
func ClassifyIncoming(ks *types.KeyStore, recipientNpub, senderNpub, eventID string) (storage.Receipt, error) {
	if isOwnIdentity(ks, senderNpub) {
		return storage.Receipt{Disposition: contact.Show}, nil
	}
	if err := InitStorage(); err != nil {
		return storage.Receipt{}, err
	}
	return storage.NewRelationStore(storage.DB).
		ClassifyReceive(recipientNpub, senderNpub, eventID, isContact(ks, senderNpub), time.Now().Unix())
}

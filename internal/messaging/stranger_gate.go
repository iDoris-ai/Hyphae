package messaging

import (
	"time"

	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/internal/storage"
	"github.com/iDoris-ai/hyphae/pkg/types"
)

// ReserveStrangerSend applies the stranger rules (docs/search/discover-friends.md)
// to one new message from sender to recipient and records it when allowed.
// Call it once per new logical message, after everything that can fail before
// queueing and right before the message is queued; outbox retries must not
// call it again. Contacts and the sender's own identities are friends and
// never touch the database (blocking a contact removes the contact, F6).
func ReserveStrangerSend(ks *types.KeyStore, senderNpub, recipient string) error {
	// Key the rules on the canonical npub: hex, upper case or padded spellings
	// of one key are the same person.
	pk, err := common.ParsePublicKey(recipient)
	if err != nil {
		return err
	}
	recipientNpub := common.EncodeNpub(pk)
	if isFriendNpub(ks, recipientNpub) {
		return nil
	}
	if err := InitStorage(); err != nil {
		return err
	}
	_, err = storage.NewRelationStore(storage.DB).
		ReserveSend(senderNpub, recipientNpub, false, time.Now().Unix())
	return storage.SendRefusal(err, recipientNpub)
}

func isFriendNpub(ks *types.KeyStore, npub string) bool {
	for _, c := range ks.Contacts {
		if c.Npub == npub {
			return true
		}
	}
	for _, id := range ks.Identities {
		if id.Npub == npub {
			return true
		}
	}
	return false
}

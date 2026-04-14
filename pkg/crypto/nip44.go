// Package crypto provides encryption and decryption using standard NIP-44
package crypto

import (
	"fmt"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/nip44"
)

// EncryptMessage encrypts a message using standard NIP-44.
// It generates a conversation key from the sender's secret key and recipient's public key.
func EncryptMessage(plaintext string, senderSK nostr.SecretKey, recipientPK nostr.PubKey) (string, error) {
	convKey, err := nip44.GenerateConversationKey(recipientPK, senderSK)
	if err != nil {
		return "", fmt.Errorf("failed to generate conversation key: %w", err)
	}

	ciphertext, err := nip44.Encrypt(plaintext, convKey)
	if err != nil {
		return "", fmt.Errorf("failed to encrypt: %w", err)
	}

	return ciphertext, nil
}

// DecryptMessage decrypts a message using standard NIP-44.
// It generates a conversation key from the recipient's secret key and sender's public key.
func DecryptMessage(ciphertext string, recipientSK nostr.SecretKey, senderPK nostr.PubKey) (string, error) {
	convKey, err := nip44.GenerateConversationKey(senderPK, recipientSK)
	if err != nil {
		return "", fmt.Errorf("failed to generate conversation key: %w", err)
	}

	plaintext, err := nip44.Decrypt(ciphertext, convKey)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt: %w", err)
	}

	return plaintext, nil
}

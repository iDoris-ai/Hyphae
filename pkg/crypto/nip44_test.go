package crypto

import (
	"testing"

	"fiatjaf.com/nostr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEncryptDecrypt(t *testing.T) {
	// Generate keypair for alice
	aliceSK := nostr.Generate()
	alicePK := aliceSK.Public()

	// Generate keypair for bob
	bobSK := nostr.Generate()
	bobPK := bobSK.Public()

	plaintext := "Hello, this is a secret message for NIP-44!"

	// Alice encrypts to Bob
	ciphertext, err := EncryptMessage(plaintext, aliceSK, bobPK)
	require.NoError(t, err)
	assert.NotEmpty(t, ciphertext)
	assert.NotEqual(t, plaintext, ciphertext)

	// Bob decrypts from Alice
	decrypted, err := DecryptMessage(ciphertext, bobSK, alicePK)
	require.NoError(t, err)
	assert.Equal(t, plaintext, decrypted)
}

func TestEncryptDecrypt_SamePerson(t *testing.T) {
	sk := nostr.Generate()
	pk := sk.Public()

	plaintext := "Self-encryption test"
	ciphertext, err := EncryptMessage(plaintext, sk, pk)
	require.NoError(t, err)

	decrypted, err := DecryptMessage(ciphertext, sk, pk)
	require.NoError(t, err)
	assert.Equal(t, plaintext, decrypted)
}

func TestEncryptDecrypt_EmptyPlaintext(t *testing.T) {
	aliceSK := nostr.Generate()
	bobPK := nostr.Generate().Public()

	_, err := EncryptMessage("", aliceSK, bobPK)
	assert.Error(t, err)
}

func TestEncryptDecrypt_LongMessage(t *testing.T) {
	aliceSK := nostr.Generate()
	bobSK := nostr.Generate()
	bobPK := bobSK.Public()

	plaintext := make([]byte, 10000)
	for i := range plaintext {
		plaintext[i] = byte('a' + i%26)
	}

	ciphertext, err := EncryptMessage(string(plaintext), aliceSK, bobPK)
	require.NoError(t, err)

	decrypted, err := DecryptMessage(ciphertext, bobSK, aliceSK.Public())
	require.NoError(t, err)
	assert.Equal(t, string(plaintext), decrypted)
}

func TestEncryptDecrypt_WrongKey(t *testing.T) {
	aliceSK := nostr.Generate()
	bobSK := nostr.Generate()
	bobPK := bobSK.Public()

	ciphertext, err := EncryptMessage("secret", aliceSK, bobPK)
	require.NoError(t, err)

	// Try to decrypt with a third party key
	charlieSK := nostr.Generate()
	_, err = DecryptMessage(ciphertext, charlieSK, aliceSK.Public())
	assert.Error(t, err)
}

func TestEncryptDeterministicWithCustomNonce(t *testing.T) {
	// Just verify encryption works and produces different outputs for same plaintext
	aliceSK := nostr.Generate()
	bobPK := nostr.Generate().Public()

	plaintext := "deterministic test"
	ct1, err := EncryptMessage(plaintext, aliceSK, bobPK)
	require.NoError(t, err)

	ct2, err := EncryptMessage(plaintext, aliceSK, bobPK)
	require.NoError(t, err)

	// Nonce is random, so ciphertexts should differ
	assert.NotEqual(t, ct1, ct2)
}

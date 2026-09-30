package crypto

import (
	"strings"
	"testing"

	"fiatjaf.com/nostr"
	"github.com/stretchr/testify/require"
)

func TestDecryptMessageEmptyPayloads(t *testing.T) {
	recipientSK := nostr.Generate()
	senderPK := nostr.Generate().Public()

	tests := []struct {
		name       string
		ciphertext string
	}{
		{name: "empty"},
		{name: "below minimum with CRLF", ciphertext: strings.Repeat("\r\n", 10)},
		{name: "132 CR characters", ciphertext: strings.Repeat("\r", 132)},
		{name: "132 LF characters", ciphertext: strings.Repeat("\n", 132)},
		{name: "132 CRLF characters", ciphertext: strings.Repeat("\r\n", 66)},
		{name: "long newline-only input", ciphertext: strings.Repeat("\r\n", 100)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plaintext, err := DecryptMessage(tt.ciphertext, recipientSK, senderPK)
			require.EqualError(t, err, "failed to decrypt: invalid payload length: 0")
			require.Empty(t, plaintext)
		})
	}
}

func TestDecryptMessageWrappedPayload(t *testing.T) {
	senderSK := nostr.Generate()
	recipientSK := nostr.Generate()
	const expected = "wrapped NIP-44 payload"

	ciphertext, err := EncryptMessage(expected, senderSK, recipientSK.Public())
	require.NoError(t, err)
	wrapped := ciphertext[:len(ciphertext)/2] + "\r\n" + ciphertext[len(ciphertext)/2:]

	plaintext, err := DecryptMessage(wrapped, recipientSK, senderSK.Public())
	require.NoError(t, err)
	require.Equal(t, expected, plaintext)
}

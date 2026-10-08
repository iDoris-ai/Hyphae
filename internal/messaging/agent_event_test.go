package messaging

import (
	"bytes"
	cryptorand "crypto/rand"
	"testing"

	"fiatjaf.com/nostr"
	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/pkg/crypto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildAgentMessageEventMatchesLegacyFields(t *testing.T) {
	sender := nostr.SecretKey{31: 1}
	recipient := nostr.SecretKey{31: 2}
	createdAt := nostr.Timestamp(1791417600)
	// NIP-44 and d tags are randomized. Replay the same entropy to compare the
	// pre-refactor construction byte for byte without changing production code.
	originalReader := cryptorand.Reader
	t.Cleanup(func() { cryptorand.Reader = originalReader })
	entropy := bytes.Repeat([]byte{1, 2, 3, 4, 5, 6, 7, 8}, 512)
	for _, plaintext := range []string{"direct message", "你好\n🌱", "hyphae.group/v1\n{\"type\":\"message\"}"} {
		t.Run(plaintext, func(t *testing.T) {
			cryptorand.Reader = bytes.NewReader(entropy)
			want := legacyEncryptedAgentEvent(t, sender, recipient.Public(), plaintext, createdAt)
			cryptorand.Reader = bytes.NewReader(entropy)
			got, err := BuildAgentMessageEvent(sender, recipient.Public(), plaintext, createdAt)
			require.NoError(t, err)
			require.Equal(t, want, got, "all event fields, including ID and signature, must match")
			assert.Equal(t, want.String(), got.String(), "serialized signed event must match byte for byte")
			require.NoError(t, ValidateAgentMessageEvent(got))
			assert.True(t, got.CheckID())
			assert.True(t, got.VerifySignature())
			decoded, encrypted, err := DecodeMessageContent(got, recipient)
			require.NoError(t, err)
			assert.True(t, encrypted)
			assert.Equal(t, plaintext, decoded)
		})
	}
}

// Snapshot of the encrypted construction shared by AgentMsgCmd and the TUI
// before S1. Keep it independent of BuildAgentMessageEvent for regression tests.
func legacyEncryptedAgentEvent(t *testing.T, sender nostr.SecretKey, recipient nostr.PubKey, plaintext string, createdAt nostr.Timestamp) *nostr.Event {
	t.Helper()
	encrypted, err := crypto.EncryptMessage(plaintext, sender, recipient)
	require.NoError(t, err)
	compressed, err := CompressText(encrypted)
	require.NoError(t, err)
	dTag, err := NewAgentMessageDTag(compressed, createdAt)
	require.NoError(t, err)
	tags := nostr.Tags{
		{"p", common.PubKeyToHex(recipient)},
		{"c", AgentTag},
		{"z", CompressTag},
		{"v", AgentVersion},
		{"d", dTag},
	}
	tags = append(tags, nostr.Tag{"enc", "nip44"})
	event := &nostr.Event{
		CreatedAt: createdAt,
		Kind:      AgentKind,
		Tags:      tags,
		Content:   compressed,
		PubKey:    sender.Public(),
	}
	require.NoError(t, ValidateAgentMessageEvent(event))
	require.NoError(t, event.Sign(sender))
	return event
}

func TestBuildAgentMessageEventUsesFreshNonces(t *testing.T) {
	sender := nostr.SecretKey{31: 1}
	recipient := nostr.SecretKey{31: 2}.Public()
	createdAt := nostr.Timestamp(1791417600)
	first, err := BuildAgentMessageEvent(sender, recipient, "same message", createdAt)
	require.NoError(t, err)
	second, err := BuildAgentMessageEvent(sender, recipient, "same message", createdAt)
	require.NoError(t, err)
	assert.NotEqual(t, first.Content, second.Content)
	assert.NotEqual(t, first.Tags.Find("d"), second.Tags.Find("d"))
	assert.NotEqual(t, first.ID, second.ID)
}

func TestBuildAgentMessageEventEncryptionErrors(t *testing.T) {
	sender := nostr.SecretKey{31: 1}
	for _, tc := range []struct {
		name      string
		recipient nostr.PubKey
		plaintext string
	}{
		{name: "invalid recipient", plaintext: "message"},
		{name: "empty plaintext", recipient: nostr.SecretKey{31: 2}.Public()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			event, err := BuildAgentMessageEvent(sender, tc.recipient, tc.plaintext, 1)
			require.ErrorContains(t, err, "failed to encrypt")
			assert.Nil(t, event)
		})
	}
}

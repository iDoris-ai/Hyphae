package messaging

import (
	"encoding/base64"
	"strings"
	"testing"

	"fiatjaf.com/nostr"
	"github.com/iDoris-ai/hyphae/pkg/crypto"
	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDecodeMessageContentPlainAndCompressedLegacy(t *testing.T) {
	plain := &nostr.Event{Content: "ordinary message"}
	got, encrypted, err := DecodeMessageContent(plain, nostr.SecretKey{})
	require.NoError(t, err)
	assert.Equal(t, "ordinary message", got)
	assert.False(t, encrypted)

	compressed, err := CompressText("compressed message")
	require.NoError(t, err)
	for name, tags := range map[string]nostr.Tags{
		"tagged":          {{"z", CompressTag}},
		"legacy untagged": nil,
	} {
		t.Run(name, func(t *testing.T) {
			event := &nostr.Event{Content: compressed, Tags: tags}
			got, encrypted, err := DecodeMessageContent(event, nostr.SecretKey{})
			require.NoError(t, err)
			assert.Equal(t, "compressed message", got)
			assert.False(t, encrypted)
		})
	}
}

func TestDecodeMessageContentAcceptsZstdSkippableFrame(t *testing.T) {
	compressed, err := CompressText("with skippable frame")
	require.NoError(t, err)
	frame, err := base64.StdEncoding.DecodeString(compressed)
	require.NoError(t, err)
	skippable := []byte{0x50, 0x2a, 0x4d, 0x18, 0x04, 0x00, 0x00, 0x00, 1, 2, 3, 4}
	content := base64.StdEncoding.EncodeToString(append(skippable, frame...))

	for name, tags := range map[string]nostr.Tags{
		"tagged":          {{"z", CompressTag}},
		"legacy untagged": nil,
	} {
		t.Run(name, func(t *testing.T) {
			got, _, err := DecodeMessageContent(&nostr.Event{Content: content, Tags: tags}, nostr.SecretKey{})
			require.NoError(t, err)
			assert.Equal(t, "with skippable frame", got)
		})
	}
}

func TestDecodeMessageContentNIP44(t *testing.T) {
	recipient := nostr.Generate()
	sender := nostr.Generate()
	plaintext := "private message"
	ciphertext, err := crypto.EncryptMessage(plaintext, sender, recipient.Public())
	require.NoError(t, err)
	compressed, err := CompressText(ciphertext)
	require.NoError(t, err)
	event := &nostr.Event{Content: compressed, PubKey: sender.Public(), Tags: nostr.Tags{{"enc", "nip44"}, {"z", CompressTag}}}
	got, encrypted, err := DecodeMessageContent(event, recipient)
	require.NoError(t, err)
	assert.Equal(t, plaintext, got)
	assert.True(t, encrypted)

	event.Content = "invalid ciphertext"
	event.Tags = nostr.Tags{{"enc", "nip44"}}
	got, encrypted, err = DecodeMessageContent(event, recipient)
	require.Error(t, err)
	assert.Empty(t, got, "failed decrypt must not return ciphertext as plaintext")
	assert.True(t, encrypted)
}

func TestDecodeMessageContentRejectsInvalidTags(t *testing.T) {
	cases := []struct {
		name string
		tags nostr.Tags
	}{
		{name: "unknown encryption", tags: nostr.Tags{{"enc", "future"}}},
		{name: "malformed encryption", tags: nostr.Tags{{"enc"}}},
		{name: "conflicting encryption", tags: nostr.Tags{{"enc", "nip44"}, {"enc", "future"}}},
		{name: "unknown compression", tags: nostr.Tags{{"z", "gzip"}}},
		{name: "malformed compression", tags: nostr.Tags{{"z"}}},
		{name: "conflicting compression", tags: nostr.Tags{{"z", "zstd"}, {"z", "gzip"}}},
		{name: "tagged invalid zstd", tags: nostr.Tags{{"z", CompressTag}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := DecodeMessageContent(&nostr.Event{Content: "not compressed", Tags: tc.tags}, nostr.SecretKey{})
			require.Error(t, err)
		})
	}
}

func TestDecodeMessageContentLimits(t *testing.T) {
	_, _, err := DecodeMessageContent(&nostr.Event{Content: strings.Repeat("x", maxIncomingContentBytes+1)}, nostr.SecretKey{})
	require.ErrorContains(t, err, "2 MiB")
	_, _, err = DecodeMessageContent(&nostr.Event{Content: strings.Repeat("x", maxIncomingBodyBytes+1)}, nostr.SecretKey{})
	require.ErrorContains(t, err, "1 MiB")

	compressed, err := CompressText(strings.Repeat("x", maxIncomingBodyBytes+1))
	require.NoError(t, err)
	_, _, err = DecodeMessageContent(&nostr.Event{Content: compressed, Tags: nostr.Tags{{"z", CompressTag}}}, nostr.SecretKey{})
	require.Error(t, err, "oversized decompression must fail rather than fall back to raw content")
}

func TestDecodeMessageContentRejectsOversizedZstdWindow(t *testing.T) {
	encoder, err := zstd.NewWriter(nil, zstd.WithEncoderConcurrency(1), zstd.WithWindowSize(16<<20))
	require.NoError(t, err)
	frame := encoder.EncodeAll([]byte(strings.Repeat("x", 9<<20)), nil)
	encoder.Close()
	encoded := base64.StdEncoding.EncodeToString(frame)
	_, _, err = DecodeMessageContent(&nostr.Event{Content: encoded, Tags: nostr.Tags{{"z", CompressTag}}}, nostr.SecretKey{})
	require.Error(t, err)
}

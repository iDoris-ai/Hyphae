package messaging

import (
	"encoding/hex"
	"strings"
	"testing"

	"fiatjaf.com/nostr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompressText(t *testing.T) {
	original := "Hello, this is a test message for compression!"
	compressed, err := CompressText(original)
	require.NoError(t, err)
	assert.NotEmpty(t, compressed)
	assert.NotEqual(t, original, compressed)
}

func TestDecompressText(t *testing.T) {
	original := "Hello, this is a test message for compression!"
	compressed, err := CompressText(original)
	require.NoError(t, err)

	decompressed, err := DecompressText(compressed)
	require.NoError(t, err)
	assert.Equal(t, original, decompressed)
}

func TestCompressDecompress_EmptyString(t *testing.T) {
	original := ""
	compressed, err := CompressText(original)
	require.NoError(t, err)

	decompressed, err := DecompressText(compressed)
	require.NoError(t, err)
	assert.Equal(t, original, decompressed)
}

func TestCompressDecompress_LongText(t *testing.T) {
	original := strings.Repeat("The quick brown fox jumps over the lazy dog. ", 1000)
	compressed, err := CompressText(original)
	require.NoError(t, err)

	decompressed, err := DecompressText(compressed)
	require.NoError(t, err)
	assert.Equal(t, original, decompressed)
}

func TestDecompressText_InvalidBase64(t *testing.T) {
	_, err := DecompressText("!!!invalid!!!")
	assert.Error(t, err)
}

func TestDecompressText_InvalidZstd(t *testing.T) {
	_, err := DecompressText("aGVsbG8=") // valid base64, invalid zstd
	assert.Error(t, err)
}

// TestDeriveMessageDTag_UniqueEvenForIdenticalContentAndTimestamp is the
// CC-82 regression test: kind 30078 is NIP-01 addressable, so a compliant
// relay collapses events sharing the same (pubkey, kind, d) coordinate.
// Before this fix, agent msg carried no "d" tag at all -- every message from
// the same sender shared the implicit d="" coordinate and would evict the
// previous one on such a relay. This asserts the pathological worst case
// (byte-identical content, same-second timestamp -- e.g. a retry with
// encryption disabled) still produces distinct d values, so consecutive
// messages from one sender keep separate coordinates and don't overwrite
// each other.
func TestDeriveMessageDTag_UniqueEvenForIdenticalContentAndTimestamp(t *testing.T) {
	content := "identical-payload"
	ts := nostr.Now()

	first, err := deriveMessageDTag(content, ts)
	require.NoError(t, err)
	second, err := deriveMessageDTag(content, ts)
	require.NoError(t, err)

	assert.NotEqual(t, first, second)
}

func TestDeriveMessageDTag_ReturnsValidHex(t *testing.T) {
	d, err := deriveMessageDTag("some content", nostr.Now())
	require.NoError(t, err)

	assert.Len(t, d, 16)
	_, err = hex.DecodeString(d)
	assert.NoError(t, err)
}

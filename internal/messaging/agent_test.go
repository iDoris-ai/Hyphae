package messaging

import (
	"strings"
	"testing"

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

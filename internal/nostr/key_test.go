package nostr

import (
	"context"
	"testing"

	"fiatjaf.com/nostr"
	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKeyGenerateCmd(t *testing.T) {
	out := captureStdout(t, func() {
		require.NoError(t, KeyCmd.Run(context.Background(), []string{"key", "generate"}))
	})
	assert.Contains(t, out, "Generated new key pair")
	assert.Contains(t, out, "Private key (nsec):")
	assert.Contains(t, out, "Public key (npub):")
}

func TestKeyPublicCmd(t *testing.T) {
	sk := nostr.Generate()
	pk := sk.Public()

	out := captureStdout(t, func() {
		require.NoError(t, KeyCmd.Run(context.Background(), []string{"key", "public", "--sec", sk.Hex()}))
	})
	assert.Contains(t, out, pk.Hex())
	assert.Contains(t, out, common.EncodeNpub(pk))
}

func TestKeyPublicCmd_InvalidSecretKey(t *testing.T) {
	err := KeyCmd.Run(context.Background(), []string{"key", "public", "--sec", "not-a-valid-key"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid secret key")
}

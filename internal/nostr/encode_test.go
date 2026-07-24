package nostr

import (
	"context"
	"testing"

	"fiatjaf.com/nostr"
	"github.com/AuraAIHQ/agent-speaker/internal/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEncodeCmd_Npub(t *testing.T) {
	pk := nostr.Generate().Public()

	out := captureStdout(t, func() {
		require.NoError(t, EncodeCmd.Run(context.Background(), []string{"encode", "--prefix", "npub", "--hex", pk.Hex()}))
	})
	assert.Equal(t, common.EncodeNpub(pk)+"\n", out)
}

func TestEncodeCmd_Nsec(t *testing.T) {
	sk := nostr.Generate()

	out := captureStdout(t, func() {
		require.NoError(t, EncodeCmd.Run(context.Background(), []string{"encode", "--prefix", "nsec", "--hex", sk.Hex()}))
	})
	assert.Equal(t, common.EncodeNsec(sk)+"\n", out)
}

func TestEncodeCmd_CaseInsensitivePrefix(t *testing.T) {
	pk := nostr.Generate().Public()

	out := captureStdout(t, func() {
		require.NoError(t, EncodeCmd.Run(context.Background(), []string{"encode", "--prefix", "NPUB", "--hex", pk.Hex()}))
	})
	assert.Equal(t, common.EncodeNpub(pk)+"\n", out)
}

func TestEncodeCmd_InvalidHex(t *testing.T) {
	err := EncodeCmd.Run(context.Background(), []string{"encode", "--prefix", "npub", "--hex", "not-hex"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid hex")
}

func TestEncodeCmd_WrongLength(t *testing.T) {
	err := EncodeCmd.Run(context.Background(), []string{"encode", "--prefix", "npub", "--hex", "abcd"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid public key length")
}

func TestEncodeCmd_UnsupportedPrefix(t *testing.T) {
	pk := nostr.Generate().Public()
	err := EncodeCmd.Run(context.Background(), []string{"encode", "--prefix", "note", "--hex", pk.Hex()})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported prefix")
}

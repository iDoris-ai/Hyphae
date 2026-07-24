package nostr

import (
	"context"
	"testing"

	"fiatjaf.com/nostr"
	"github.com/AuraAIHQ/agent-speaker/internal/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDecodeCmd_ValidNpub(t *testing.T) {
	pk := nostr.Generate().Public()
	npub := common.EncodeNpub(pk)

	out := captureStdout(t, func() {
		require.NoError(t, DecodeCmd.Run(context.Background(), []string{"decode", "--input", npub}))
	})
	assert.Contains(t, out, "Prefix: npub")
	assert.Contains(t, out, pk.Hex())
}

func TestDecodeCmd_EmptyInputErrors(t *testing.T) {
	err := DecodeCmd.Run(context.Background(), []string{"decode", "--input", ""})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "input is required")
}

func TestDecodeCmd_MalformedInputErrors(t *testing.T) {
	err := DecodeCmd.Run(context.Background(), []string{"decode", "--input", "not-a-bech32-string!!"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to decode")
}

package nostr

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRelayInfoCmd_UnreachableRelayErrors(t *testing.T) {
	err := RelayCmd.Run(context.Background(), []string{"relay", "info", "ws://127.0.0.1:1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to connect")
}

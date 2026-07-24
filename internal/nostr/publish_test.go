package nostr

import (
	"context"
	"testing"

	"fiatjaf.com/nostr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPublishCmd_EmptyJSONErrors(t *testing.T) {
	sk := nostr.Generate()
	err := PublishCmd.Run(context.Background(), []string{"publish", "--sec", sk.Hex(), ""})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "JSON event is required")
}

func TestPublishCmd_MalformedJSONErrors(t *testing.T) {
	sk := nostr.Generate()
	err := PublishCmd.Run(context.Background(), []string{"publish", "--sec", sk.Hex(), "{not json"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid JSON")
}

func TestPublishCmd_InvalidSecretKeyErrors(t *testing.T) {
	err := PublishCmd.Run(context.Background(), []string{"publish", "--sec", "not-a-key", `{"kind":1,"content":"hi"}`})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid secret key")
}

// TestPublishCmd_UnreachableRelayReportsFailure exercises the full success
// path up through the relay-publish attempt (no network needed -- the
// relay URL is unreachable and fails fast) rather than only the early
// validation-error paths above.
func TestPublishCmd_UnreachableRelayReportsFailure(t *testing.T) {
	sk := nostr.Generate()

	out := captureStdout(t, func() {
		require.NoError(t, PublishCmd.Run(context.Background(), []string{
			"publish", "--sec", sk.Hex(), "--relay", "ws://127.0.0.1:1", `{"kind":1,"content":"hi"}`,
		}))
	})
	assert.Contains(t, out, "❌")
	assert.Contains(t, out, "Published to 0/1 relays")
}

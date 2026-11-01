package nostr

import (
	"context"
	"encoding/json"
	"testing"

	"fiatjaf.com/nostr"
	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEventCmd_JSONOnlyDoesNotPublish exercises EventCmd's --json path,
// which returns before ever touching the network -- covers secret key
// parsing, both tag-flag forms (key=value and a bare key), event
// construction, and signing.
func TestEventCmd_JSONOnlyDoesNotPublish(t *testing.T) {
	sk := nostr.Generate()

	out := captureStdout(t, func() {
		require.NoError(t, EventCmd.Run(context.Background(), []string{
			"event",
			"--sec", sk.Hex(),
			"--kind", "1",
			"--content", "hello world",
			"--tag", "foo=bar",
			"--tag", "standalone",
			"--json",
		}))
	})

	var event nostr.Event
	require.NoError(t, json.Unmarshal([]byte(out), &event))
	assert.Equal(t, "hello world", event.Content)
	assert.Equal(t, nostr.Kind(1), event.Kind)
	assert.True(t, event.VerifySignature(), "the printed event must be validly signed")
	assert.Contains(t, event.Tags, nostr.Tag{"foo", "bar"})
	assert.Contains(t, event.Tags, nostr.Tag{"standalone"})
}

func TestEventCmd_InvalidSecretKey(t *testing.T) {
	err := EventCmd.Run(context.Background(), []string{"event", "--sec", "not-a-key", "--json"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid secret key")
}

// A publish that no relay accepts must fail (non-zero exit), not print "0/1" and exit 0.
func TestEventCmd_UnreachableRelayFails(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	sk := nostr.Generate()
	var runErr error
	out := captureStdout(t, func() {
		runErr = EventCmd.Run(context.Background(), []string{
			"event", "--sec", sk.Hex(), "--kind", "1", "--content", "hi", "--relay", "ws://127.0.0.1:1",
		})
	})
	assert.Contains(t, out, "Published to 0/1 relays")
	// plain 64-char hex id, not the library's debug form "id::<hex>"
	assert.Regexp(t, `Event ID: [0-9a-f]{64}\n`, out)
	assert.NotContains(t, out, "id::")
	var exitErr *common.ExitError
	require.ErrorAs(t, runErr, &exitErr)
	assert.Equal(t, common.ErrCodeNetwork, exitErr.Code)
}

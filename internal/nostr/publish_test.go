package nostr

import (
	"context"
	"testing"

	"fiatjaf.com/nostr"
	"github.com/iDoris-ai/hyphae/internal/relayconfig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func freshPublishCmd() *cli.Command {
	cmd := *PublishCmd
	cmd.Flags = []cli.Flag{
		&cli.StringFlag{Name: "sec", Aliases: []string{"s"}, Required: true},
		&cli.StringSliceFlag{Name: "relay", Aliases: []string{"r"}},
	}
	return &cmd
}

func TestPublishCmd_EmptyJSONErrors(t *testing.T) {
	sk := nostr.Generate()
	err := freshPublishCmd().Run(context.Background(), []string{"publish", "--sec", sk.Hex(), ""})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "JSON event is required")
}

func TestPublishUsesConfiguredRelayAndExplicitOverride(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	configURL := "ws://127.0.0.1:1"
	explicitURL := "ws://127.0.0.1:2"
	r, err := relayconfig.New()
	require.NoError(t, err)
	require.NoError(t, r.Set([]string{configURL}))
	sk := nostr.Generate()
	args := []string{"publish", "--sec", sk.Hex(), `{"kind":1,"content":"hello"}`}
	out := captureStdout(t, func() { require.NoError(t, freshPublishCmd().Run(context.Background(), args)) })
	assert.Contains(t, out, configURL)
	assert.NotContains(t, out, explicitURL)
	args = []string{"publish", "--sec", sk.Hex(), "--relay", explicitURL, `{"kind":1,"content":"hello"}`}
	out = captureStdout(t, func() { require.NoError(t, freshPublishCmd().Run(context.Background(), args)) })
	assert.Contains(t, out, explicitURL)
	assert.NotContains(t, out, configURL)
}

func TestPublishCmd_MalformedJSONErrors(t *testing.T) {
	sk := nostr.Generate()
	err := freshPublishCmd().Run(context.Background(), []string{"publish", "--sec", sk.Hex(), "{not json"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid JSON")
}

func TestPublishCmd_InvalidSecretKeyErrors(t *testing.T) {
	err := freshPublishCmd().Run(context.Background(), []string{"publish", "--sec", "not-a-key", `{"kind":1,"content":"hi"}`})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid secret key")
}

// TestPublishCmd_UnreachableRelayReportsFailure exercises the full success
// path up through the relay-publish attempt (no network needed -- the
// relay URL is unreachable and fails fast) rather than only the early
// validation-error paths above.
func TestPublishCmd_UnreachableRelayReportsFailure(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	sk := nostr.Generate()

	out := captureStdout(t, func() {
		require.NoError(t, freshPublishCmd().Run(context.Background(), []string{
			"publish", "--sec", sk.Hex(), "--relay", "ws://127.0.0.1:1", `{"kind":1,"content":"hi"}`,
		}))
	})
	assert.Contains(t, out, "❌")
	assert.Contains(t, out, "Published to 0/1 relays")
}

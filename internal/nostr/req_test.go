package nostr

import (
	"context"
	"testing"

	"fiatjaf.com/nostr"
	"github.com/AuraAIHQ/agent-speaker/internal/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReqCmd_UnreachableRelayFindsNothing covers filter construction (kinds,
// authors) and the connect-failure skip path, without needing a live relay.
func TestReqCmd_UnreachableRelayFindsNothing(t *testing.T) {
	pk := nostr.Generate().Public()

	out := captureStdout(t, func() {
		require.NoError(t, ReqCmd.Run(context.Background(), []string{
			"req",
			"--relay", "ws://127.0.0.1:1",
			"--kinds", "1",
			"--authors", common.EncodeNpub(pk),
			"--limit", "5",
		}))
	})
	assert.Contains(t, out, "connection failed")
	assert.Contains(t, out, "Found 0 events")
}

// TestReqCmd_InvalidAuthorIsSilentlyIgnored covers the "authors" parsing
// branch's error path: an unparseable author string is dropped rather than
// erroring out (per req.go's `if err == nil { filter.Authors = append(...) }`).
func TestReqCmd_InvalidAuthorIsSilentlyIgnored(t *testing.T) {
	out := captureStdout(t, func() {
		require.NoError(t, ReqCmd.Run(context.Background(), []string{
			"req", "--relay", "ws://127.0.0.1:1", "--authors", "not-a-valid-npub",
		}))
	})
	assert.Contains(t, out, "Found 0 events")
}

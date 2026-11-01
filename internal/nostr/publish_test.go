package nostr

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"fiatjaf.com/nostr"
	"github.com/coder/websocket"
	"github.com/iDoris-ai/hyphae/internal/common"
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
	out := captureStdout(t, func() { require.Error(t, freshPublishCmd().Run(context.Background(), args)) })
	assert.Contains(t, out, configURL)
	assert.NotContains(t, out, explicitURL)
	args = []string{"publish", "--sec", sk.Hex(), "--relay", explicitURL, `{"kind":1,"content":"hello"}`}
	out = captureStdout(t, func() { require.Error(t, freshPublishCmd().Run(context.Background(), args)) })
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

	var runErr error
	out := captureStdout(t, func() {
		runErr = freshPublishCmd().Run(context.Background(), []string{
			"publish", "--sec", sk.Hex(), "--relay", "ws://127.0.0.1:1", `{"kind":1,"content":"hi"}`,
		})
	})
	assert.Contains(t, out, "❌")
	assert.Contains(t, out, "Published to 0/1 relays")
	var exitErr *common.ExitError
	require.ErrorAs(t, runErr, &exitErr, "no relay accepted the event: the command must fail, not exit 0")
	assert.Equal(t, common.ErrCodeNetwork, exitErr.Code)
}

// okRelay answers every EVENT with ["OK", id, accept, reason].
func okRelay(t *testing.T, accept bool) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		for {
			_, data, err := conn.Read(context.Background())
			if err != nil {
				return
			}
			var msg []json.RawMessage
			if json.Unmarshal(data, &msg) != nil || len(msg) < 2 {
				continue
			}
			var verb string
			_ = json.Unmarshal(msg[0], &verb)
			if verb != "EVENT" {
				continue
			}
			var ev struct {
				ID string `json:"id"`
			}
			_ = json.Unmarshal(msg[1], &ev)
			reply, _ := json.Marshal([]any{"OK", ev.ID, accept, "blocked: test"})
			_ = conn.Write(context.Background(), websocket.MessageText, reply)
		}
	}))
	t.Cleanup(server.Close)
	return "ws" + strings.TrimPrefix(server.URL, "http")
}

func TestPublishCmd_ExitStatusFollowsRelayAcceptance(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	sk := nostr.Generate()
	run := func(relays ...string) error {
		args := []string{"publish", "--sec", sk.Hex()}
		for _, r := range relays {
			args = append(args, "--relay", r)
		}
		args = append(args, `{"kind":1,"content":"hi"}`)
		var err error
		captureStdout(t, func() { err = freshPublishCmd().Run(context.Background(), args) })
		return err
	}
	accepting, rejecting := okRelay(t, true), okRelay(t, false)

	require.NoError(t, run(accepting), "accepted by the only relay")
	require.NoError(t, run("ws://127.0.0.1:1", accepting), "partial success stays exit 0")

	var exitErr *common.ExitError
	require.ErrorAs(t, run(rejecting), &exitErr, "a relay that answers OK=false did not accept the event")
	assert.Equal(t, common.ErrCodeNetwork, exitErr.Code)
}

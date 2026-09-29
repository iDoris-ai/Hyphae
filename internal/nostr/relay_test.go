package nostr

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRelayInfoCmd_UnreachableRelayErrors(t *testing.T) {
	err := RelayCmd.Run(context.Background(), []string{"relay", "info", "ws://127.0.0.1:1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to connect")
	var exitErr *common.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, common.ErrCodeNetwork, exitErr.Code)
}

func TestRelayInfoClosesCanceledProbeConnections(t *testing.T) {
	const childEnv = "HYPHAE_RELAY_INFO_LEAK_TEST_CHILD"
	if os.Getenv(childEnv) == "1" {
		runtime.GC()
		time.Sleep(50 * time.Millisecond)
		runtime.GC()
		baseline := runtime.NumGoroutine()

		for range 30 {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			err := RelayCmd.Run(ctx, []string{"relay", "info", "--timeout", "2", "ws://127.0.0.1:1"})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "failed to connect")
		}

		assert.Eventually(t, func() bool {
			return runtime.NumGoroutine()-baseline < 5
		}, 3*time.Second, 10*time.Millisecond, "canceled relay probes left goroutines behind")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestRelayInfoClosesCanceledProbeConnections$")
	cmd.Env = append(os.Environ(), childEnv+"=1", "HOME="+t.TempDir())
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("relay leak test child timed out: %s", output)
	}
	if err != nil {
		t.Fatalf("relay leak test child failed: %v\n%s", err, output)
	}
}

func TestRelayInfoRejectsInvalidTimeout(t *testing.T) {
	err := RelayCmd.Run(context.Background(), []string{"relay", "info", "--timeout", "0", "ws://127.0.0.1:1"})
	require.Error(t, err)
	var exitErr *common.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, common.ErrCodeUser, exitErr.Code)
}

func TestRelaySetListJSON(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("HYPHAE_OUTPUT", "json")
	setOut := captureStdout(t, func() {
		require.NoError(t, RelayCmd.Run(context.Background(), []string{"relay", "set", "--relay", "wss://one.example", "--relay", "ws://two.example"}))
	})
	assert.JSONEq(t, `{"ok":true,"data":{"relays":["wss://one.example","ws://two.example"],"source":"config"}}`, strings.TrimSpace(setOut))
	listOut := captureStdout(t, func() { require.NoError(t, RelayCmd.Run(context.Background(), []string{"relay", "list"})) })
	assert.JSONEq(t, `{"ok":true,"data":{"relays":["wss://one.example","ws://two.example"],"source":"config"}}`, strings.TrimSpace(listOut))
}

func TestRelayInfoJSONConnectionResult(t *testing.T) {
	t.Setenv("HYPHAE_OUTPUT", "json")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		_, _, _ = conn.Read(context.Background())
	}))
	defer server.Close()
	url := "ws" + strings.TrimPrefix(server.URL, "http")
	out := captureStdout(t, func() {
		require.NoError(t, RelayCmd.Run(context.Background(), []string{"relay", "info", "--timeout", "2", url}))
	})
	assert.JSONEq(t, `{"ok":true,"data":{"url":"`+url+`","connected":true}}`, strings.TrimSpace(out))
}

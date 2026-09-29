package daemon

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"github.com/gorilla/websocket"
	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/internal/identity"
	"github.com/iDoris-ai/hyphae/internal/messaging"
	"github.com/iDoris-ai/hyphae/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestValidateDaemonIntervals(t *testing.T) {
	maxInt := int64(^uint64(0) >> 1)
	tests := []struct {
		name      string
		retry     int64
		watch     int64
		wantError string
	}{
		{name: "valid", retry: 1, watch: 30},
		{name: "zero retry", retry: 0, watch: 30, wantError: "retry-interval"},
		{name: "negative watch", retry: 60, watch: -1, wantError: "watch-interval"},
		{name: "retry overflow", retry: maxInt, watch: 30, wantError: "retry-interval"},
		{name: "watch overflow", retry: 60, watch: maxInt, wantError: "watch-interval"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			retry, watch, err := validateDaemonIntervals(tc.retry, tc.watch)
			if tc.wantError != "" {
				require.ErrorContains(t, err, tc.wantError)
				assert.Zero(t, retry)
				assert.Zero(t, watch)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, time.Second, retry)
			assert.Equal(t, 30*time.Second, watch)
		})
	}
}

func TestDaemonInvalidIntervalsFailBeforeTouchingHomeAndEmitJSONError(t *testing.T) {
	maxInt := int64(^uint64(0) >> 1)
	tests := []struct {
		name string
		args []string
	}{
		{name: "zero", args: []string{"--retry-interval", "0"}},
		{name: "duration overflow", args: []string{"--watch-interval", strconv.FormatInt(maxInt, 10)}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			args := append([]string{"hyphae", "--json", "daemon"}, tc.args...)
			stdout, stderr, err := runDaemonCommandCaptured(args)
			require.Error(t, err)
			assert.Empty(t, stdout, "JSON mode must not print human or usage text to stdout")
			var result common.Result
			require.NoError(t, json.Unmarshal([]byte(stderr), &result), "stderr should contain only a JSON error envelope")
			assert.False(t, result.OK)
			assert.Equal(t, common.ErrCodeUser, result.Error)
			assert.ErrorIs(t, statKeystoreDir(home), os.ErrNotExist, "interval validation must happen before loading the keystore")
		})
	}
}

func runDaemonCommandCaptured(args []string) (string, string, error) {
	stdoutRead, stdoutWrite, _ := os.Pipe()
	stderrRead, stderrWrite, _ := os.Pipe()
	originalStdout, originalStderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = stdoutWrite, stderrWrite
	app := &cli.Command{
		Name:     "hyphae",
		Flags:    []cli.Flag{&cli.BoolFlag{Name: "json"}},
		Commands: []*cli.Command{DaemonCmd},
	}
	err := app.Run(context.Background(), args)
	if err != nil {
		common.EmitError(common.JSONModeFromArgs(args), err)
	}
	_ = stdoutWrite.Close()
	_ = stderrWrite.Close()
	os.Stdout, os.Stderr = originalStdout, originalStderr
	stdout, _ := io.ReadAll(stdoutRead)
	stderr, _ := io.ReadAll(stderrRead)
	_ = stdoutRead.Close()
	_ = stderrRead.Close()
	return string(stdout), string(stderr), err
}

func statKeystoreDir(home string) error {
	_, err := os.Stat(filepath.Join(home, identity.KeyStoreDirName))
	return err
}

func TestDaemonProcessStopsOnSIGTERMWhileRelayIsStalled(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	ks := &types.KeyStore{Identities: make(map[string]*types.Identity), Contacts: make(map[string]*types.Contact)}
	_, err := identity.CreateIdentityWithPassword(ks, "alice", "daemon-test-password")
	require.NoError(t, err)
	ks.MasterKey = nil
	require.NoError(t, identity.SaveKeyStore(ks))

	reqReceived := make(chan struct{}, 1)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
		_, payload, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if strings.HasPrefix(string(payload), `["REQ"`) {
			select {
			case reqReceived <- struct{}{}:
			default:
			}
		}
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer server.Close()
	relayURL := "ws" + strings.TrimPrefix(server.URL, "http")

	command := exec.Command(os.Args[0], "-test.run=^TestDaemonSignalChild$")
	command.Stdin = strings.NewReader("daemon-test-password\n")
	command.Env = append(os.Environ(),
		"HOME="+home,
		"HYPHAE_DAEMON_SIGNAL_CHILD=1",
		"HYPHAE_DAEMON_SIGNAL_RELAY="+relayURL,
	)
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	require.NoError(t, command.Start())
	wait := make(chan error, 1)
	go func() { wait <- command.Wait() }()
	select {
	case <-reqReceived:
	case <-time.After(5 * time.Second):
		_ = command.Process.Kill()
		<-wait
		t.Fatalf("daemon child did not subscribe to the stalled relay; output: %s", output.String())
	}
	require.NoError(t, command.Process.Signal(syscall.SIGTERM))
	select {
	case err := <-wait:
		require.NoError(t, err, output.String())
	case <-time.After(2 * time.Second):
		_ = command.Process.Kill()
		<-wait
		t.Fatalf("daemon child did not stop promptly after SIGTERM; output: %s", output.String())
	}
}

func TestDaemonSignalChild(t *testing.T) {
	if os.Getenv("HYPHAE_DAEMON_SIGNAL_CHILD") != "1" {
		return
	}
	relayURL := os.Getenv("HYPHAE_DAEMON_SIGNAL_RELAY")
	app := &cli.Command{Name: "hyphae", Commands: []*cli.Command{DaemonCmd}}
	args := []string{"hyphae", "daemon", "--relay", relayURL, "--retry-interval", "3600", "--watch-interval", "3600", "--notify=false", "--password-stdin"}
	if err := app.Run(context.Background(), args); err != nil {
		t.Fatalf("daemon command failed: %v", err)
	}
}

func TestProcessOutboxDoesNothingAfterCancellation(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	event := &nostr.Event{Kind: 1, Content: "retry"}
	event.ID = [32]byte{4}
	eventJSON, err := json.Marshal(event)
	require.NoError(t, err)
	require.NoError(t, messaging.SaveOutbox(&types.Outbox{Entries: []types.OutboxEntry{{
		ID: hex.EncodeToString(event.ID[:]), EventJSON: string(eventJSON), Status: "pending", RetryCount: 0, MaxRetries: 10,
	}}}))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	processOutbox(ctx, &types.Identity{Nickname: "alice"}, []string{"ws://127.0.0.1:1"})
	outbox, err := messaging.LoadOutbox()
	require.NoError(t, err)
	require.Len(t, outbox.Entries, 1)
	assert.Zero(t, outbox.Entries[0].RetryCount, "shutdown must not consume retry budget for unattempted entries")
}

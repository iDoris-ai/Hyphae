package profile

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"github.com/gorilla/websocket"
	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/internal/identity"
	"github.com/iDoris-ai/hyphae/internal/relay"
	"github.com/iDoris-ai/hyphae/pkg/types"
	"github.com/stretchr/testify/require"
)

func TestProfilePublishPasswordStdinCLI(t *testing.T) {
	cli := buildProfilePasswordCLITestBinary(t)
	localRelay := startProfilePasswordCLIRelay(t)

	t.Run("encrypted identities unlock only from opted-in valid stdin", func(t *testing.T) {
		const password = " profile password with spaces  "
		home, publicKeys := createEncryptedProfilePasswordHome(t, password)
		beforeAuthAttempts := localRelay.requests.Load()

		failureCases := []struct {
			name         string
			passwordFlag bool
			stdin        string
			secret       string
		}{
			{name: "missing opt-in", stdin: password + "\n", secret: password},
			{name: "wrong password", passwordFlag: true, stdin: "wrong profile secret\n", secret: "wrong profile secret"},
			{name: "empty stdin", passwordFlag: true, stdin: "\r\n"},
			{name: "oversized stdin", passwordFlag: true, stdin: strings.Repeat("x", 4097), secret: "xxxxxxxx"},
		}
		for _, tc := range failureCases {
			t.Run(tc.name, func(t *testing.T) {
				args := profilePasswordPublishArgs("bob", "must not publish", localRelay.url)
				if tc.passwordFlag {
					args = append(args, "--password-stdin")
				}
				result := runProfilePasswordCLI(t, cli, home, tc.stdin, args...)
				require.Equal(t, common.ExitAuthError, result.exitCode)
				require.Empty(t, result.stdout)
				var envelope common.Result
				require.NoError(t, json.Unmarshal([]byte(result.stderr), &envelope), "stderr should be one JSON error envelope")
				require.False(t, envelope.OK)
				require.Equal(t, common.ErrCodeAuth, envelope.Error)
				if tc.secret != "" {
					require.NotContains(t, result.stdout, tc.secret)
					require.NotContains(t, result.stderr, tc.secret)
				}
				require.Equal(t, beforeAuthAttempts, localRelay.requests.Load(), "auth failures must happen before any relay connection or publication")
			})
		}

		for _, tc := range []struct {
			nickname   string
			lineEnding string
			name       string
		}{
			{nickname: "bob", lineEnding: "\n", name: "Bob profile from LF stdin"},
			{nickname: "alice", lineEnding: "\r\n", name: "Alice profile from CRLF stdin"},
		} {
			args := profilePasswordPublishArgs(tc.nickname, tc.name, localRelay.url)
			args = append(args, "--password-stdin")
			result := runProfilePasswordCLI(t, cli, home, password+tc.lineEnding, args...)
			var envelope struct {
				OK   bool `json:"ok"`
				Data struct {
					Name        string `json:"name"`
					PublishedTo int    `json:"published_to"`
					RelayCount  int    `json:"relay_count"`
				} `json:"data"`
			}
			require.Equal(t, 0, result.exitCode, "stderr: %s", result.stderr)
			require.Empty(t, result.stderr)
			require.NoError(t, json.Unmarshal([]byte(result.stdout), &envelope), "stdout should contain one JSON success envelope")
			require.True(t, envelope.OK)
			require.Equal(t, tc.name, envelope.Data.Name)
			require.Equal(t, 1, envelope.Data.PublishedTo)
			require.Equal(t, 1, envelope.Data.RelayCount)
			require.NotContains(t, result.stdout, password)
			require.NotContains(t, result.stderr, password)

			event := queryProfilePasswordCLIRelay(t, localRelay.url, publicKeys[tc.nickname])
			assertPublishedProfile(t, event, publicKeys[tc.nickname], tc.name)
		}
	})

	t.Run("unencrypted identity does not read password stdin", func(t *testing.T) {
		home, publicKey := createUnencryptedProfilePasswordHome(t)
		args := profilePasswordPublishArgs("plain", "Unencrypted profile", localRelay.url)
		args = append(args, "--password-stdin")
		result := runProfilePasswordCLI(t, cli, home, strings.Repeat("bad-stdin", 600), args...)

		var envelope struct {
			OK   bool `json:"ok"`
			Data struct {
				Name        string `json:"name"`
				PublishedTo int    `json:"published_to"`
			} `json:"data"`
		}
		require.Equal(t, 0, result.exitCode, "bad stdin must not be treated as a password for an unencrypted store: %s", result.stderr)
		require.Empty(t, result.stderr)
		require.NoError(t, json.Unmarshal([]byte(result.stdout), &envelope), "stdout should contain one JSON success envelope")
		require.True(t, envelope.OK)
		require.Equal(t, "Unencrypted profile", envelope.Data.Name)
		require.Equal(t, 1, envelope.Data.PublishedTo)

		event := queryProfilePasswordCLIRelay(t, localRelay.url, publicKey)
		assertPublishedProfile(t, event, publicKey, "Unencrypted profile")
	})
}

func buildProfilePasswordCLITestBinary(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate profile CLI test source")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(source), "../.."))
	binary := filepath.Join(t.TempDir(), "hyphae")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "build", "-o", binary, "./cmd/hyphae")
	command.Dir = root
	command.Stdout = io.Discard
	var stderr strings.Builder
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("build CLI: %v (%s)", err, stderr.String())
	}
	return binary
}

type profilePasswordCLIResult struct {
	stdout   string
	stderr   string
	exitCode int
}

func runProfilePasswordCLI(t *testing.T, binary, home, stdin string, args ...string) profilePasswordCLIResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	commandArgs := append([]string{"--json"}, args...)
	command := exec.CommandContext(ctx, binary, commandArgs...)
	command.Env = profilePasswordCLIEnv(home)
	command.Stdin = strings.NewReader(stdin)
	var stdout, stderr strings.Builder
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	result := profilePasswordCLIResult{stdout: stdout.String(), stderr: stderr.String()}
	if err == nil {
		return result
	}
	if ctx.Err() != nil {
		t.Fatalf("profile CLI timed out: %v", ctx.Err())
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("run profile CLI: %v", err)
	}
	result.exitCode = exitErr.ExitCode()
	return result
}

func profilePasswordCLIEnv(home string) []string {
	env := make([]string, 0, len(os.Environ())+1)
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "HOME=") || strings.HasPrefix(entry, "HYPHAE_OUTPUT=") || strings.HasPrefix(entry, "AGENT_SPEAKER_OUTPUT=") {
			continue
		}
		env = append(env, entry)
	}
	return append(env, "HOME="+home)
}

func profilePasswordPublishArgs(nickname, name, relayURL string) []string {
	return []string{
		"profile", "publish", "--as", nickname, "--mode", "structured",
		"--name", name, "--description", "published from a headless CLI process",
		"--capability", "read-only:today", "--relay", relayURL,
	}
}

func createEncryptedProfilePasswordHome(t *testing.T, password string) (string, map[string]nostr.PubKey) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	store := &types.KeyStore{
		Identities: make(map[string]*types.Identity),
		Contacts:   make(map[string]*types.Contact),
	}
	publicKeys := make(map[string]nostr.PubKey)
	for _, nickname := range []string{"alice", "bob"} {
		if _, err := identity.CreateIdentityWithPassword(store, nickname, password); err != nil {
			t.Fatal(err)
		}
		secretKey, err := identity.GetSecretKey(store, nickname)
		if err != nil {
			t.Fatal(err)
		}
		publicKeys[nickname] = secretKey.Public()
	}
	store.MasterKey = nil
	if err := identity.SaveKeyStore(store); err != nil {
		t.Fatal(err)
	}
	return home, publicKeys
}

func createUnencryptedProfilePasswordHome(t *testing.T) (string, nostr.PubKey) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	store := &types.KeyStore{
		Identities: make(map[string]*types.Identity),
		Contacts:   make(map[string]*types.Contact),
	}
	if _, err := identity.CreateIdentity(store, "plain"); err != nil {
		t.Fatal(err)
	}
	secretKey, err := identity.GetSecretKey(store, "plain")
	if err != nil {
		t.Fatal(err)
	}
	return home, secretKey.Public()
}

type profilePasswordCLIRelay struct {
	url      string
	requests atomic.Int32
}

func startProfilePasswordCLIRelay(t *testing.T) *profilePasswordCLIRelay {
	t.Helper()
	handler, cleanup, err := relay.New(relay.Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	tracked := &profilePasswordCLIRelay{}
	trackedHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tracked.requests.Add(1)
		handler.ServeHTTP(w, r)
	})
	server := httptest.NewServer(trackedHandler)
	t.Cleanup(func() {
		server.Close()
		cleanup()
	})
	tracked.url = "ws" + strings.TrimPrefix(server.URL, "http")
	return tracked
}

func queryProfilePasswordCLIRelay(t *testing.T, relayURL string, author nostr.PubKey) nostr.Event {
	t.Helper()
	conn, _, err := websocket.DefaultDialer.Dial(relayURL, nil)
	if err != nil {
		t.Fatalf("connect to local relay: %v", err)
	}
	defer conn.Close()
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	const subscriptionID = "profile-password-stdin"
	filter := map[string]any{
		"kinds":   []int{ProfileKind},
		"authors": []string{author.Hex()},
		"#d":      []string{ProfileDTag},
		"#c":      []string{ProfileTag},
	}
	if err := conn.WriteJSON([]any{"REQ", subscriptionID, filter}); err != nil {
		t.Fatalf("query local relay: %v", err)
	}
	var matches []nostr.Event
	for {
		var message []json.RawMessage
		if err := conn.ReadJSON(&message); err != nil {
			t.Fatalf("read local relay query: %v", err)
		}
		if len(message) == 0 {
			continue
		}
		var command string
		if err := json.Unmarshal(message[0], &command); err != nil {
			t.Fatalf("decode local relay command: %v", err)
		}
		switch command {
		case "EVENT":
			if len(message) != 3 {
				t.Fatalf("unexpected EVENT response: %s", message)
			}
			var gotSubscription string
			if err := json.Unmarshal(message[1], &gotSubscription); err != nil {
				t.Fatal(err)
			}
			if gotSubscription != subscriptionID {
				t.Fatalf("EVENT subscription = %q, want %q", gotSubscription, subscriptionID)
			}
			var event nostr.Event
			if err := json.Unmarshal(message[2], &event); err != nil {
				t.Fatal(err)
			}
			matches = append(matches, event)
		case "EOSE":
			if len(message) != 2 {
				t.Fatalf("unexpected EOSE response: %s", message)
			}
			var gotSubscription string
			if err := json.Unmarshal(message[1], &gotSubscription); err != nil {
				t.Fatal(err)
			}
			if gotSubscription != subscriptionID {
				t.Fatalf("EOSE subscription = %q, want %q", gotSubscription, subscriptionID)
			}
			if len(matches) != 1 {
				t.Fatalf("got %d matching profile events, want 1", len(matches))
			}
			return matches[0]
		case "CLOSED":
			t.Fatalf("relay closed profile query: %s", message)
		default:
			t.Fatalf("unexpected local relay response: %s", message)
		}
	}
}

func assertPublishedProfile(t *testing.T, event nostr.Event, expectedAuthor nostr.PubKey, expectedName string) {
	t.Helper()
	if event.Kind != nostr.Kind(ProfileKind) {
		t.Fatalf("published profile kind = %d, want %d", event.Kind, ProfileKind)
	}
	if event.PubKey != expectedAuthor {
		t.Fatalf("published profile author = %s, want selected identity %s", event.PubKey.Hex(), expectedAuthor.Hex())
	}
	if !event.CheckID() || !event.VerifySignature() {
		t.Fatalf("relay returned event with invalid ID or signature: %s", event.ID.Hex())
	}
	if !hasProfileCLIEventTag(event.Tags, "d", ProfileDTag) || !hasProfileCLIEventTag(event.Tags, "c", ProfileTag) {
		t.Fatalf("published profile tags = %v, want d=%q and c=%q", event.Tags, ProfileDTag, ProfileTag)
	}
	parsed, err := EventToProfile(&event)
	if err != nil {
		t.Fatalf("parse published profile content: %v", err)
	}
	if parsed.Name != expectedName || parsed.Mode != types.ModeStructured || parsed.Description != "published from a headless CLI process" {
		t.Fatalf("published profile content = %#v, want name %q and structured content", parsed, expectedName)
	}
	if len(parsed.Capabilities) != 1 || parsed.Capabilities[0].Name != "read-only" || parsed.Capabilities[0].Description != "today" {
		t.Fatalf("published profile capabilities = %#v", parsed.Capabilities)
	}
}

func hasProfileCLIEventTag(tags nostr.Tags, name, value string) bool {
	for _, tag := range tags {
		if len(tag) >= 2 && tag[0] == name && tag[1] == value {
			return true
		}
	}
	return false
}

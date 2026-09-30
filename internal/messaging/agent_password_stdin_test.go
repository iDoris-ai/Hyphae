package messaging

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"github.com/gorilla/websocket"
	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/internal/identity"
	"github.com/iDoris-ai/hyphae/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPasswordStdinCLIProcessSendInboxAndNoDecrypt(t *testing.T) {
	password := " pass with spaces  "
	home := t.TempDir()
	t.Setenv("HOME", home)
	ResetStoreForTest()
	t.Cleanup(ResetStoreForTest)
	ks := &types.KeyStore{Identities: make(map[string]*types.Identity), Contacts: make(map[string]*types.Contact)}
	_, err := identity.CreateIdentityWithPassword(ks, "alice", password)
	require.NoError(t, err)
	_, err = identity.CreateIdentityWithPassword(ks, "bob", password)
	require.NoError(t, err)
	aliceSK, err := identity.GetSecretKey(ks, "alice")
	require.NoError(t, err)
	bobSK, err := identity.GetSecretKey(ks, "bob")
	require.NoError(t, err)
	ks.MasterKey = nil
	require.NoError(t, identity.SaveKeyStore(ks))
	relay := startPasswordStdinRelay(t)

	missingArgs := passwordCLIArgs("agent", "msg", "--from", "alice", "--to", "bob", "--content", "encrypted CLI test", "--relay", relay.url)
	beforeConnections := relay.connections.Load()
	stdout, stderr, status := runPasswordStdinCLIProcess(t, home, missingArgs, "")
	assert.Equal(t, common.ExitAuthError, status)
	assert.Empty(t, stdout)
	assertPasswordCLIError(t, stderr, "auth_error")
	assert.Equal(t, beforeConnections, relay.connections.Load(), "missing password opt-in must fail before network access")

	wrongArgs := append(passwordCLIArgs("agent", "msg", "--from", "alice", "--to", "bob", "--content", "encrypted CLI test", "--relay", relay.url), "--password-stdin")
	stdout, stderr, status = runPasswordStdinCLIProcess(t, home, wrongArgs, "incorrect secret\n")
	assert.Equal(t, common.ExitAuthError, status)
	assert.Empty(t, stdout)
	assertPasswordCLIError(t, stderr, "auth_error")
	assert.NotContains(t, stderr, "incorrect secret")
	assert.Equal(t, beforeConnections, relay.connections.Load(), "wrong password must fail before network access")

	sendArgs := append(passwordCLIArgs("agent", "msg", "--from", "alice", "--to", "bob", "--content", "encrypted CLI test", "--relay", relay.url), "--password-stdin")
	stdout, stderr, status = runPasswordStdinCLIProcess(t, home, sendArgs, password+"\r\n")
	require.Equal(t, 0, status, stderr)
	assert.Empty(t, stderr)
	var sent struct {
		OK   bool `json:"ok"`
		Data struct {
			Encrypted bool   `json:"encrypted"`
			EventID   string `json:"event_id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &sent))
	assert.True(t, sent.OK)
	assert.True(t, sent.Data.Encrypted)
	select {
	case event := <-relay.published:
		assert.Equal(t, sent.Data.EventID, event.ID.Hex())
		assert.Equal(t, aliceSK.Public(), event.PubKey)
		plain, encrypted, err := DecodeMessageContent(&event, bobSK)
		require.NoError(t, err)
		assert.True(t, encrypted)
		assert.Equal(t, "encrypted CLI test", plain)
	case <-time.After(3 * time.Second):
		t.Fatal("local relay did not receive the encrypted event")
	}

	inboxArgs := append(passwordCLIArgs("agent", "inbox", "--as", "bob", "--relay", relay.url), "--password-stdin")
	stdout, stderr, status = runPasswordStdinCLIProcess(t, home, inboxArgs, password+"\n")
	require.Equal(t, 0, status, stderr)
	assert.Empty(t, stderr)
	var inbox struct {
		OK   bool `json:"ok"`
		Data []struct {
			Content   string `json:"content"`
			EventID   string `json:"event_id"`
			Encrypted bool   `json:"encrypted"`
			Decrypted bool   `json:"decrypted"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &inbox))
	assert.True(t, inbox.OK)
	require.Len(t, inbox.Data, 1)
	assert.Equal(t, "encrypted CLI test", inbox.Data[0].Content)
	assert.True(t, inbox.Data[0].Encrypted)
	assert.True(t, inbox.Data[0].Decrypted)

	noDecryptArgs := append(passwordCLIArgs("agent", "inbox", "--as", "bob", "--relay", relay.url, "--decrypt=false"), "--password-stdin")
	stdout, stderr, status = runPasswordStdinCLIProcess(t, home, noDecryptArgs, "wrong secret\n")
	require.Equal(t, 0, status, stderr)
	assert.Empty(t, stderr)
	var noDecrypt struct {
		OK   bool `json:"ok"`
		Data []struct {
			Content   string `json:"content"`
			EventID   string `json:"event_id"`
			Decrypted bool   `json:"decrypted"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &noDecrypt))
	assert.True(t, noDecrypt.OK)
	require.Len(t, noDecrypt.Data, 1)
	assert.Equal(t, "[encrypted message]", noDecrypt.Data[0].Content)
	assert.False(t, noDecrypt.Data[0].Decrypted)
	store, err := GetStore()
	require.NoError(t, err)
	stored, err := store.GetMessage(noDecrypt.Data[0].EventID)
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.Equal(t, "encrypted CLI test", stored.Plaintext, "decrypt=false must leave earlier plaintext untouched")
}

func passwordCLIArgs(args ...string) []string {
	return append([]string{"hyphae", "--json"}, args...)
}

func runPasswordStdinCLIProcess(t *testing.T, home string, args []string, stdin string) (string, string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, outboxCLI, args[1:]...)
	command.Env = isolatedOutboxCLIEnv(os.Environ(), home, nil)
	command.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	if err == nil {
		return stdout.String(), stderr.String(), 0
	}
	if ctx.Err() != nil {
		t.Fatalf("CLI timed out: %v\nstdout: %s\nstderr: %s", ctx.Err(), stdout.String(), stderr.String())
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("run CLI child: %v", err)
	}
	return stdout.String(), stderr.String(), exitErr.ExitCode()
}

func assertPasswordCLIError(t *testing.T, stderr, code string) {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(stderr), "\n")
	require.Len(t, lines, 1)
	var result common.Result
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &result))
	assert.False(t, result.OK)
	assert.Equal(t, code, result.Error)
}

type passwordStdinRelay struct {
	url         string
	published   chan nostr.Event
	connections atomic.Int32
	mu          sync.Mutex
	events      []nostr.Event
}

func startPasswordStdinRelay(t *testing.T) *passwordStdinRelay {
	t.Helper()
	relay := &passwordStdinRelay{published: make(chan nostr.Event, 8)}
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		relay.connections.Add(1)
		defer conn.Close()
		for {
			_ = conn.SetReadDeadline(time.Now().Add(8 * time.Second))
			_, payload, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var envelope []json.RawMessage
			if json.Unmarshal(payload, &envelope) != nil || len(envelope) < 2 {
				continue
			}
			var command string
			if json.Unmarshal(envelope[0], &command) != nil {
				continue
			}
			switch command {
			case "EVENT":
				var event nostr.Event
				if json.Unmarshal(envelope[1], &event) != nil {
					continue
				}
				relay.mu.Lock()
				relay.events = append(relay.events, event)
				relay.mu.Unlock()
				select {
				case relay.published <- event:
				default:
				}
				_ = conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
				_ = conn.WriteJSON([]any{"OK", event.ID.Hex(), true, "accepted"})
			case "REQ":
				var subscription string
				if json.Unmarshal(envelope[1], &subscription) != nil {
					continue
				}
				relay.mu.Lock()
				events := append([]nostr.Event(nil), relay.events...)
				relay.mu.Unlock()
				for _, event := range events {
					_ = conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
					_ = conn.WriteJSON([]any{"EVENT", subscription, event})
				}
				_ = conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
				_ = conn.WriteJSON([]any{"EOSE", subscription})
			}
		}
	}))
	t.Cleanup(server.Close)
	relay.url = "ws" + strings.TrimPrefix(server.URL, "http")
	return relay
}

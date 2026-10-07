package messaging

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fiatjaf.com/nostr"
	"github.com/gorilla/websocket"
	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/internal/identity"
	"github.com/iDoris-ai/hyphae/pkg/crypto"
	"github.com/iDoris-ai/hyphae/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestAgentInboxEOSEDeduplicatesSortsAndStores(t *testing.T) {
	recipient, recipientPK := setupAgentInbox(t)
	sender := nostr.Generate()
	now := nostr.Now()
	older := makeInboxEvent(t, recipientPK, sender, now-1, "older", nil)
	newer := makeInboxEvent(t, recipientPK, sender, now, "newer", nil)
	tieLaterID := makeInboxEvent(t, recipientPK, sender, now, "tie-later-id", nil)
	relayA := startInboxRelay(t, inboxRelayPlan{events: []nostr.Event{older, newer}})
	relayB := startInboxRelay(t, inboxRelayPlan{events: []nostr.Event{newer, tieLaterID}})

	stdout := captureStdout(t, func() {
		require.NoError(t, runAgentInboxCLI(context.Background(), []string{"--json", "--as", "alice", "--limit", "2", "--relay", relayA, "--relay", relayB}))
	})
	var response struct {
		OK   bool              `json:"ok"`
		Data []agentInboxEntry `json:"data"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &response))
	assert.True(t, response.OK)
	require.Len(t, response.Data, 2)
	expectedTieIDs := []string{newer.ID.Hex(), tieLaterID.ID.Hex()}
	if expectedTieIDs[0] > expectedTieIDs[1] {
		expectedTieIDs[0], expectedTieIDs[1] = expectedTieIDs[1], expectedTieIDs[0]
	}
	assert.Equal(t, expectedTieIDs, inboxIDs(response.Data))
	assert.Equal(t, common.EncodeNpub(sender.Public()), response.Data[0].SenderNpub)
	entryByID := make(map[string]agentInboxEntry, len(response.Data))
	for _, entry := range response.Data {
		entryByID[entry.EventID] = entry
	}
	assert.Equal(t, "newer", entryByID[newer.ID.Hex()].Content)
	for _, event := range []nostr.Event{newer, tieLaterID} {
		stored, err := mustGetStoredMessage(t, event.ID.Hex())
		require.NoError(t, err)
		require.NotNil(t, stored)
		assert.Equal(t, recipient.Npub, stored.RecipientNpub)
	}
	olderStored, err := mustGetStoredMessage(t, older.ID.Hex())
	require.NoError(t, err)
	assert.Nil(t, olderStored, "the final result and storage must honor the requested limit")
}

func TestAgentInboxPartialRelayFailureReturnsEntriesAndWarning(t *testing.T) {
	_, recipientPK := setupAgentInbox(t)
	message := makeInboxEvent(t, recipientPK, nostr.Generate(), nostr.Now(), "partial result", nil)
	goodRelay := startInboxRelay(t, inboxRelayPlan{events: []nostr.Event{message}})
	badRelay := startInboxRelay(t, inboxRelayPlan{events: []nostr.Event{message}, closed: "relay unavailable"})
	var stderr string
	stdout := captureStdout(t, func() {
		stderr = captureInboxStderr(t, func() {
			err := runAgentInboxCLI(context.Background(), []string{"--json", "--relay", goodRelay, "--relay", badRelay})
			require.NoError(t, err)
		})
	})
	var response struct {
		OK   bool              `json:"ok"`
		Data []agentInboxEntry `json:"data"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &response))
	assert.True(t, response.OK)
	require.Len(t, response.Data, 1)
	assert.Contains(t, stderr, badRelay)
	assert.Contains(t, stderr, "relay unavailable")
}

func TestAgentInboxStoresDecodedCompressedPlaintext(t *testing.T) {
	recipient, recipientPK := setupAgentInbox(t)
	compressed, err := CompressText("decoded compressed body")
	require.NoError(t, err)
	event := makeInboxEvent(t, recipientPK, nostr.Generate(), nostr.Now(), compressed, nostr.Tags{{"z", CompressTag}})
	relay := startInboxRelay(t, inboxRelayPlan{events: []nostr.Event{event}})
	stdout := captureStdout(t, func() {
		require.NoError(t, runAgentInboxCLI(context.Background(), []string{"--json", "--relay", relay}))
	})
	var response struct {
		OK   bool              `json:"ok"`
		Data []agentInboxEntry `json:"data"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &response))
	assert.True(t, response.OK)
	require.Len(t, response.Data, 1)
	assert.Equal(t, "decoded compressed body", response.Data[0].Content)
	stored, err := mustGetStoredMessage(t, event.ID.Hex())
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.Equal(t, recipient.Npub, stored.RecipientNpub)
	assert.Equal(t, "decoded compressed body", stored.Plaintext)
}

func TestAgentInboxAllRelaysFailRetainsPartialEventsInNetworkError(t *testing.T) {
	_, recipientPK := setupAgentInbox(t)
	message := makeInboxEvent(t, recipientPK, nostr.Generate(), nostr.Now(), "received before CLOSED", nil)
	failedRelay := startInboxRelay(t, inboxRelayPlan{events: []nostr.Event{message}, closed: "incomplete"})

	var runErr error
	stdout := captureStdout(t, func() {
		runErr = runAgentInboxCLI(context.Background(), []string{"--json", "--relay", failedRelay})
	})
	require.Error(t, runErr)
	assert.Empty(t, stdout)
	stderr := captureInboxError(t, runErr)
	var response struct {
		OK      bool              `json:"ok"`
		Error   string            `json:"error"`
		Message string            `json:"message"`
		Data    []agentInboxEntry `json:"data"`
	}
	require.NoError(t, json.Unmarshal([]byte(stderr), &response))
	assert.False(t, response.OK)
	assert.Equal(t, common.ErrCodeNetwork, response.Error)
	assert.Contains(t, response.Message, failedRelay)
	require.Len(t, response.Data, 1)
	assert.Equal(t, message.ID.Hex(), response.Data[0].EventID)
}

func TestAgentInboxEventFailurePreservesUsableEntries(t *testing.T) {
	_, recipientPK := setupAgentInbox(t)
	good := makeInboxEvent(t, recipientPK, nostr.Generate(), nostr.Now(), "usable", nil)
	bad := makeInboxEvent(t, recipientPK, nostr.Generate(), nostr.Now()+1, "aGVsbG8=", nostr.Tags{{"z", CompressTag}})
	relay := startInboxRelay(t, inboxRelayPlan{events: []nostr.Event{bad, good}})
	var runErr error
	stdout := captureStdout(t, func() {
		runErr = runAgentInboxCLI(context.Background(), []string{"--json", "--relay", relay})
	})
	require.Error(t, runErr)
	assert.Empty(t, stdout)
	stderr := captureInboxError(t, runErr)
	var response struct {
		OK      bool              `json:"ok"`
		Message string            `json:"message"`
		Data    []agentInboxEntry `json:"data"`
	}
	require.NoError(t, json.Unmarshal([]byte(stderr), &response))
	assert.False(t, response.OK)
	assert.Contains(t, response.Message, bad.ID.Hex())
	require.Len(t, response.Data, 1)
	assert.Equal(t, good.ID.Hex(), response.Data[0].EventID)
	assert.Equal(t, "usable", response.Data[0].Content)
}

func TestAgentInboxDecodeAndStoreFailuresKeepUsableDataAndNoPlaceholderRows(t *testing.T) {
	for _, tc := range []struct {
		name       string
		event      func(*testing.T, nostr.PubKey) nostr.Event
		breakState func(*testing.T, string)
		want       string
	}{
		{
			name: "invalid compressed body",
			event: func(t *testing.T, recipient nostr.PubKey) nostr.Event {
				return makeInboxEvent(t, recipient, nostr.Generate(), nostr.Now(), "aGVsbG8=", nostr.Tags{{"z", CompressTag}})
			},
			want: "decompress zstd",
		},
		{
			name: "invalid encrypted body",
			event: func(t *testing.T, recipient nostr.PubKey) nostr.Event {
				return makeInboxEvent(t, recipient, nostr.Generate(), nostr.Now(), "not ciphertext", nostr.Tags{{"enc", "nip44"}})
			},
			want: "decrypt NIP-44",
		},
		{
			name: "history storage failure",
			event: func(t *testing.T, recipient nostr.PubKey) nostr.Event {
				return makeInboxEvent(t, recipient, nostr.Generate(), nostr.Now(), "plain", nil)
			},
			breakState: func(t *testing.T, home string) {
				require.NoError(t, os.Mkdir(filepath.Join(home, ".hyphae", "messages.db"), 0700))
			},
			want: "store received message",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, recipientPK := setupAgentInbox(t)
			event := tc.event(t, recipientPK)
			relay := startInboxRelay(t, inboxRelayPlan{events: []nostr.Event{event}})
			if tc.breakState != nil {
				tc.breakState(t, os.Getenv("HOME"))
			}
			var runErr error
			stdout := captureStdout(t, func() {
				runErr = runAgentInboxCLI(context.Background(), []string{"--json", "--relay", relay})
			})
			require.Error(t, runErr)
			assert.Empty(t, stdout)
			stderr := captureInboxError(t, runErr)
			lines := strings.Split(strings.TrimSpace(stderr), "\n")
			require.Len(t, lines, 1, "JSON error mode must write one envelope")
			var response struct {
				OK      bool              `json:"ok"`
				Message string            `json:"message"`
				Data    []agentInboxEntry `json:"data"`
			}
			require.NoError(t, json.Unmarshal([]byte(lines[0]), &response))
			assert.False(t, response.OK)
			assert.Contains(t, response.Message, event.ID.Hex())
			assert.Contains(t, response.Message, tc.want)
			assert.Empty(t, response.Data, "failed events must not be confused with usable entries")
			if tc.breakState == nil {
				stored, err := mustGetStoredMessage(t, event.ID.Hex())
				require.NoError(t, err)
				assert.Nil(t, stored, "decode failure must not store ciphertext or a placeholder")
			}
		})
	}
}

func TestAgentInboxDecryptFalseKeepsExistingPlaintextWithoutSecretKey(t *testing.T) {
	home, _, recipientPK := setupAgentInboxLocked(t)
	recipientKS, err := identity.LoadKeyStore()
	require.NoError(t, err)
	recipient, err := identity.GetIdentity(recipientKS, "alice")
	require.NoError(t, err)
	sender := nostr.Generate()
	ciphertext, err := crypto.EncryptMessage("secret", sender, recipientPK)
	require.NoError(t, err)
	compressed, err := CompressText(ciphertext)
	require.NoError(t, err)
	event := makeInboxEvent(t, recipientPK, sender, nostr.Now(), compressed, nostr.Tags{{"enc", "nip44"}, {"z", CompressTag}})
	first, err := StoreIncomingMessageOnce(&event, recipient.Npub, "previous plaintext", true)
	require.NoError(t, err)
	assert.True(t, first)
	newEncrypted := makeInboxEvent(t, recipientPK, sender, nostr.Now()+1, "arbitrary invalid ciphertext", nostr.Tags{{"enc", "nip44"}})
	relay := startInboxRelay(t, inboxRelayPlan{events: []nostr.Event{event, newEncrypted}})
	stdout := captureStdout(t, func() {
		require.NoError(t, runAgentInboxCLI(context.Background(), []string{"--json", "--decrypt=false", "--relay", relay}))
	})
	var response struct {
		OK   bool              `json:"ok"`
		Data []agentInboxEntry `json:"data"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &response))
	assert.True(t, response.OK)
	require.Len(t, response.Data, 2)
	assert.Equal(t, "[encrypted message]", response.Data[0].Content)
	assert.True(t, response.Data[0].Encrypted)
	assert.False(t, response.Data[0].Decrypted)
	assert.Equal(t, "[encrypted message]", response.Data[1].Content, "ciphertext is not required to be valid without decryption")
	stored, err := mustGetStoredMessage(t, event.ID.Hex())
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.True(t, stored.IsIncoming)
	assert.Equal(t, "previous plaintext", stored.Plaintext)
	assert.NotEqual(t, "[encrypted message]", stored.Plaintext)
	assert.NotEqual(t, ciphertext, stored.Plaintext)
	newStored, err := mustGetStoredMessage(t, newEncrypted.ID.Hex())
	require.NoError(t, err)
	assert.Nil(t, newStored, "encrypted content viewed without decryption must not be registered")
	assert.DirExists(t, filepath.Join(home, ".hyphae"))
}

func TestAgentInboxRejectsInvalidLimitBeforeTouchingDisk(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("HYPHAE_OUTPUT", "json")
	var runErr error
	stdout := captureStdout(t, func() {
		runErr = runAgentInboxCLI(context.Background(), []string{"--json", "--limit", "0"})
	})
	require.Error(t, runErr)
	assert.Empty(t, stdout)
	assert.NoDirExists(t, filepath.Join(home, ".hyphae"))
	stderr := captureInboxError(t, runErr)
	assert.Contains(t, stderr, `"error":"user_error"`)
	assert.Contains(t, stderr, "limit must be positive")
}

func TestDecodeInboxContentWithoutDecryptionValidatesTagsAndCompression(t *testing.T) {
	cases := []struct {
		name    string
		content string
		tags    nostr.Tags
		wantErr string
	}{
		{name: "unknown encryption", content: "cipher", tags: nostr.Tags{{"enc", "future"}}, wantErr: "unsupported encryption"},
		{name: "unknown compression", content: "body", tags: nostr.Tags{{"z", "gzip"}}, wantErr: "unsupported compression"},
		{name: "bad compressed encrypted body", content: "bad!", tags: nostr.Tags{{"enc", "nip44"}, {"z", CompressTag}}, wantErr: "invalid base64"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			content, encrypted, decrypted, err := decodeInboxContent(&nostr.Event{Content: tc.content, Tags: tc.tags}, nostr.SecretKey{}, false)
			require.ErrorContains(t, err, tc.wantErr)
			assert.Empty(t, content)
			assert.False(t, decrypted)
			if strings.Contains(tc.name, "encrypted") {
				assert.True(t, encrypted)
			}
		})
	}
}

type inboxRelayPlan struct {
	events []nostr.Event
	closed string
}

func startInboxRelay(t *testing.T, plan inboxRelayPlan) string {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_, request, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var envelope []json.RawMessage
		if json.Unmarshal(request, &envelope) != nil || len(envelope) < 2 {
			return
		}
		var subscription string
		if json.Unmarshal(envelope[1], &subscription) != nil {
			return
		}
		for _, event := range plan.events {
			if err := conn.WriteJSON([]any{"EVENT", subscription, event}); err != nil {
				return
			}
		}
		if plan.closed != "" {
			_ = conn.WriteJSON([]any{"CLOSED", subscription, plan.closed})
		} else {
			_ = conn.WriteJSON([]any{"EOSE", subscription})
		}
		_, _, _ = conn.ReadMessage()
	}))
	t.Cleanup(server.Close)
	return "ws" + strings.TrimPrefix(server.URL, "http")
}

func setupAgentInbox(t *testing.T) (*types.Identity, nostr.PubKey) {
	t.Helper()
	return setupAgentInboxWithPassword(t, "")
}

func setupAgentInboxLocked(t *testing.T) (string, *types.Identity, nostr.PubKey) {
	t.Helper()
	alice, pub := setupAgentInboxWithPassword(t, "inbox-test-password")
	ks, err := identity.LoadKeyStore()
	require.NoError(t, err)
	ks.MasterKey = nil
	require.NoError(t, identity.SaveKeyStore(ks))
	return os.Getenv("HOME"), alice, pub
}

func setupAgentInboxWithPassword(t *testing.T, password string) (*types.Identity, nostr.PubKey) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("HYPHAE_OUTPUT", "json")
	t.Setenv("AGENT_SPEAKER_OUTPUT", "")
	ResetStoreForTest()
	t.Cleanup(ResetStoreForTest)
	ks := &types.KeyStore{Identities: make(map[string]*types.Identity), Contacts: make(map[string]*types.Contact)}
	var alice *types.Identity
	var err error
	if password == "" {
		alice, err = identity.CreateIdentity(ks, "alice")
	} else {
		alice, err = identity.CreateIdentityWithPassword(ks, "alice", password)
	}
	require.NoError(t, err)
	pub, err := identity.GetPublicKey(ks, alice.Nickname)
	require.NoError(t, err)
	return alice, pub
}

func runAgentInboxCLI(ctx context.Context, args []string) error {
	cmd := &cli.Command{
		Name:   AgentInboxCmd.Name,
		Action: AgentInboxCmd.Action,
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "as"},
			&cli.StringSliceFlag{Name: "relay"},
			&cli.IntFlag{Name: "limit", Value: 10},
			&cli.BoolFlag{Name: "decrypt", Value: true},
			&cli.BoolFlag{Name: "password-stdin"},
			&cli.BoolFlag{Name: "json"},
		},
	}
	return cmd.Run(ctx, args)
}

func makeInboxEvent(t *testing.T, recipient nostr.PubKey, sender nostr.SecretKey, created nostr.Timestamp, content string, extra nostr.Tags) nostr.Event {
	t.Helper()
	tags := nostr.Tags{
		{"p", hex.EncodeToString(recipient[:])},
		{"c", AgentTag},
		{"v", AgentVersion},
	}
	tags = append(tags, extra...)
	event := nostr.Event{CreatedAt: created, Kind: AgentKind, Tags: tags, Content: content}
	require.NoError(t, event.Sign(sender))
	return event
}

func inboxIDs(entries []agentInboxEntry) []string {
	ids := make([]string, len(entries))
	for i := range entries {
		ids[i] = entries[i].EventID
	}
	return ids
}

func captureInboxStderr(t *testing.T, fn func()) string {
	t.Helper()
	original := os.Stderr
	readEnd, writeEnd, err := os.Pipe()
	require.NoError(t, err)
	os.Stderr = writeEnd
	fn()
	require.NoError(t, writeEnd.Close())
	os.Stderr = original
	data, err := io.ReadAll(readEnd)
	require.NoError(t, err)
	return string(data)
}

func captureInboxError(t *testing.T, err error) string {
	t.Helper()
	var output string
	output = captureInboxStderr(t, func() {
		code := common.EmitError(true, err)
		assert.NotZero(t, code)
	})
	return output
}

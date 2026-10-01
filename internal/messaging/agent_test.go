package messaging

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"github.com/gorilla/websocket"
	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/internal/identity"
	"github.com/iDoris-ai/hyphae/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestCompressText(t *testing.T) {
	original := "Hello, this is a test message for compression!"
	compressed, err := CompressText(original)
	require.NoError(t, err)
	assert.NotEmpty(t, compressed)
	assert.NotEqual(t, original, compressed)
}

func TestDecompressText(t *testing.T) {
	original := "Hello, this is a test message for compression!"
	compressed, err := CompressText(original)
	require.NoError(t, err)

	decompressed, err := DecompressText(compressed)
	require.NoError(t, err)
	assert.Equal(t, original, decompressed)
}

func TestCompressDecompress_EmptyString(t *testing.T) {
	original := ""
	compressed, err := CompressText(original)
	require.NoError(t, err)

	decompressed, err := DecompressText(compressed)
	require.NoError(t, err)
	assert.Equal(t, original, decompressed)
}

func TestCompressDecompress_LongText(t *testing.T) {
	original := strings.Repeat("The quick brown fox jumps over the lazy dog. ", 1000)
	compressed, err := CompressText(original)
	require.NoError(t, err)

	decompressed, err := DecompressText(compressed)
	require.NoError(t, err)
	assert.Equal(t, original, decompressed)
}

func TestDecompressText_InvalidBase64(t *testing.T) {
	_, err := DecompressText("!!!invalid!!!")
	assert.Error(t, err)
}

func TestDecompressText_InvalidZstd(t *testing.T) {
	_, err := DecompressText("aGVsbG8=") // valid base64, invalid zstd
	assert.Error(t, err)
}

// TestDeriveMessageDTag_UniqueEvenForIdenticalContentAndTimestamp is the
// CC-82 regression test: kind 30078 is NIP-01 addressable, so a compliant
// relay collapses events sharing the same (pubkey, kind, d) coordinate.
// Before this fix, agent msg carried no "d" tag at all -- every message from
// the same sender shared the implicit d="" coordinate and would evict the
// previous one on such a relay. This asserts the pathological worst case
// (byte-identical content, same-second timestamp -- e.g. a retry with
// encryption disabled) still produces distinct d values, so consecutive
// messages from one sender keep separate coordinates and don't overwrite
// each other.
func TestDeriveMessageDTag_UniqueEvenForIdenticalContentAndTimestamp(t *testing.T) {
	content := "identical-payload"
	ts := nostr.Now()

	first, err := deriveMessageDTag(content, ts)
	require.NoError(t, err)
	second, err := deriveMessageDTag(content, ts)
	require.NoError(t, err)

	assert.NotEqual(t, first, second)
}

func TestDeriveMessageDTag_ReturnsValidHex(t *testing.T) {
	d, err := deriveMessageDTag("some content", nostr.Now())
	require.NoError(t, err)

	assert.Len(t, d, 16)
	_, err = hex.DecodeString(d)
	assert.NoError(t, err)
}

func TestAgentMsgCmd_AcknowledgedEventIsQueuedBeforePublishAndRemovedAfter(t *testing.T) {
	setupAgentMsgCLI(t)
	relayURL, received := startAgentTestRelay(t, true)
	var published nostr.Event
	stdout := captureStdout(t, func() {
		err := runAgentMsgCLICommand(context.Background(), agentMsgArgs(relayURL))
		require.NoError(t, err)
	})
	require.True(t, json.Valid([]byte(stdout)), "agent msg JSON output: %s", stdout)
	var response struct {
		OK   bool           `json:"ok"`
		Data agentMsgResult `json:"data"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &response))
	assert.True(t, response.OK)
	assert.True(t, response.Data.HistoryStored)
	assert.False(t, response.Data.QueuedForRetry)
	assert.Equal(t, 1, response.Data.PublishedTo)
	assert.False(t, response.Data.Superseded)

	select {
	case published = <-received:
	case <-time.After(3 * time.Second):
		t.Fatal("test relay did not receive the message")
	}
	assert.Equal(t, response.Data.EventID, published.ID.Hex())
	assert.NotEqual(t, [64]byte{}, published.Sig)

	ob, err := LoadOutbox()
	require.NoError(t, err)
	assert.Empty(t, ob.Entries, "an acknowledged event should be removed by its QueueID")
	stored, err := mustGetStoredMessage(t, response.Data.EventID)
	require.NoError(t, err)
	assert.Equal(t, "reliable send test", stored.Plaintext)
	assert.False(t, stored.IsEncrypted)
}

func TestAgentMsgCmd_UnavailableRelayLeavesSameSignedEventQueued(t *testing.T) {
	setupAgentMsgCLI(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	url := "ws://" + listener.Addr().String()
	require.NoError(t, listener.Close())
	var response struct {
		OK   bool           `json:"ok"`
		Data agentMsgResult `json:"data"`
	}
	stdout := captureStdout(t, func() {
		err := runAgentMsgCLICommand(context.Background(), agentMsgArgs(url))
		require.NoError(t, err, "failed relay publication is a successfully queued command")
	})
	require.NoError(t, json.Unmarshal([]byte(stdout), &response))
	assert.True(t, response.OK)
	assert.Equal(t, 0, response.Data.PublishedTo)
	assert.True(t, response.Data.QueuedForRetry)
	assert.True(t, response.Data.HistoryStored)

	ob, err := LoadOutbox()
	require.NoError(t, err)
	require.Len(t, ob.Entries, 1)
	var queued nostr.Event
	require.NoError(t, json.Unmarshal([]byte(ob.Entries[0].EventJSON), &queued))
	assert.Equal(t, response.Data.EventID, queued.ID.Hex())
	assert.Equal(t, response.Data.EventID, ob.Entries[0].ID)
	assert.Equal(t, []string{url}, ob.Entries[0].Relays)
	stored, err := mustGetStoredMessage(t, response.Data.EventID)
	require.NoError(t, err)
	assert.Equal(t, "reliable send test", stored.Plaintext)
}

func TestAgentMsgCmd_HistoryOrOutboxFailureNeverPublishes(t *testing.T) {
	for _, tc := range []struct {
		name       string
		breakState func(*testing.T, string)
		history    bool
		unknown    bool
	}{
		{
			name: "history write failure",
			breakState: func(t *testing.T, home string) {
				path := filepath.Join(home, ".hyphae", "messages.db")
				require.NoError(t, os.Mkdir(path, 0700))
			},
			history: false,
		},
		{
			name: "invalid outbox JSON",
			breakState: func(t *testing.T, home string) {
				path := filepath.Join(home, ".hyphae", "outbox.json")
				require.NoError(t, os.WriteFile(path, []byte("{"), 0600))
			},
			history: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := setupAgentMsgCLI(t)
			relayURL, received := startAgentTestRelay(t, true)
			tc.breakState(t, home)
			var runErr error
			stdout := captureStdout(t, func() {
				runErr = runAgentMsgCLICommand(context.Background(), agentMsgArgs(relayURL))
			})
			require.Error(t, runErr)
			assert.Empty(t, stdout, "failure mode should keep JSON stdout empty")

			stderr := captureAgentMsgError(t, runErr)
			var response struct {
				OK   bool `json:"ok"`
				Data struct {
					EventID           string `json:"event_id"`
					HistoryStored     bool   `json:"history_stored"`
					PublishedTo       int    `json:"published_to"`
					QueueStateUnknown bool   `json:"queue_state_unknown"`
				} `json:"data"`
			}
			require.NoError(t, json.Unmarshal([]byte(stderr), &response))
			assert.False(t, response.OK)
			assert.Equal(t, tc.history, response.Data.HistoryStored)
			assert.Zero(t, response.Data.PublishedTo)
			assert.Equal(t, tc.unknown, response.Data.QueueStateUnknown)
			select {
			case event := <-received:
				t.Fatalf("relay received event %s despite local write failure", event.ID.Hex())
			case <-time.After(100 * time.Millisecond):
			}
		})
	}
}

func TestSendQueuedAgentMessage_PostAckStoreErrorRetainsOutcomeData(t *testing.T) {
	setupAgentMsgCLI(t)
	event := nostr.Event{CreatedAt: nostr.Now(), Kind: AgentKind, Content: "signed event"}
	secret := nostr.Generate()
	event.PubKey = secret.Public()
	require.NoError(t, event.Sign(secret))
	storeCalls := 0
	store := func(event *nostr.Event, recipient, plaintext string, encrypted bool) error {
		storeCalls++
		if storeCalls == 2 {
			return fmt.Errorf("simulated post-ACK history failure")
		}
		return StoreOutgoingMessage(event, recipient, plaintext, encrypted)
	}
	result, err := sendQueuedAgentMessage(context.Background(), &event, "npub1recipient", "plain", "alice", "bob", false,
		[]string{"wss://relay.local"}, time.Second, store, enqueueOutboxEntry,
		func(_ context.Context, targets []string, published nostr.Event, _ time.Duration) ([]agentMsgRelayResult, int) {
			queued, loadErr := LoadOutbox()
			require.NoError(t, loadErr)
			require.Len(t, queued.Entries, 1, "the queue entry must be durable before publishing")
			assert.NotEmpty(t, queued.Entries[0].QueueID)
			assert.Equal(t, published.ID.Hex(), queued.Entries[0].ID)
			stored, historyErr := mustGetStoredMessage(t, published.ID.Hex())
			require.NoError(t, historyErr)
			require.NotNil(t, stored)
			assert.Equal(t, "plain", stored.Plaintext, "local plaintext history must precede publishing")
			return []agentMsgRelayResult{{URL: targets[0], OK: true}}, 1
		})
	require.Error(t, err)
	assert.Equal(t, 1, result.PublishedTo)
	assert.True(t, result.HistoryStored, "the pre-publish local history write succeeded")
	assert.True(t, result.QueuedForRetry, "the failed post-ACK bookkeeping must leave the entry queued")
	assert.False(t, result.QueueStateUnknown)

	ob, err := LoadOutbox()
	require.NoError(t, err)
	require.Len(t, ob.Entries, 1)
	assert.Equal(t, event.ID.Hex(), ob.Entries[0].ID)
}

func TestSendQueuedAgentMessage_UncertainEnqueueNeverPublishes(t *testing.T) {
	setupAgentMsgCLI(t)
	secret := nostr.Generate()
	event := nostr.Event{CreatedAt: nostr.Now(), Kind: AgentKind, Content: "durability uncertain", PubKey: secret.Public()}
	require.NoError(t, event.Sign(secret))
	originalPersist := persistOutbox
	persistOutbox = func(path string, outbox *types.Outbox) error {
		if err := originalPersist(path, outbox); err != nil {
			return err
		}
		return &outboxCommitUncertainError{fmt.Errorf("directory sync failed after rename")}
	}
	t.Cleanup(func() { persistOutbox = originalPersist })
	publisherCalled := false
	result, err := sendQueuedAgentMessage(context.Background(), &event, "npub1recipient", "plain", "alice", "bob", false,
		[]string{"wss://relay.local"}, time.Second, StoreOutgoingMessage, enqueueOutboxEntry,
		func(context.Context, []string, nostr.Event, time.Duration) ([]agentMsgRelayResult, int) {
			publisherCalled = true
			return nil, 0
		})
	require.Error(t, err)
	assert.True(t, result.HistoryStored)
	assert.True(t, result.QueueStateUnknown)
	assert.False(t, result.QueuedForRetry)
	assert.False(t, publisherCalled, "an uncertain enqueue must never begin network publishing")
	ob, err := LoadOutbox()
	require.NoError(t, err)
	require.Len(t, ob.Entries, 1, "the rename completed even though directory durability was uncertain")
	assert.Equal(t, event.ID.Hex(), ob.Entries[0].ID)
}

func setupAgentMsgCLI(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("HYPHAE_OUTPUT", "json")
	t.Setenv("AGENT_SPEAKER_OUTPUT", "")
	ResetStoreForTest()
	t.Cleanup(ResetStoreForTest)
	ks := &types.KeyStore{Identities: make(map[string]*types.Identity), Contacts: make(map[string]*types.Contact)}
	_, err := identity.CreateIdentity(ks, "alice")
	require.NoError(t, err)
	_, err = identity.CreateIdentity(ks, "bob")
	require.NoError(t, err)
	return home
}

func agentMsgArgs(relay string) []string {
	return []string{"msg", "--from", "alice", "--to", "bob", "--content", "reliable send test", "--relay", relay, "--encrypt=false"}
}

func runAgentMsgCLICommand(ctx context.Context, args []string) error {
	cmd := &cli.Command{
		Name:   AgentMsgCmd.Name,
		Action: AgentMsgCmd.Action,
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "from"},
			&cli.StringFlag{Name: "to"},
			&cli.StringFlag{Name: "content"},
			&cli.StringFlag{Name: "content-file"},
			&cli.StringSliceFlag{Name: "relay"},
			&cli.BoolFlag{Name: "encrypt", Value: true},
			&cli.BoolFlag{Name: "password-stdin"},
		},
	}
	return cmd.Run(ctx, args)
}

func startAgentTestRelay(t *testing.T, accept bool) (string, <-chan nostr.Event) {
	t.Helper()
	events := make(chan nostr.Event, 4)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var envelope []json.RawMessage
			if json.Unmarshal(data, &envelope) != nil || len(envelope) < 2 {
				continue
			}
			var command string
			if json.Unmarshal(envelope[0], &command) != nil || command != "EVENT" {
				continue
			}
			var event nostr.Event
			if json.Unmarshal(envelope[1], &event) != nil {
				continue
			}
			select {
			case events <- event:
			default:
			}
			response, _ := json.Marshal([]any{"OK", event.ID.Hex(), accept, "test relay"})
			if err := conn.WriteMessage(websocket.TextMessage, response); err != nil {
				return
			}
		}
	}))
	t.Cleanup(server.Close)
	return "ws" + strings.TrimPrefix(server.URL, "http"), events
}

func captureAgentMsgError(t *testing.T, err error) string {
	t.Helper()
	original := os.Stderr
	readEnd, writeEnd, pipeErr := os.Pipe()
	require.NoError(t, pipeErr)
	defer func() { os.Stderr = original }()
	os.Stderr = writeEnd
	code := common.EmitError(true, err)
	require.Equal(t, common.ExitOtherError, code)
	require.NoError(t, writeEnd.Close())
	os.Stderr = original
	data, readErr := io.ReadAll(readEnd)
	require.NoError(t, readErr)
	return string(data)
}

func mustGetStoredMessage(t *testing.T, id string) (*types.StoredMessage, error) {
	t.Helper()
	store, err := GetStore()
	require.NoError(t, err)
	return store.GetMessage(id)
}

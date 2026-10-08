package messaging

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/internal/identity"
	"github.com/iDoris-ai/hyphae/pkg/types"
	"github.com/stretchr/testify/require"
)

// The child stops inside the persistence boundary while holding the real
// process lock. Only IDs cross the readiness pipe; SIGKILL, not cancellation,
// terminates it. No scheduler timing is used to choose the crash point.
func TestExactlyOnceCrashHelper(t *testing.T) {
	stage := os.Getenv("HYPHAE_CRASH_STAGE")
	if stage == "" {
		return
	}
	ks, err := identity.LoadKeyStore()
	require.NoError(t, err)
	sk, err := identity.GetSecretKey(ks, "alice")
	require.NoError(t, err)
	peer, err := identity.GetIdentity(ks, "bob")
	require.NoError(t, err)
	pk, err := common.ParsePublicKey(peer.Npub)
	require.NoError(t, err)
	event := makeInboxEvent(t, pk, sk, nostr.Now(), "crash-boundary-message", nil)
	original := persistOutbox
	stop := func() error {
		fmt.Println(event.ID.Hex())
		var b [1]byte
		_, _ = os.Stdin.Read(b[:])
		return fmt.Errorf("parent closed crash gate without killing child")
	}
	if stage == "before-commit" {
		renameOutbox = func(string, string) error { return stop() }
	}
	persistOutbox = func(path string, ob *types.Outbox) error {
		// Execute the actual marshal/write/fsync/close/rename path. The
		// before-commit gate is in renameOutbox, after the temp is synced.
		if err := original(path, ob); err != nil {
			return err
		}
		return stop()
	}
	_, err = SendQueuedAgentMessage(context.Background(), &event, peer.Npub, event.Content, false,
		[]string{"ws://127.0.0.1:1"}, time.Second)
	t.Fatalf("crash gate unexpectedly returned: %v", err)
}

func TestExactlyOnceCrashDuringEnqueue(t *testing.T) {
	for _, stage := range []string{"before-commit", "after-commit"} {
		t.Run(stage, func(t *testing.T) {
			setupAgentMsgCLI(t)
			seed := &types.Outbox{Entries: []types.OutboxEntry{{ID: "already-durable", QueueID: "seed", Status: "pending"}}}
			require.NoError(t, SaveOutbox(seed))
			cmd := exec.Command(os.Args[0], "-test.run=^TestExactlyOnceCrashHelper$")
			cmd.Env = append(os.Environ(), "HYPHAE_CRASH_STAGE="+stage)
			out, err := cmd.StdoutPipe()
			require.NoError(t, err)
			in, err := cmd.StdinPipe()
			require.NoError(t, err)
			defer in.Close()
			require.NoError(t, cmd.Start())
			t.Cleanup(func() { _ = cmd.Process.Kill() })
			ready := make(chan string, 1)
			go func() {
				scanner := bufio.NewScanner(out)
				if scanner.Scan() {
					ready <- scanner.Text()
				} else {
					ready <- ""
				}
			}()
			var eventID string
			select {
			case eventID = <-ready:
				require.Len(t, eventID, 64, "child must reach the persistence barrier")
			case <-time.After(10 * time.Second):
				t.Fatal("child did not reach crash barrier")
			}
			require.NoError(t, cmd.Process.Kill())
			require.Error(t, cmd.Wait(), "child must be terminated without graceful cleanup")
			path, err := GetOutboxPath()
			require.NoError(t, err)
			data, err := os.ReadFile(path)
			require.NoError(t, err)
			var ob types.Outbox
			require.NoError(t, json.Unmarshal(data, &ob), "kill cannot corrupt committed JSON")
			require.Equal(t, seed.Entries[0], ob.Entries[0], "previously durable work must survive")
			stored, err := mustGetStoredMessage(t, eventID)
			require.NoError(t, err)
			require.Nil(t, stored, "no outgoing history may precede the signed-event queue commit")
			if stage == "before-commit" {
				require.Len(t, ob.Entries, 1, "uncommitted send was never acknowledged as queued")
			} else {
				require.Len(t, ob.Entries, 2, "committed send must remain recoverable after kill")
				require.Equal(t, eventID, ob.Entries[1].ID)
			}
			_, err = UpdateOutbox(func(*types.Outbox) error { return nil })
			require.NoError(t, err, "SIGKILL must release the process lock")
		})
	}
}

func TestExactlyOnceHistoryFailureRetainsOriginalQueue(t *testing.T) {
	setupAgentMsgCLI(t)
	secret := nostr.Generate()
	peer := nostr.Generate().Public()
	event := makeInboxEvent(t, peer, secret, nostr.Now(), "history-write-failed", nil)
	published := false
	result, err := sendQueuedAgentMessage(context.Background(), &event, common.EncodeNpub(peer), event.Content, "", "", false,
		[]string{"ws://127.0.0.1:1"}, time.Second,
		func(*nostr.Event, string, string, bool) error { return fmt.Errorf("history unavailable") }, enqueueOutboxEntry,
		func(context.Context, []string, nostr.Event, time.Duration) ([]agentMsgRelayResult, int) {
			published = true
			return nil, 0
		})
	require.Error(t, err)
	require.False(t, published, "a failed history write must not begin publication")
	require.False(t, result.HistoryStored)
	require.True(t, result.QueuedForRetry, "committed signed event must survive a failed history write")
	require.Equal(t, AgentMessageQueued, deliveryState(result))
	require.Equal(t, AgentMessageIssueHistoryNotStored, deliveryIssue(result, err))
	ob, err := LoadOutbox()
	require.NoError(t, err)
	require.Len(t, ob.Entries, 1)
	var queued nostr.Event
	require.NoError(t, json.Unmarshal([]byte(ob.Entries[0].EventJSON), &queued))
	require.Equal(t, event, queued)
	stored, err := mustGetStoredMessage(t, event.ID.Hex())
	require.NoError(t, err)
	require.Nil(t, stored)
}

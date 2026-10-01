//go:build linux || darwin

package daemon

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"github.com/gorilla/websocket"
	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/internal/identity"
	"github.com/iDoris-ai/hyphae/internal/messaging"
	"github.com/iDoris-ai/hyphae/internal/relayconfig"
	"github.com/iDoris-ai/hyphae/internal/relayquery"
	"github.com/iDoris-ai/hyphae/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type failAtWrite struct {
	writeCalls int
	failAt     int
	lines      bytes.Buffer
}

func (w *failAtWrite) Write(p []byte) (int, error) {
	w.writeCalls++
	if w.writeCalls == w.failAt {
		_, _ = w.lines.Write(p[:len(p)/2])
		return len(p) / 2, nil
	}
	return w.lines.Write(p)
}

func TestStatusModelReplay125AcrossFreshScanReset(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	messaging.ResetStoreForTest()
	t.Cleanup(messaging.ResetStoreForTest)
	require.NoError(t, messaging.InitStorage())
	ks := &types.KeyStore{Identities: make(map[string]*types.Identity), Contacts: make(map[string]*types.Contact)}
	myIdentity, err := identity.CreateIdentity(ks, "alice")
	require.NoError(t, err)
	mySK, err := identity.GetSecretKey(ks, "alice")
	require.NoError(t, err)
	sender := nostr.Generate()
	timestamp := nostr.Now() - 1
	events := make([]*nostr.Event, 125)
	for i := range events {
		compressed, err := messaging.CompressText(fmt.Sprintf("backlog-%03d", i))
		require.NoError(t, err)
		events[i] = &nostr.Event{
			CreatedAt: timestamp,
			Kind:      messaging.AgentKind,
			Tags:      nostr.Tags{{"p", common.PubKeyToHex(mySK.Public())}, {"z", messaging.CompressTag}},
			Content:   compressed,
			PubKey:    sender.Public(),
		}
		require.NoError(t, events[i].Sign(sender))
	}
	relayURL, _ := startHistoryRelay(t, events)
	runScan := func() (StatusSnapshot, int, relayScanResult, string) {
		var streamOutput bytes.Buffer
		generation, err := newDaemonStatusGeneration()
		require.NoError(t, err)
		status, err := NewStatusStream(&streamOutput, generation, 123, myIdentity.Npub, time.Now(), 1)
		require.NoError(t, err)
		require.NoError(t, status.SetProcessState(StatusProcessRunning, time.Now()))
		require.NoError(t, status.BeginScan(time.Now()))
		observer := relayScanObserver{
			begin: func(index int) error { return status.BeginRelay(index, time.Now()) },
			finish: func(result relayScanResult) error {
				return status.FinishRelay(result.RelayIndex, result.Stats, result.NewMessages, result.ProcessingFailures, result.QueryFailed, result.Canceled, time.Now())
			},
		}
		count, results, err := watchInboxObserved(context.Background(), myIdentity, ks, newSeenSet(), []string{relayURL}, false, false, relayquery.Walk, newDaemonLogger(&bytes.Buffer{}), observer)
		require.NoError(t, err)
		require.NoError(t, status.FinishScan(false, time.Now()))
		require.Len(t, results, 1)
		return status.Snapshot(), count, results[0], streamOutput.String()
	}
	first, firstNew, firstRelay, firstLines := runScan()
	assert.Equal(t, 125, firstNew)
	assert.Equal(t, 125, firstRelay.Stats.Fetched)
	assert.Equal(t, 3, firstRelay.Stats.Pages)
	assert.Equal(t, 125, firstRelay.NewMessages)
	assert.Equal(t, StatusScanIdle, first.Scan.State)
	assert.False(t, first.Scan.Relays[0].FinishedHint, "ordinary EOSE is not a finish hint")
	second, secondNew, secondRelay, secondLines := runScan()
	assert.Equal(t, 0, secondNew, "durable incoming uniqueness makes restart replay non-new")
	assert.Equal(t, 125, secondRelay.Stats.Fetched)
	assert.Equal(t, 0, secondRelay.NewMessages)
	assert.Equal(t, StatusScanIdle, second.Scan.State)
	assert.NotEqual(t, first.Generation, second.Generation, "each fresh status stream gets a new generation")
	for _, output := range []string{firstLines, secondLines} {
		for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
			var envelope StatusEnvelope
			require.NoError(t, json.Unmarshal([]byte(line), &envelope))
			assert.True(t, envelope.OK)
			assert.NotContains(t, line, "backlog-")
		}
	}
	inbox, err := messaging.GetInbox(nil, myIdentity.Npub, 200)
	require.NoError(t, err)
	assert.Len(t, inbox, 125)
}

// This exercises the actual CLI lifecycle against a local relay. Keep this
// source even in environments where sandbox policy prevents binding listeners.
func TestDaemonJSONLocalRelay125AcrossProcessRestart(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	messaging.ResetStoreForTest()
	t.Cleanup(messaging.ResetStoreForTest)
	ks := &types.KeyStore{Identities: make(map[string]*types.Identity), Contacts: make(map[string]*types.Contact)}
	myIdentity, err := identity.CreateIdentityWithPassword(ks, "alice", "s2b-test-secret")
	require.NoError(t, err)
	require.NoError(t, identity.SaveKeyStore(ks))

	mySK, err := identity.GetSecretKey(ks, "alice")
	require.NoError(t, err)
	sender := nostr.Generate()
	timestamp := nostr.Now() - 1
	events := make([]*nostr.Event, 125)
	for i := range events {
		compressed, err := messaging.CompressText(fmt.Sprintf("cli-backlog-%03d", i))
		require.NoError(t, err)
		events[i] = &nostr.Event{
			CreatedAt: timestamp,
			Kind:      messaging.AgentKind,
			Tags:      nostr.Tags{{"p", common.PubKeyToHex(mySK.Public())}, {"z", messaging.CompressTag}},
			Content:   compressed,
			PubKey:    sender.Public(),
		}
		require.NoError(t, events[i].Sign(sender))
	}
	relayURL, _ := startHistoryRelay(t, events)
	first, firstStdout, _ := runDaemonJSONChildWithRelays(t, home, "flag", []string{relayURL}, func(line StatusEnvelope) bool {
		return line.Data.Scan.State == StatusScanIdle && len(line.Data.Scan.Relays) == 1 && line.Data.Scan.Relays[0].NewMessages == 125
	})
	require.NotEmpty(t, first)
	assert.NotContains(t, firstStdout, "cli-backlog-")
	firstGeneration := first[0].Data.Generation
	firstScan := findStatus(t, first, func(line StatusEnvelope) bool { return line.Data.Scan.State == StatusScanIdle })
	require.Len(t, firstScan.Data.Scan.Relays, 1)
	assert.Equal(t, 125, firstScan.Data.Scan.Relays[0].Fetched)
	assert.Equal(t, 125, firstScan.Data.Scan.Relays[0].NewMessages)
	assert.False(t, firstScan.Data.Scan.Relays[0].FinishedHint)
	assert.Equal(t, StatusProcessStopped, first[len(first)-1].Data.Process)

	second, secondStdout, _ := runDaemonJSONChildWithRelays(t, home, "flag", []string{relayURL}, func(line StatusEnvelope) bool {
		return line.Data.Scan.State == StatusScanIdle && len(line.Data.Scan.Relays) == 1 && line.Data.Scan.Relays[0].Fetched == 125
	})
	require.NotEmpty(t, second)
	assert.NotContains(t, secondStdout, "cli-backlog-")
	secondScan := findStatus(t, second, func(line StatusEnvelope) bool { return line.Data.Scan.State == StatusScanIdle })
	assert.NotEqual(t, firstGeneration, second[0].Data.Generation)
	assert.Equal(t, 125, secondScan.Data.Scan.Relays[0].Fetched)
	assert.Zero(t, secondScan.Data.Scan.Relays[0].NewMessages)
	inbox, err := messaging.GetInbox(nil, myIdentity.Npub, 200)
	require.NoError(t, err)
	assert.Len(t, inbox, 125)
}

func TestDaemonJSONLocalRelayEmptyAndPartial(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	ks := &types.KeyStore{Identities: make(map[string]*types.Identity), Contacts: make(map[string]*types.Contact)}
	_, err := identity.CreateIdentityWithPassword(ks, "alice", "s2b-test-secret")
	require.NoError(t, err)
	require.NoError(t, identity.SaveKeyStore(ks))
	emptyRelay, _ := startHistoryRelay(t, nil)
	empty := runDaemonJSONChildForRelay(t, home, []string{emptyRelay}, func(line StatusEnvelope) bool {
		return line.Data.Scan.State == StatusScanIdle && len(line.Data.Scan.Relays) == 1
	})
	emptyScan := findStatus(t, empty, func(line StatusEnvelope) bool { return line.Data.Scan.State == StatusScanIdle })
	assert.Zero(t, emptyScan.Data.Scan.Relays[0].NewMessages)
	assert.Equal(t, StatusRelayAvailable, emptyScan.Data.Scan.Relays[0].State)

	partial := runDaemonJSONChildForRelay(t, home, []string{emptyRelay, "ws://127.0.0.1:1/private-content-marker"}, func(line StatusEnvelope) bool {
		return line.Data.Scan.State == StatusScanIncomplete
	})
	partialScan := findStatus(t, partial, func(line StatusEnvelope) bool { return line.Data.Scan.State == StatusScanIncomplete })
	require.Len(t, partialScan.Data.Scan.Relays, 2)
	assert.Equal(t, StatusRelayAvailable, partialScan.Data.Scan.Relays[0].State)
	assert.Equal(t, StatusRelayIncomplete, partialScan.Data.Scan.Relays[1].State)
}

func TestDaemonJSONLocalRelayWithoutEOSE(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	ks := &types.KeyStore{Identities: make(map[string]*types.Identity), Contacts: make(map[string]*types.Contact)}
	_, err := identity.CreateIdentityWithPassword(ks, "alice", "s2b-test-secret")
	require.NoError(t, err)
	require.NoError(t, identity.SaveKeyStore(ks))
	relayURL := startNoEOSERelay(t)
	lines := runDaemonJSONChildForRelay(t, home, []string{relayURL}, func(line StatusEnvelope) bool {
		return line.Data.Scan.State == StatusScanIncomplete
	})
	scan := findStatus(t, lines, func(line StatusEnvelope) bool { return line.Data.Scan.State == StatusScanIncomplete })
	require.Len(t, scan.Data.Scan.Relays, 1)
	require.NotNil(t, scan.Data.Scan.Relays[0].Error)
	assert.Equal(t, StatusErrorQueryFailed, *scan.Data.Scan.Relays[0].Error)
	assert.Equal(t, StatusProcessStopped, lines[len(lines)-1].Data.Process)
}

func TestDaemonJSONLocalRelayStorageFailure(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	messaging.ResetStoreForTest()
	t.Cleanup(messaging.ResetStoreForTest)
	ks := &types.KeyStore{Identities: make(map[string]*types.Identity), Contacts: make(map[string]*types.Contact)}
	myIdentity, err := identity.CreateIdentityWithPassword(ks, "alice", "s2b-test-secret")
	require.NoError(t, err)
	require.NoError(t, identity.SaveKeyStore(ks))
	mySK, err := identity.GetSecretKey(ks, "alice")
	require.NoError(t, err)
	sender := nostr.Generate()
	compressed, err := messaging.CompressText("storage-error-fixture")
	require.NoError(t, err)
	event := &nostr.Event{
		CreatedAt: nostr.Now() - 1,
		Kind:      messaging.AgentKind,
		Tags:      nostr.Tags{{"p", common.PubKeyToHex(mySK.Public())}, {"z", messaging.CompressTag}},
		Content:   compressed,
		PubKey:    sender.Public(),
	}
	require.NoError(t, event.Sign(sender))
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".hyphae"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(home, ".hyphae", "messages.db"), []byte("not a sqlite database"), 0o600))
	relayURL, _ := startHistoryRelay(t, []*nostr.Event{event})
	lines := runDaemonJSONChildForRelay(t, home, []string{relayURL}, func(line StatusEnvelope) bool {
		return line.Data.Scan.State == StatusScanIncomplete
	})
	scan := findStatus(t, lines, func(line StatusEnvelope) bool { return line.Data.Scan.State == StatusScanIncomplete })
	require.Len(t, scan.Data.Scan.Relays, 1)
	assert.Equal(t, 1, scan.Data.Scan.Relays[0].ProcessingFailures)
	assert.Equal(t, StatusErrorProcessingFailed, *scan.Data.Scan.Relays[0].Error)
	assert.Zero(t, scan.Data.Scan.Relays[0].NewMessages)
	assert.NotContains(t, lineText(scan), "storage-error-fixture")
	assert.NotEmpty(t, myIdentity.Npub)
}

func TestDaemonJSONConfigRelaySetAvailableAndUnavailable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	ks := &types.KeyStore{Identities: make(map[string]*types.Identity), Contacts: make(map[string]*types.Contact)}
	_, err := identity.CreateIdentityWithPassword(ks, "alice", "s2b-test-secret")
	require.NoError(t, err)
	require.NoError(t, identity.SaveKeyStore(ks))

	availableURL, requests := startHistoryRelay(t, nil)
	unavailableURL := "ws://127.0.0.1:1/config-failure-marker"
	resolver, err := relayconfig.New()
	require.NoError(t, err)
	require.NoError(t, resolver.Set([]string{availableURL, unavailableURL}))
	resolved, err := resolver.Resolve(nil)
	require.NoError(t, err)
	assert.Equal(t, "config", resolved.Source)
	assert.Equal(t, []string{availableURL, unavailableURL}, resolved.Relays)

	lines, stdout, _ := runDaemonJSONChildWithRelays(t, home, "flag", nil, func(line StatusEnvelope) bool {
		return line.Data.Scan.State == StatusScanIncomplete
	})
	scan := findStatus(t, lines, func(line StatusEnvelope) bool { return line.Data.Scan.State == StatusScanIncomplete })
	require.Len(t, scan.Data.Scan.Relays, 2)
	assert.Equal(t, StatusRelayAvailable, scan.Data.Scan.Relays[0].State)
	assert.Equal(t, StatusRelayIncomplete, scan.Data.Scan.Relays[1].State)
	assert.False(t, scan.Data.Scan.Relays[0].FinishedHint, "ordinary EOSE only ends this query")
	assert.Equal(t, int32(1), requests.Load(), "daemon must read the saved relay set without explicit --relay flags")
	assert.NotContains(t, stdout, availableURL)
	assert.NotContains(t, stdout, unavailableURL)
	assert.NotContains(t, stdout, "s2b-test-secret")
}

func TestDaemonJSONSIGTERMLeavesUnvisitedRelayUnknown(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	ks := &types.KeyStore{Identities: make(map[string]*types.Identity), Contacts: make(map[string]*types.Contact)}
	_, err := identity.CreateIdentityWithPassword(ks, "alice", "s2b-test-secret")
	require.NoError(t, err)
	require.NoError(t, identity.SaveKeyStore(ks))

	firstRelay := startNoEOSERelay(t)
	secondRelay, secondRequests := startHistoryRelay(t, nil)
	lines, stdout, _ := runDaemonJSONChildWithRelays(t, home, "flag", []string{firstRelay, secondRelay}, func(line StatusEnvelope) bool {
		return line.Data.Scan.State == StatusScanScanning && len(line.Data.Scan.Relays) == 2 && line.Data.Scan.Relays[0].State == StatusRelayScanning
	})
	scan := findStatus(t, lines, func(line StatusEnvelope) bool { return line.Data.Scan.State == StatusScanCanceled })
	require.Len(t, scan.Data.Scan.Relays, 2)
	assert.Equal(t, StatusRelayCanceled, scan.Data.Scan.Relays[0].State)
	assert.Equal(t, StatusRelayUnknown, scan.Data.Scan.Relays[1].State)
	assert.Equal(t, int32(0), secondRequests.Load(), "SIGTERM during the first local relay must prevent visiting the second")
	assert.Equal(t, StatusProcessStopped, lines[len(lines)-1].Data.Process)
	assert.NotContains(t, stdout, firstRelay)
	assert.NotContains(t, stdout, secondRelay)
	assert.NotContains(t, stdout, "s2b-test-secret")
}

func TestDaemonJSONLocalRelayDisconnectBeforeEOSE(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	messaging.ResetStoreForTest()
	t.Cleanup(messaging.ResetStoreForTest)
	ks := &types.KeyStore{Identities: make(map[string]*types.Identity), Contacts: make(map[string]*types.Contact)}
	myIdentity, err := identity.CreateIdentityWithPassword(ks, "alice", "s2b-test-secret")
	require.NoError(t, err)
	require.NoError(t, identity.SaveKeyStore(ks))
	mySK, err := identity.GetSecretKey(ks, "alice")
	require.NoError(t, err)
	compressed, err := messaging.CompressText("disconnect-private-body-marker")
	require.NoError(t, err)
	sender := nostr.Generate()
	event := &nostr.Event{
		CreatedAt: nostr.Now() - 1,
		Kind:      messaging.AgentKind,
		Tags:      nostr.Tags{{"p", common.PubKeyToHex(mySK.Public())}, {"z", messaging.CompressTag}},
		Content:   compressed,
		PubKey:    sender.Public(),
	}
	require.NoError(t, event.Sign(sender))
	relayURL := startDisconnectRelay(t, []*nostr.Event{event})
	lines, stdout, _ := runDaemonJSONChildWithRelays(t, home, "flag", []string{relayURL}, func(line StatusEnvelope) bool {
		return line.Data.Scan.State == StatusScanIncomplete
	})
	scan := findStatus(t, lines, func(line StatusEnvelope) bool { return line.Data.Scan.State == StatusScanIncomplete })
	require.Len(t, scan.Data.Scan.Relays, 1)
	assert.Equal(t, StatusRelayIncomplete, scan.Data.Scan.Relays[0].State)
	assert.Equal(t, StatusErrorQueryFailed, *scan.Data.Scan.Relays[0].Error)
	assert.Equal(t, 1, scan.Data.Scan.Relays[0].Fetched)
	assert.Equal(t, 1, scan.Data.Scan.Relays[0].NewMessages)
	assert.Zero(t, scan.Data.Scan.Relays[0].ProcessingFailures)
	inbox, err := messaging.GetInbox(nil, myIdentity.Npub, 10)
	require.NoError(t, err)
	require.Len(t, inbox, 1, "the event received before the disconnect remains durably stored")
	assert.NotContains(t, stdout, "disconnect-private-body-marker")
	assert.NotContains(t, stdout, relayURL)
	assert.NotContains(t, stdout, "s2b-test-secret")
}

func TestDaemonJSONCLIBrokenStdoutExitsNonZero(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	ks := &types.KeyStore{Identities: make(map[string]*types.Identity), Contacts: make(map[string]*types.Contact)}
	_, err := identity.CreateIdentityWithPassword(ks, "alice", "s2b-test-secret")
	require.NoError(t, err)
	require.NoError(t, identity.SaveKeyStore(ks))
	readEnd, writeEnd, err := os.Pipe()
	require.NoError(t, err)
	command := exec.Command(daemonLockCLI, "--json", "daemon", "--identity", "alice", "--relay", "ws://127.0.0.1:1/broken-pipe-marker", "--retry-interval", "3600", "--watch-interval", "3600", "--notify=false", "--password-stdin")
	command.Env = append(withoutDaemonOutputEnv(os.Environ()), "HOME="+home)
	command.Stdin = strings.NewReader("s2b-test-secret\n")
	command.Stdout = writeEnd
	var stderr bytes.Buffer
	command.Stderr = &stderr
	require.NoError(t, command.Start())
	require.NoError(t, writeEnd.Close())
	require.NoError(t, readEnd.Close())
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	select {
	case waitErr := <-done:
		require.Error(t, waitErr, "daemon must exit non-zero when status stdout is broken")
		var exitErr *exec.ExitError
		require.ErrorAs(t, waitErr, &exitErr)
		assert.NotZero(t, exitErr.ExitCode())
	case <-time.After(8 * time.Second):
		_ = command.Process.Kill()
		waitErr := <-done
		t.Fatalf("daemon kept running after stdout was closed; after kill Wait returned %v", waitErr)
	}
	assert.NotContains(t, stderr.String(), "s2b-test-secret")
	assert.NotContains(t, stderr.String(), "private-message-body-marker")
	assert.NotContains(t, stderr.String(), "broken-pipe-marker")
}

func findStatus(t *testing.T, lines []StatusEnvelope, match func(StatusEnvelope) bool) StatusEnvelope {
	t.Helper()
	for _, line := range lines {
		if match(line) {
			return line
		}
	}
	t.Fatalf("requested status was not emitted; got %d lines", len(lines))
	return StatusEnvelope{}
}

func TestStatusStreamEmptyAndPartialRealRelayResults(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	messaging.ResetStoreForTest()
	t.Cleanup(messaging.ResetStoreForTest)
	require.NoError(t, messaging.InitStorage())
	ks := &types.KeyStore{Identities: make(map[string]*types.Identity), Contacts: make(map[string]*types.Contact)}
	myIdentity, err := identity.CreateIdentity(ks, "alice")
	require.NoError(t, err)
	emptyRelayURL, _ := startHistoryRelay(t, nil)
	empty, count, results, err := runStatusBackfill(t, myIdentity, ks, []string{emptyRelayURL})
	require.NoError(t, err)
	assert.Zero(t, count)
	require.Len(t, results, 1)
	assert.Equal(t, StatusScanIdle, empty.Scan.State)
	assert.Equal(t, StatusRelayAvailable, empty.Scan.Relays[0].State)
	assert.False(t, empty.Scan.Relays[0].FinishedHint, "ordinary EOSE is not a finish hint")

	partial, count, results, err := runStatusBackfill(t, myIdentity, ks, []string{emptyRelayURL, "ws://127.0.0.1:1/private-content-marker"})
	require.Error(t, err)
	assert.Zero(t, count)
	require.Len(t, results, 2)
	assert.Equal(t, StatusScanIncomplete, partial.Scan.State)
	assert.Equal(t, StatusRelayAvailable, partial.Scan.Relays[0].State)
	assert.Equal(t, StatusRelayIncomplete, partial.Scan.Relays[1].State)
	require.NotNil(t, partial.Scan.Relays[1].Error)
	assert.Equal(t, StatusErrorQueryFailed, *partial.Scan.Relays[1].Error)
}

func runStatusBackfill(t *testing.T, myIdentity *types.Identity, ks *types.KeyStore, relays []string) (StatusSnapshot, int, []relayScanResult, error) {
	t.Helper()
	var output bytes.Buffer
	generation, err := newDaemonStatusGeneration()
	require.NoError(t, err)
	status, err := NewStatusStream(&output, generation, 456, myIdentity.Npub, time.Now(), len(relays))
	require.NoError(t, err)
	require.NoError(t, status.SetProcessState(StatusProcessRunning, time.Now()))
	require.NoError(t, status.BeginScan(time.Now()))
	observer := relayScanObserver{
		begin: func(index int) error { return status.BeginRelay(index, time.Now()) },
		finish: func(result relayScanResult) error {
			return status.FinishRelay(result.RelayIndex, result.Stats, result.NewMessages, result.ProcessingFailures, result.QueryFailed, result.Canceled, time.Now())
		},
	}
	count, results, scanErr := watchInboxObserved(context.Background(), myIdentity, ks, newSeenSet(), relays, false, false, relayquery.Walk, newDaemonLogger(&bytes.Buffer{}), observer)
	finishErr := status.FinishScan(false, time.Now())
	return status.Snapshot(), count, results, errors.Join(scanErr, finishErr)
}

func TestWatchInboxObserverKeepsRelayIndicesAndCompletionOrder(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ks := &types.KeyStore{Identities: make(map[string]*types.Identity), Contacts: make(map[string]*types.Contact)}
	myIdentity, err := identity.CreateIdentity(ks, "alice")
	require.NoError(t, err)
	var begins, finishes []int
	walkCalls := 0
	walk := func(_ context.Context, url string, _ nostr.Filter, _ func(nostr.Event) error) (relayquery.Stats, error) {
		walkCalls++
		assert.Equal(t, []string{"wss://relay-one.invalid", "wss://relay-two.invalid"}[walkCalls-1], url)
		return relayquery.Stats{Pages: 1, Fetched: walkCalls}, nil
	}
	observer := relayScanObserver{
		begin: func(index int) error {
			begins = append(begins, index)
			return nil
		},
		finish: func(result relayScanResult) error {
			finishes = append(finishes, result.RelayIndex)
			assert.True(t, result.Visited)
			return nil
		},
	}
	_, results, err := watchInboxObserved(context.Background(), myIdentity, ks, newSeenSet(), []string{"wss://relay-one.invalid", "wss://relay-two.invalid"}, false, false, walk, newDaemonLogger(&bytes.Buffer{}), observer)
	require.NoError(t, err)
	assert.Equal(t, []int{0, 1}, begins)
	assert.Equal(t, []int{0, 1}, finishes)
	require.Len(t, results, 2)
	assert.Equal(t, 0, results[0].RelayIndex)
	assert.Equal(t, 1, results[0].Stats.Fetched)
	assert.Equal(t, 1, results[1].RelayIndex)
	assert.Equal(t, 2, results[1].Stats.Fetched)
}

func TestStatusRuntimeCancellationKeepsUnvisitedRelayUnknown(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ks := &types.KeyStore{Identities: make(map[string]*types.Identity), Contacts: make(map[string]*types.Contact)}
	myIdentity, err := identity.CreateIdentity(ks, "alice")
	require.NoError(t, err)
	var output bytes.Buffer
	status := newRuntimeTestStatus(t, &output, myIdentity.Npub, 2)
	require.NoError(t, status.SetProcessState(StatusProcessRunning, time.Now()))
	require.NoError(t, status.BeginScan(time.Now()))
	ctx, cancel := context.WithCancel(context.Background())
	walkCalls := 0
	walk := func(ctx context.Context, _ string, _ nostr.Filter, _ func(nostr.Event) error) (relayquery.Stats, error) {
		walkCalls++
		cancel()
		return relayquery.Stats{Pages: 1}, ctx.Err()
	}
	observer := relayScanObserver{
		begin: func(index int) error { return status.BeginRelay(index, time.Now()) },
		finish: func(result relayScanResult) error {
			return status.FinishRelay(result.RelayIndex, result.Stats, result.NewMessages, result.ProcessingFailures, result.QueryFailed, result.Canceled, time.Now())
		},
	}
	_, results, err := watchInboxObserved(ctx, myIdentity, ks, newSeenSet(), []string{"wss://first.invalid", "wss://second.invalid"}, false, false, walk, newDaemonLogger(&bytes.Buffer{}), observer)
	require.ErrorIs(t, err, context.Canceled)
	require.Len(t, results, 2)
	assert.Equal(t, 1, walkCalls)
	assert.True(t, results[0].Visited)
	assert.True(t, results[0].Canceled)
	assert.False(t, results[0].QueryFailed)
	assert.False(t, results[1].Visited)
	require.NoError(t, status.FinishScan(true, time.Now()))
	snapshot := status.Snapshot()
	assert.Equal(t, StatusScanCanceled, snapshot.Scan.State)
	assert.Equal(t, StatusRelayCanceled, snapshot.Scan.Relays[0].State)
	assert.Equal(t, StatusRelayUnknown, snapshot.Scan.Relays[1].State)
}

func TestStatusRuntimeClassifiesStorageFailureAndMissingEOSE(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	messaging.ResetStoreForTest()
	t.Cleanup(messaging.ResetStoreForTest)
	require.NoError(t, messaging.InitStorage())
	ks := &types.KeyStore{Identities: make(map[string]*types.Identity), Contacts: make(map[string]*types.Contact)}
	myIdentity, err := identity.CreateIdentity(ks, "alice")
	require.NoError(t, err)
	mySK, err := identity.GetSecretKey(ks, "alice")
	require.NoError(t, err)
	compressed, err := messaging.CompressText("storage failure body")
	require.NoError(t, err)
	event := signedIncomingEvent(t, nostr.Generate(), mySK.Public(), compressed, nostr.Tags{{"z", messaging.CompressTag}})
	hooks := incomingReceiveHooks{store: func(*nostr.Event, string, string, bool) (bool, error) {
		return false, errors.New("simulated storage failure")
	}}
	walkEvent := func(ctx context.Context, _ string, _ nostr.Filter, callback func(nostr.Event) error) (relayquery.Stats, error) {
		if err := callback(*event); err != nil {
			return relayquery.Stats{Pages: 1}, err
		}
		return relayquery.Stats{Pages: 1, Fetched: 1}, nil
	}
	storageResult, storageErr := watchOneRelayWithHooksResult(context.Background(), "wss://storage.invalid", nostr.Filter{}, ks, mySK, newSeenSet(), false, false, myIdentity, nil, hooks, walkEvent)
	require.ErrorContains(t, storageErr, "failed processing")
	assert.Equal(t, 1, storageResult.ProcessingFailures)
	assert.Zero(t, storageResult.NewMessages)

	var output bytes.Buffer
	status := newRuntimeTestStatus(t, &output, myIdentity.Npub, 1)
	require.NoError(t, status.SetProcessState(StatusProcessRunning, time.Now()))
	require.NoError(t, status.BeginScan(time.Now()))
	require.NoError(t, status.BeginRelay(0, time.Now()))
	require.NoError(t, status.FinishRelay(0, storageResult.Stats, storageResult.NewMessages, storageResult.ProcessingFailures, storageResult.QueryFailed, storageResult.Canceled, time.Now()))
	require.NoError(t, status.FinishScan(false, time.Now()))
	assert.Equal(t, StatusScanIncomplete, status.Snapshot().Scan.State)
	assert.Equal(t, StatusErrorProcessingFailed, *status.Snapshot().Scan.Relays[0].Error)

	missingEOSE := errors.New("relay closed before EOSE")
	noEOSEWalk := func(context.Context, string, nostr.Filter, func(nostr.Event) error) (relayquery.Stats, error) {
		return relayquery.Stats{Pages: 1}, missingEOSE
	}
	queryResult, queryErr := watchOneRelayWithHooksResult(context.Background(), "wss://no-eose.invalid", nostr.Filter{}, ks, mySK, newSeenSet(), false, false, myIdentity, nil, hooks, noEOSEWalk)
	require.ErrorIs(t, queryErr, missingEOSE)
	assert.True(t, queryResult.QueryFailed)
	assert.False(t, queryResult.Canceled)
	require.NoError(t, status.BeginScan(time.Now()))
	require.NoError(t, status.BeginRelay(0, time.Now()))
	require.NoError(t, status.FinishRelay(0, queryResult.Stats, queryResult.NewMessages, queryResult.ProcessingFailures, queryResult.QueryFailed, queryResult.Canceled, time.Now()))
	require.NoError(t, status.FinishScan(false, time.Now()))
	assert.Equal(t, StatusScanIncomplete, status.Snapshot().Scan.State)
	assert.Equal(t, StatusErrorQueryFailed, *status.Snapshot().Scan.Relays[0].Error)
}

func newRuntimeTestStatus(t *testing.T, writer io.Writer, npub string, relayCount int) *StatusStream {
	t.Helper()
	status, err := NewStatusStream(writer, "000102030405060708090a0b0c0d0e0f", 1, npub, time.Now(), relayCount)
	require.NoError(t, err)
	return status
}

func TestRuntimeStatusShortWriteStopsFurtherRelayAndScheduledWork(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	messaging.ResetStoreForTest()
	t.Cleanup(messaging.ResetStoreForTest)
	require.NoError(t, messaging.InitStorage())
	ks := &types.KeyStore{Identities: make(map[string]*types.Identity), Contacts: make(map[string]*types.Contact)}
	myIdentity, err := identity.CreateIdentity(ks, "alice")
	require.NoError(t, err)
	writer := &failAtWrite{failAt: 5}
	status, err := NewStatusStream(writer, "000102030405060708090a0b0c0d0e0f", 1, myIdentity.Npub, time.Now(), 2)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	logger := newDaemonLogger(&bytes.Buffer{})
	var outboxCalls, scanCalls, walkCalls, cleanupCalls int
	work := daemonRuntimeWork{
		processOutbox: func() error { outboxCalls++; return nil },
		scanInbox: func() error {
			scanCalls++
			if err := status.BeginScan(time.Now()); err != nil {
				return err
			}
			observer := relayScanObserver{
				begin: func(index int) error { return status.BeginRelay(index, time.Now()) },
				finish: func(result relayScanResult) error {
					return status.FinishRelay(result.RelayIndex, result.Stats, result.NewMessages, result.ProcessingFailures, result.QueryFailed, result.Canceled, time.Now())
				},
			}
			_, _, scanErr := watchInboxObserved(ctx, myIdentity, ks, newSeenSet(), []string{"wss://one.invalid", "wss://two.invalid"}, false, false,
				func(context.Context, string, nostr.Filter, func(nostr.Event) error) (relayquery.Stats, error) {
					walkCalls++
					return relayquery.Stats{Pages: 1}, nil
				}, logger, observer)
			if errors.Is(scanErr, errStatusOutput) {
				return errStatusOutput
			}
			return scanErr
		},
		cleanup: func() { cleanupCalls++ },
	}
	err = runDaemonRuntime(ctx, logger, status, time.Millisecond, time.Millisecond, time.Millisecond, work)
	require.ErrorIs(t, err, errStatusOutput)
	require.EqualError(t, err, "daemon status output failed", "do not echo downstream query details after stream failure")
	assert.Equal(t, 1, outboxCalls, "the initial outbox pass precedes the first scan")
	assert.Equal(t, 1, scanCalls)
	assert.Equal(t, 1, walkCalls, "the failed result write must stop before the next relay")
	assert.Zero(t, cleanupCalls)
	assert.Equal(t, 5, writer.writeCalls, "no next scan or shutdown status is attempted after a terminal write failure")
	assert.NotContains(t, writer.lines.String(), "relay-one.invalid")
	assert.NotContains(t, writer.lines.String(), "private body")
}

func TestDaemonJSONLCLIOutputModesAndGenerationRestart(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	ks := &types.KeyStore{Identities: make(map[string]*types.Identity), Contacts: make(map[string]*types.Contact)}
	_, err := identity.CreateIdentityWithPassword(ks, "alice", "s2b-test-secret")
	require.NoError(t, err)
	require.NoError(t, identity.SaveKeyStore(ks))

	modes := []string{"flag", "current-env", "legacy-env", "flag"}
	generations := make(map[string]struct{})
	for _, mode := range modes {
		lines, stdout, stderr := runDaemonJSONChild(t, home, mode)
		require.GreaterOrEqual(t, len(lines), 8)
		for _, line := range lines {
			assert.True(t, line.OK)
			assert.NotContains(t, lineText(line), "127.0.0.1:1")
		}
		generation := lines[0].Data.Generation
		assert.Len(t, generation, 32)
		if mode == "flag" && len(generations) > 0 {
			_, alreadySeen := generations[generation]
			assert.False(t, alreadySeen, "each daemon process restart gets a fresh generation")
		}
		generations[generation] = struct{}{}
		assert.Equal(t, StatusProcessStarting, lines[0].Data.Process)
		assert.Equal(t, StatusProcessRunning, lines[1].Data.Process)
		assert.Equal(t, StatusScanIncomplete, lines[5].Data.Scan.State)
		require.Len(t, lines[5].Data.Scan.Relays, 1)
		assert.Equal(t, StatusRelayIncomplete, lines[5].Data.Scan.Relays[0].State)
		require.NotNil(t, lines[5].Data.Scan.Relays[0].Error)
		assert.Equal(t, StatusErrorQueryFailed, *lines[5].Data.Scan.Relays[0].Error)
		assert.Equal(t, StatusProcessStopping, lines[len(lines)-2].Data.Process)
		assert.Equal(t, StatusProcessStopped, lines[len(lines)-1].Data.Process)
		for index, line := range lines {
			assert.Equal(t, uint64(index+1), line.Data.EventSequence)
			assert.Equal(t, generation, line.Data.Generation)
		}
		assert.NotContains(t, stdout, "127.0.0.1:1/private-content-marker")
		assert.NotContains(t, stdout, "s2b-test-secret")
		assert.NotContains(t, stdout, "Starting daemon")
		assert.Contains(t, stderr, "Inbox scan incomplete", "ordinary diagnostics belong on stderr in JSON mode")
	}
}

func TestDaemonHumanModeKeepsExistingStartupAndStopText(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	ks := &types.KeyStore{Identities: make(map[string]*types.Identity), Contacts: make(map[string]*types.Contact)}
	_, err := identity.CreateIdentityWithPassword(ks, "alice", "s2b-test-secret")
	require.NoError(t, err)
	require.NoError(t, identity.SaveKeyStore(ks))
	_, stdout, stderr := runDaemonJSONChild(t, home, "human")
	assert.Contains(t, stdout, "🚀 Starting daemon for 'alice'")
	assert.Contains(t, stdout, "Relays: [ws://127.0.0.1:1/private-content-marker]")
	assert.Contains(t, stdout, "Press Ctrl+C to stop")
	assert.Contains(t, stdout, "Stopping daemon")
	assert.Contains(t, stdout, "Inbox scan incomplete")
	assert.NotContains(t, stdout, `"schema":"hyphae-daemon-status/1"`)
	assert.Empty(t, stderr)
}

func TestDaemonJSONKillDoesNotClaimStopped(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	ks := &types.KeyStore{Identities: make(map[string]*types.Identity), Contacts: make(map[string]*types.Contact)}
	_, err := identity.CreateIdentityWithPassword(ks, "alice", "s2b-test-secret")
	require.NoError(t, err)
	require.NoError(t, identity.SaveKeyStore(ks))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	firstRelay := startNoEOSERelay(t)
	command := exec.CommandContext(ctx, daemonLockCLI, "--json", "daemon", "--identity", "alice", "--relay", firstRelay, "--retry-interval", "3600", "--watch-interval", "3600", "--notify=false", "--password-stdin")
	command.Env = append(withoutDaemonOutputEnv(os.Environ()), "HOME="+home)
	command.Stdin = strings.NewReader("s2b-test-secret\n")
	stdoutPipe, err := command.StdoutPipe()
	require.NoError(t, err)
	stderrPipe, err := command.StderrPipe()
	require.NoError(t, err)
	require.NoError(t, command.Start())
	stdoutReader := bufio.NewReader(stdoutPipe)
	waited := false
	defer func() {
		if waited {
			return
		}
		_ = command.Process.Kill()
		_, _ = io.Copy(io.Discard, stdoutReader)
		_, _ = io.Copy(io.Discard, stderrPipe)
		_ = command.Wait()
	}()
	var killedLines strings.Builder
	var firstGeneration string
	for {
		line, readErr := stdoutReader.ReadString('\n')
		require.NoError(t, readErr)
		killedLines.WriteString(line)
		var envelope StatusEnvelope
		require.NoError(t, json.Unmarshal([]byte(line), &envelope), "every stdout line must be a complete status envelope")
		if firstGeneration == "" {
			firstGeneration = envelope.Data.Generation
		}
		if envelope.Data.Scan.State == StatusScanScanning && len(envelope.Data.Scan.Relays) == 1 && envelope.Data.Scan.Relays[0].State == StatusRelayScanning {
			break
		}
	}
	require.NoError(t, command.Process.Kill())
	rest, err := io.ReadAll(stdoutReader)
	require.NoError(t, err)
	stderr, err := io.ReadAll(stderrPipe)
	require.NoError(t, err)
	waitErr := command.Wait()
	waited = true
	require.Error(t, waitErr, "SIGKILL must terminate the producer without a success exit")
	killedLines.Write(rest)
	assert.NotContains(t, killedLines.String(), `"process":"stopped"`)
	assert.NotContains(t, string(stderr), "stopped")

	secondRelay, _ := startHistoryRelay(t, nil)
	second, _, _ := runDaemonJSONChildWithRelays(t, home, "flag", []string{secondRelay}, func(line StatusEnvelope) bool {
		return line.Data.Scan.State == StatusScanIdle && len(line.Data.Scan.Relays) == 1
	})
	require.NotEmpty(t, second)
	assert.NotEqual(t, firstGeneration, second[0].Data.Generation, "a restarted daemon must start a new generation after the killed stream reached EOF")
	assert.Equal(t, StatusProcessStarting, second[0].Data.Process)
	assert.Equal(t, StatusProcessStopped, second[len(second)-1].Data.Process)
}

func lineText(envelope StatusEnvelope) string {
	data, _ := json.Marshal(envelope)
	return string(data)
}

func runDaemonJSONChild(t *testing.T, home, mode string) ([]StatusEnvelope, string, string) {
	t.Helper()
	target := func(line StatusEnvelope) bool { return line.Data.Scan.State == StatusScanIncomplete }
	return runDaemonJSONChildWithRelays(t, home, mode, []string{"ws://127.0.0.1:1/private-content-marker"}, target)
}

func runDaemonJSONChildForRelay(t *testing.T, home string, relays []string, target func(StatusEnvelope) bool) []StatusEnvelope {
	t.Helper()
	lines, _, _ := runDaemonJSONChildWithRelays(t, home, "flag", relays, target)
	return lines
}

func runDaemonJSONChildWithRelays(t *testing.T, home, mode string, relays []string, target func(StatusEnvelope) bool) ([]StatusEnvelope, string, string) {
	t.Helper()
	args := []string{"daemon", "--identity", "alice"}
	for _, relayURL := range relays {
		args = append(args, "--relay", relayURL)
	}
	args = append(args, "--retry-interval", "3600", "--watch-interval", "3600", "--notify=false", "--password-stdin")
	if mode == "flag" {
		args = append([]string{"--json"}, args...)
	}
	command := exec.Command(daemonLockCLI, args...)
	stdoutPipe, err := command.StdoutPipe()
	require.NoError(t, err)
	stderrPipe, err := command.StderrPipe()
	require.NoError(t, err)
	command.Stdin = strings.NewReader("s2b-test-secret\n")
	command.Env = withoutDaemonOutputEnv(os.Environ())
	command.Env = append(command.Env,
		"HOME="+home,
	)
	switch mode {
	case "current-env":
		command.Env = append(command.Env, "HYPHAE_OUTPUT=json")
	case "legacy-env":
		command.Env = append(command.Env, "AGENT_SPEAKER_OUTPUT=json")
	}
	require.NoError(t, command.Start())
	var stderr bytes.Buffer
	stderrDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(&stderr, stderrPipe)
		close(stderrDone)
	}()
	type scanOutput struct {
		line string
		err  error
		done bool
	}
	output := make(chan scanOutput, 32)
	readerStop := make(chan struct{})
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		scanner := bufio.NewScanner(stdoutPipe)
		for scanner.Scan() {
			select {
			case output <- scanOutput{line: scanner.Text()}:
			case <-readerStop:
				return
			}
		}
		select {
		case output <- scanOutput{err: scanner.Err(), done: true}:
		case <-readerStop:
		}
	}()
	waited := false
	defer func() {
		if !waited {
			_ = command.Process.Kill()
		}
		close(readerStop)
		<-readerDone
		<-stderrDone
		if !waited {
			_ = command.Wait()
		}
	}()
	var lines []StatusEnvelope
	var stdout strings.Builder
	signaled := false
	eof := false
	deadline := time.NewTimer(8 * time.Second)
	defer deadline.Stop()
	for !eof {
		select {
		case item := <-output:
			if item.done {
				require.NoError(t, item.err, "read daemon stdout")
				eof = true
				continue
			}
			stdout.WriteString(item.line)
			stdout.WriteByte('\n')
			if mode == "human" {
				if strings.Contains(item.line, "Inbox scan incomplete") && !signaled {
					require.NoError(t, command.Process.Signal(syscall.SIGTERM))
					signaled = true
				}
				continue
			}
			var envelope StatusEnvelope
			require.NoError(t, json.Unmarshal([]byte(item.line), &envelope), "every stdout line must be a complete status envelope")
			lines = append(lines, envelope)
			if target != nil && target(envelope) && !signaled {
				require.NoError(t, command.Process.Signal(syscall.SIGTERM))
				signaled = true
			}
		case <-deadline.C:
			t.Fatalf("daemon did not produce the expected state stream; stdout=%s", stdout.String())
		}
	}
	require.True(t, signaled, "target status must be emitted before shutdown")
	<-stderrDone
	waitErr := command.Wait()
	waited = true
	require.NoError(t, waitErr, stderr.String())
	return lines, stdout.String(), stderr.String()
}

func withoutDaemonOutputEnv(env []string) []string {
	result := make([]string, 0, len(env))
	for _, item := range env {
		if strings.HasPrefix(item, "HYPHAE_OUTPUT=") || strings.HasPrefix(item, "AGENT_SPEAKER_OUTPUT=") {
			continue
		}
		result = append(result, item)
	}
	return result
}

func startDisconnectRelay(t *testing.T, events []*nostr.Event) string {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		if _, request, err := conn.ReadMessage(); err != nil {
			return
		} else {
			var fields []json.RawMessage
			if json.Unmarshal(request, &fields) != nil || len(fields) < 2 {
				return
			}
			var subID string
			if json.Unmarshal(fields[1], &subID) != nil {
				return
			}
			for _, event := range events {
				wire, _ := json.Marshal([]any{"EVENT", subID, event})
				if conn.WriteMessage(websocket.TextMessage, wire) != nil {
					return
				}
			}
		}
		// Close the TCP connection without EOSE or a websocket close frame.
		_ = conn.UnderlyingConn().Close()
	}))
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http")
}

func startNoEOSERelay(t *testing.T) string {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetReadDeadline(time.Now().Add(7 * time.Second))
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
		// Deliberately keep the subscription open without EOSE. The real
		// relayquery fetch must classify its timeout as an incomplete query.
		_, _, _ = conn.ReadMessage()
	}))
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http")
}

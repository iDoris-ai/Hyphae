package daemon

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/internal/relayquery"
)

const testGeneration = "00112233445566778899aabbccddeeff"

func statusTestTime(minute int) time.Time {
	return time.Date(2026, time.October, 1, 12, minute, 0, 0, time.FixedZone("test", 7*60*60))
}

func newTestStatus(t *testing.T, writer *bytes.Buffer, relayCount int) *StatusStream {
	t.Helper()
	stream, err := NewStatusStream(writer, testGeneration, 42, common.EncodeNpub(nostr.Generate().Public()), statusTestTime(0), relayCount)
	if err != nil {
		t.Fatal(err)
	}
	return stream
}

func TestStatusStreamJSONLinesAndFreshScanReset(t *testing.T) {
	var output bytes.Buffer
	stream := newTestStatus(t, &output, 1)
	if err := stream.SetProcessState(StatusProcessRunning, statusTestTime(1)); err != nil {
		t.Fatal(err)
	}
	if err := stream.BeginScan(statusTestTime(2)); err != nil {
		t.Fatal(err)
	}
	if err := stream.BeginRelay(0, statusTestTime(3)); err != nil {
		t.Fatal(err)
	}
	if err := stream.FinishRelay(0, relayquery.Stats{Pages: 1, Fetched: 2}, 2, 0, false, false, statusTestTime(4)); err != nil {
		t.Fatal(err)
	}
	if err := stream.FinishScan(false, statusTestTime(5)); err != nil {
		t.Fatal(err)
	}
	if got := stream.Snapshot(); got.Scan.State != StatusScanIdle || got.Scan.Relays[0].State != StatusRelayAvailable || got.Scan.Relays[0].FinishedHint || got.Scan.NewMessages != 2 {
		t.Fatalf("unexpected normal EOSE snapshot: %+v", got.Scan)
	}
	if err := stream.BeginScan(statusTestTime(6)); err != nil {
		t.Fatal(err)
	}
	fresh := stream.Snapshot()
	if fresh.Scan.Sequence != 2 || fresh.Scan.NewMessages != 0 || fresh.Scan.State != StatusScanScanning || fresh.Scan.EndedAt != nil || fresh.Scan.Relays[0].State != StatusRelayUnknown {
		t.Fatalf("scan did not reset: %+v", fresh.Scan)
	}
	if err := stream.BeginRelay(0, statusTestTime(7)); err != nil {
		t.Fatal(err)
	}
	if err := stream.FinishRelay(0, relayquery.Stats{Pages: 1, FinishedHint: true}, 0, 0, false, false, statusTestTime(8)); err != nil {
		t.Fatal(err)
	}
	if got := stream.Snapshot().Scan.Relays[0]; !got.FinishedHint || got.State != StatusRelayAvailable {
		t.Fatalf("finish hint incorrectly changed availability: %+v", got)
	}

	lines := strings.Split(strings.TrimSuffix(output.String(), "\n"), "\n")
	if len(lines) != 9 || !strings.HasSuffix(output.String(), "\n") {
		t.Fatalf("got %d lines and trailing newline=%t", len(lines), strings.HasSuffix(output.String(), "\n"))
	}
	for i, line := range lines {
		var envelope StatusEnvelope
		if err := json.Unmarshal([]byte(line), &envelope); err != nil {
			t.Fatalf("line %d: %v", i+1, err)
		}
		if !envelope.OK || envelope.Data.EventSequence != uint64(i+1) {
			t.Fatalf("line %d has invalid envelope: %+v", i+1, envelope)
		}
	}
	for _, secret := range []string{"wss://relay.example", "message body", "nsec1", "private relay error"} {
		if strings.Contains(output.String(), secret) {
			t.Fatalf("status output leaked %q", secret)
		}
	}
}

func TestStatusStreamPartialFailureAndCancellationClassification(t *testing.T) {
	var output bytes.Buffer
	stream := newTestStatus(t, &output, 3)
	if err := stream.SetProcessState(StatusProcessRunning, statusTestTime(1)); err != nil {
		t.Fatal(err)
	}
	if err := stream.BeginScan(statusTestTime(2)); err != nil {
		t.Fatal(err)
	}
	if err := stream.BeginRelay(0, statusTestTime(3)); err != nil {
		t.Fatal(err)
	}
	if err := stream.FinishRelay(0, relayquery.Stats{Pages: 2, Fetched: 5}, 1, 1, false, false, statusTestTime(4)); err != nil {
		t.Fatal(err)
	}
	if err := stream.BeginRelay(1, statusTestTime(5)); err != nil {
		t.Fatal(err)
	}
	if err := stream.FinishRelay(1, relayquery.Stats{Pages: 1, Fetched: 1}, 0, 0, true, false, statusTestTime(6)); err != nil {
		t.Fatal(err)
	}
	if err := stream.FinishScan(false, statusTestTime(7)); err != nil {
		t.Fatal(err)
	}
	snapshot := stream.Snapshot()
	if snapshot.Scan.State != StatusScanIncomplete || snapshot.Scan.Relays[0].Error == nil || *snapshot.Scan.Relays[0].Error != StatusErrorProcessingFailed || snapshot.Scan.Relays[1].Error == nil || *snapshot.Scan.Relays[1].Error != StatusErrorQueryFailed || snapshot.Scan.Relays[2].State != StatusRelayUnknown {
		t.Fatalf("unexpected partial result: %+v", snapshot.Scan)
	}
	if snapshot.Scan.Relays[0].Pages != 2 || snapshot.Scan.Relays[0].Fetched != 5 || snapshot.Scan.NewMessages != 1 {
		t.Fatalf("statistics not preserved: %+v", snapshot.Scan)
	}

	var cancelOutput bytes.Buffer
	canceled := newTestStatus(t, &cancelOutput, 2)
	if err := canceled.SetProcessState(StatusProcessRunning, statusTestTime(1)); err != nil {
		t.Fatal(err)
	}
	if err := canceled.BeginScan(statusTestTime(2)); err != nil {
		t.Fatal(err)
	}
	if err := canceled.BeginRelay(0, statusTestTime(3)); err != nil {
		t.Fatal(err)
	}
	if err := canceled.FinishScan(true, statusTestTime(4)); err != nil {
		t.Fatal(err)
	}
	got := canceled.Snapshot().Scan
	if got.State != StatusScanCanceled || got.Relays[0].State != StatusRelayCanceled || got.Relays[1].State != StatusRelayUnknown {
		t.Fatalf("parent cancellation misclassified: %+v", got)
	}

	var mixedOutput bytes.Buffer
	mixed := newTestStatus(t, &mixedOutput, 1)
	if err := mixed.SetProcessState(StatusProcessRunning, statusTestTime(1)); err != nil {
		t.Fatal(err)
	}
	if err := mixed.BeginScan(statusTestTime(2)); err != nil {
		t.Fatal(err)
	}
	if err := mixed.BeginRelay(0, statusTestTime(3)); err != nil {
		t.Fatal(err)
	}
	if err := mixed.FinishRelay(0, relayquery.Stats{Pages: 1, Fetched: 3}, 1, 1, true, true, statusTestTime(4)); err != nil {
		t.Fatal(err)
	}
	if err := mixed.FinishScan(true, statusTestTime(5)); err != nil {
		t.Fatal(err)
	}
	mixedRelay := mixed.Snapshot().Scan.Relays[0]
	if mixedRelay.State != StatusRelayCanceled || mixedRelay.Error == nil || *mixedRelay.Error != StatusErrorCanceled || mixedRelay.NewMessages != 1 || mixedRelay.ProcessingFailures != 1 {
		t.Fatalf("parent cancellation did not take precedence/preserve counts: %+v", mixedRelay)
	}

	var pendingOutput bytes.Buffer
	pending := newTestStatus(t, &pendingOutput, 2)
	if err := pending.SetProcessState(StatusProcessRunning, statusTestTime(1)); err != nil {
		t.Fatal(err)
	}
	if err := pending.BeginScan(statusTestTime(2)); err != nil {
		t.Fatal(err)
	}
	if err := pending.FinishScan(false, statusTestTime(3)); err != nil {
		t.Fatal(err)
	}
	if got := pending.Snapshot().Scan.State; got != StatusScanIncomplete {
		t.Fatalf("unvisited relays should leave scan incomplete, got %s", got)
	}

	var timedOutput bytes.Buffer
	timed := newTestStatus(t, &timedOutput, 1)
	if err := timed.SetProcessState(StatusProcessRunning, statusTestTime(1)); err != nil {
		t.Fatal(err)
	}
	if err := timed.BeginScan(statusTestTime(2)); err != nil {
		t.Fatal(err)
	}
	if err := timed.BeginRelay(0, statusTestTime(3)); err != nil {
		t.Fatal(err)
	}
	if err := timed.FinishRelay(0, relayquery.Stats{}, 0, 0, false, true, statusTestTime(4)); err != nil {
		t.Fatal(err)
	}
	if err := timed.FinishScan(false, statusTestTime(5)); err != nil {
		t.Fatal(err)
	}
	if got := timed.Snapshot().Scan.State; got != StatusScanIncomplete {
		t.Fatalf("relay-local timeout with live parent should be incomplete, got %s", got)
	}
}

type controlledStatusWriter struct {
	bytes.Buffer
	fail  bool
	short bool
}

func (w *controlledStatusWriter) Write(p []byte) (int, error) {
	if w.fail {
		return 0, errors.New("secret relay failure")
	}
	if w.short {
		return len(p) - 1, nil
	}
	return w.Buffer.Write(p)
}

func TestStatusStreamOutputFailureLatchesWithoutCommitting(t *testing.T) {
	writer := &controlledStatusWriter{}
	stream := newTestStatus(t, &writer.Buffer, 1)
	// The constructor wrote the initial record to the embedded buffer. Switch the
	// writer reference to the controllable wrapper for subsequent records.
	stream.writer = writer
	before := stream.Snapshot()
	writer.short = true
	if err := stream.SetProcessState(StatusProcessRunning, statusTestTime(1)); !errors.Is(err, errStatusOutput) {
		t.Fatalf("short write error = %v", err)
	}
	if got := stream.Snapshot(); got.EventSequence != before.EventSequence || got.Process != before.Process {
		t.Fatalf("failed line was committed: %+v", got)
	}
	written := writer.Len()
	writer.short = false
	if err := stream.SetProcessState(StatusProcessRunning, statusTestTime(2)); !errors.Is(err, errStatusOutput) {
		t.Fatalf("latched error = %v", err)
	}
	if writer.Len() != written {
		t.Fatal("latched stream wrote after partial line")
	}
	if strings.Contains(errStatusOutput.Error(), "secret relay failure") {
		t.Fatal("output error echoed external details")
	}

	var failedWriter controlledStatusWriter
	failedStream := newTestStatus(t, &failedWriter.Buffer, 0)
	failedStream.writer = &failedWriter
	beforeFailure := failedWriter.Len()
	failedWriter.fail = true
	if err := failedStream.SetProcessState(StatusProcessRunning, statusTestTime(1)); !errors.Is(err, errStatusOutput) {
		t.Fatalf("writer error = %v", err)
	}
	if failedWriter.Len() != beforeFailure {
		t.Fatal("failing writer unexpectedly emitted bytes")
	}
	afterFailure := failedWriter.Len()
	failedWriter.fail = false
	if err := failedStream.SetProcessState(StatusProcessRunning, statusTestTime(2)); !errors.Is(err, errStatusOutput) {
		t.Fatalf("writer failure did not latch: %v", err)
	}
	if failedWriter.Len() != afterFailure {
		t.Fatal("latched failed writer emitted bytes")
	}

	var failOutput controlledStatusWriter
	failOutput.fail = true
	if _, err := NewStatusStream(&failOutput, testGeneration, 42, common.EncodeNpub(nostr.Generate().Public()), statusTestTime(0), 0); !errors.Is(err, errStatusOutput) {
		t.Fatalf("initial output failure = %v", err)
	}
}

func TestStatusStreamValidationAndDetachedSnapshot(t *testing.T) {
	var output bytes.Buffer
	stream := newTestStatus(t, &output, 1)
	if err := stream.SetProcessState(StatusProcessRunning, statusTestTime(1)); err != nil {
		t.Fatal(err)
	}
	if err := stream.BeginScan(statusTestTime(2)); err != nil {
		t.Fatal(err)
	}
	if err := stream.BeginScan(statusTestTime(2)); !errors.Is(err, errStatusTransition) {
		t.Fatalf("nested scan accepted: %v", err)
	}
	if err := stream.BeginRelay(0, statusTestTime(3)); err != nil {
		t.Fatal(err)
	}
	if err := stream.FinishRelay(0, relayquery.Stats{}, 0, 0, true, false, statusTestTime(4)); err != nil {
		t.Fatal(err)
	}
	if err := stream.FinishScan(false, statusTestTime(5)); err != nil {
		t.Fatal(err)
	}
	detached := stream.Snapshot()
	detached.Scan.Relays[0].State = StatusRelayAvailable
	*detached.Scan.Relays[0].Error = StatusErrorCanceled
	*detached.Scan.StartedAt = time.Time{}
	if got := stream.Snapshot(); got.Scan.Relays[0].State != StatusRelayIncomplete || *got.Scan.Relays[0].Error != StatusErrorQueryFailed || got.Scan.StartedAt.IsZero() {
		t.Fatalf("snapshot shared mutable state: %+v", got.Scan)
	}

	if err := stream.SetProcessState(StatusProcessState("unknown"), statusTestTime(6)); !errors.Is(err, errStatusTransition) {
		t.Fatalf("unknown process state accepted: %v", err)
	}
	if err := stream.BeginRelay(-1, statusTestTime(6)); !errors.Is(err, errStatusTransition) {
		t.Fatalf("negative index accepted: %v", err)
	}
	if err := stream.FinishRelay(0, relayquery.Stats{Fetched: -1}, 0, 0, false, false, statusTestTime(6)); !errors.Is(err, errStatusTransition) {
		t.Fatalf("negative stats accepted: %v", err)
	}
	if _, err := NewStatusStream(&bytes.Buffer{}, strings.Repeat("g", 32), 1, common.EncodeNpub(nostr.Generate().Public()), statusTestTime(0), 0); !errors.Is(err, errStatusConfiguration) {
		t.Fatalf("bad generation accepted: %v", err)
	}
	if _, err := NewStatusStream(&bytes.Buffer{}, testGeneration, 1, "nsec1do-not-echo", statusTestTime(0), 0); !errors.Is(err, errStatusConfiguration) {
		t.Fatalf("invalid identity accepted: %v", err)
	}
	if _, err := NewStatusStream(&bytes.Buffer{}, testGeneration, 1, common.EncodeNpub(nostr.Generate().Public()), statusTestTime(0), -1); !errors.Is(err, errStatusConfiguration) {
		t.Fatalf("negative relay count accepted: %v", err)
	}
	if err := stream.SetProcessState(StatusProcessStopped, statusTestTime(6)); !errors.Is(err, errStatusTransition) {
		t.Fatalf("illegal process transition accepted: %v", err)
	}
	var lifecycleOutput bytes.Buffer
	lifecycle := newTestStatus(t, &lifecycleOutput, 0)
	if err := lifecycle.SetProcessState(StatusProcessRunning, statusTestTime(1)); err != nil {
		t.Fatal(err)
	}
	if err := lifecycle.BeginScan(statusTestTime(2)); err != nil {
		t.Fatal(err)
	}
	if err := lifecycle.SetProcessState(StatusProcessStopping, statusTestTime(3)); !errors.Is(err, errStatusTransition) {
		t.Fatalf("stopping with active scan accepted: %v", err)
	}
	if err := lifecycle.FinishScan(false, statusTestTime(3)); err != nil {
		t.Fatal(err)
	}
	if err := lifecycle.SetProcessState(StatusProcessStopping, statusTestTime(4)); err != nil {
		t.Fatal(err)
	}
	if err := lifecycle.BeginScan(statusTestTime(5)); !errors.Is(err, errStatusTransition) {
		t.Fatalf("scan after stopping accepted: %v", err)
	}
}

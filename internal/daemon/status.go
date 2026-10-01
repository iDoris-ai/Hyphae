package daemon

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/internal/relayquery"
)

const daemonStatusSchema = "hyphae-daemon-status/1"

var (
	errStatusConfiguration = errors.New("invalid daemon status configuration")
	errStatusTransition    = errors.New("invalid daemon status transition")
	errStatusOutput        = errors.New("daemon status output failed")
)

type StatusProcessState string

type StatusScanState string

type StatusRelayState string

type StatusErrorKind string

const (
	StatusProcessStarting StatusProcessState = "starting"
	StatusProcessRunning  StatusProcessState = "running"
	StatusProcessStopping StatusProcessState = "stopping"
	StatusProcessStopped  StatusProcessState = "stopped"

	StatusScanUnknown    StatusScanState = "unknown"
	StatusScanScanning   StatusScanState = "scanning"
	StatusScanIdle       StatusScanState = "idle"
	StatusScanIncomplete StatusScanState = "incomplete"
	StatusScanCanceled   StatusScanState = "canceled"

	StatusRelayUnknown    StatusRelayState = "unknown"
	StatusRelayScanning   StatusRelayState = "scanning"
	StatusRelayAvailable  StatusRelayState = "available"
	StatusRelayIncomplete StatusRelayState = "incomplete"
	StatusRelayCanceled   StatusRelayState = "canceled"

	StatusErrorQueryFailed      StatusErrorKind = "query_failed"
	StatusErrorProcessingFailed StatusErrorKind = "processing_failed"
	StatusErrorCanceled         StatusErrorKind = "canceled"
)

type StatusEnvelope struct {
	OK   bool           `json:"ok"`
	Data StatusSnapshot `json:"data"`
}

type StatusSnapshot struct {
	Schema        string             `json:"schema"`
	Generation    string             `json:"generation"`
	PID           int                `json:"pid"`
	EventSequence uint64             `json:"event_sequence"`
	IdentityNpub  string             `json:"identity_npub"`
	StartedAt     time.Time          `json:"started_at"`
	UpdatedAt     time.Time          `json:"updated_at"`
	Process       StatusProcessState `json:"process"`
	Scan          StatusScan         `json:"scan"`
}

type StatusScan struct {
	Sequence    uint64          `json:"sequence"`
	State       StatusScanState `json:"state"`
	StartedAt   *time.Time      `json:"started_at"`
	EndedAt     *time.Time      `json:"ended_at"`
	NewMessages int             `json:"new_messages"`
	Relays      []StatusRelay   `json:"relays"`
}

type StatusRelay struct {
	Index              int              `json:"index"`
	State              StatusRelayState `json:"state"`
	Pages              int              `json:"pages"`
	Fetched            int              `json:"fetched"`
	NewMessages        int              `json:"new_messages"`
	ProcessingFailures int              `json:"processing_failures"`
	FinishedHint       bool             `json:"finished_hint"`
	Error              *StatusErrorKind `json:"error"`
}

// StatusStream emits one complete JSON envelope per line. It is instance-local;
// callers may use stdout for the stream without changing the process-wide writer.
type StatusStream struct {
	mu          sync.Mutex
	writer      io.Writer
	snapshot    StatusSnapshot
	relayCount  int
	terminalErr bool
}

// NewStatusStream validates process metadata and writes the initial starting
// snapshot. generation is exactly 16 random bytes encoded as lowercase hex.
func NewStatusStream(writer io.Writer, generation string, pid int, identityNpub string, startedAt time.Time, relayCount int) (*StatusStream, error) {
	if writer == nil || !validGeneration(generation) || pid <= 0 || relayCount < 0 || startedAt.IsZero() {
		return nil, errStatusConfiguration
	}
	pubkey, err := common.ParsePublicKey(identityNpub)
	if err != nil || !strings.HasPrefix(identityNpub, "npub1") || common.EncodeNpub(pubkey) != identityNpub {
		return nil, errStatusConfiguration
	}
	startedAt = startedAt.UTC()
	relays := make([]StatusRelay, relayCount)
	for i := range relays {
		relays[i] = StatusRelay{Index: i, State: StatusRelayUnknown}
	}
	stream := &StatusStream{
		writer:     writer,
		relayCount: relayCount,
		snapshot: StatusSnapshot{
			Schema: daemonStatusSchema, Generation: generation, PID: pid,
			IdentityNpub: identityNpub, StartedAt: startedAt, UpdatedAt: startedAt,
			Process: StatusProcessStarting,
			Scan:    StatusScan{State: StatusScanUnknown, Relays: relays},
		},
	}
	if err := stream.emitLocked(stream.snapshot, startedAt); err != nil {
		return nil, err
	}
	return stream, nil
}

func validGeneration(generation string) bool {
	if len(generation) != 32 || strings.ToLower(generation) != generation {
		return false
	}
	decoded, err := hex.DecodeString(generation)
	return err == nil && len(decoded) == 16
}

// Snapshot returns a detached copy of the last completely written snapshot.
func (s *StatusStream) Snapshot() StatusSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneStatusSnapshot(s.snapshot)
}

func cloneStatusSnapshot(snapshot StatusSnapshot) StatusSnapshot {
	snapshot.Scan.Relays = append([]StatusRelay{}, snapshot.Scan.Relays...)
	if snapshot.Scan.StartedAt != nil {
		started := *snapshot.Scan.StartedAt
		snapshot.Scan.StartedAt = &started
	}
	if snapshot.Scan.EndedAt != nil {
		ended := *snapshot.Scan.EndedAt
		snapshot.Scan.EndedAt = &ended
	}
	for i := range snapshot.Scan.Relays {
		if snapshot.Scan.Relays[i].Error != nil {
			errorKind := *snapshot.Scan.Relays[i].Error
			snapshot.Scan.Relays[i].Error = &errorKind
		}
	}
	return snapshot
}

// SetProcessState emits a snapshot with the supplied closed-set process state.
func (s *StatusStream) SetProcessState(state StatusProcessState, now time.Time) error {
	if !validProcessState(state) || now.IsZero() {
		return errStatusTransition
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validProcessTransition(s.snapshot.Process, state) {
		return errStatusTransition
	}
	if state == StatusProcessStopping && s.snapshot.Scan.State == StatusScanScanning {
		return errStatusTransition
	}
	candidate := cloneStatusSnapshot(s.snapshot)
	candidate.Process = state
	return s.emitLocked(candidate, now)
}

// BeginScan resets all per-scan values, marks the new scan active, and advances
// its sequence only if the resulting line is written successfully.
func (s *StatusStream) BeginScan(now time.Time) error {
	if now.IsZero() {
		return errStatusTransition
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.snapshot.Process != StatusProcessRunning || s.snapshot.Scan.State == StatusScanScanning {
		return errStatusTransition
	}
	if s.snapshot.Scan.Sequence == math.MaxUint64 {
		return errStatusTransition
	}
	relays := make([]StatusRelay, s.relayCount)
	for i := range relays {
		relays[i] = StatusRelay{Index: i, State: StatusRelayUnknown}
	}
	candidate := cloneStatusSnapshot(s.snapshot)
	candidate.Scan = StatusScan{
		Sequence:  s.snapshot.Scan.Sequence + 1,
		State:     StatusScanScanning,
		StartedAt: timePointer(now),
		Relays:    relays,
	}
	return s.emitLocked(candidate, now)
}

// BeginRelay records that the configured relay at index has started this scan.
func (s *StatusStream) BeginRelay(index int, now time.Time) error {
	if now.IsZero() {
		return errStatusTransition
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.snapshot.Process != StatusProcessRunning || s.snapshot.Scan.State != StatusScanScanning || !validRelayIndex(index, s.relayCount) || s.snapshot.Scan.Relays[index].State != StatusRelayUnknown {
		return errStatusTransition
	}
	candidate := cloneStatusSnapshot(s.snapshot)
	candidate.Scan.Relays[index].State = StatusRelayScanning
	return s.emitLocked(candidate, now)
}

// FinishRelay applies only classified query/processing/cancellation results.
// Detailed errors and relay URLs are intentionally absent from the model.
func (s *StatusStream) FinishRelay(index int, stats relayquery.Stats, newMessages, processingFailures int, queryFailed, canceled bool, now time.Time) error {
	if now.IsZero() || stats.Pages < 0 || stats.Fetched < 0 || newMessages < 0 || processingFailures < 0 || newMessages > stats.Fetched || processingFailures > stats.Fetched-newMessages {
		return errStatusTransition
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.snapshot.Process != StatusProcessRunning || s.snapshot.Scan.State != StatusScanScanning || !validRelayIndex(index, s.relayCount) || s.snapshot.Scan.Relays[index].State != StatusRelayScanning {
		return errStatusTransition
	}
	candidate := cloneStatusSnapshot(s.snapshot)
	relay := StatusRelay{
		Index: index, Pages: stats.Pages, Fetched: stats.Fetched,
		NewMessages: newMessages, ProcessingFailures: processingFailures,
		FinishedHint: stats.FinishedHint,
	}
	switch {
	case canceled:
		relay.State = StatusRelayCanceled
		kind := StatusErrorCanceled
		relay.Error = &kind
	case queryFailed || processingFailures > 0:
		relay.State = StatusRelayIncomplete
		kind := StatusErrorProcessingFailed
		if queryFailed {
			kind = StatusErrorQueryFailed
		}
		relay.Error = &kind
	default:
		if stats.Pages == 0 {
			return errStatusTransition
		}
		relay.State = StatusRelayAvailable
	}
	candidate.Scan.Relays[index] = relay
	if newMessages > math.MaxInt-candidate.Scan.NewMessages {
		return errStatusTransition
	}
	candidate.Scan.NewMessages += newMessages
	return s.emitLocked(candidate, now)
}

// FinishScan records the scan summary. Unvisited relays remain unknown and any
// pending relay becomes canceled only when the parent context was canceled.
func (s *StatusStream) FinishScan(parentCanceled bool, now time.Time) error {
	if now.IsZero() {
		return errStatusTransition
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.snapshot.Process != StatusProcessRunning || s.snapshot.Scan.State != StatusScanScanning {
		return errStatusTransition
	}
	candidate := cloneStatusSnapshot(s.snapshot)
	candidate.Scan.EndedAt = timePointer(now)
	allAvailable := len(candidate.Scan.Relays) > 0
	anyIncomplete := false
	for i := range candidate.Scan.Relays {
		relay := &candidate.Scan.Relays[i]
		switch relay.State {
		case StatusRelayAvailable:
		case StatusRelayIncomplete:
			anyIncomplete = true
		case StatusRelayCanceled:
			allAvailable = false
		case StatusRelayUnknown, StatusRelayScanning:
			allAvailable = false
			if parentCanceled && relay.State == StatusRelayScanning {
				relay.State = StatusRelayCanceled
				kind := StatusErrorCanceled
				relay.Error = &kind
			}
		default:
			return errStatusTransition
		}
	}
	switch {
	case parentCanceled:
		candidate.Scan.State = StatusScanCanceled
	case anyIncomplete:
		candidate.Scan.State = StatusScanIncomplete
	case allAvailable:
		candidate.Scan.State = StatusScanIdle
	default:
		candidate.Scan.State = StatusScanIncomplete
	}
	return s.emitLocked(candidate, now)
}

func (s *StatusStream) emitLocked(candidate StatusSnapshot, now time.Time) error {
	if s.terminalErr {
		return errStatusOutput
	}
	if s.snapshot.EventSequence == math.MaxUint64 {
		return errStatusTransition
	}
	candidate.EventSequence = s.snapshot.EventSequence + 1
	candidate.UpdatedAt = now.UTC()
	if !validSnapshot(candidate, s.relayCount) {
		return errStatusTransition
	}
	line, err := json.Marshal(StatusEnvelope{OK: true, Data: candidate})
	if err != nil {
		return errStatusOutput
	}
	line = append(line, '\n')
	written, err := s.writer.Write(line)
	if err != nil || written != len(line) {
		s.terminalErr = true
		return errStatusOutput
	}
	s.snapshot = cloneStatusSnapshot(candidate)
	return nil
}

func validSnapshot(snapshot StatusSnapshot, relayCount int) bool {
	if snapshot.Schema != daemonStatusSchema || !validGeneration(snapshot.Generation) || snapshot.PID <= 0 || snapshot.EventSequence == 0 || snapshot.IdentityNpub == "" || snapshot.StartedAt.IsZero() || snapshot.UpdatedAt.IsZero() || !validProcessState(snapshot.Process) || !validScanState(snapshot.Scan.State) || snapshot.Scan.NewMessages < 0 || len(snapshot.Scan.Relays) != relayCount {
		return false
	}
	if snapshot.Scan.Sequence == 0 {
		if snapshot.Scan.State != StatusScanUnknown || snapshot.Scan.StartedAt != nil || snapshot.Scan.EndedAt != nil {
			return false
		}
	} else if snapshot.Scan.StartedAt == nil || snapshot.Scan.StartedAt.IsZero() || snapshot.Scan.State == StatusScanUnknown {
		return false
	}
	if snapshot.Scan.State == StatusScanScanning && snapshot.Scan.EndedAt != nil || snapshot.Scan.State != StatusScanScanning && snapshot.Scan.Sequence > 0 && snapshot.Scan.EndedAt == nil {
		return false
	}
	for i, relay := range snapshot.Scan.Relays {
		if relay.Index != i || !validRelayState(relay.State) || relay.Pages < 0 || relay.Fetched < 0 || relay.NewMessages < 0 || relay.ProcessingFailures < 0 {
			return false
		}
		if relay.Error != nil && !validStatusError(*relay.Error) {
			return false
		}
		if relay.State == StatusRelayUnknown && (relay.Pages != 0 || relay.Fetched != 0 || relay.NewMessages != 0 || relay.ProcessingFailures != 0 || relay.FinishedHint || relay.Error != nil) || relay.State == StatusRelayScanning && (relay.Pages != 0 || relay.Fetched != 0 || relay.NewMessages != 0 || relay.ProcessingFailures != 0 || relay.FinishedHint || relay.Error != nil) {
			return false
		}
		if relay.State == StatusRelayAvailable && relay.Error != nil || relay.State == StatusRelayIncomplete && relay.Error == nil || relay.State == StatusRelayCanceled && relay.Error == nil {
			return false
		}
	}
	return true
}

func validProcessTransition(current, next StatusProcessState) bool {
	switch current {
	case StatusProcessStarting:
		return next == StatusProcessRunning
	case StatusProcessRunning:
		return next == StatusProcessStopping
	case StatusProcessStopping:
		return next == StatusProcessStopped
	default:
		return false
	}
}

func validProcessState(state StatusProcessState) bool {
	return state == StatusProcessStarting || state == StatusProcessRunning || state == StatusProcessStopping || state == StatusProcessStopped
}

func validScanState(state StatusScanState) bool {
	return state == StatusScanUnknown || state == StatusScanScanning || state == StatusScanIdle || state == StatusScanIncomplete || state == StatusScanCanceled
}

func validRelayState(state StatusRelayState) bool {
	return state == StatusRelayUnknown || state == StatusRelayScanning || state == StatusRelayAvailable || state == StatusRelayIncomplete || state == StatusRelayCanceled
}

func validStatusError(kind StatusErrorKind) bool {
	return kind == StatusErrorQueryFailed || kind == StatusErrorProcessingFailed || kind == StatusErrorCanceled
}

func validRelayIndex(index, relayCount int) bool { return index >= 0 && index < relayCount }

func timePointer(value time.Time) *time.Time {
	utc := value.UTC()
	return &utc
}

package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"github.com/iDoris-ai/hyphae/pkg/types"
)

func TestGroupAttemptRejectsForeignSignedPayloadBeforeReserve(t *testing.T) {
	entry := groupAttemptEntry(t)
	var a nostr.Event
	if err := json.Unmarshal([]byte(entry.EventJSON), &a); err != nil {
		t.Fatal(err)
	}
	if !a.CheckID() || !a.VerifySignature() {
		t.Fatal("A must be signed")
	}
	b := nostr.Event{Kind: a.Kind, CreatedAt: a.CreatedAt, Content: a.Content + " foreign"}
	if err := b.Sign(nostr.Generate()); err != nil {
		t.Fatal(err)
	}
	if !b.CheckID() || !b.VerifySignature() {
		t.Fatal("B must be signed")
	}
	data, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	entry.EventJSON = string(data)
	h := &fakeAttemptHandler{}
	published := 0
	result, err := attemptSendGroupAttempt(context.Background(), entry, nil, time.Second, h,
		func(context.Context, []string, nostr.Event, time.Duration) bool { published++; return true })
	if !errors.Is(err, ErrGroupAttemptPayloadMismatch) || result.Issue != AgentMessageIssueOutboxBookkeepingFailed || len(h.calls) != 0 || published != 0 {
		t.Fatalf("result=%+v err=%v calls=%v published=%d", result, err, h.calls, published)
	}
}

func TestGroupAttemptRejectsInvalidSignatureBeforeReserve(t *testing.T) {
	entry := groupAttemptEntry(t)
	var event nostr.Event
	if err := json.Unmarshal([]byte(entry.EventJSON), &event); err != nil {
		t.Fatal(err)
	}
	event.Sig[0] ^= 1
	if !event.CheckID() || event.VerifySignature() {
		t.Fatal("fixture must isolate signature")
	}
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	entry.EventJSON = string(data)
	h := &fakeAttemptHandler{}
	result, err := attemptSendGroupAttempt(context.Background(), entry, nil, time.Second, h,
		func(context.Context, []string, nostr.Event, time.Duration) bool {
			t.Fatal("published invalid signature")
			return true
		})
	if !errors.Is(err, ErrGroupAttemptPayloadMismatch) || result.Issue != AgentMessageIssueOutboxBookkeepingFailed || len(h.calls) != 0 {
		t.Fatalf("result=%+v err=%v calls=%v", result, err, h.calls)
	}
}

func TestGroupAttemptAuthenticPayloadPublishesA(t *testing.T) {
	entry := groupAttemptEntry(t)
	h := &fakeAttemptHandler{}
	published := 0
	result, err := attemptSendGroupAttempt(context.Background(), entry, nil, time.Second, h,
		func(_ context.Context, _ []string, event nostr.Event, _ time.Duration) bool {
			published++
			if event.ID.Hex() != entry.ID || !event.CheckID() || !event.VerifySignature() {
				t.Fatal("published event differs from A")
			}
			return true
		})
	if err != nil || !result.Sent || published != 1 || fmt.Sprint(h.calls) != "[reserve start:7:0 accepted:7]" {
		t.Fatalf("result=%+v err=%v calls=%v published=%d", result, err, h.calls, published)
	}
}

type fakeAttemptHandler struct {
	reserveErr, startErr, acceptedErr, failedErr error
	exhausted                                    bool
	calls                                        []string
	token                                        GroupAttemptToken
}

func (f *fakeAttemptHandler) Reserve(entry types.OutboxEntry) (GroupAttemptToken, error) {
	f.calls = append(f.calls, "reserve")
	f.token = GroupAttemptToken{EventID: entry.ID, QueueID: entry.QueueID, AttemptGeneration: 7}
	return f.token, f.reserveErr
}
func (f *fakeAttemptHandler) Start(token GroupAttemptToken, targets int, _ int64) error {
	f.calls = append(f.calls, fmt.Sprintf("start:%d:%d", token.AttemptGeneration, targets))
	return f.startErr
}
func (f *fakeAttemptHandler) Accepted(token GroupAttemptToken, _ int64) error {
	f.calls = append(f.calls, fmt.Sprintf("accepted:%d", token.AttemptGeneration))
	return f.acceptedErr
}
func (f *fakeAttemptHandler) Failed(token GroupAttemptToken) (bool, error) {
	f.calls = append(f.calls, fmt.Sprintf("failed:%d", token.AttemptGeneration))
	return f.exhausted, f.failedErr
}

func groupAttemptEntry(t *testing.T) types.OutboxEntry {
	t.Helper()
	parsed, err := ParseRawGroupEntry(legacyFixtureEntry(t, "legacy-165-group-nil-relays.json"))
	if err != nil {
		t.Fatal(err)
	}
	return parsed.Entry
}

func runGroupAttempt(t *testing.T, h *fakeAttemptHandler, publishOK bool) (SendResult, error, int) {
	t.Helper()
	published := 0
	publish := func(_ context.Context, targets []string, event nostr.Event, _ time.Duration) bool {
		published++
		if len(targets) != 2 || event.ID.Hex() == "" {
			t.Fatalf("publish got targets=%v", targets)
		}
		return publishOK
	}
	store := func(*nostr.Event, string, string, bool) error {
		t.Fatal("DM history store must not run on the group route")
		return nil
	}
	opts := AttemptOptions{Handlers: OutboxHandlers{Attempt: h, Group: &refusingGroupHandler{t}}}
	result, err := attemptSendRouted(context.Background(), &types.Outbox{}, groupAttemptEntry(t), []string{"wss://a", "wss://b"}, time.Second, opts, publish, store)
	return result, err, published
}

// refusingGroupHandler proves the legacy handler is bypassed when Attempt is set.
type refusingGroupHandler struct{ t *testing.T }

func (r *refusingGroupHandler) BeforePublish(types.OutboxEntry) (bool, error) {
	r.t.Fatal("legacy GroupOutboxHandler must not run")
	return false, nil
}
func (r *refusingGroupHandler) MarkRelayAccepted(types.OutboxEntry, int, int) error { return nil }
func (r *refusingGroupHandler) RecordAttemptFailure(types.OutboxEntry, bool, AgentMessageDeliveryIssue) error {
	return nil
}

func TestAttemptSendGroupAttemptOutcomes(t *testing.T) {
	cases := map[string]struct {
		h         *fakeAttemptHandler
		publishOK bool
		want      SendResult
		wantErr   error
		calls     []string
		published int
	}{
		"reserve stale": {
			h:    &fakeAttemptHandler{reserveErr: fmt.Errorf("%w: g", ErrGroupAttemptStale)},
			want: SendResult{Superseded: true, Issue: AgentMessageIssueQueueSuperseded}, wantErr: ErrGroupAttemptStale, calls: []string{"reserve"},
		},
		"reserve pending": {
			h:    &fakeAttemptHandler{reserveErr: fmt.Errorf("%w: g", ErrGroupAttemptPending)},
			want: SendResult{QueueStateUnknown: true, Issue: AgentMessageIssueQueueStateUnknown}, wantErr: ErrGroupAttemptPending, calls: []string{"reserve"},
		},
		"reserve queue conflict": {
			h:    &fakeAttemptHandler{reserveErr: ErrGroupAttemptQueueConflict},
			want: SendResult{Issue: AgentMessageIssueQueueConflict}, wantErr: ErrGroupAttemptQueueConflict, calls: []string{"reserve"},
		},
		"reserve queue missing": {
			h:    &fakeAttemptHandler{reserveErr: ErrGroupAttemptQueueMissing},
			want: SendResult{Issue: AgentMessageIssueQueueMissing}, wantErr: ErrGroupAttemptQueueMissing, calls: []string{"reserve"},
		},
		"reserve other": {
			h:    &fakeAttemptHandler{reserveErr: errors.New("exhausted")},
			want: SendResult{Issue: AgentMessageIssueOutboxBookkeepingFailed}, wantErr: nil, calls: []string{"reserve"},
		},
		"start conflict, no publish": {
			h:    &fakeAttemptHandler{startErr: ErrGroupAttemptConflict},
			want: SendResult{Issue: AgentMessageIssueOutboxBookkeepingFailed}, wantErr: ErrGroupAttemptConflict, calls: []string{"reserve", "start:7:2"},
		},
		"publish fails, budget left": {
			h: &fakeAttemptHandler{}, publishOK: false,
			want: SendResult{Attempted: true, Queued: true, Issue: AgentMessageIssueSendFailed}, calls: []string{"reserve", "start:7:2", "failed:7"}, published: 1,
		},
		"publish fails, exhausted": {
			h: &fakeAttemptHandler{exhausted: true}, publishOK: false,
			want: SendResult{Attempted: true, MarkedFailed: true, Issue: AgentMessageIssueRetryExhausted}, calls: []string{"reserve", "start:7:2", "failed:7"}, published: 1,
		},
		"publish fails, O3 pending": {
			h: &fakeAttemptHandler{failedErr: ErrGroupAttemptPending}, publishOK: false,
			want: SendResult{Attempted: true, QueueStateUnknown: true, Issue: AgentMessageIssueQueueStateUnknown}, wantErr: ErrGroupAttemptPending, calls: []string{"reserve", "start:7:2", "failed:7"}, published: 1,
		},
		"publish ok": {
			h: &fakeAttemptHandler{}, publishOK: true,
			want: SendResult{Attempted: true, Sent: true}, calls: []string{"reserve", "start:7:2", "accepted:7"}, published: 1,
		},
		"publish ok, cleanup pending": {
			h: &fakeAttemptHandler{acceptedErr: ErrGroupAttemptCleanupPending}, publishOK: true,
			want: SendResult{Attempted: true, Sent: true, QueueStateUnknown: true}, wantErr: ErrGroupAttemptCleanupPending, calls: []string{"reserve", "start:7:2", "accepted:7"}, published: 1,
		},
		"publish ok, late ACK stale": {
			h: &fakeAttemptHandler{acceptedErr: ErrGroupAttemptStale}, publishOK: true,
			want: SendResult{Attempted: true, Sent: true, Superseded: true, Issue: AgentMessageIssueQueueSuperseded}, wantErr: ErrGroupAttemptStale, calls: []string{"reserve", "start:7:2", "accepted:7"}, published: 1,
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			result, err, published := runGroupAttempt(t, c.h, c.publishOK)
			if result != c.want {
				t.Fatalf("result = %+v, want %+v", result, c.want)
			}
			if c.wantErr != nil && !errors.Is(err, c.wantErr) {
				t.Fatalf("err = %v, want %v", err, c.wantErr)
			}
			if c.wantErr == nil && name != "reserve other" && err != nil {
				t.Fatalf("unexpected err %v", err)
			}
			if fmt.Sprint(c.h.calls) != fmt.Sprint(c.calls) || published != c.published {
				t.Fatalf("calls = %v (published %d), want %v (%d)", c.h.calls, published, c.calls, c.published)
			}
		})
	}
}

func TestAttemptSendRoutedPrefersAttemptHandlerAndKeepsLegacyFallback(t *testing.T) {
	entry := groupAttemptEntry(t)
	publish := func(context.Context, []string, nostr.Event, time.Duration) bool { return false }
	store := func(*nostr.Event, string, string, bool) error { return nil }
	result, err := attemptSendRouted(context.Background(), &types.Outbox{}, entry, nil, time.Second, AttemptOptions{}, publish, store)
	if !errors.Is(err, ErrGroupRouteHandlerMissing) || result.Issue != AgentMessageIssueRouteHandlerMissing {
		t.Fatalf("no handler: %+v %v", result, err)
	}
	bad := entry
	bad.EventJSON = "{"
	h := &fakeAttemptHandler{}
	result, err = attemptSendRouted(context.Background(), &types.Outbox{}, bad, nil, time.Second, AttemptOptions{Handlers: OutboxHandlers{Attempt: h}}, publish, store)
	if err == nil || result.Issue != AgentMessageIssueOutboxBookkeepingFailed || len(h.calls) != 0 {
		t.Fatalf("corrupt frozen event: %+v %v calls=%v", result, err, h.calls)
	}
}

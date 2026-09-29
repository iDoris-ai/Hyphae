package relayquery

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"github.com/gorilla/websocket"
)

func TestFetchEOSEAndEmptyEOSE(t *testing.T) {
	event := signedEvent(t)
	for _, test := range []struct {
		name   string
		events []nostr.Event
	}{
		{name: "empty"},
		{name: "event before EOSE", events: []nostr.Event{event}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newRelayFixture(t, relayPlan{events: test.events, eose: true})
			started := time.Now()
			page, err := Fetch(context.Background(), fixture.url, nostr.Filter{Kinds: []nostr.Kind{1}})
			if err != nil {
				t.Fatalf("Fetch: %v", err)
			}
			if len(page.Events) != len(test.events) {
				t.Fatalf("got %d events, want %d", len(page.Events), len(test.events))
			}
			if len(test.events) == 1 && page.Events[0].ID != event.ID {
				t.Fatalf("event was not retained: got %s want %s", page.Events[0].ID, event.ID)
			}
			if elapsed := time.Since(started); elapsed >= time.Second {
				t.Fatalf("EOSE query took %s; it should not wait for the five-second deadline", elapsed)
			}
			fixture.requireClosed(t)
		})
	}
}

func TestFetchEOSEImmediatelyFollowedByClosed(t *testing.T) {
	event := signedEvent(t)
	fixture := newRelayFixture(t, relayPlan{
		events: []nostr.Event{event}, eose: true, closed: "query complete", requests: 100,
	})
	for i := 0; i < 100; i++ {
		page, err := Fetch(context.Background(), fixture.url, nostr.Filter{Kinds: []nostr.Kind{1}})
		if err != nil {
			t.Fatalf("iteration %d: Fetch returned error after EOSE: %v", i, err)
		}
		if !sameEvents(page.Events, []nostr.Event{event}) {
			t.Fatalf("iteration %d: got events %#v, want signed event %s", i, page.Events, event.ID)
		}
		fixture.requireClosed(t)
	}
}

func TestFetchClosedWithoutEOSEIsError(t *testing.T) {
	fixture := newRelayFixture(t, relayPlan{closed: "auth required"})
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	page, err := Fetch(ctx, fixture.url, nostr.Filter{})
	if err == nil || !strings.Contains(err.Error(), "closed subscription before EOSE") {
		t.Fatalf("Fetch error = %v, want CLOSED without EOSE", err)
	}
	if len(page.Events) != 0 {
		t.Fatalf("got unexpected events: %#v", page.Events)
	}
	fixture.requireClosed(t)
}

func TestFetchWithoutEOSEReturnsPartialEventsOnDeadline(t *testing.T) {
	fixture := newRelayFixture(t, relayPlan{events: []nostr.Event{signedEvent(t)}})
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	page, err := Fetch(ctx, fixture.url, nostr.Filter{Kinds: []nostr.Kind{1}})
	if err == nil {
		t.Fatal("Fetch succeeded without EOSE")
	}
	if len(page.Events) != 1 {
		t.Fatalf("got %d partial events, want one", len(page.Events))
	}
	if !strings.Contains(err.Error(), "deadline exceeded") {
		t.Fatalf("Fetch error = %v, want deadline error", err)
	}
	fixture.requireClosed(t)
}

func TestFetchWithoutEOSEIsErrorOnCancellation(t *testing.T) {
	fixture := newRelayFixture(t, relayPlan{events: []nostr.Event{signedEvent(t)}})
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-fixture.eventSent
		cancel()
	}()
	page, err := Fetch(ctx, fixture.url, nostr.Filter{Kinds: []nostr.Kind{1}})
	if err == nil {
		t.Fatal("Fetch succeeded without EOSE")
	}
	if len(page.Events) > 1 || (len(page.Events) == 1 && page.Events[0].Content != "signed fixture event") {
		t.Fatalf("Fetch returned events outside the relay response: %#v", page.Events)
	}
	if !strings.Contains(err.Error(), "canceled") && !strings.Contains(err.Error(), "incomplete") && !strings.Contains(err.Error(), "before EOSE") {
		t.Fatalf("Fetch error = %v, want cancellation or incomplete-query error", err)
	}
	fixture.requireClosed(t)
}

func TestFetchWithoutEOSEUsesFiveSecondBound(t *testing.T) {
	fixture := newRelayFixture(t, relayPlan{})
	started := time.Now()
	page, err := Fetch(context.Background(), fixture.url, nostr.Filter{})
	if err == nil || !strings.Contains(err.Error(), "deadline exceeded") {
		t.Fatalf("Fetch error = %v, want bounded query deadline", err)
	}
	if len(page.Events) != 0 {
		t.Fatalf("got unexpected events: %#v", page.Events)
	}
	if elapsed := time.Since(started); elapsed < 4*time.Second || elapsed > 6*time.Second {
		t.Fatalf("query took %s, want about five seconds", elapsed)
	}
	fixture.requireClosed(t)
}

func TestFetchDisconnectWithoutEOSEIsError(t *testing.T) {
	fixture := newRelayFixture(t, relayPlan{events: []nostr.Event{signedEvent(t)}, disconnect: true})
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	page, err := Fetch(ctx, fixture.url, nostr.Filter{Kinds: []nostr.Kind{1}})
	if err == nil {
		t.Fatal("Fetch succeeded after relay disconnected without EOSE")
	}
	if len(page.Events) > 1 || (len(page.Events) == 1 && page.Events[0].Content != "signed fixture event") {
		t.Fatalf("Fetch returned events outside the relay response: %#v", page.Events)
	}
	if !strings.Contains(err.Error(), "incomplete") && !strings.Contains(err.Error(), "before EOSE") {
		t.Fatalf("Fetch error = %v, want explicit incomplete-query error", err)
	}
	fixture.requireClosed(t)
}

func TestFetchEOSEThenImmediateSocketCloseNeverReturnsIncompleteSuccess(t *testing.T) {
	events := signedEvents(t, 32)
	fixture := newRelayFixture(t, relayPlan{events: events, eose: true, disconnect: true})
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	page, err := Fetch(ctx, fixture.url, nostr.Filter{Kinds: []nostr.Kind{1}})
	if err == nil && !sameEvents(page.Events, events) {
		t.Fatalf("Fetch returned incomplete page as success: got %d of %d events", len(page.Events), len(events))
	}
	if err != nil && !strings.Contains(err.Error(), "incomplete") && !strings.Contains(err.Error(), "before EOSE") {
		t.Fatalf("Fetch error = %v, want an explicit incomplete-query error", err)
	}
	fixture.requireClosed(t)
}

func TestFetchMultiEventEOSECallerCancelRaceNeverReturnsIncompleteSuccess(t *testing.T) {
	events := signedEvents(t, 24)
	for attempt := 0; attempt < 20; attempt++ {
		fixture := newRelayFixture(t, relayPlan{events: events, eose: true})
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			select {
			case <-fixture.eoseSent:
				cancel()
			case <-ctx.Done():
			}
		}()
		page, err := Fetch(ctx, fixture.url, nostr.Filter{Kinds: []nostr.Kind{1}})
		cancel()
		if err == nil && !sameEvents(page.Events, events) {
			t.Fatalf("attempt %d: caller cancellation returned incomplete success: got %d of %d events", attempt, len(page.Events), len(events))
		}
		if len(page.Events) > len(events) {
			t.Fatalf("attempt %d: got %d events, sent %d", attempt, len(page.Events), len(events))
		}
		if err != nil && !strings.Contains(err.Error(), "incomplete") && !strings.Contains(err.Error(), "canceled") && !strings.Contains(err.Error(), "before EOSE") {
			t.Fatalf("attempt %d: Fetch error = %v, want explicit cancellation/incomplete error", attempt, err)
		}
		fixture.requireClosed(t)
	}
}

func TestFetchAuthHintReturnsHintsAndPartialEvents(t *testing.T) {
	event := signedEvent(t)
	hints := []string{"auth", "more", "opaque-hint"}
	fixture := newRelayFixture(t, relayPlan{events: []nostr.Event{event}, eose: true, hints: hints})
	page, err := Fetch(context.Background(), fixture.url, nostr.Filter{Kinds: []nostr.Kind{1}})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "auth") {
		t.Fatalf("Fetch error = %v, want explicit authorization error", err)
	}
	if len(page.Events) != 1 || page.Events[0].ID != event.ID {
		t.Fatalf("partial event was not preserved: %#v", page.Events)
	}
	if len(page.Hints) != len(hints) {
		t.Fatalf("hints = %#v, want %#v", page.Hints, hints)
	}
	for i := range hints {
		if page.Hints[i] != hints[i] {
			t.Fatalf("hints = %#v, want %#v", page.Hints, hints)
		}
	}
	fixture.requireClosed(t)
}

func TestFetchPreservesUnknownEOSEHints(t *testing.T) {
	hints := []string{"future-auth-mode", "opaque"}
	fixture := newRelayFixture(t, relayPlan{eose: true, hints: hints})
	page, err := Fetch(context.Background(), fixture.url, nostr.Filter{})
	if err != nil {
		t.Fatalf("unknown EOSE hints should not be treated as auth: %v", err)
	}
	if len(page.Hints) != len(hints) || page.Hints[0] != hints[0] || page.Hints[1] != hints[1] {
		t.Fatalf("hints = %#v, want %#v", page.Hints, hints)
	}
	fixture.requireClosed(t)
}

func signedEvent(t *testing.T) nostr.Event {
	t.Helper()
	event := nostr.Event{CreatedAt: nostr.Now(), Kind: 1, Content: "signed fixture event"}
	if err := event.Sign([32]byte{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	return event
}

func signedEvents(t *testing.T, count int) []nostr.Event {
	t.Helper()
	events := make([]nostr.Event, count)
	secret := [32]byte{1, 2, 3}
	created := nostr.Now()
	for i := range count {
		events[i] = nostr.Event{
			CreatedAt: created + nostr.Timestamp(i),
			Kind:      1,
			Content:   fmt.Sprintf("signed fixture event %d", i),
		}
		if err := events[i].Sign(secret); err != nil {
			t.Fatal(err)
		}
	}
	return events
}

func sameEvents(got, want []nostr.Event) bool {
	if len(got) != len(want) {
		return false
	}
	counts := make(map[string]int, len(want))
	for _, event := range want {
		counts[event.ID.Hex()]++
	}
	for _, event := range got {
		id := event.ID.Hex()
		if counts[id] == 0 {
			return false
		}
		counts[id]--
	}
	return true
}

type relayPlan struct {
	events     []nostr.Event
	eose       bool
	hints      []string
	closed     string
	disconnect bool
	requests   int
}

type relayFixture struct {
	url       string
	closed    chan struct{}
	eventSent chan struct{}
	eoseSent  chan struct{}
}

func newRelayFixture(t *testing.T, plan relayPlan) *relayFixture {
	t.Helper()
	if plan.requests == 0 {
		plan.requests = 1
	}
	fixture := &relayFixture{
		closed:    make(chan struct{}, plan.requests+1),
		eventSent: make(chan struct{}, plan.requests+1),
		eoseSent:  make(chan struct{}, plan.requests+1),
	}
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() {
			_ = conn.Close()
			fixture.closed <- struct{}{}
		}()
		_ = conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
		_, request, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var envelope []json.RawMessage
		if err := json.Unmarshal(request, &envelope); err != nil || len(envelope) < 2 {
			return
		}
		var label string
		if err := json.Unmarshal(envelope[1], &label); err != nil {
			return
		}
		for _, event := range plan.events {
			if err := conn.WriteJSON([]any{"EVENT", label, event}); err != nil {
				return
			}
			select {
			case fixture.eventSent <- struct{}{}:
			default:
			}
		}
		if plan.eose {
			var response []any
			if len(plan.hints) == 0 {
				response = []any{"EOSE", label}
			} else {
				response = []any{"EOSE", label, plan.hints}
			}
			if err := conn.WriteJSON(response); err != nil {
				return
			}
			select {
			case fixture.eoseSent <- struct{}{}:
			default:
			}
		}
		if plan.closed != "" {
			_ = conn.WriteJSON([]any{"CLOSED", label, plan.closed})
		}
		if plan.disconnect {
			return
		}
		_, _, _ = conn.ReadMessage()
	}))
	t.Cleanup(server.Close)
	fixture.url = "ws" + strings.TrimPrefix(server.URL, "http")
	return fixture
}

func (f *relayFixture) requireClosed(t *testing.T) {
	t.Helper()
	select {
	case <-f.closed:
	case <-time.After(time.Second):
		t.Fatal("relay WebSocket connection was not closed")
	}
}

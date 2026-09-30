package relayquery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"github.com/gorilla/websocket"
)

func TestWalkPagesInclusiveAndDeduplicates(t *testing.T) {
	e1 := paginationEvent(t, 150, "newest")
	e2 := paginationEvent(t, 100, "tie-a")
	e3 := paginationEvent(t, 100, "tie-b")
	e4 := paginationEvent(t, 99, "older")
	fixture := newPaginationRelay(t, func(filter nostr.Filter) ([]nostr.Event, []string) {
		if filter.Until == 200 {
			return []nostr.Event{e1, e2, e3}, []string{"more"}
		}
		if filter.Until == 100 {
			return []nostr.Event{e2, e3, e4}, nil
		}
		return nil, nil
	})

	var got []nostr.Event
	stats, err := Walk(context.Background(), fixture.url, nostr.Filter{
		Since: 50, Until: 200, Kinds: []nostr.Kind{30078}, Tags: nostr.TagMap{"p": {"recipient"}}, Limit: 7,
	}, func(event nostr.Event) error {
		got = append(got, event)
		return nil
	})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if stats.Pages != 2 || stats.Fetched != 4 || stats.FinishedHint {
		t.Fatalf("unexpected stats: %#v", stats)
	}
	wantIDs := map[nostr.ID]bool{e1.ID: true, e2.ID: true, e3.ID: true, e4.ID: true}
	if len(got) != 4 {
		t.Fatalf("callbacks got %#v; want four unique events", eventContents(got))
	}
	for _, event := range got {
		if !wantIDs[event.ID] {
			t.Fatalf("callback returned unexpected event %s", event.ID)
		}
		delete(wantIDs, event.ID)
	}
	if len(wantIDs) != 0 {
		t.Fatalf("callback missed event IDs: %#v", wantIDs)
	}
	filters := fixture.filters()
	if len(filters) != 2 {
		t.Fatalf("relay got %d queries, want 2", len(filters))
	}
	for _, filter := range filters {
		if filter.Since != 50 || filter.Limit != firstPageLimit || len(filter.Kinds) != 1 || filter.Kinds[0] != 30078 || filter.Tags["p"][0] != "recipient" {
			t.Fatalf("Walk changed caller filters: %#v", filter)
		}
	}
	if filters[0].Until != 200 || filters[1].Until != 100 {
		t.Fatalf("Until boundaries = %d, %d; want fixed 200 then inclusive 100", filters[0].Until, filters[1].Until)
	}
}

func TestWalkEmptyEOSEIsAvailableHistoryNotExplicitFinish(t *testing.T) {
	fixture := newPaginationRelay(t, func(nostr.Filter) ([]nostr.Event, []string) { return nil, nil })
	stats, err := Walk(context.Background(), fixture.url, nostr.Filter{Until: 100}, func(nostr.Event) error { return nil })
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if stats.Pages != 1 || stats.Fetched != 0 || stats.FinishedHint {
		t.Fatalf("ordinary empty EOSE should not claim finish: %#v", stats)
	}
}

func TestWalkExplicitFinishStopsEvenOnFullPage(t *testing.T) {
	events := make([]nostr.Event, firstPageLimit)
	for i := range events {
		events[i] = paginationEvent(t, 80, fmt.Sprintf("full-%d", i))
	}
	fixture := newPaginationRelay(t, func(nostr.Filter) ([]nostr.Event, []string) {
		return events, []string{"finish"}
	})
	stats, err := Walk(context.Background(), fixture.url, nostr.Filter{Until: 100}, func(nostr.Event) error { return nil })
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if stats.Pages != 1 || stats.Fetched != firstPageLimit || !stats.FinishedHint {
		t.Fatalf("explicit finish was not retained: %#v", stats)
	}
}

func TestWalkRejectsConflictingMoreAndFinishHints(t *testing.T) {
	event := paginationEvent(t, 80, "conflict")
	fixture := newPaginationRelay(t, func(nostr.Filter) ([]nostr.Event, []string) {
		return []nostr.Event{event}, []string{"more", "finish"}
	})
	stats, err := Walk(context.Background(), fixture.url, nostr.Filter{Until: 100}, func(nostr.Event) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "conflicting more and finish") || stats.Fetched != 1 {
		t.Fatalf("conflicting hints: stats=%#v err=%v", stats, err)
	}
}

func TestWalkProcessesVerifiedPartialEventsBeforeFetchError(t *testing.T) {
	event := paginationEvent(t, 80, "partial from auth relay")
	fixture := newPaginationRelay(t, func(nostr.Filter) ([]nostr.Event, []string) {
		return []nostr.Event{event}, []string{"auth"}
	})
	var got []nostr.Event
	stats, err := Walk(context.Background(), fixture.url, nostr.Filter{Until: 100}, func(event nostr.Event) error {
		got = append(got, event)
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "authorization") || stats.Fetched != 1 || len(got) != 1 || got[0].ID != event.ID {
		t.Fatalf("auth page lost partial event: stats=%#v events=%#v err=%v", stats, got, err)
	}
}

func TestWalkCallbackErrorStopsImmediately(t *testing.T) {
	event := paginationEvent(t, 80, "callback fixture")
	fixture := newPaginationRelay(t, func(nostr.Filter) ([]nostr.Event, []string) {
		return []nostr.Event{event}, []string{"more"}
	})
	callbackErr := errors.New("stop callback with private event content")
	stats, err := Walk(context.Background(), fixture.url, nostr.Filter{Until: 100}, func(nostr.Event) error { return callbackErr })
	if !errors.Is(err, callbackErr) || strings.Contains(err.Error(), "private event content") || stats.Pages != 1 || stats.Fetched != 0 {
		t.Fatalf("callback error result: stats=%#v err=%v", stats, err)
	}
}

func TestWalkExpandsSaturatedSameSecondThrough500(t *testing.T) {
	page100 := make([]nostr.Event, 100)
	page500 := make([]nostr.Event, 500)
	for i := range page500 {
		page500[i] = syntheticPaginationEvent(50, i)
		if i < len(page100) {
			page100[i] = page500[i]
		}
	}
	var mu sync.Mutex
	var limits []int
	fetch := func(_ context.Context, _ string, filter nostr.Filter) (Page, error) {
		mu.Lock()
		limits = append(limits, filter.Limit)
		mu.Unlock()
		switch len(limits) {
		case 1:
			return Page{Events: page100, Hints: []string{"more"}}, nil
		case 2:
			return Page{Events: page100, Hints: []string{"more"}}, nil
		default:
			return Page{Events: page500, Hints: []string{"more"}}, nil
		}
	}
	var callbacks int
	stats, err := walk(context.Background(), "ws://fixture.invalid", nostr.Filter{Until: 100}, func(nostr.Event) error {
		callbacks++
		return nil
	}, fetch)
	if err == nil || !strings.Contains(err.Error(), "no progress at maximum page size") {
		t.Fatalf("saturated same-second query should be incomplete, got %v", err)
	}
	if stats.Pages != 4 || stats.Fetched != 500 || callbacks != 500 {
		t.Fatalf("unexpected saturated walk: stats=%#v callbacks=%d", stats, callbacks)
	}
	if fmt.Sprint(limits) != "[100 100 500 500]" {
		t.Fatalf("page limits = %v", limits)
	}
}

func TestWalkCancellationReturnsPartialEventsAndError(t *testing.T) {
	event := paginationEvent(t, 80, "partial on cancel")
	ctx, cancel := context.WithCancel(context.Background())
	var seen bool
	fetch := func(ctx context.Context, _ string, _ nostr.Filter) (Page, error) {
		cancel()
		<-ctx.Done()
		return Page{Events: []nostr.Event{event}}, ctx.Err()
	}
	stats, err := walk(ctx, "ws://fixture.invalid", nostr.Filter{Until: 100}, func(nostr.Event) error {
		seen = true
		return nil
	}, fetch)
	if err == nil || seen || stats.Fetched != 0 || !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("canceled query lost partial result: stats=%#v seen=%t err=%v", stats, seen, err)
	}
}

func syntheticPaginationEvent(createdAt nostr.Timestamp, index int) nostr.Event {
	var event nostr.Event
	event.CreatedAt = createdAt
	event.ID[0] = byte(index)
	event.ID[1] = byte(index >> 8)
	return event
}

func TestWalkPageAndUniqueEventBudgetsAreErrors(t *testing.T) {
	t.Run("page budget", func(t *testing.T) {
		calls := 0
		fetch := func(_ context.Context, _ string, filter nostr.Filter) (Page, error) {
			calls++
			event := nostr.Event{CreatedAt: filter.Until - 1}
			event.ID[0] = byte(calls)
			event.ID[1] = byte(calls >> 8)
			return Page{Events: []nostr.Event{event}, Hints: []string{"more"}}, nil
		}
		stats, err := walk(context.Background(), "ws://fixture.invalid", nostr.Filter{Until: 1000}, func(nostr.Event) error { return nil }, fetch)
		if err == nil || !strings.Contains(err.Error(), "page budget reached") || stats.Pages != maxPages || stats.Fetched != maxPages {
			t.Fatalf("page budget result: stats=%#v err=%v", stats, err)
		}
	})

	t.Run("unique-event budget", func(t *testing.T) {
		events := make([]nostr.Event, maxEvents+1)
		for i := range events {
			events[i].CreatedAt = 50
			events[i].ID[0] = byte(i)
			events[i].ID[1] = byte(i >> 8)
			events[i].ID[2] = byte(i >> 16)
		}
		callbacks := 0
		fetch := func(context.Context, string, nostr.Filter) (Page, error) { return Page{Events: events}, nil }
		stats, err := walk(context.Background(), "ws://fixture.invalid", nostr.Filter{Until: 100}, func(nostr.Event) error {
			callbacks++
			return nil
		}, fetch)
		if err == nil || !strings.Contains(err.Error(), "unique-event budget exceeded") || stats.Fetched != maxEvents || callbacks != maxEvents {
			t.Fatalf("unique-event budget result: stats=%#v callbacks=%d err=%v", stats, callbacks, err)
		}
	})
}

func TestWalkZeroUntilIsFixedOnceAndZeroBoundaryStopsIncomplete(t *testing.T) {
	t.Run("fixed initial now", func(t *testing.T) {
		calls := 0
		firstUntil := nostr.Timestamp(0)
		fetch := func(_ context.Context, _ string, filter nostr.Filter) (Page, error) {
			calls++
			if calls == 1 {
				firstUntil = filter.Until
				event := nostr.Event{CreatedAt: filter.Until - 1}
				event.ID[0] = 1
				return Page{Events: []nostr.Event{event}, Hints: []string{"more"}}, nil
			}
			event := nostr.Event{CreatedAt: filter.Until - 1}
			event.ID[0] = 2
			return Page{Events: []nostr.Event{event}}, nil
		}
		stats, err := walk(context.Background(), "ws://fixture.invalid", nostr.Filter{}, func(nostr.Event) error { return nil }, fetch)
		if err != nil || calls != 2 || firstUntil == 0 || stats.Fetched != 2 {
			t.Fatalf("zero Until did not stay fixed: stats=%#v calls=%d err=%v", stats, calls, err)
		}
	})

	t.Run("epoch boundary", func(t *testing.T) {
		fetch := func(_ context.Context, _ string, filter nostr.Filter) (Page, error) {
			event := nostr.Event{CreatedAt: 0}
			event.ID[0] = 9
			return Page{Events: []nostr.Event{event}, Hints: []string{"more"}}, nil
		}
		stats, err := walk(context.Background(), "ws://fixture.invalid", nostr.Filter{Until: 10}, func(nostr.Event) error { return nil }, fetch)
		if err == nil || !strings.Contains(err.Error(), "timestamp 0") || stats.Fetched != 1 {
			t.Fatalf("zero boundary result: stats=%#v err=%v", stats, err)
		}
	})
}

func paginationEvent(t *testing.T, createdAt int64, content string) nostr.Event {
	t.Helper()
	secret := nostr.Generate()
	event := nostr.Event{
		CreatedAt: nostr.Timestamp(createdAt), Kind: 30078, PubKey: secret.Public(),
		Tags: nostr.Tags{{"p", "recipient"}}, Content: content,
	}
	if err := event.Sign(secret); err != nil {
		t.Fatal(err)
	}
	return event
}

func eventContents(events []nostr.Event) []string {
	contents := make([]string, 0, len(events))
	for _, event := range events {
		contents = append(contents, event.Content)
	}
	return contents
}

type paginationRelay struct {
	url      string
	server   *httptest.Server
	mu       sync.Mutex
	queries  []nostr.Filter
	respond  func(nostr.Filter) ([]nostr.Event, []string)
	upgrader websocket.Upgrader
}

func newPaginationRelay(t *testing.T, respond func(nostr.Filter) ([]nostr.Event, []string)) *paginationRelay {
	t.Helper()
	fixture := &paginationRelay{
		respond:  respond,
		upgrader: websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }},
	}
	fixture.server = httptest.NewServer(http.HandlerFunc(fixture.serveHTTP))
	fixture.url = "ws" + strings.TrimPrefix(fixture.server.URL, "http")
	t.Cleanup(fixture.server.Close)
	return fixture
}

func (f *paginationRelay) serveHTTP(w http.ResponseWriter, r *http.Request) {
	conn, err := f.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	for {
		_ = conn.SetReadDeadline(time.Now().Add(8 * time.Second))
		var request []json.RawMessage
		if err := conn.ReadJSON(&request); err != nil {
			return
		}
		if len(request) < 3 || string(request[0]) != `"REQ"` {
			continue
		}
		var subscription string
		var filter nostr.Filter
		if json.Unmarshal(request[1], &subscription) != nil || json.Unmarshal(request[2], &filter) != nil {
			return
		}
		f.mu.Lock()
		f.queries = append(f.queries, filter)
		f.mu.Unlock()
		events, hints := f.respond(filter)
		for _, event := range events {
			_ = conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
			if err := conn.WriteJSON([]any{"EVENT", subscription, event}); err != nil {
				return
			}
		}
		_ = conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
		response := []any{"EOSE", subscription}
		if len(hints) > 0 {
			response = append(response, hints)
		}
		if err := conn.WriteJSON(response); err != nil {
			return
		}
	}
}

func (f *paginationRelay) filters() []nostr.Filter {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]nostr.Filter(nil), f.queries...)
}

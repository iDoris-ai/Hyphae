package relayquery

import (
	"context"
	"errors"
	"fmt"
	"time"

	"fiatjaf.com/nostr"
)

const (
	firstPageLimit = 100
	maxPageLimit   = 500
	maxPages       = 100
	maxEvents      = 10000
	walkTimeout    = 30 * time.Second
)

// Stats reports work completed by Walk. FinishedHint is true only when the
// relay sent the explicit NIP-67 "finish" hint; an ordinary short EOSE only
// means the currently available history was fetched.
type Stats struct {
	Fetched      int  // unique events whose callback returned nil
	Pages        int  // Fetch calls started
	FinishedHint bool // true only for an explicit NIP-67 "finish" hint
}

type pageFetcher func(context.Context, string, nostr.Filter) (Page, error)

// Walk fetches available history in inclusive, descending time pages and
// invokes callback once for each unique event. Walk manages Filter.Limit,
// using 100 events initially and raising it to 500 when a page makes no
// progress. Other filters are preserved. An Until of zero is fixed to the
// current time once at the start of the walk.
func Walk(ctx context.Context, url string, filter nostr.Filter, callback func(nostr.Event) error) (Stats, error) {
	return walk(ctx, url, filter, callback, Fetch)
}

func walk(parent context.Context, url string, filter nostr.Filter, callback func(nostr.Event) error, fetch pageFetcher) (Stats, error) {
	stats := Stats{}
	if callback == nil {
		return stats, errors.New("relay history callback is required")
	}
	if fetch == nil {
		return stats, errors.New("relay history fetcher is required")
	}
	ctx, cancel := context.WithTimeout(parent, walkTimeout)
	defer cancel()

	cursor := filter.Until
	if cursor == 0 {
		cursor = nostr.Now()
	}
	limit := firstPageLimit
	seen := make(map[nostr.ID]struct{})

	for stats.Pages < maxPages {
		if err := ctx.Err(); err != nil {
			return stats, pageError(url, cursor, "history walk ended before the next page", err)
		}
		request := filter
		request.Until = cursor
		request.Limit = limit
		request.LimitZero = false
		page, fetchErr := fetch(ctx, url, request)
		stats.Pages++

		newEvents := 0
		var earliest nostr.Timestamp
		for i, event := range page.Events {
			if i == 0 || event.CreatedAt < earliest {
				earliest = event.CreatedAt
			}
			if _, exists := seen[event.ID]; exists {
				continue
			}
			if err := ctx.Err(); err != nil {
				return stats, pageError(url, cursor, "history walk deadline or cancellation reached", err)
			}
			if stats.Fetched == maxEvents {
				return stats, pageError(url, cursor, fmt.Sprintf("unique-event budget exceeded (%d)", maxEvents), nil)
			}
			seen[event.ID] = struct{}{}
			if err := callback(event); err != nil {
				return stats, &callbackPageError{url: url, until: cursor, cause: err}
			}
			stats.Fetched++
			newEvents++
		}

		if fetchErr != nil {
			return stats, pageError(url, cursor, "query failed", fetchErr)
		}
		if err := ctx.Err(); err != nil {
			return stats, pageError(url, cursor, "history walk deadline or cancellation reached", err)
		}

		more, finish := hasHint(page.Hints, "more"), hasHint(page.Hints, "finish")
		if more && finish {
			return stats, pageError(url, cursor, "relay returned conflicting more and finish hints", nil)
		}
		if finish {
			stats.FinishedHint = true
			return stats, nil
		}

		continuePaging := more || len(page.Events) >= limit
		if !continuePaging {
			return stats, nil
		}
		if len(page.Events) > 0 && earliest == 0 {
			return stats, pageError(url, cursor, "cannot continue from timestamp 0 with an inclusive Until filter", nil)
		}

		timeProgress := len(page.Events) > 0 && earliest < cursor
		if !timeProgress && newEvents == 0 {
			if limit < maxPageLimit {
				limit = maxPageLimit
				continue
			}
			return stats, pageError(url, cursor, "incomplete history: no progress at maximum page size", nil)
		}

		if timeProgress {
			cursor = earliest
			limit = firstPageLimit
		}
		if stats.Fetched == maxEvents {
			return stats, pageError(url, cursor, "incomplete history: unique-event budget reached", nil)
		}
	}

	return stats, pageError(url, cursor, fmt.Sprintf("incomplete history: page budget reached (%d)", maxPages), nil)
}

type callbackPageError struct {
	url   string
	until nostr.Timestamp
	cause error
}

func (e *callbackPageError) Error() string {
	return fmt.Sprintf("relay %s history at until %d: event callback failed", e.url, e.until)
}

func (e *callbackPageError) Unwrap() error { return e.cause }

func hasHint(hints []string, value string) bool {
	for _, hint := range hints {
		if hint == value {
			return true
		}
	}
	return false
}

func pageError(url string, until nostr.Timestamp, message string, cause error) error {
	if cause != nil {
		return fmt.Errorf("relay %s history at until %d: %s: %w", url, until, message, cause)
	}
	return fmt.Errorf("relay %s history at until %d: %s", url, until, message)
}

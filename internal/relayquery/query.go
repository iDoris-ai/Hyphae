package relayquery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"fiatjaf.com/nostr"
	"github.com/coder/websocket"
)

const (
	queryTimeout   = 5 * time.Second
	querySubID     = "hyphae-fetch"
	queryReadLimit = 2 << 24 // Match the nostr SDK relay read limit.
)

// Page is the set of matching events received before the relay's EOSE.
// Hints are returned unchanged; EOSE does not prove that this is full history.
type Page struct {
	Events []nostr.Event
	Hints  []string
}

// Fetch performs one relay query. It succeeds only after a real EOSE arrives;
// callers may use Page.Hints to decide whether and how to request another page.
func Fetch(ctx context.Context, url string, filter nostr.Filter) (Page, error) {
	page := Page{Events: make([]nostr.Event, 0)}
	queryCtx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	conn, _, err := websocket.Dial(queryCtx, url, nil)
	if err != nil {
		return page, fmt.Errorf("connect to relay %s: %w", url, err)
	}
	defer conn.CloseNow()
	conn.SetReadLimit(queryReadLimit)

	req, err := (nostr.ReqEnvelope{SubscriptionID: querySubID, Filters: []nostr.Filter{filter}}).MarshalJSON()
	if err != nil {
		return page, fmt.Errorf("encode query for relay %s: %w", url, err)
	}
	if err := conn.Write(queryCtx, websocket.MessageText, req); err != nil {
		return page, fmt.Errorf("send query to relay %s before EOSE: %w", url, err)
	}

	for {
		messageType, message, err := conn.Read(queryCtx)
		if err != nil {
			if queryCtx.Err() != nil {
				return page, fmt.Errorf("relay %s query incomplete: did not receive EOSE: %w", url, queryCtx.Err())
			}
			return page, fmt.Errorf("relay %s disconnected before EOSE: %w", url, err)
		}
		if messageType != websocket.MessageText {
			continue
		}

		envelope, err := nostr.ParseMessage(string(message))
		if err != nil {
			// Unknown relay extensions should not end a query.
			if errors.Is(err, nostr.UnknownLabel) {
				continue
			}
			return page, fmt.Errorf("decode relay message before EOSE: %w", err)
		}

		switch env := envelope.(type) {
		case *nostr.EventEnvelope:
			if env.SubscriptionID == nil || *env.SubscriptionID != querySubID {
				continue
			}
			if !env.Event.CheckID() || !env.Event.VerifySignature() || !filter.Matches(env.Event) {
				continue
			}
			page.Events = append(page.Events, env.Event)
		case *nostr.EOSEEnvelope:
			hints, id, err := parseEOSE(message)
			if err != nil {
				return page, fmt.Errorf("decode EOSE from relay %s: %w", url, err)
			}
			if id != querySubID || env.SubscriptionID != querySubID {
				continue
			}
			return finish(page, hints)
		case *nostr.ClosedEnvelope:
			if env.SubscriptionID == querySubID {
				return page, fmt.Errorf("relay %s closed subscription before EOSE: %s", url, env.Reason)
			}
		}
	}
}

func parseEOSE(message []byte) ([]string, string, error) {
	var fields []json.RawMessage
	if err := json.Unmarshal(message, &fields); err != nil {
		return nil, "", err
	}
	if len(fields) < 2 || len(fields) > 3 {
		return nil, "", errors.New("invalid EOSE field count")
	}
	var label, subID string
	if err := json.Unmarshal(fields[0], &label); err != nil || label != "EOSE" {
		return nil, "", errors.New("invalid EOSE label")
	}
	if err := json.Unmarshal(fields[1], &subID); err != nil {
		return nil, "", errors.New("invalid EOSE subscription ID")
	}
	var hints []string
	if len(fields) == 3 {
		if err := json.Unmarshal(fields[2], &hints); err != nil || hints == nil {
			return nil, "", errors.New("EOSE hints must be an array of strings")
		}
	}
	return hints, subID, nil
}

func finish(page Page, hints []string) (Page, error) {
	page.Hints = append(page.Hints, hints...)
	for _, hint := range page.Hints {
		if hint == "auth" {
			return page, fmt.Errorf("relay requires authorization: %q", hint)
		}
	}
	return page, nil
}

//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"github.com/gorilla/websocket"
	"github.com/iDoris-ai/hyphae/internal/relay"
)

func TestRelayEventRetentionAndSignatureContract(t *testing.T) {
	handler, cleanup, err := relay.New(relay.Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(func() { server.Close(); cleanup() })
	url := "ws" + strings.TrimPrefix(server.URL, "http")
	secret := nostr.Generate()
	pubkey := secret.Public()
	base := nostr.Now()

	// 8787 is a candidate regular-kind example for this test. This only records
	// behavior of the pinned Khatru + BoltDB stack; it does not freeze a protocol rule.
	regularA := signedRelayEvent(t, secret, base, 8787, nostr.Tags{{"d", "shared-regular-d"}}, "regular-a")
	regularB := signedRelayEvent(t, secret, base+1, 8787, nostr.Tags{{"d", "shared-regular-d"}}, "regular-b")
	addressOld := signedRelayEvent(t, secret, base+2, 30078, nostr.Tags{{"d", "same"}}, "address-old")
	addressNew := signedRelayEvent(t, secret, base+3, 30078, nostr.Tags{{"d", "same"}}, "address-new")
	addressOtherD := signedRelayEvent(t, secret, base+4, 30078, nostr.Tags{{"d", "other"}}, "address-other-d")

	for _, event := range []nostr.Event{regularA, regularB, addressOld, addressNew, addressOtherD} {
		if ok, id, message := publishRelayEvent(t, url, event); !ok || id != event.ID.Hex() {
			t.Fatalf("publish kind=%d: ok=%t acknowledged_id=%q want=%q message=%q", event.Kind, ok, id, event.ID.Hex(), message)
		}
	}
	// A stale addressable event arriving after the newer version must not replace it.
	addressLateOld := signedRelayEvent(t, secret, base+2, 30078, nostr.Tags{{"d", "same"}}, "address-late-old")
	if ok, id, message := publishRelayEvent(t, url, addressLateOld); !ok || id != addressLateOld.ID.Hex() {
		t.Fatalf("publish older addressable event: ok=%t acknowledged_id=%q want=%q message=%q", ok, id, addressLateOld.ID.Hex(), message)
	}

	invalid := signedRelayEvent(t, secret, base+5, 8787, nostr.Tags{{"d", "shared-regular-d"}}, "invalid-signature")
	invalid.Sig[0] ^= 1
	if ok, id, message := publishRelayEvent(t, url, invalid); ok || id != invalid.ID.Hex() {
		t.Fatalf("invalid-signature publish: ok=%t acknowledged_id=%q want=%q message=%q", ok, id, invalid.ID.Hex(), message)
	} else {
		t.Logf("relay rejected invalid signature: %s", message)
	}

	regular := queryRelayEvents(t, url, map[string]any{"kinds": []int{8787}, "authors": []string{pubkey.Hex()}})
	assertRelayEventIDs(t, regular, regularA.ID.Hex(), regularB.ID.Hex())
	addresses := queryRelayEvents(t, url, map[string]any{"kinds": []int{30078}, "authors": []string{pubkey.Hex()}})
	assertRelayEventIDs(t, addresses, addressNew.ID.Hex(), addressOtherD.ID.Hex())
}

func signedRelayEvent(t *testing.T, sk nostr.SecretKey, created nostr.Timestamp, kind nostr.Kind, tags nostr.Tags, content string) nostr.Event {
	t.Helper()
	event := nostr.Event{CreatedAt: created, Kind: kind, Tags: tags, Content: content}
	if err := event.Sign(sk); err != nil {
		t.Fatal(err)
	}
	return event
}

func publishRelayEvent(t *testing.T, url string, event nostr.Event) (bool, string, string) {
	t.Helper()
	conn := dialRelayProbe(t, url)
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if err := conn.WriteJSON([]any{"EVENT", event}); err != nil {
		t.Fatal(err)
	}
	var response []json.RawMessage
	if err := conn.ReadJSON(&response); err != nil {
		t.Fatal(err)
	}
	if len(response) != 4 || string(response[0]) != `"OK"` {
		t.Fatalf("unexpected publish response: %s", response)
	}
	var id, message string
	var ok bool
	if err := json.Unmarshal(response[1], &id); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(response[2], &ok); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(response[3], &message); err != nil {
		t.Fatal(err)
	}
	return ok, id, message
}

func queryRelayEvents(t *testing.T, url string, filter map[string]any) []nostr.Event {
	t.Helper()
	const subscriptionID = "relay-contract"
	conn := dialRelayProbe(t, url)
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if err := conn.WriteJSON([]any{"REQ", subscriptionID, filter}); err != nil {
		t.Fatal(err)
	}
	var events []nostr.Event
	for {
		var response []json.RawMessage
		if err := conn.ReadJSON(&response); err != nil {
			t.Fatal(err)
		}
		if len(response) == 2 && string(response[0]) == `"EOSE"` {
			var gotSubscriptionID string
			if err := json.Unmarshal(response[1], &gotSubscriptionID); err != nil {
				t.Fatal(err)
			}
			if gotSubscriptionID != subscriptionID {
				t.Fatalf("EOSE subscription ID = %q, want %q", gotSubscriptionID, subscriptionID)
			}
			return events
		}
		if len(response) == 3 && string(response[0]) == `"EVENT"` {
			var gotSubscriptionID string
			if err := json.Unmarshal(response[1], &gotSubscriptionID); err != nil {
				t.Fatal(err)
			}
			if gotSubscriptionID != subscriptionID {
				t.Fatalf("EVENT subscription ID = %q, want %q", gotSubscriptionID, subscriptionID)
			}
			var event nostr.Event
			if err := json.Unmarshal(response[2], &event); err != nil {
				t.Fatal(err)
			}
			if !event.CheckID() || !event.VerifySignature() {
				t.Fatalf("relay returned unverifiable event %s", event.ID.Hex())
			}
			events = append(events, event)
		}
	}
}

func assertRelayEventIDs(t *testing.T, events []nostr.Event, wantIDs ...string) {
	t.Helper()
	got := make(map[string]bool, len(events))
	for _, event := range events {
		got[event.ID.Hex()] = true
	}
	if len(got) != len(wantIDs) {
		t.Fatalf("got event IDs %v, want exactly %v", got, wantIDs)
	}
	for _, id := range wantIDs {
		if !got[id] {
			t.Errorf("event ID %s missing from %v", id, got)
		}
	}
}

func dialRelayProbe(t *testing.T, url string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, url, nil)
	if err != nil {
		t.Fatalf("connect to relay: %v", err)
	}
	return conn
}

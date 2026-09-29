package relay

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"github.com/gorilla/websocket"
)

func TestNewSecuresDataDirectory(t *testing.T) {
	dataDir := t.TempDir() + "/private"
	cfg, err := ParseConfig("127.0.0.1", dataDir, 3334, false)
	if err != nil {
		t.Fatal(err)
	}
	_, closeStore, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	closeStore()

	dirInfo, err := os.Stat(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if got := dirInfo.Mode().Perm(); got != 0700 {
		t.Fatalf("data directory mode = %04o, want 0700", got)
	}
	dbInfo, err := os.Stat(dataDir + "/events.db")
	if err != nil {
		t.Fatal(err)
	}
	if got := dbInfo.Mode().Perm(); got != 0600 {
		t.Fatalf("database mode = %04o, want 0600", got)
	}
}

func TestNewReturnsDataPathError(t *testing.T) {
	file := t.TempDir() + "/not-a-directory"
	if err := os.WriteFile(file, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := ParseConfig("127.0.0.1", file+"/child", 3334, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := New(cfg); err == nil {
		t.Fatal("New() succeeded for a data path below a regular file")
	}
}

func TestRelayStoresAndReplacesEventsAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	server, cleanup := startTestRelay(t, dir)
	secret := [32]byte{1}
	first := signedAddressable(t, secret, "older", 100)
	latest := signedAddressable(t, secret, "newer", 200)
	publish(t, server.URL, first)
	publish(t, server.URL, latest)
	cleanup()

	server, cleanup = startTestRelay(t, dir)
	defer cleanup()
	got := queryAddressable(t, server.URL)
	if len(got) != 1 || got[0].Content != "newer" {
		t.Fatalf("query after restart returned %#v, want only latest addressable event", got)
	}
	if !got[0].VerifySignature() {
		t.Fatal("stored event has invalid signature")
	}
}

func TestRelayRejectsInvalidSignatureAndHonorsFilter(t *testing.T) {
	server, cleanup := startTestRelay(t, t.TempDir())
	defer cleanup()
	event := signedAddressable(t, [32]byte{2}, "signed", 100)
	event.Content = "tampered after signing"
	conn := connect(t, server.URL)
	if err := conn.WriteJSON([]any{"EVENT", event}); err != nil {
		t.Fatal(err)
	}
	var response []json.RawMessage
	if err := conn.ReadJSON(&response); err != nil {
		t.Fatal(err)
	}
	if len(response) < 4 || string(response[0]) != `"OK"` || string(response[2]) != "false" {
		t.Fatalf("invalid publish response = %s, want rejected OK", response)
	}

	if got := queryEvents(t, server.URL, `{"kinds":[30000]}`); len(got) != 0 {
		t.Fatalf("invalid event was stored: %#v", got)
	}
	valid := signedAddressable(t, [32]byte{3}, "valid", 101)
	publish(t, server.URL, valid)
	if got := queryEvents(t, server.URL, `{"kinds":[30001]}`); len(got) != 0 {
		t.Fatalf("non-matching filter returned events: %#v", got)
	}
}

func TestCleanupClosesActiveWebSocketsBeforeStore(t *testing.T) {
	server, cleanup := startTestRelay(t, t.TempDir())
	conn := connect(t, server.URL)
	if err := conn.WriteMessage(websocket.TextMessage, []byte(`["REQ","active",{"kinds":[30000]}]`)); err != nil {
		t.Fatal(err)
	}
	var response []json.RawMessage
	if err := conn.ReadJSON(&response); err != nil {
		t.Fatal(err)
	}
	cleanup()
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatal("websocket remained open after relay cleanup")
	}
}

func startTestRelay(t *testing.T, dir string) (*httptest.Server, func()) {
	t.Helper()
	cfg, err := ParseConfig("127.0.0.1", dir, 3334, false)
	if err != nil {
		t.Fatal(err)
	}
	handler, closeStore, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(handler)
	return srv, func() {
		srv.Close()
		closeStore()
	}
}

func signedAddressable(t *testing.T, secret [32]byte, content string, created int64) nostr.Event {
	t.Helper()
	event := nostr.Event{
		CreatedAt: nostr.Timestamp(created),
		Kind:      30000,
		Tags:      nostr.Tags{nostr.Tag{"d", "hyphae-test"}},
		Content:   content,
	}
	if err := event.Sign(secret); err != nil {
		t.Fatal(err)
	}
	return event
}

func connect(t *testing.T, url string) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(url, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	return conn
}

func publish(t *testing.T, url string, event nostr.Event) {
	t.Helper()
	conn := connect(t, url)
	if err := conn.WriteJSON([]any{"EVENT", event}); err != nil {
		t.Fatal(err)
	}
	var response []json.RawMessage
	if err := conn.ReadJSON(&response); err != nil {
		t.Fatal(err)
	}
	if len(response) < 4 || string(response[0]) != `"OK"` || string(response[2]) != "true" {
		t.Fatalf("publish response = %s, want accepted OK", response)
	}
}

func queryAddressable(t *testing.T, url string) []nostr.Event {
	return queryEvents(t, url, `{"kinds":[30000]}`)
}

func queryEvents(t *testing.T, url, filter string) []nostr.Event {
	t.Helper()
	conn := connect(t, url)
	if err := conn.WriteMessage(websocket.TextMessage, []byte(`["REQ","query",`+filter+`]`)); err != nil {
		t.Fatal(err)
	}
	var events []nostr.Event
	for {
		var response []json.RawMessage
		if err := conn.ReadJSON(&response); err != nil {
			t.Fatal(err)
		}
		if len(response) > 0 && string(response[0]) == `"EOSE"` {
			return events
		}
		if len(response) == 3 && string(response[0]) == `"EVENT"` {
			var event nostr.Event
			if err := json.Unmarshal(response[2], &event); err != nil {
				t.Fatal(err)
			}
			events = append(events, event)
		}
	}
}

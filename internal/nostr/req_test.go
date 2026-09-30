package nostr

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func reqRelay(t *testing.T, handler func(*websocket.Conn)) string {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		handler(conn)
	}))
	t.Cleanup(server.Close)
	return "ws" + strings.TrimPrefix(server.URL, "http")
}

func reqSignedEvent(t *testing.T) nostr.Event {
	t.Helper()
	sk := nostr.Generate()
	event := nostr.Event{CreatedAt: nostr.Now(), Kind: 1, Content: "req fixture event"}
	require.NoError(t, event.Sign(sk))
	return event
}

func reqWriteEvent(t *testing.T, conn *websocket.Conn, event nostr.Event) {
	t.Helper()
	data, err := json.Marshal([]any{"EVENT", "hyphae-fetch", event})
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, data))
}

func TestReqCmdReturnsOnEOSE(t *testing.T) {
	event := reqSignedEvent(t)
	eoseWritten := make(chan struct{})
	clientClosed := make(chan struct{})
	url := reqRelay(t, func(conn *websocket.Conn) {
		_, _, _ = conn.ReadMessage()
		require.NoError(t, conn.WriteJSON([]any{"EVENT", "hyphae-fetch", event}))
		require.NoError(t, conn.WriteJSON([]any{"EOSE", "hyphae-fetch"}))
		close(eoseWritten)
		// Keep the relay connection open until the query client finishes.
		require.NoError(t, conn.SetReadDeadline(time.Now().Add(2*time.Second)))
		_, _, _ = conn.ReadMessage()
		close(clientClosed)
	})
	started := time.Now()
	var runErr error
	out := captureStdout(t, func() {
		runErr = ReqCmd.Run(context.Background(), []string{"req", "--relay", url})
	})
	require.NoError(t, runErr)
	assert.Less(t, time.Since(started), time.Second)
	select {
	case <-eoseWritten:
	default:
		t.Fatal("query returned before the relay sent EOSE")
	}
	select {
	case <-clientClosed:
	case <-time.After(time.Second):
		t.Fatal("query did not close the still-open relay after EOSE")
	}
	assert.Contains(t, out, "Found 1 events")
	assert.Contains(t, out, event.Content)
}

func TestReqCmdKeepsPartialEventsAndReturnsRelayFailure(t *testing.T) {
	event := reqSignedEvent(t)
	secondEvent := reqSignedEvent(t)
	first := reqRelay(t, func(conn *websocket.Conn) {
		_, _, _ = conn.ReadMessage()
		reqWriteEvent(t, conn, event)
		// Handler return closes the socket before EOSE.
	})
	second := reqRelay(t, func(conn *websocket.Conn) {
		_, _, _ = conn.ReadMessage()
		reqWriteEvent(t, conn, secondEvent)
		require.NoError(t, conn.WriteJSON([]any{"EOSE", "hyphae-fetch"}))
	})
	var runErr error
	out := captureStdout(t, func() {
		runErr = ReqCmd.Run(context.Background(), []string{"req", "--relay", first, "--relay", second})
	})
	require.Error(t, runErr)
	assert.Contains(t, runErr.Error(), first)
	assert.Contains(t, runErr.Error(), "EOSE")
	assert.Contains(t, out, "Found 2 events")
	assert.Contains(t, out, event.Content)
	assert.Contains(t, out, secondEvent.Content)
}

func TestReqCmdCancellationStopsBeforeNextRelay(t *testing.T) {
	event := reqSignedEvent(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := reqRelay(t, func(conn *websocket.Conn) {
		_, _, _ = conn.ReadMessage()
		reqWriteEvent(t, conn, event)
		cancel()
		// Wait for Fetch to close the connection after observing cancellation.
		require.NoError(t, conn.SetReadDeadline(time.Now().Add(2*time.Second)))
		_, _, _ = conn.ReadMessage()
	})
	var secondHit atomic.Bool
	second := reqRelay(t, func(conn *websocket.Conn) { secondHit.Store(true) })
	var runErr error
	captureStdout(t, func() {
		runErr = ReqCmd.Run(ctx, []string{"req", "--relay", first, "--relay", second})
	})
	require.Error(t, runErr)
	assert.False(t, secondHit.Load(), "must not connect to later relays after cancellation")
}

func TestReqCmdUnreachableRelayReturnsDiagnostic(t *testing.T) {
	var runErr error
	out := captureStdout(t, func() {
		runErr = ReqCmd.Run(context.Background(), []string{"req", "--relay", "ws://127.0.0.1:1"})
	})
	require.Error(t, runErr)
	assert.Contains(t, runErr.Error(), "127.0.0.1:1")
	assert.Contains(t, out, "Found 0 events")
}

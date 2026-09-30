package profile

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"github.com/gorilla/websocket"
	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/pkg/types"
)

func TestDiscoverTimeoutRejectsInvalidValues(t *testing.T) {
	for _, seconds := range []int64{0, -1, int64((1<<63-1)/int64(time.Second)) + 1} {
		if _, err := discoverTimeout(seconds); err == nil {
			t.Errorf("discoverTimeout(%d) succeeded", seconds)
		}
	}
	got, err := discoverTimeout(7)
	if err != nil || got != 7*time.Second {
		t.Fatalf("discoverTimeout(7) = %s, %v; want 7s", got, err)
	}
}

func TestDiscoverInvalidTimeoutUsesUserError(t *testing.T) {
	for _, value := range []string{"0", "-1", "9223372037"} {
		t.Run(value, func(t *testing.T) {
			setupIsolatedCLIEnv(t)
			err := profileDiscoverCmd.Run(context.Background(), []string{"discover", "--timeout", value})
			var exitErr *common.ExitError
			if !errors.As(err, &exitErr) || exitErr.Code != common.ErrCodeUser {
				t.Fatalf("discover error = %v, want user_error", err)
			}
			if _, err := os.Stat(strings.TrimRight(os.Getenv("HOME"), "/") + "/.hyphae"); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("invalid timeout touched profile/config storage: stat error = %v", err)
			}
		})
	}
}

func TestDiscoverReportsRelayFailureAndKeepsPartialProfiles(t *testing.T) {
	setupIsolatedCLIEnv(t)
	brokenEvent := signedProfileEvent(t, "saved before disconnect", [32]byte{1, 2, 3})
	goodEvent := signedProfileEvent(t, "saved after disconnect", [32]byte{4, 5, 6})
	broken := newDiscoverRelay(t, []nostr.Event{brokenEvent}, false, 0, nil)
	good := newDiscoverRelay(t, []nostr.Event{goodEvent}, true, 0, nil)

	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	t.Setenv("HYPHAE_OUTPUT", "json")
	cmdErr := profileDiscoverCmd.Run(context.Background(), []string{"discover", "--relay", broken, "--relay", good, "--timeout", "1"})
	_ = w.Close()
	os.Stdout = oldStdout
	stdout, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Close()

	var exitErr *common.ExitError
	if !errors.As(cmdErr, &exitErr) || exitErr.Code != common.ErrCodeNetwork || !strings.Contains(cmdErr.Error(), broken) || !strings.Contains(cmdErr.Error(), "EOSE") {
		t.Fatalf("discover error = %v, want network_error identifying broken relay and missing EOSE", cmdErr)
	}
	if len(stdout) != 0 {
		t.Fatalf("JSON failure wrote successful stdout: %q", stdout)
	}
	db, err := NewDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, saved := range []struct {
		event    nostr.Event
		wantName string
	}{
		{brokenEvent, "saved before disconnect"},
		{goodEvent, "saved after disconnect"},
	} {
		got, err := db.GetProfile(common.EncodeNpub(saved.event.PubKey))
		if err != nil || got == nil || got.Name != saved.wantName {
			t.Fatalf("profile %q = %#v, %v; want saved profile", saved.wantName, got, err)
		}
	}
}

func TestDiscoverEOSEReturnsWithOpenSocketAndCustomTimeoutOverFiveSeconds(t *testing.T) {
	setupIsolatedCLIEnv(t)
	event := signedProfileEvent(t, "slow but complete", [32]byte{1, 2, 3})
	relay := newDiscoverRelay(t, []nostr.Event{event}, true, 5200*time.Millisecond, nil)
	started := time.Now()
	err := profileDiscoverCmd.Run(context.Background(), []string{"discover", "--relay", relay, "--timeout", "8"})
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if elapsed := time.Since(started); elapsed < 5*time.Second || elapsed > 7*time.Second {
		t.Fatalf("discover took %s; want completion after delayed EOSE and before timeout", elapsed)
	}
}

func TestDiscoverParentCancellationDoesNotConnectNextRelay(t *testing.T) {
	setupIsolatedCLIEnv(t)
	started := make(chan struct{})
	first := newDiscoverRelay(t, nil, false, 0, nil, started)
	var secondRequests atomic.Int32
	second := newDiscoverRelay(t, nil, true, 0, &secondRequests)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-started:
			cancel()
		case <-ctx.Done():
		}
	}()
	err := profileDiscoverCmd.Run(ctx, []string{"discover", "--relay", first, "--relay", second, "--timeout", "5"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("discover error = %v, want parent cancellation", err)
	}
	if got := secondRequests.Load(); got != 0 {
		t.Fatalf("connected to second relay %d times after parent cancellation", got)
	}
}

func TestDiscoverStoreFailureReturnsOtherErrorWithDiagnostic(t *testing.T) {
	setupIsolatedCLIEnv(t)
	db, err := NewDB()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.Exec(`CREATE TRIGGER reject_profile BEFORE INSERT ON agent_profiles BEGIN SELECT RAISE(FAIL, 'fixture store failure'); END`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	event := signedProfileEvent(t, "cannot save", [32]byte{1, 2, 3})
	relay := newDiscoverRelay(t, []nostr.Event{event}, true, 0, nil)
	var stdout bytes.Buffer
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	t.Setenv("HYPHAE_OUTPUT", "json")
	cmdErr := profileDiscoverCmd.Run(context.Background(), []string{"discover", "--relay", relay, "--timeout", "1"})
	_ = w.Close()
	os.Stdout = oldStdout
	if _, err := io.Copy(&stdout, r); err != nil {
		t.Fatal(err)
	}
	_ = r.Close()
	var exitErr *common.ExitError
	if !errors.As(cmdErr, &exitErr) || exitErr.Code != common.ErrCodeOther || !strings.Contains(cmdErr.Error(), event.ID.Hex()) || !strings.Contains(cmdErr.Error(), common.EncodeNpub(event.PubKey)) {
		t.Fatalf("discover error = %v, want other_error with event and npub diagnostic", cmdErr)
	}
	if stdout.Len() != 0 {
		t.Fatalf("JSON failure wrote successful stdout: %q", stdout.String())
	}
}

func setupIsolatedCLIEnv(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("HYPHAE_OUTPUT", "")
	t.Setenv("AGENT_SPEAKER_OUTPUT", "")
}

func signedProfileEvent(t *testing.T, name string, sk [32]byte) nostr.Event {
	t.Helper()
	profile := &types.AgentProfile{Name: name, Mode: types.ModeSimple}
	event, err := ProfileToEvent(profile, nostr.PubKey{})
	if err != nil {
		t.Fatal(err)
	}
	if err := event.Sign(sk); err != nil {
		t.Fatal(err)
	}
	return *event
}

func newDiscoverRelay(t *testing.T, events []nostr.Event, eose bool, eoseDelay time.Duration, requests *atomic.Int32, started ...chan struct{}) string {
	t.Helper()
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
		_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if requests != nil {
			requests.Add(1)
		}
		_, request, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var req []json.RawMessage
		if err := json.Unmarshal(request, &req); err != nil || len(req) < 2 {
			return
		}
		var subscriptionID string
		if err := json.Unmarshal(req[1], &subscriptionID); err != nil {
			return
		}
		if len(started) > 0 {
			close(started[0])
		}
		for _, event := range events {
			if err := conn.WriteJSON([]any{"EVENT", subscriptionID, event}); err != nil {
				return
			}
		}
		if eose {
			if eoseDelay > 0 {
				time.Sleep(eoseDelay)
			}
			_ = conn.WriteJSON([]any{"EOSE", subscriptionID})
			_, _, _ = conn.ReadMessage()
		} else if len(started) > 0 {
			_, _, _ = conn.ReadMessage()
		}
	}))
	t.Cleanup(server.Close)
	return "ws" + strings.TrimPrefix(server.URL, "http")
}

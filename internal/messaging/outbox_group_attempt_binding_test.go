package messaging

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/pkg/types"
)

// resignedGroupEvent returns a fully valid signed event B derived from entry
// A's frozen event: same kind/tags/content shape, new created_at, signed by sk.
func resignedGroupEvent(t *testing.T, entry types.OutboxEntry, sk nostr.SecretKey, recipientHex string) nostr.Event {
	t.Helper()
	var event nostr.Event
	if err := json.Unmarshal([]byte(entry.EventJSON), &event); err != nil {
		t.Fatal(err)
	}
	event.CreatedAt++
	if recipientHex != "" {
		for i, tag := range event.Tags {
			if len(tag) > 1 && tag[0] == "p" {
				event.Tags[i] = nostr.Tag{"p", recipientHex}
			}
		}
	}
	if err := event.Sign(sk); err != nil {
		t.Fatal(err)
	}
	if !event.CheckID() || !event.VerifySignature() {
		t.Fatal("repro event B must be fully valid")
	}
	return event
}

func encodeEvent(t *testing.T, event nostr.Event) string {
	t.Helper()
	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// #221: entry A's metadata with a caller-supplied, fully valid signed event B
// must be refused before Reserve, Start, publish, or any outbox write.
func TestAttemptSendGroupAttemptRejectsUnboundSignedEventBeforeReserve(t *testing.T) {
	base := groupAttemptEntry(t)
	other := nostr.Generate()
	cases := map[string]func() types.OutboxEntry{
		"entry A metadata, foreign signed event B": func() types.OutboxEntry {
			e := base
			e.EventJSON = encodeEvent(t, resignedGroupEvent(t, base, other, ""))
			return e
		},
		"event ID matches entry but recipient differs": func() types.OutboxEntry {
			e := base
			b := resignedGroupEvent(t, base, other, recipientHex(nostr.Generate().Public()))
			e.ID, e.EventJSON = b.ID.Hex(), encodeEvent(t, b)
			return e
		},
		"duplicate normalized event key": func() types.OutboxEntry {
			e := base
			e.EventJSON = strings.Replace(base.EventJSON, `"id":`, `"\u0069d":"`+strings.Repeat("0", 64)+`","id":`, 1)
			return e
		},
		"invalid UTF-8 in event": func() types.OutboxEntry {
			e := base
			e.EventJSON = strings.Replace(base.EventJSON, `"content":"`, "\"content\":\"\xff", 1)
			return e
		},
	}
	for name, build := range cases {
		t.Run(name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			entry := build()
			before := entry.EventJSON
			h := &fakeAttemptHandler{}
			published := 0
			publish := func(context.Context, []string, nostr.Event, time.Duration) bool { published++; return true }
			store := func(*nostr.Event, string, string, bool) error { t.Fatal("history store must not run"); return nil }
			result, err := attemptSendRouted(context.Background(), nil, entry, []string{"wss://a", "wss://b"}, time.Second,
				AttemptOptions{Handlers: OutboxHandlers{Attempt: h}}, publish, store)
			if err == nil || result.Attempted || result.Sent || result.Issue != AgentMessageIssueOutboxBookkeepingFailed {
				t.Fatalf("result = %+v, err = %v", result, err)
			}
			if len(h.calls) != 0 || published != 0 {
				t.Fatalf("calls = %v published = %d; want no Reserve/Start/publish", h.calls, published)
			}
			if entry.EventJSON != before {
				t.Fatal("route mutated the caller snapshot")
			}
			if path, err := GetOutboxPath(); err != nil {
				t.Fatal(err)
			} else if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("outbox file touched: %v", statErr)
			}
		})
	}
}

func TestAttemptSendGroupAttemptRejectsSenderOutsideProcessIdentity(t *testing.T) {
	base := groupAttemptEntry(t)
	var event nostr.Event
	if err := json.Unmarshal([]byte(base.EventJSON), &event); err != nil {
		t.Fatal(err)
	}
	ks := &types.KeyStore{Identities: map[string]*types.Identity{"me": {Npub: common.EncodeNpub(nostr.Generate().Public())}}}
	h := &fakeAttemptHandler{}
	publish := func(context.Context, []string, nostr.Event, time.Duration) bool { t.Fatal("publish"); return false }
	result, err := attemptSendRouted(context.Background(), nil, base, []string{"wss://a", "wss://b"}, time.Second,
		AttemptOptions{Handlers: OutboxHandlers{Attempt: h}, KeyStore: ks}, publish, nil)
	if err == nil || result.Attempted || len(h.calls) != 0 {
		t.Fatalf("foreign sender: %+v %v calls=%v", result, err, h.calls)
	}
	ks.Identities["sender"] = &types.Identity{Npub: common.EncodeNpub(event.PubKey)}
	publishOK := func(context.Context, []string, nostr.Event, time.Duration) bool { return true }
	result, err = attemptSendRouted(context.Background(), nil, base, []string{"wss://a", "wss://b"}, time.Second,
		AttemptOptions{Handlers: OutboxHandlers{Attempt: h}, KeyStore: ks}, publishOK, nil)
	if err != nil || !result.Sent || fmt.Sprint(h.calls) != "[reserve start:7:2 accepted:7]" {
		t.Fatalf("own sender: %+v %v calls=%v", result, err, h.calls)
	}
}

// Valid control: escaped canonical id/pubkey member names still publish the
// same signed event, and the caller's bytes are untouched.
func TestAttemptSendGroupAttemptPublishesCanonicalEscapedEvent(t *testing.T) {
	entry := groupAttemptEntry(t)
	var expected nostr.Event
	if err := json.Unmarshal([]byte(entry.EventJSON), &expected); err != nil {
		t.Fatal(err)
	}
	entry.EventJSON = strings.Replace(entry.EventJSON, `"id":`, `"\u0069d":`, 1)
	entry.EventJSON = strings.Replace(entry.EventJSON, `"pubkey":`, `"pub\u006bey":`, 1)
	original := entry.EventJSON
	h := &fakeAttemptHandler{}
	var captured nostr.Event
	publish := func(_ context.Context, _ []string, event nostr.Event, _ time.Duration) bool {
		captured = event
		return false
	}
	result, err := attemptSendRouted(context.Background(), nil, entry, []string{"wss://a", "wss://b"}, time.Second,
		AttemptOptions{Handlers: OutboxHandlers{Attempt: h}}, publish, nil)
	if err != nil || !result.Attempted || result.Sent {
		t.Fatalf("result = %+v, err = %v", result, err)
	}
	if captured.ID != expected.ID || captured.PubKey != expected.PubKey || captured.Sig != expected.Sig || captured.Content != expected.Content ||
		!captured.CheckID() || !captured.VerifySignature() {
		t.Fatalf("published event differs from frozen signed event")
	}
	if entry.EventJSON != original || fmt.Sprint(h.calls) != "[reserve start:7:2 failed:7]" {
		t.Fatalf("bytes changed or calls = %v", h.calls)
	}
}

func recipientHex(pk nostr.PubKey) string { return hex.EncodeToString(pk[:]) }

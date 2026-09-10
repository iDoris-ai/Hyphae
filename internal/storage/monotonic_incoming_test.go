package storage

import (
	"testing"

	"github.com/iDoris-ai/hyphae/pkg/types"
)

// A message that the daemon already recorded as INCOMING must not be turned back
// into an outgoing one by the sender's own store.
//
// Why this exists: `agent msg` publishes to the relay BEFORE it stores the
// message locally. If the daemon's own subscription receives the echo inside
// that window it writes the row with is_incoming=1 — and the sender's
// subsequent write then overwrote it back to 0, while the daemon's `seen` set
// already held the id so it never reprocessed it. The message was permanently
// "outgoing" in the local database.
//
// Reported downstream by Agent24, whose inbound-liveness canary sends to itself
// and waits to see the message arrive: it could never confirm.
//
// How often the window is hit is NOT established. An earlier downstream reading
// (sent=105, confirmed=2, lost=94) did not reproduce on a later controlled A/B
// run against a local relay -- patched and unpatched binaries scored the same --
// and that reading recorded no relay, binary, or date, so it cannot be
// re-examined. The defect below is real and this test pins it deterministically;
// the frequency claim is deliberately left out rather than repeated.
func TestIncomingIsNotUndoneByTheSendersOwnWrite(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	const id = "e3f1a2b3c4d5e6f708192a3b4c5d6e7f80912a3b4c5d6e7f80912a3b4c5d6e7f"

	// 1. The daemon sees the echo first and records it as incoming.
	if err := store.StoreMessage(&types.StoredMessage{
		ID:            id,
		SenderNpub:    "npub1self",
		RecipientNpub: "npub1self",
		Plaintext:     "canary",
		IsIncoming:    true,
	}); err != nil {
		t.Fatalf("storing the incoming echo: %v", err)
	}

	// 2. The sender's own write lands afterwards, describing the same event as
	//    outgoing. It must not erase what the daemon observed.
	if err := store.StoreMessage(&types.StoredMessage{
		ID:            id,
		SenderNpub:    "npub1self",
		RecipientNpub: "npub1self",
		Plaintext:     "canary",
		IsIncoming:    false,
	}); err != nil {
		t.Fatalf("storing the outgoing copy: %v", err)
	}

	got, err := store.GetMessage(id)
	if err != nil {
		t.Fatalf("reading it back: %v", err)
	}
	isIncoming := 0
	if got.IsIncoming {
		isIncoming = 1
	}
	if isIncoming != 1 {
		t.Fatalf("is_incoming went back to %d: an arrival the daemon already saw "+
			"was erased by the sender's own write", isIncoming)
	}
}

// The control. Without it, an implementation that hardcoded is_incoming=1 would
// pass the test above, and "monotonic" would be indistinguishable from "always
// incoming".
func TestAPurelyOutgoingMessageStaysOutgoing(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	const id = "aaaaaaaabbbbbbbbccccccccddddddddeeeeeeeeffffffff0000000011111111"
	if err := store.StoreMessage(&types.StoredMessage{
		ID:            id,
		SenderNpub:    "npub1self",
		RecipientNpub: "npub1peer",
		Plaintext:     "hello",
		IsIncoming:    false,
	}); err != nil {
		t.Fatalf("storing: %v", err)
	}

	got, err := store.GetMessage(id)
	if err != nil {
		t.Fatalf("reading it back: %v", err)
	}
	isIncoming := 0
	if got.IsIncoming {
		isIncoming = 1
	}
	if isIncoming != 0 {
		t.Fatalf("a message that was only ever sent reads as incoming (%d)", isIncoming)
	}
}

// `INSERT OR REPLACE` is delete-then-insert, so a second write that omits a
// column silently blanks it. The upsert must not have inherited that.
func TestASecondWriteDoesNotBlankFieldsItOmits(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	const id = "1111111122222222333333334444444455555555666666667777777788888888"
	if err := store.StoreMessage(&types.StoredMessage{
		ID:            id,
		SenderNpub:    "npub1peer",
		RecipientNpub: "npub1self",
		Plaintext:     "the decrypted text",
		IsIncoming:    true,
	}); err != nil {
		t.Fatalf("first write: %v", err)
	}
	// The sender's copy has no plaintext to add.
	if err := store.StoreMessage(&types.StoredMessage{
		ID:            id,
		SenderNpub:    "npub1peer",
		RecipientNpub: "npub1self",
		IsIncoming:    false,
	}); err != nil {
		t.Fatalf("second write: %v", err)
	}

	got, err := store.GetMessage(id)
	if err != nil {
		t.Fatalf("reading it back: %v", err)
	}
	plaintext := got.Plaintext
	if plaintext != "the decrypted text" {
		t.Fatalf("the decrypted text was blanked by a later write: %q", plaintext)
	}
}

// The limit of the guard above, pinned deliberately so nobody reads the upsert
// and assumes plaintext is now safe in general.
//
// `plaintext` is only preserved when the incoming value is EMPTY. A second write
// carrying a non-empty but wrong value still overwrites a good decrypted
// plaintext, and there is a live path that does exactly that:
// internal/messaging/outbox.go's retry calls
// StoreOutgoingMessage(&event, ..., event.Content, true), where `event` was
// unmarshalled from the queued EventJSON — so `event.Content` is the encrypted
// (and possibly zstd-compressed) payload, not plaintext. If the daemon has
// already stored the decrypted text for that id, the retry replaces it with
// ciphertext.
//
// Fixing that belongs at the source (outbox.go should not pass ciphertext as
// plaintext), not by making this UPDATE clause guess which of two non-empty
// strings is "more plaintext". Tracked as a follow-up; this test documents
// today's behaviour rather than asserting the behaviour we want.
func TestPlaintextGuardDoesNotCoverNonEmptyOverwrite(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	const id = "99999999aaaaaaaa88888888bbbbbbbb77777777cccccccc66666666dddddddd"
	if err := store.StoreMessage(&types.StoredMessage{
		ID:            id,
		SenderNpub:    "npub1peer",
		RecipientNpub: "npub1self",
		Plaintext:     "the real decrypted text",
		IsIncoming:    true,
	}); err != nil {
		t.Fatalf("first write: %v", err)
	}
	// Shaped like the outbox retry: non-empty, but it is the ciphertext.
	if err := store.StoreMessage(&types.StoredMessage{
		ID:            id,
		SenderNpub:    "npub1peer",
		RecipientNpub: "npub1self",
		Plaintext:     "AAAA+ciphertext+blob==",
		IsIncoming:    false,
	}); err != nil {
		t.Fatalf("second write: %v", err)
	}

	got, err := store.GetMessage(id)
	if err != nil {
		t.Fatalf("reading it back: %v", err)
	}
	if got.Plaintext != "AAAA+ciphertext+blob==" {
		t.Fatalf("this test documents CURRENT behaviour: a non-empty second write "+
			"still wins. It read back %q. If this now fails because the overwrite "+
			"was fixed at the source, delete this test — do not weaken it.", got.Plaintext)
	}
}

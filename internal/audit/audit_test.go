package audit

import (
	"os"
	"path/filepath"
	"testing"
)

func setupTestAudit(t *testing.T) {
	t.Helper()
	tmpDir := t.TempDir()
	os.Setenv("HOME", tmpDir)
	resetForTest()
	t.Cleanup(resetForTest)
}

// TestGetDBRetriesAfterFailure covers the Codex-review finding: getDB used to
// cache a failed initialization forever via sync.Once, which would be fine
// for one-shot CLI commands but would permanently disable audit logging for
// the rest of a long-running daemon process if the very first attempt hit a
// transient error. It must retry, not latch the failure.
func TestGetDBRetriesAfterFailure(t *testing.T) {
	t.Helper()
	resetForTest()
	t.Cleanup(resetForTest)

	// HOME pointing at a file (not a directory) makes os.MkdirAll fail inside
	// keyStoreDir() — a stand-in for "first attempt hits a transient error".
	notADir := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(notADir, []byte("x"), 0644); err != nil {
		t.Fatalf("failed to set up fixture file: %v", err)
	}
	os.Setenv("HOME", notADir)

	if _, err := getDB(); err == nil {
		t.Fatal("expected getDB to fail while HOME points at a non-directory")
	}

	// "Recovery": HOME now points somewhere getDB can actually use.
	os.Setenv("HOME", t.TempDir())
	if _, err := getDB(); err != nil {
		t.Fatalf("expected getDB to recover on retry after HOME was fixed, got: %v", err)
	}
}

// TestComputeHashNoDelimiterAmbiguity covers the Codex-review finding: the
// original preimage was built with fmt.Sprintf("%d|%d|%s|%s|%s|%s", ...),
// which is not self-delimiting — a "|" inside actor/action/details could
// shift field boundaries and produce the same preimage (and therefore hash)
// for two logically different tuples. The JSON-based encoding must not have
// this problem.
func TestComputeHashNoDelimiterAmbiguity(t *testing.T) {
	// Under naive "%s|%s" concatenation these two tuples would produce an
	// identical preimage: ("a|b", "c") and ("a", "b|c") both concatenate to
	// "a|b|c" once joined with "|".
	h1 := computeHash(1, 100, "a|b", "c", "{}", genesisHash)
	h2 := computeHash(1, 100, "a", "b|c", "{}", genesisHash)
	if h1 == h2 {
		t.Fatal("computeHash produced identical hashes for tuples that would collide under naive delimiter concatenation")
	}
}

func TestLogActionAndVerifyChain(t *testing.T) {
	setupTestAudit(t)

	for i := 0; i < 5; i++ {
		if err := LogAction("alice", ActionMessageSent, map[string]any{"n": i}); err != nil {
			t.Fatalf("LogAction failed: %v", err)
		}
	}

	entries, err := List(0)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(entries) != 5 {
		t.Fatalf("expected 5 entries, got %d", len(entries))
	}

	ok, brokenAt, err := VerifyChain()
	if err != nil {
		t.Fatalf("VerifyChain failed: %v", err)
	}
	if !ok {
		t.Fatalf("expected chain to verify ok, broken at seq %d", brokenAt)
	}
}

func TestVerifyChainDetectsTamperedDetails(t *testing.T) {
	setupTestAudit(t)

	for i := 0; i < 3; i++ {
		if err := LogAction("alice", ActionMessageSent, map[string]any{"n": i}); err != nil {
			t.Fatalf("LogAction failed: %v", err)
		}
	}

	conn, err := getDB()
	if err != nil {
		t.Fatalf("getDB failed: %v", err)
	}
	// Tamper with the middle row's details without recomputing its hash —
	// simulates someone editing the DB file directly, bypassing LogAction.
	if _, err := conn.Exec(`UPDATE audit_log SET details = '{"tampered":true}' WHERE seq = 2`); err != nil {
		t.Fatalf("failed to tamper with row: %v", err)
	}

	ok, brokenAt, err := VerifyChain()
	if err != nil {
		t.Fatalf("VerifyChain failed: %v", err)
	}
	if ok {
		t.Fatal("expected VerifyChain to detect tampering, got ok=true")
	}
	if brokenAt != 2 {
		t.Errorf("expected break reported at seq 2, got %d", brokenAt)
	}
}

func TestVerifyChainDetectsBrokenPrevHashLink(t *testing.T) {
	setupTestAudit(t)

	for i := 0; i < 3; i++ {
		if err := LogAction("alice", ActionMessageSent, nil); err != nil {
			t.Fatalf("LogAction failed: %v", err)
		}
	}

	conn, err := getDB()
	if err != nil {
		t.Fatalf("getDB failed: %v", err)
	}
	// Rewrite row 3's prev_hash to something that doesn't match row 2's hash
	// — simulates deleting a row out of the middle of the chain.
	if _, err := conn.Exec(`UPDATE audit_log SET prev_hash = 'deadbeef' WHERE seq = 3`); err != nil {
		t.Fatalf("failed to corrupt prev_hash: %v", err)
	}

	ok, brokenAt, err := VerifyChain()
	if err != nil {
		t.Fatalf("VerifyChain failed: %v", err)
	}
	if ok {
		t.Fatal("expected VerifyChain to detect the broken prev_hash link")
	}
	if brokenAt != 3 {
		t.Errorf("expected break reported at seq 3, got %d", brokenAt)
	}
}

func TestVerifyChainEmptyLogIsOK(t *testing.T) {
	setupTestAudit(t)

	ok, brokenAt, err := VerifyChain()
	if err != nil {
		t.Fatalf("VerifyChain failed: %v", err)
	}
	if !ok || brokenAt != 0 {
		t.Errorf("expected empty chain to verify ok, got ok=%v brokenAt=%d", ok, brokenAt)
	}
}

func TestLogActionFailureDoesNotPanicOnNilDetails(t *testing.T) {
	setupTestAudit(t)

	if err := LogAction("bob", ActionIdentityCreated, nil); err != nil {
		t.Fatalf("LogAction with nil details failed: %v", err)
	}

	entries, err := List(1)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Actor != "bob" || entries[0].Action != string(ActionIdentityCreated) {
		t.Errorf("unexpected entry: %+v", entries[0])
	}
}

func TestListRespectsLimit(t *testing.T) {
	setupTestAudit(t)

	for i := 0; i < 10; i++ {
		if err := LogAction("alice", ActionMessageSent, nil); err != nil {
			t.Fatalf("LogAction failed: %v", err)
		}
	}

	entries, err := List(3)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries with limit=3, got %d", len(entries))
	}
	// Most recent first.
	if entries[0].Seq != 10 {
		t.Errorf("expected first entry seq=10 (most recent), got %d", entries[0].Seq)
	}
}

package audit

import (
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
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

// TestLogActionConcurrentCallsDoNotCorruptOrHang exercises the documented
// "single-writer" concurrency limitation for real: LogAction does a
// read-then-insert inside one transaction with no busy_timeout configured
// until this task's second Codex/self-review pass added one. Verifies that
// under real concurrent callers (as would happen in the daemon watching
// several relays at once), every call either succeeds or fails cleanly, the
// chain stays internally consistent (no corruption, no hang), and — now that
// busy_timeout is set — most calls actually succeed rather than being
// dropped immediately on SQLITE_BUSY.
func TestLogActionConcurrentCallsDoNotCorruptOrHang(t *testing.T) {
	setupTestAudit(t)

	const n = 50
	errCh := make(chan error, n)
	done := make(chan struct{})
	go func() {
		var wg sync.WaitGroup
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				errCh <- LogAction("alice", ActionMessageSent, map[string]any{"n": i})
			}(i)
		}
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("concurrent LogAction calls did not complete within 10s (hang / deadlock)")
	}
	close(errCh)

	var succeeded, failed int
	for err := range errCh {
		if err == nil {
			succeeded++
		} else {
			failed++
		}
	}
	t.Logf("concurrent LogAction: %d succeeded, %d failed (out of %d)", succeeded, failed, n)

	// Whatever subset actually got committed, the chain itself must still be
	// internally consistent — no seq collision, no broken hash link.
	ok, brokenAt, err := VerifyChain()
	if err != nil {
		t.Fatalf("VerifyChain failed: %v", err)
	}
	if !ok {
		t.Fatalf("chain corrupted by concurrent writers, broken at seq %d", brokenAt)
	}

	entries, err := List(0)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(entries) != succeeded {
		t.Errorf("chain has %d entries but %d LogAction calls reported success — mismatch", len(entries), succeeded)
	}

	// With busy_timeout set, SQLite waits for the lock instead of failing
	// immediately, so almost everything should get through in 10s of wall time.
	if succeeded < n*8/10 {
		t.Errorf("expected busy_timeout to let most concurrent writes through, only %d/%d succeeded", succeeded, n)
	}
}

// TestGetDBWaitsForLockedFreshDatabase: the first open of a fresh database
// switches it to WAL, which needs a lock. When another process holds that lock
// (several CLI processes starting at once), getDB must wait for it instead of
// failing at once with SQLITE_BUSY and dropping the audit entry. This goes red
// without execRetryBusy; it does not pin the order of busy_timeout.
func TestGetDBWaitsForLockedFreshDatabase(t *testing.T) {
	setupTestAudit(t)
	dir, err := keyStoreDir()
	if err != nil {
		t.Fatal(err)
	}
	holder, err := sql.Open("sqlite", filepath.Join(dir, "messages.db"))
	if err != nil {
		t.Fatal(err)
	}
	holder.SetMaxOpenConns(1)
	defer holder.Close()
	if _, err := holder.Exec("CREATE TABLE lock_probe (x INTEGER)"); err != nil {
		t.Fatal(err)
	}
	if _, err := holder.Exec("BEGIN EXCLUSIVE"); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(300 * time.Millisecond)
		_, _ = holder.Exec("ROLLBACK")
	}()
	if _, err := getDB(); err != nil {
		t.Fatalf("getDB must wait for the lock holder, got: %v", err)
	}
}

// TestLogActionAcrossProcesses: several CLI processes append to the same chain
// at once. Every append must succeed and the chain must stay verifiable. With a
// deferred transaction (read the tail, then upgrade to write) a process that
// loses the race gets SQLITE_BUSY immediately and its entry is dropped.
func TestLogActionAcrossProcesses(t *testing.T) {
	if os.Getenv("AUDIT_HELPER_PROCESS") == "1" {
		for i := 0; i < 25; i++ {
			if err := LogAction("helper", Action("x"), map[string]any{"i": i}); err != nil {
				fmt.Fprintln(os.Stderr, "LogAction failed:", err)
				os.Exit(3)
			}
		}
		os.Exit(0)
	}
	setupTestAudit(t)
	const procs = 8
	var wg sync.WaitGroup
	outs := make([]string, procs)
	errs := make([]error, procs)
	for i := 0; i < procs; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cmd := exec.Command(os.Args[0], "-test.run=^TestLogActionAcrossProcesses$")
			cmd.Env = append(os.Environ(), "AUDIT_HELPER_PROCESS=1")
			b, err := cmd.CombinedOutput()
			outs[i], errs[i] = string(b), err
		}(i)
	}
	wg.Wait()
	for i := range errs {
		if errs[i] != nil {
			t.Fatalf("process %d: %v\n%s", i, errs[i], outs[i])
		}
	}
	ok, broken, err := VerifyChain()
	if err != nil || !ok {
		t.Fatalf("chain not verifiable: ok=%v broken=%d err=%v", ok, broken, err)
	}
	entries, err := List(1000)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != procs*25 {
		t.Fatalf("got %d entries, want %d", len(entries), procs*25)
	}
}

// TestGetDBRejectsQueryCharactersInPath: the driver would read "?" in the path
// as a DSN query string and open a different file, silently dropping _txlock.
func TestGetDBRejectsQueryCharactersInPath(t *testing.T) {
	resetForTest()
	t.Cleanup(resetForTest)
	home := filepath.Join(t.TempDir(), "a?b")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	if _, err := getDB(); err == nil {
		t.Fatal("expected getDB to refuse a keystore path containing '?'")
	}
}

package messaging

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/iDoris-ai/hyphae/pkg/types"
)

var outboxCLI string

func TestMain(m *testing.M) {
	if os.Getenv("HYPHAE_OUTBOX_HELPER_ID") != "" {
		os.Exit(m.Run())
	}
	_, source, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
	buildDir, err := os.MkdirTemp("", "hyphae-outbox-cli-")
	if err != nil {
		panic(err)
	}
	outboxCLI = filepath.Join(buildDir, "hyphae")
	buildCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	build := exec.CommandContext(buildCtx, "go", "build", "-o", outboxCLI, "./cmd/hyphae")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		_ = os.RemoveAll(buildDir)
		panic("build CLI: " + err.Error() + ": " + string(output))
	}
	code := m.Run()
	_ = os.RemoveAll(buildDir)
	os.Exit(code)
}

type outboxCLIResult struct {
	stdout string
	stderr string
	code   int
}

func runOutboxCLI(t *testing.T, home string, env []string, args ...string) outboxCLIResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, outboxCLI, args...)
	cmd.Env = isolatedOutboxCLIEnv(os.Environ(), home, env)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	result := outboxCLIResult{stdout: stdout.String(), stderr: stderr.String()}
	if err == nil {
		return result
	}
	if ctx.Err() != nil {
		t.Fatalf("CLI timed out: %v\nstdout: %s\nstderr: %s", ctx.Err(), result.stdout, result.stderr)
	}
	if exit, ok := err.(*exec.ExitError); ok {
		result.code = exit.ExitCode()
		return result
	}
	t.Fatalf("run CLI: %v", err)
	return result
}

func isolatedOutboxCLIEnv(current []string, home string, extra []string) []string {
	env := make([]string, 0, len(current)+len(extra)+1)
	for _, item := range current {
		if strings.HasPrefix(item, "HOME=") || strings.HasPrefix(item, "HYPHAE_OUTPUT=") || strings.HasPrefix(item, "AGENT_SPEAKER_OUTPUT=") {
			continue
		}
		env = append(env, item)
	}
	env = append(env, "HOME="+home)
	return append(env, extra...)
}

func writeOutboxFixture(t *testing.T, home string, ob *types.Outbox) []byte {
	t.Helper()
	dir := filepath.Join(home, ".hyphae")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(ob)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "outbox.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return data
}

func decodeOutboxResult(t *testing.T, raw string) map[string]any {
	t.Helper()
	var result map[string]any
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("not a single JSON response: %v\n%s", err, raw)
	}
	return result
}

func TestOutboxListJSONWhitelistAndEmptyArray(t *testing.T) {
	home := t.TempDir()
	writeOutboxFixture(t, home, &types.Outbox{Entries: []types.OutboxEntry{{
		ID: "aabb", EventJSON: `{"content":"secret body","secret":"nsec1abcdefghijklmnopqrstuv"}`,
		RecipientNpub: "npub1recipient", Relays: []string{"wss://relay.example"},
		Status: "pending", RetryCount: 3, MaxRetries: 3, CreatedAt: 12, LastAttempt: 34,
	}}})
	result := runOutboxCLI(t, home, nil, "storage", "outbox", "list", "--json")
	if result.code != 0 || result.stderr != "" {
		t.Fatalf("list exit=%d stdout=%q stderr=%q", result.code, result.stdout, result.stderr)
	}
	data := decodeOutboxResult(t, result.stdout)["data"].([]any)
	if len(data) != 1 {
		t.Fatalf("list returned %#v", data)
	}
	entry := data[0].(map[string]any)
	want := []string{"id", "recipient_npub", "relays", "status", "retry_count", "max_retries", "created_at", "last_attempt", "duplicate_id", "stuck"}
	if len(entry) != len(want) {
		t.Fatalf("unexpected list fields: %#v", entry)
	}
	for _, key := range want {
		if _, ok := entry[key]; !ok {
			t.Errorf("missing field %q: %#v", key, entry)
		}
	}
	if entry["id"] != "61616262" || entry["stuck"] != true || entry["event_json"] != nil {
		t.Fatalf("unexpected safe entry: %#v", entry)
	}
	if strings.Contains(result.stdout, "secret body") || strings.Contains(result.stdout, "nsec1") {
		t.Fatalf("outbox JSON leaked event data: %s", result.stdout)
	}

	emptyHome := t.TempDir()
	empty := runOutboxCLI(t, emptyHome, []string{"HYPHAE_OUTPUT=json"}, "storage", "outbox", "list")
	if empty.code != 0 || empty.stderr != "" {
		t.Fatalf("empty list exit=%d stderr=%q", empty.code, empty.stderr)
	}
	if got := decodeOutboxResult(t, empty.stdout)["data"].([]any); len(got) != 0 {
		t.Fatalf("empty list data = %#v, want []", got)
	}
}

func TestOutboxClearJSONValidationDoesNotTouchLegacyQueue(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{name: "missing yes", args: []string{"--failed"}},
		{name: "missing failed", args: []string{"--yes"}},
		{name: "negative threshold", args: []string{"--failed", "--yes", "--min-failures", "-1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			before := writeOutboxFixture(t, home, &types.Outbox{Entries: []types.OutboxEntry{{ID: "legacy", EventJSON: `{"content":"keep"}`, Status: "failed"}}})
			args := append([]string{"storage", "outbox", "clear"}, tc.args...)
			args = append(args, "--json")
			result := runOutboxCLI(t, home, nil, args...)
			if result.code != 1 || result.stdout != "" {
				t.Fatalf("invalid clear exit=%d stdout=%q stderr=%q", result.code, result.stdout, result.stderr)
			}
			errorResult := decodeOutboxResult(t, result.stderr)
			if errorResult["ok"] != false || errorResult["error"] != "user_error" {
				t.Fatalf("unexpected JSON error: %#v", errorResult)
			}
			after, err := os.ReadFile(filepath.Join(home, ".hyphae", "outbox.json"))
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("rejected clear modified legacy queue: err=%v before=%s after=%s", err, before, after)
			}
		})
	}
}

func TestOutboxClearJSONSuccessAndCorruptQueueError(t *testing.T) {
	home := t.TempDir()
	writeOutboxFixture(t, home, &types.Outbox{Entries: []types.OutboxEntry{
		{ID: "failed", EventJSON: `{"content":"private"}`, Status: "failed"},
		{ID: "healthy", Status: "pending", RetryCount: 0},
	}})
	result := runOutboxCLI(t, home, nil, "storage", "outbox", "clear", "--failed", "--yes", "--json")
	if result.code != 0 || result.stderr != "" {
		t.Fatalf("clear exit=%d stdout=%q stderr=%q", result.code, result.stdout, result.stderr)
	}
	data := decodeOutboxResult(t, result.stdout)["data"].(map[string]any)
	if data["removed"] != float64(1) || data["remaining"] != float64(1) {
		t.Fatalf("unexpected clear response: %#v", data)
	}
	noMatchHome := t.TempDir()
	writeOutboxFixture(t, noMatchHome, &types.Outbox{Entries: []types.OutboxEntry{{ID: "healthy", Status: "pending"}}})
	noMatch := runOutboxCLI(t, noMatchHome, nil, "storage", "outbox", "clear", "--failed", "--yes", "--json")
	if noMatch.code != 0 || noMatch.stderr != "" {
		t.Fatalf("no-match clear exit=%d stderr=%q", noMatch.code, noMatch.stderr)
	}
	noMatchData := decodeOutboxResult(t, noMatch.stdout)["data"].(map[string]any)
	if noMatchData["removed"] != float64(0) || noMatchData["remaining"] != float64(1) {
		t.Fatalf("unexpected no-match response: %#v", noMatchData)
	}

	corruptHome := t.TempDir()
	dir := filepath.Join(corruptHome, ".hyphae")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "outbox.json"), []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	bad := runOutboxCLI(t, corruptHome, nil, "storage", "outbox", "list", "--json")
	if bad.code != 4 || bad.stdout != "" || decodeOutboxResult(t, bad.stderr)["error"] != "other_error" {
		t.Fatalf("corrupt queue response exit=%d stdout=%q stderr=%q", bad.code, bad.stdout, bad.stderr)
	}
	diskHome := t.TempDir()
	diskDir := filepath.Join(diskHome, ".hyphae")
	if err := os.MkdirAll(diskDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(diskDir, "outbox.json"), 0700); err != nil {
		t.Fatal(err)
	}
	diskError := runOutboxCLI(t, diskHome, nil, "storage", "outbox", "list", "--json")
	if diskError.code != 4 || diskError.stdout != "" || decodeOutboxResult(t, diskError.stderr)["error"] != "other_error" {
		t.Fatalf("disk-error response exit=%d stdout=%q stderr=%q", diskError.code, diskError.stdout, diskError.stderr)
	}
}

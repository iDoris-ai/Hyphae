package identity

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/iDoris-ai/hyphae/internal/common"
)

var cliBinary string

func TestMain(m *testing.M) {
	_, source, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
	buildDir, err := os.MkdirTemp("", "hyphae-identity-cli-")
	if err != nil {
		panic(err)
	}
	cliBinary = filepath.Join(buildDir, "hyphae")
	build := exec.Command("go", "build", "-o", cliBinary, "./cmd/hyphae")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		panic("build CLI: " + err.Error() + ": " + string(output))
	}
	code := m.Run()
	_ = os.RemoveAll(buildDir)
	os.Exit(code)
}

type cliResult struct {
	stdout string
	stderr string
	code   int
	spent  time.Duration
}

func runIdentityCLI(t *testing.T, home string, stdin io.Reader, env []string, args ...string) cliResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, cliBinary, args...)
	cmd.Env = cleanCLIEnv(os.Environ(), home, env)
	cmd.Stdin = stdin
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	started := time.Now()
	err := cmd.Run()
	result := cliResult{stdout: stdout.String(), stderr: stderr.String(), spent: time.Since(started)}
	if err == nil {
		return result
	}
	if ctx.Err() != nil {
		t.Fatalf("CLI timed out after %s: %v\nstdout: %s\nstderr: %s", result.spent, ctx.Err(), result.stdout, result.stderr)
	}
	if exit, ok := err.(*exec.ExitError); ok {
		result.code = exit.ExitCode()
		return result
	}
	t.Fatalf("run CLI: %v", err)
	return result
}

func cleanCLIEnv(current []string, home string, extra []string) []string {
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

func decodeSuccess(t *testing.T, result cliResult) map[string]any {
	t.Helper()
	if result.code != 0 {
		t.Fatalf("CLI exit=%d\nstdout: %s\nstderr: %s", result.code, result.stdout, result.stderr)
	}
	if strings.TrimSpace(result.stderr) != "" {
		t.Fatalf("unexpected stderr: %s", result.stderr)
	}
	var response map[string]any
	if err := json.Unmarshal([]byte(result.stdout), &response); err != nil {
		t.Fatalf("stdout is not a single JSON response: %v\n%s", err, result.stdout)
	}
	if response["ok"] != true {
		t.Fatalf("unexpected response: %#v", response)
	}
	return response
}

func TestIdentityAndContactJSONCLI(t *testing.T) {
	home := t.TempDir()
	aliceResult := runIdentityCLI(t, home, nil, nil,
		"identity", "create", "--nickname", "alice", "--default", "--json")
	alice := decodeSuccess(t, aliceResult)["data"].(map[string]any)
	if alice["nickname"] != "alice" || alice["default"] != true || alice["encrypted"] != false {
		t.Fatalf("unexpected create data: %#v", alice)
	}
	if strings.Contains(aliceResult.stdout, "nsec") {
		t.Fatalf("identity JSON exposed a private key: %s", aliceResult.stdout)
	}
	bobResult := runIdentityCLI(t, home, nil, nil, "identity", "create", "--nickname", "bob", "--json")
	bob := decodeSuccess(t, bobResult)["data"].(map[string]any)
	npub := bob["npub"].(string)

	list := decodeSuccess(t, runIdentityCLI(t, home, nil, nil, "identity", "list", "--json"))["data"].([]any)
	if len(list) != 2 {
		t.Fatalf("identity list has %d entries, want 2", len(list))
	}
	if got := identityDefault(list, "alice"); got != true {
		t.Fatalf("alice default = %v, want true", got)
	}
	if got := identityDefault(list, "bob"); got != false {
		t.Fatalf("bob default = %v, want false", got)
	}

	use := decodeSuccess(t, runIdentityCLI(t, home, nil, nil, "identity", "use", "--nickname", "bob", "--json"))["data"].(map[string]any)
	if use["nickname"] != "bob" || use["default"] != true || use["npub"] != npub {
		t.Fatalf("unexpected use response: %#v", use)
	}
	humanCreate := runIdentityCLI(t, home, nil, nil, "identity", "create", "--nickname", "charlie")
	if humanCreate.code != 0 || !strings.Contains(humanCreate.stdout, "Created identity 'charlie'") || !strings.Contains(humanCreate.stdout, "Nsec: [hidden]") {
		t.Fatalf("human identity create output changed: %#v", humanCreate)
	}

	// The environment switch must produce [] for an empty contact list.
	empty := decodeSuccess(t, runIdentityCLI(t, home, nil, []string{"HYPHAE_OUTPUT=json"}, "contact", "list"))["data"].([]any)
	if len(empty) != 0 {
		t.Fatalf("empty contact list = %#v, want []", empty)
	}

	pubkey, err := common.ParsePublicKey(npub)
	if err != nil {
		t.Fatal(err)
	}
	added := decodeSuccess(t, runIdentityCLI(t, home, nil, nil,
		"contact", "add", "--nickname", "bob", "--npub", pubkey.Hex(), "--role", "agent", "--json"))["data"].(map[string]any)
	if added["nickname"] != "bob" || added["npub"] != npub || added["role"] != "agent" {
		t.Fatalf("unexpected contact add response: %#v", added)
	}
	contacts := decodeSuccess(t, runIdentityCLI(t, home, nil, []string{"HYPHAE_OUTPUT=json"}, "contact", "list"))["data"].([]any)
	if len(contacts) != 1 {
		t.Fatalf("contact list = %#v, want one contact", contacts)
	}
	contact := contacts[0].(map[string]any)
	if contact["nickname"] != "bob" || contact["npub"] != npub || contact["role"] != "agent" {
		t.Fatalf("unexpected contact list entry: %#v", contact)
	}

	human := runIdentityCLI(t, home, nil, nil, "contact", "list")
	if human.code != 0 || !strings.Contains(human.stdout, "📇 Contacts:") || strings.HasPrefix(strings.TrimSpace(human.stdout), "{") {
		t.Fatalf("human output changed: %#v", human)
	}
}

func identityDefault(entries []any, nickname string) bool {
	for _, item := range entries {
		entry := item.(map[string]any)
		if entry["nickname"] == nickname {
			return entry["default"].(bool)
		}
	}
	return false
}

func TestEncryptedIdentityJSONPasswordHandling(t *testing.T) {
	home := t.TempDir()
	first := runIdentityCLI(t, home, nil, nil, "identity", "create", "--nickname", "alice", "--password", "secret", "--json")
	if got := decodeSuccess(t, first)["data"].(map[string]any)["encrypted"]; got != true {
		t.Fatalf("encrypted flag = %v, want true", got)
	}
	second := runIdentityCLI(t, home, nil, nil, "identity", "create", "--nickname", "bob", "--password", "secret", "--json")
	if got := decodeSuccess(t, second)["data"].(map[string]any)["encrypted"]; got != true {
		t.Fatalf("second identity encrypted = %v, want true", got)
	}
	var store struct {
		Identities map[string]struct {
			Nsec string `json:"nsec"`
		} `json:"identities"`
	}
	data, err := os.ReadFile(filepath.Join(home, ".hyphae", "keystore.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &store); err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(store.Identities["bob"].Nsec, "nsec1") {
		t.Fatal("second identity in encrypted keystore was stored in plaintext")
	}
	wrong := runIdentityCLI(t, home, nil, nil, "identity", "create", "--nickname", "wrong", "--password", "bad", "--json")
	if wrong.code != 3 || strings.TrimSpace(wrong.stdout) != "" || !strings.Contains(wrong.stderr, `"error":"auth_error"`) {
		t.Fatalf("wrong password was not reported as auth_error: %#v", wrong)
	}

	stdin, keepOpen, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	defer keepOpen.Close()
	missing := runIdentityCLI(t, home, stdin, nil, "identity", "create", "--nickname", "charlie", "--json")
	if missing.code != 3 || strings.TrimSpace(missing.stdout) != "" || strings.Contains(missing.stderr, "Keystore password:") || missing.spent > 2*time.Second {
		t.Fatalf("password-less JSON create did not fail promptly with auth_error: %#v", missing)
	}
	var failure map[string]any
	if err := json.Unmarshal([]byte(missing.stderr), &failure); err != nil || failure["error"] != "auth_error" {
		t.Fatalf("unexpected password error envelope: %s", missing.stderr)
	}

	prompt := runIdentityCLI(t, t.TempDir(), stdin, nil, "identity", "create", "--nickname", "prompted", "--password-prompt", "--json")
	if prompt.code != 3 || strings.TrimSpace(prompt.stdout) != "" || strings.Contains(prompt.stderr, "Enter password:") {
		t.Fatalf("password prompt ran in JSON mode: %#v", prompt)
	}
}

func TestIdentityCLIErrorPathsNeverReportSuccess(t *testing.T) {
	home := t.TempDir()
	checks := []struct {
		name     string
		args     []string
		wantCode int
	}{
		{name: "missing nickname", args: []string{"identity", "create", "--json"}, wantCode: 1},
		{name: "missing contact fields", args: []string{"contact", "add", "--json"}, wantCode: 1},
		{name: "missing use nickname", args: []string{"identity", "use", "--json"}, wantCode: 1},
		{name: "invalid role", args: []string{"contact", "add", "--nickname", "x", "--npub", "npub1invalid", "--role", "admin", "--json"}, wantCode: 1},
		{name: "unknown identity", args: []string{"identity", "use", "--nickname", "ghost", "--json"}, wantCode: 1},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			result := runIdentityCLI(t, home, nil, nil, check.args...)
			if result.code != check.wantCode || strings.TrimSpace(result.stdout) != "" {
				t.Fatalf("unexpected failed result: %#v", result)
			}
			var envelope map[string]any
			if err := json.Unmarshal([]byte(result.stderr), &envelope); err != nil || envelope["ok"] != false {
				t.Fatalf("stderr is not a JSON error envelope: %s", result.stderr)
			}
		})
	}

	diskHome := t.TempDir()
	if err := os.WriteFile(filepath.Join(diskHome, ".hyphae"), []byte("file"), 0600); err != nil {
		t.Fatal(err)
	}
	disk := runIdentityCLI(t, diskHome, nil, nil, "identity", "create", "--nickname", "alice", "--json")
	if disk.code == 0 || strings.TrimSpace(disk.stdout) != "" {
		t.Fatalf("disk failure reported success: %#v", disk)
	}
	if !strings.Contains(disk.stderr, `"ok":false`) {
		t.Fatalf("disk failure missing JSON error envelope: %s", disk.stderr)
	}
}

func TestPasswordCreateRefusesPartiallyEncryptingExistingKeystore(t *testing.T) {
	home := t.TempDir()
	created := runIdentityCLI(t, home, nil, nil, "identity", "create", "--nickname", "original", "--json")
	original := decodeSuccess(t, created)["data"].(map[string]any)
	storePath := filepath.Join(home, ".hyphae", "keystore.json")
	before, err := os.ReadFile(storePath)
	if err != nil {
		t.Fatal(err)
	}

	failed := runIdentityCLI(t, home, nil, nil, "identity", "create", "--nickname", "new", "--password", "secret", "--json")
	if failed.code != 1 || strings.TrimSpace(failed.stdout) != "" || !strings.Contains(failed.stderr, "identity change-password") {
		t.Fatalf("create partially encrypted an existing store: %#v", failed)
	}
	after, err := os.ReadFile(storePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("failed create changed the existing keystore bytes")
	}
	entries := decodeSuccess(t, runIdentityCLI(t, home, nil, nil, "identity", "list", "--json"))["data"].([]any)
	if len(entries) != 1 {
		t.Fatalf("identity list after refusal = %#v", entries)
	}
	entry := entries[0].(map[string]any)
	if entry["nickname"] != "original" || entry["npub"] != original["npub"] || entry["encrypted"] != false {
		t.Fatalf("existing identity changed after refusal: %#v", entry)
	}
}

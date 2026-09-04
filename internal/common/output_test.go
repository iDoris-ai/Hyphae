package common

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/urfave/cli/v3"
)

func runJSONModeCommand(t *testing.T, args []string, env map[string]string) bool {
	t.Helper()
	for k, v := range env {
		t.Setenv(k, v)
	}

	var got bool
	cmd := &cli.Command{
		Name: "app",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "json"},
		},
		Action: func(ctx context.Context, c *cli.Command) error {
			got = JSONMode(c)
			return nil
		},
	}
	if err := cmd.Run(context.Background(), args); err != nil {
		t.Fatalf("cmd.Run failed: %v", err)
	}
	return got
}

func TestJSONModeFlagTakesPrecedence(t *testing.T) {
	if got := runJSONModeCommand(t, []string{"app", "--json"}, nil); !got {
		t.Error("expected JSONMode true when --json flag is set")
	}
}

func TestJSONModeEnvFallback(t *testing.T) {
	if got := runJSONModeCommand(t, []string{"app"}, map[string]string{"HYPHAE_OUTPUT": "json"}); !got {
		t.Error("expected JSONMode true when HYPHAE_OUTPUT=json and no --json flag")
	}
}

func TestJSONModeEnvFallback_LegacyAlias(t *testing.T) {
	// AGENT_SPEAKER_OUTPUT is the pre-rename name, kept working indefinitely
	// so existing scripts/automation don't silently stop getting JSON output.
	if got := runJSONModeCommand(t, []string{"app"}, map[string]string{"AGENT_SPEAKER_OUTPUT": "json"}); !got {
		t.Error("expected JSONMode true when the legacy AGENT_SPEAKER_OUTPUT=json is set and no --json flag")
	}
}

func TestJSONModeDefaultFalse(t *testing.T) {
	if got := runJSONModeCommand(t, []string{"app"}, map[string]string{"HYPHAE_OUTPUT": ""}); got {
		t.Error("expected JSONMode false with no flag and no env var")
	}
}

func TestJSONModeIgnoresUnrelatedEnvValue(t *testing.T) {
	if got := runJSONModeCommand(t, []string{"app"}, map[string]string{"HYPHAE_OUTPUT": "yaml"}); got {
		t.Error("expected JSONMode false when HYPHAE_OUTPUT is set to something other than 'json'")
	}
}

func TestEmitHumanModeCallsHumanFn(t *testing.T) {
	called := false
	Emit(false, map[string]string{"a": "b"}, func() { called = true })
	if !called {
		t.Fatal("expected humanFn to be called in human mode")
	}
}

func TestEmitJSONModeDoesNotCallHumanFn(t *testing.T) {
	called := false
	Emit(true, map[string]string{"a": "b"}, func() { called = true })
	if called {
		t.Fatal("expected humanFn NOT to be called in json mode")
	}
}

func TestExitCodeForKnownCodes(t *testing.T) {
	cases := map[string]int{
		ErrCodeUser:          ExitUserError,
		ErrCodeNetwork:       ExitNetworkError,
		ErrCodeAuth:          ExitAuthError,
		ErrCodeWriteConflict: ExitWriteConflict,
		ErrCodeOther:         ExitOtherError,
		"unknown_code":       ExitOtherError,
	}
	for code, want := range cases {
		if got := exitCodeFor(code); got != want {
			t.Errorf("exitCodeFor(%q) = %d, want %d", code, got, want)
		}
	}
}

func TestEmitErrorPlainErrorDefaultsToOther(t *testing.T) {
	got := EmitError(true, errors.New("boom"))
	if got != ExitOtherError {
		t.Errorf("EmitError with plain error = %d, want %d", got, ExitOtherError)
	}
}

func TestEmitErrorExitErrorUsesWrappedCode(t *testing.T) {
	got := EmitError(true, NewExitError(ErrCodeNetwork, errors.New("connect failed")))
	if got != ExitNetworkError {
		t.Errorf("EmitError with network ExitError = %d, want %d", got, ExitNetworkError)
	}
}

func TestExitErrorUnwrap(t *testing.T) {
	inner := errors.New("inner")
	wrapped := NewExitError(ErrCodeAuth, inner)
	if !errors.Is(wrapped, inner) {
		t.Fatal("expected errors.Is to see through ExitError via Unwrap")
	}
}

func TestJSONModeFromArgsFlagAnyPosition(t *testing.T) {
	cases := [][]string{
		{"hyphae", "--json", "agent", "msg"},
		{"hyphae", "agent", "msg", "--json"},
		{"hyphae", "agent", "--json", "msg"},
	}
	for _, args := range cases {
		if !JSONModeFromArgs(args) {
			t.Errorf("JSONModeFromArgs(%v) = false, want true", args)
		}
	}
}

func TestJSONModeFromArgsEqualsForm(t *testing.T) {
	if !JSONModeFromArgs([]string{"hyphae", "agent", "msg", "--json=true"}) {
		t.Error("expected --json=true to enable JSON mode")
	}
	if JSONModeFromArgs([]string{"hyphae", "agent", "msg", "--json=false"}) {
		t.Error("expected --json=false to NOT enable JSON mode")
	}
}

func TestJSONModeFromArgsEnvFallback(t *testing.T) {
	t.Setenv("HYPHAE_OUTPUT", "json")
	if !JSONModeFromArgs([]string{"hyphae", "agent", "msg"}) {
		t.Error("expected env fallback to enable JSON mode when --json is absent from argv")
	}
}

func TestJSONModeFromArgsEnvFallback_LegacyAlias(t *testing.T) {
	t.Setenv("AGENT_SPEAKER_OUTPUT", "json")
	if !JSONModeFromArgs([]string{"hyphae", "agent", "msg"}) {
		t.Error("expected the legacy AGENT_SPEAKER_OUTPUT env fallback to still enable JSON mode")
	}
}

func TestJSONModeFromArgsDefaultFalse(t *testing.T) {
	t.Setenv("HYPHAE_OUTPUT", "")
	if JSONModeFromArgs([]string{"hyphae", "agent", "msg"}) {
		t.Error("expected JSONModeFromArgs false with no flag and no env var")
	}
}

func TestClassifyUnwrappedErrorRequiredFlags(t *testing.T) {
	cases := []error{
		errors.New(`Required flag "to" not set`),
		errors.New(`Required flags "to, content" not set`),
	}
	for _, err := range cases {
		if got := classifyUnwrappedError(err); got != ErrCodeUser {
			t.Errorf("classifyUnwrappedError(%q) = %q, want %q", err, got, ErrCodeUser)
		}
	}
}

func TestClassifyUnwrappedErrorUnknownDefaultsToOther(t *testing.T) {
	if got := classifyUnwrappedError(errors.New("something else broke")); got != ErrCodeOther {
		t.Errorf("classifyUnwrappedError = %q, want %q", got, ErrCodeOther)
	}
}

func TestRedactSecretsRedactsNsec(t *testing.T) {
	msg := "'nsec1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq' is not a known nickname or valid npub"
	got := redactSecrets(msg)
	if strings.Contains(got, "qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq") {
		t.Errorf("redactSecrets did not redact the nsec: %q", got)
	}
	if !strings.Contains(got, "[redacted-nsec]") {
		t.Errorf("redactSecrets output missing redaction marker: %q", got)
	}
}

func TestRedactSecretsLeavesNonSecretTextAlone(t *testing.T) {
	msg := "sender not found: identity 'alice' not found"
	if got := redactSecrets(msg); got != msg {
		t.Errorf("redactSecrets altered a message with no secrets: %q", got)
	}
}

func TestEmitErrorRedactsMessage(t *testing.T) {
	err := errors.New("'nsec1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq' is invalid")
	// EmitError writes to stderr; this test only checks it doesn't panic and
	// returns the expected exit code — redactSecrets itself is covered above.
	if got := EmitError(true, err); got != ExitOtherError {
		t.Errorf("EmitError = %d, want %d", got, ExitOtherError)
	}
}

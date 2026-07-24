package common

import (
	"context"
	"errors"
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
	if got := runJSONModeCommand(t, []string{"app"}, map[string]string{"AGENT_SPEAKER_OUTPUT": "json"}); !got {
		t.Error("expected JSONMode true when AGENT_SPEAKER_OUTPUT=json and no --json flag")
	}
}

func TestJSONModeDefaultFalse(t *testing.T) {
	if got := runJSONModeCommand(t, []string{"app"}, map[string]string{"AGENT_SPEAKER_OUTPUT": ""}); got {
		t.Error("expected JSONMode false with no flag and no env var")
	}
}

func TestJSONModeIgnoresUnrelatedEnvValue(t *testing.T) {
	if got := runJSONModeCommand(t, []string{"app"}, map[string]string{"AGENT_SPEAKER_OUTPUT": "yaml"}); got {
		t.Error("expected JSONMode false when AGENT_SPEAKER_OUTPUT is set to something other than 'json'")
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

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var hyphaeBinary string

func TestMain(m *testing.M) {
	tempDir, err := os.MkdirTemp("", "hyphae-cli-test-")
	if err != nil {
		panic(err)
	}
	hyphaeBinary = filepath.Join(tempDir, "hyphae")
	cmd := exec.Command("go", "build", "-o", hyphaeBinary, ".")
	if output, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build hyphae binary: %v\n%s", err, output)
		_ = os.RemoveAll(tempDir)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(tempDir)
	os.Exit(code)
}

func runHyphae(t *testing.T, bin string, args []string, extraEnv ...string) *exec.Cmd {
	t.Helper()

	cmd := exec.Command(bin, args...)
	cmd.Env = withoutOutputModeEnv(os.Environ())
	cmd.Env = append(cmd.Env, "HOME="+t.TempDir())
	cmd.Env = append(cmd.Env, extraEnv...)
	return cmd
}

func withoutOutputModeEnv(env []string) []string {
	filtered := make([]string, 0, len(env))
	for _, entry := range env {
		if strings.HasPrefix(entry, "HYPHAE_OUTPUT=") || strings.HasPrefix(entry, "AGENT_SPEAKER_OUTPUT=") || strings.HasPrefix(entry, "HOME=") {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

func TestRequiredFlagErrorsAreSingleJSONEnvelope(t *testing.T) {
	tests := []struct {
		name            string
		args            []string
		env             []string
		messageContains string
	}{
		{name: "both flags with json flag", args: []string{"agent", "msg", "--json"}, messageContains: `Required flag "to" not set`},
		{name: "missing recipient with json flag", args: []string{"agent", "msg", "--content", "hello", "--json"}, messageContains: `Required flag "to" not set`},
		{name: "missing content with json flag", args: []string{"agent", "msg", "--to", "bob", "--json"}, messageContains: `provide exactly one of --content or --content-file`},
		{name: "both flags with output environment", args: []string{"agent", "msg"}, env: []string{"HYPHAE_OUTPUT=json"}, messageContains: `Required flag "to" not set`},
		{name: "missing recipient with output environment", args: []string{"agent", "msg", "--content", "hello"}, env: []string{"HYPHAE_OUTPUT=json"}, messageContains: `Required flag "to" not set`},
		{name: "missing content with output environment", args: []string{"agent", "msg", "--to", "bob"}, env: []string{"HYPHAE_OUTPUT=json"}, messageContains: `provide exactly one of --content or --content-file`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := runHyphae(t, hyphaeBinary, tt.args, tt.env...)
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			err := cmd.Run()
			if exitCode(err) != 1 {
				t.Fatalf("exit code = %d, want 1; stderr: %s", exitCode(err), stderr.String())
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout = %q, want empty", stdout.String())
			}

			dec := json.NewDecoder(bytes.NewReader(stderr.Bytes()))
			var result struct {
				OK      bool   `json:"ok"`
				Error   string `json:"error"`
				Message string `json:"message"`
			}
			if err := dec.Decode(&result); err != nil {
				t.Fatalf("stderr is not one JSON envelope: %v; stderr: %q", err, stderr.String())
			}
			if err := dec.Decode(new(any)); err != io.EOF {
				t.Fatalf("stderr has trailing data after JSON envelope: %q", stderr.String())
			}
			if result.OK || result.Error != "user_error" || !strings.Contains(result.Message, tt.messageContains) {
				t.Fatalf("unexpected error envelope: %+v", result)
			}
		})
	}
}

func TestExplicitlyEmptyRequiredFlagsAreRejected(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "recipient", args: []string{"agent", "msg", "--to", "", "--content", "hello", "--json"}, want: "recipient is required"},
		{name: "content", args: []string{"agent", "msg", "--to", "bob", "--content", "", "--json"}, want: "message content is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := runHyphae(t, hyphaeBinary, tt.args)
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			if err := cmd.Run(); exitCode(err) != 1 {
				t.Fatalf("exit code = %d, want 1; stderr: %s", exitCode(err), stderr.String())
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout = %q, want empty", stdout.String())
			}
			if !strings.Contains(stderr.String(), tt.want) {
				t.Fatalf("stderr = %q, want it to contain %q", stderr.String(), tt.want)
			}
		})
	}
}

func TestRequiredFlagErrorsKeepHumanUsage(t *testing.T) {
	cmd := runHyphae(t, hyphaeBinary, []string{"agent", "msg"})
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); exitCode(err) != 1 {
		t.Fatalf("exit code = %d, want 1", exitCode(err))
	}
	if !strings.Contains(stderr.String(), "Incorrect Usage:") {
		t.Fatalf("stderr lacks human usage error: %q", stderr.String())
	}
	if !strings.Contains(stdout.String(), "Send a message to another agent") {
		t.Fatalf("stdout lacks command help: %q", stdout.String())
	}
}

func TestPositionalArgumentBeforeFlagIsPreserved(t *testing.T) {
	cmd := runHyphae(t, hyphaeBinary, []string{"publish", "{}", "--sec", "invalid"})
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); exitCode(err) != 4 {
		t.Fatalf("exit code = %d, want 4; stderr: %s", exitCode(err), stderr.String())
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "invalid secret key") {
		t.Fatalf("positional argument was not consumed before trailing flag: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode()
	}
	return -1
}

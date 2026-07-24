package common

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/urfave/cli/v3"
)

// Exit codes for --json mode error reporting. See specs/m1.5/tasks/02-json-output-mode.md.
const (
	ExitOK            = 0
	ExitUserError     = 1
	ExitNetworkError  = 2
	ExitAuthError     = 3
	ExitOtherError    = 4
	ExitWriteConflict = 5
)

// Error codes used in ExitError.Code, mapped to exit codes by exitCodeFor.
const (
	ErrCodeUser          = "user_error"
	ErrCodeNetwork       = "network_error"
	ErrCodeAuth          = "auth_error"
	ErrCodeWriteConflict = "write_conflict"
	ErrCodeOther         = "other_error"
)

// ExitError wraps an error with a machine-readable code (for --json output) and
// process exit code. Commands that want a non-default exit code in --json mode
// should return one of these instead of a plain error.
type ExitError struct {
	Code string // one of the ErrCode* constants
	Err  error
}

func (e *ExitError) Error() string { return e.Err.Error() }
func (e *ExitError) Unwrap() error { return e.Err }

// NewExitError wraps err with a machine-readable error code.
func NewExitError(code string, err error) *ExitError {
	return &ExitError{Code: code, Err: err}
}

// JSONMode resolves whether --json output is active for the current command:
// the --json flag takes precedence, falling back to AGENT_SPEAKER_OUTPUT=json
// if the flag wasn't set. All commands should call this instead of reading
// c.Bool("json") directly, so the env fallback applies consistently on every
// code path (success and error alike).
func JSONMode(c *cli.Command) bool {
	return c.Bool("json") || os.Getenv("AGENT_SPEAKER_OUTPUT") == "json"
}

// JSONModeFromArgs resolves --json mode by scanning the raw process argv
// directly, instead of asking a *cli.Command for a flag value.
//
// main()'s top-level error handler needs this variant: by the time app.Run
// returns an error, the failure may have happened before the relevant
// subcommand's flags were fully parsed (e.g. a missing required flag), so
// there is no *cli.Command in scope with a reliable c.Bool("json") — and a
// value latched once via a root Before hook is stale for --json placed after
// the subcommand name, since the root command only sees flags parsed up to
// that point. Scanning argv directly sidesteps cli's parse-order entirely:
// --json means --json no matter where in the command line it appears or
// which command failed.
func JSONModeFromArgs(args []string) bool {
	for _, a := range args {
		if a == "--json" {
			return true
		}
		if v, ok := strings.CutPrefix(a, "--json="); ok {
			return v != "" && v != "false" && v != "0"
		}
	}
	return os.Getenv("AGENT_SPEAKER_OUTPUT") == "json"
}

// Result is the JSON envelope written to stdout (success) or stderr (failure)
// when --json mode is active.
type Result struct {
	OK      bool   `json:"ok"`
	Data    any    `json:"data,omitempty"`
	Error   string `json:"error,omitempty"`
	Message string `json:"message,omitempty"`
}

// Emit prints a successful result. In JSON mode it writes {"ok":true,"data":data}
// to stdout. In human mode it calls humanFn, which should contain the existing
// fmt.Printf-based output for that command.
func Emit(jsonMode bool, data any, humanFn func()) {
	if !jsonMode {
		humanFn()
		return
	}
	writeResult(os.Stdout, Result{OK: true, Data: data})
}

// EmitError prints an error result and returns the process exit code the caller
// should os.Exit with. In JSON mode it writes {"ok":false,"error":...,"message":...}
// to stderr. In human mode it preserves the existing "Error: <message>" convention.
// The message is redacted (see redactSecrets) in both modes before printing,
// since it may echo back invalid user input verbatim (e.g. a mistyped nsec
// passed where an npub was expected).
func EmitError(jsonMode bool, err error) int {
	code := ErrCodeOther
	var exitErr *ExitError
	if errors.As(err, &exitErr) {
		code = exitErr.Code
	} else {
		code = classifyUnwrappedError(err)
	}

	message := redactSecrets(err.Error())

	if !jsonMode {
		fmt.Fprintf(os.Stderr, "Error: %s\n", message)
	} else {
		writeResult(os.Stderr, Result{OK: false, Error: code, Message: message})
	}
	return exitCodeFor(code)
}

// classifyUnwrappedError gives plain (non-ExitError) errors a best-effort
// ErrCode based on known urfave/cli v3 framework error message shapes (e.g.
// "Required flag %q not set") — these originate inside the cli library as
// unexported error types we can't type-assert from here, so this is a
// message-prefix heuristic rather than a type check. Anything unrecognized
// still falls back to ErrCodeOther.
func classifyUnwrappedError(err error) string {
	msg := err.Error()
	if strings.HasPrefix(msg, "Required flag ") || strings.HasPrefix(msg, "Required flags ") {
		return ErrCodeUser
	}
	return ErrCodeOther
}

// nsecPattern matches bech32-encoded Nostr secret keys (NIP-19 "nsec1..."),
// the one secret shape that's unambiguous to redact — unlike bare 64-char hex,
// which is also the shape of non-secret values (event IDs, hex pubkeys), so
// redacting that generically would over-redact.
var nsecPattern = regexp.MustCompile(`nsec1[02-9ac-hj-np-z]{20,}`)

// redactSecrets scrubs anything that looks like a raw nsec out of an error
// message before it's ever written to stdout/stderr/JSON. Error messages can
// echo back invalid user input verbatim (e.g. "'<value>' is not a valid npub"
// when the user meant to type an npub but pasted an nsec instead), and this
// tool's own design goal is being invoked by scripts whose stderr commonly
// ends up logged somewhere more persistent than a terminal.
func redactSecrets(msg string) string {
	return nsecPattern.ReplaceAllString(msg, "[redacted-nsec]")
}

func writeResult(w io.Writer, r Result) {
	enc := json.NewEncoder(w)
	// Encode errors here are unrecoverable (broken stdout/stderr); nothing
	// sensible to do but drop them rather than panic mid-exit.
	_ = enc.Encode(r)
}

func exitCodeFor(code string) int {
	switch code {
	case ErrCodeUser:
		return ExitUserError
	case ErrCodeNetwork:
		return ExitNetworkError
	case ErrCodeAuth:
		return ExitAuthError
	case ErrCodeWriteConflict:
		return ExitWriteConflict
	default:
		return ExitOtherError
	}
}

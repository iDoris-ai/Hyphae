package common

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

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
func EmitError(jsonMode bool, err error) int {
	code := ErrCodeOther
	var exitErr *ExitError
	if errors.As(err, &exitErr) {
		code = exitErr.Code
	}

	if !jsonMode {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
	} else {
		writeResult(os.Stderr, Result{OK: false, Error: code, Message: err.Error()})
	}
	return exitCodeFor(code)
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

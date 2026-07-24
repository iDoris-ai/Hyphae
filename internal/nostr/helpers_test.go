package nostr

import (
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureStdout runs fn with os.Stdout redirected to a pipe and returns
// everything written to it. Every command in this package prints directly
// rather than taking an io.Writer, so this is the smallest way to assert on
// a `cli.Command`'s Action output.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w

	fn()

	require.NoError(t, w.Close())
	os.Stdout = orig

	out, err := io.ReadAll(r)
	require.NoError(t, err)
	return string(out)
}

func TestMustAtoi(t *testing.T) {
	assert.Equal(t, 42, MustAtoi("42"))
	assert.Equal(t, 0, MustAtoi("not a number"), "invalid input silently yields the zero value, never panics despite the name")
	assert.Equal(t, 0, MustAtoi(""))
	assert.Equal(t, -7, MustAtoi("-7"))
}

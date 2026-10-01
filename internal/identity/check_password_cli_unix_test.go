//go:build darwin || linux

package identity

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckPasswordCLIRejectsFIFOWithoutBlocking(t *testing.T) {
	home := t.TempDir()
	directory := filepath.Join(home, KeyStoreDirName)
	require.NoError(t, os.Mkdir(directory, 0700))
	path := filepath.Join(directory, KeyStoreFile)
	require.NoError(t, syscall.Mkfifo(path, 0600))
	before := snapshotHomeTree(t, home)
	result := runIdentityCLI(t, home, strings.NewReader("not-a-password\n"), nil,
		"identity", "check-password", "--password-stdin", "--json")
	assertCheckPasswordError(t, result, 4, "other_error", "not-a-password")
	assert.Less(t, result.spent.Seconds(), 5.0, "FIFO detection must not wait for a writer")
	require.Equal(t, before, snapshotHomeTree(t, home))
}

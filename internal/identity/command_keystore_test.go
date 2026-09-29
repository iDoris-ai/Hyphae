package identity

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadPasswordStdinPreservesSpacesAndTrimsOneLineEnding(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  string
	}{
		{input: " pass  \n", want: " pass  "},
		{input: " pass  \r\n", want: " pass  "},
		{input: "a\n\n", want: "a\n"},
	} {
		got, err := readPasswordStdin(strings.NewReader(tc.input))
		require.NoError(t, err)
		assert.Equal(t, tc.want, got)
	}
}

func TestReadPasswordStdinEnforcesInputLimitAndRejectsEmpty(t *testing.T) {
	got, err := readPasswordStdin(strings.NewReader(strings.Repeat("p", maxPasswordStdinBytes)))
	require.NoError(t, err)
	assert.Len(t, got, maxPasswordStdinBytes)

	_, err = readPasswordStdin(strings.NewReader(strings.Repeat("p", maxPasswordStdinBytes+1)))
	require.ErrorContains(t, err, "4096 bytes")
	for _, input := range []string{"", "\n", "\r\n"} {
		_, err = readPasswordStdin(strings.NewReader(input))
		require.ErrorContains(t, err, "empty")
	}
}

func TestLoadKeyStoreForCommandUnlocksOnlyWhenRequired(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const password = " leading and trailing  "
	createLockedCommandKeyStore(t, password)

	read := &countingPasswordReader{input: password + "\r\n"}
	ks, err := LoadKeyStoreForCommand(KeyStoreCommandOptions{
		JSONMode: true, RequireSecret: true, PasswordStdin: true, Stdin: read,
	})
	require.NoError(t, err)
	readsAfterUnlock := read.calls
	assert.Positive(t, readsAfterUnlock)
	assert.NotNil(t, ks.MasterKey)
	secret, err := GetSecretKey(ks, "alice")
	require.NoError(t, err)
	assert.NotEqual(t, [32]byte{}, secret)

	locked, err := LoadKeyStoreForCommand(KeyStoreCommandOptions{
		JSONMode: true, RequireSecret: false, PasswordStdin: true, Stdin: read,
	})
	require.NoError(t, err)
	assert.Nil(t, locked.MasterKey)
	assert.Equal(t, readsAfterUnlock, read.calls, "commands that do not need a secret must not read stdin")
}

func TestLoadKeyStoreForCommandPasswordFailuresAreAuthAndDoNotEchoInput(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	createLockedCommandKeyStore(t, "correct secret")

	for _, tc := range []struct {
		name   string
		reader passwordReader
		want   string
	}{
		{name: "wrong password", reader: strings.NewReader("private wrong password\n"), want: "failed to unlock"},
		{name: "empty stdin", reader: strings.NewReader("\r\n"), want: "empty"},
		{name: "oversized stdin", reader: strings.NewReader(strings.Repeat("x", maxPasswordStdinBytes+1)), want: "4096 bytes"},
		{name: "read failure", reader: failingPasswordReader{}, want: "failed to read"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadKeyStoreForCommand(KeyStoreCommandOptions{
				JSONMode: true, RequireSecret: true, PasswordStdin: true, Stdin: tc.reader,
			})
			require.Error(t, err)
			var exitErr *common.ExitError
			require.ErrorAs(t, err, &exitErr)
			assert.Equal(t, common.ErrCodeAuth, exitErr.Code)
			assert.Contains(t, err.Error(), tc.want)
			assert.NotContains(t, err.Error(), "private wrong password")
		})
	}
}

func TestLoadKeyStoreForCommandJSONRequiresExplicitPasswordStdin(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	createLockedCommandKeyStore(t, "secret")
	reader := &countingPasswordReader{input: "secret\n"}
	_, err := LoadKeyStoreForCommand(KeyStoreCommandOptions{JSONMode: true, RequireSecret: true, Stdin: reader})
	require.Error(t, err)
	var exitErr *common.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, common.ErrCodeAuth, exitErr.Code)
	assert.Contains(t, err.Error(), "--password-stdin")
	assert.Zero(t, reader.calls)
}

func TestLoadKeyStoreForCommandLeavesKeystoreReadErrorsUnclassified(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path, err := EnsureKeyStore()
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(path, KeyStoreFile), []byte("{"), 0600))
	_, err = LoadKeyStoreForCommand(KeyStoreCommandOptions{JSONMode: true, RequireSecret: true, PasswordStdin: true, Stdin: strings.NewReader("secret\n")})
	require.Error(t, err)
	var exitErr *common.ExitError
	assert.False(t, errors.As(err, &exitErr), "corrupt keystore data remains an ordinary storage error")
	assert.Contains(t, err.Error(), "failed to parse keystore")
}

func createLockedCommandKeyStore(t *testing.T, password string) {
	t.Helper()
	ks := &types.KeyStore{Identities: make(map[string]*types.Identity), Contacts: make(map[string]*types.Contact)}
	_, err := CreateIdentityWithPassword(ks, "alice", password)
	require.NoError(t, err)
	ks.MasterKey = nil
	require.NoError(t, SaveKeyStore(ks))
}

type passwordReader interface {
	Read([]byte) (int, error)
}

type countingPasswordReader struct {
	input string
	calls int
}

func (r *countingPasswordReader) Read(p []byte) (int, error) {
	r.calls++
	if r.input == "" {
		return 0, io.EOF
	}
	n := copy(p, r.input)
	r.input = r.input[n:]
	return n, nil
}

type failingPasswordReader struct{}

func (failingPasswordReader) Read([]byte) (int, error) { return 0, errors.New("reader failure") }

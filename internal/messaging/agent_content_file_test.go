package messaging

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/internal/identity"
	"github.com/iDoris-ai/hyphae/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadMessageContentFileBoundaries(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, data []byte) string {
		t.Helper()
		path := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(path, data, 0600))
		return path
	}

	t.Run("preserves bytes and trailing newline", func(t *testing.T) {
		want := "  hello\n\n"
		got, err := readMessageContentFile(write("text", []byte(want)))
		require.NoError(t, err)
		assert.Equal(t, want, got)
	})
	t.Run("accepts exact size limit", func(t *testing.T) {
		got, err := readMessageContentFile(write("max", bytes.Repeat([]byte{'x'}, maxMessageContentFileBytes)))
		require.NoError(t, err)
		assert.Len(t, got, maxMessageContentFileBytes)
	})
	t.Run("rejects larger file", func(t *testing.T) {
		_, err := readMessageContentFile(write("large", bytes.Repeat([]byte{'x'}, maxMessageContentFileBytes+1)))
		require.EqualError(t, err, "message content file exceeds 1 MiB")
	})
	t.Run("rejects invalid UTF-8", func(t *testing.T) {
		_, err := readMessageContentFile(write("invalid", []byte{0xff, 0xfe}))
		require.EqualError(t, err, "message content file must contain valid UTF-8")
	})
	t.Run("rejects empty", func(t *testing.T) {
		got, err := readMessageContentFile(write("empty", nil))
		require.NoError(t, err)
		assert.Empty(t, got) // the command applies its normal empty-message check
	})
	t.Run("missing path is safe", func(t *testing.T) {
		path := filepath.Join(dir, "private-missing-path")
		_, err := readMessageContentFile(path)
		require.EqualError(t, err, "unable to read message content file")
		assert.NotContains(t, err.Error(), path)
	})
	t.Run("rejects directory", func(t *testing.T) {
		_, err := readMessageContentFile(dir)
		require.EqualError(t, err, "content file must be a regular file")
	})
	t.Run("rejects FIFO without blocking", func(t *testing.T) {
		path := filepath.Join(dir, "pipe")
		require.NoError(t, syscall.Mkfifo(path, 0600))
		done := make(chan error, 1)
		go func() { _, err := readMessageContentFile(path); done <- err }()
		select {
		case err := <-done:
			require.EqualError(t, err, "content file must be a regular file")
		case <-time.After(time.Second):
			t.Fatal("FIFO open blocked")
		}
	})
	t.Run("dash is not stdin", func(t *testing.T) {
		_, err := readMessageContentFile("-")
		require.EqualError(t, err, "content file must be a regular file")
	})
}

func TestAgentMsgContentFileCLI_EncryptedQueue(t *testing.T) {
	password := "test password"
	home := t.TempDir()
	t.Setenv("HOME", home)
	ResetStoreForTest()
	t.Cleanup(ResetStoreForTest)
	ks := &types.KeyStore{Identities: make(map[string]*types.Identity), Contacts: make(map[string]*types.Contact)}
	_, err := identity.CreateIdentityWithPassword(ks, "alice", password)
	require.NoError(t, err)
	_, err = identity.CreateIdentityWithPassword(ks, "bob", password)
	require.NoError(t, err)
	bobSK, err := identity.GetSecretKey(ks, "bob")
	require.NoError(t, err)
	ks.MasterKey = nil
	require.NoError(t, identity.SaveKeyStore(ks))

	content := "sensitive body\nretains trailing newline\n"
	contentPath := filepath.Join(t.TempDir(), "message.txt")
	require.NoError(t, os.WriteFile(contentPath, []byte(content), 0600))
	args := passwordCLIArgs("agent", "msg", "--from", "alice", "--to", "bob", "--content-file", contentPath, "--relay", "ws://127.0.0.1:1")
	assert.NotContains(t, strings.Join(args, " "), content)
	args = append(args, "--password-stdin")
	stdout, stderr, status := runPasswordStdinCLIProcess(t, home, args, password+"\n")
	require.Equal(t, 0, status, stderr)
	assert.Empty(t, stderr)
	var result struct {
		OK   bool           `json:"ok"`
		Data agentMsgResult `json:"data"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &result))
	require.True(t, result.OK)
	assert.True(t, result.Data.QueuedForRetry)

	ob, err := LoadOutbox()
	require.NoError(t, err)
	require.Len(t, ob.Entries, 1)
	var event nostr.Event
	require.NoError(t, json.Unmarshal([]byte(ob.Entries[0].EventJSON), &event))
	plain, encrypted, err := DecodeMessageContent(&event, bobSK)
	require.NoError(t, err)
	assert.True(t, encrypted)
	assert.Equal(t, content, plain)
}

func TestAgentMsgContentSourceValidationBeforeUnlockOrWrites(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	ks := &types.KeyStore{Identities: make(map[string]*types.Identity), Contacts: make(map[string]*types.Contact)}
	_, err := identity.CreateIdentityWithPassword(ks, "alice", "secret")
	require.NoError(t, err)
	_, err = identity.CreateIdentityWithPassword(ks, "bob", "secret")
	require.NoError(t, err)
	ks.MasterKey = nil
	require.NoError(t, identity.SaveKeyStore(ks))
	beforeState := hyphaeStateSnapshot(t, home)

	contentPath := filepath.Join(t.TempDir(), "body")
	require.NoError(t, os.WriteFile(contentPath, []byte("body"), 0600))
	invalidDir := t.TempDir()
	invalidUTF8Path := filepath.Join(invalidDir, "invalid-utf8")
	require.NoError(t, os.WriteFile(invalidUTF8Path, []byte{0xff, 0xfe}, 0600))
	oversizePath := filepath.Join(invalidDir, "oversize")
	require.NoError(t, os.WriteFile(oversizePath, bytes.Repeat([]byte{'x'}, maxMessageContentFileBytes+1), 0600))
	directoryPath := filepath.Join(invalidDir, "directory")
	require.NoError(t, os.Mkdir(directoryPath, 0700))
	for _, tc := range []struct {
		name string
		args []string
		path string
	}{
		{name: "neither"},
		{name: "both", args: []string{"--content", "body", "--content-file", contentPath}},
		{name: "explicit empty content", args: []string{"--content", ""}},
		{name: "explicit empty content alias", args: []string{"-c", ""}},
		{name: "alias and file", args: []string{"-c", "body", "--content-file", contentPath}},
		{name: "empty file", args: []string{"--content-file", writeEmptyContentFixture(t)}},
		{name: "missing file", args: []string{"--content-file", filepath.Join(invalidDir, "missing")}, path: filepath.Join(invalidDir, "missing")},
		{name: "directory", args: []string{"--content-file", directoryPath}, path: directoryPath},
		{name: "oversize file", args: []string{"--content-file", oversizePath}, path: oversizePath},
		{name: "invalid UTF-8 file", args: []string{"--content-file", invalidUTF8Path}, path: invalidUTF8Path},
		{name: "dash is not stdin", args: []string{"--content-file", "-"}, path: "-"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append(passwordCLIArgs("agent", "msg", "--from", "alice", "--to", "bob", "--relay", "ws://127.0.0.1:1"), tc.args...)
			stdout, stderr, status := runPasswordStdinCLIProcess(t, home, args, "")
			assert.Equal(t, common.ExitUserError, status)
			assert.Empty(t, stdout)
			assert.NotContains(t, stderr, "body")
			assert.NotContains(t, stderr, "secret")
			if tc.path != "" {
				assert.NotContains(t, stderr, tc.path, "file errors must not reveal paths")
			}
			assert.Equal(t, beforeState, hyphaeStateSnapshot(t, home), "invalid content must fail before any state write")
		})
	}
}

func writeEmptyContentFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "empty")
	require.NoError(t, os.WriteFile(path, nil, 0600))
	return path
}

func hyphaeStateSnapshot(t *testing.T, home string) map[string]string {
	t.Helper()
	root := filepath.Join(home, ".hyphae")
	snapshot := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if errors.Is(err, os.ErrNotExist) && path == root {
			return nil
		}
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(data)
		snapshot[rel] = hex.EncodeToString(digest[:])
		return nil
	})
	require.NoError(t, err)
	return snapshot
}

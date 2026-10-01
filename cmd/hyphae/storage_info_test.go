package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type storageInfoCLIResult struct {
	stdout string
	stderr string
	code   int
}

type storageInfoData struct {
	Path         string   `json:"path"`
	Exists       bool     `json:"exists"`
	SizeBytes    int64    `json:"size_bytes"`
	Mode         string   `json:"mode"`
	MessageCount int64    `json:"message_count"`
	Tables       []string `json:"tables"`
}

func runStorageInfoCLI(t *testing.T, home string, env []string, args ...string) storageInfoCLIResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, hyphaeBinary, args...)
	cmd.Env = append(withoutOutputModeEnv(os.Environ()), "HOME="+home)
	cmd.Env = append(cmd.Env, env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	result := storageInfoCLIResult{stdout: stdout.String(), stderr: stderr.String(), code: exitCode(err)}
	if ctx.Err() != nil {
		t.Fatalf("storage info timed out: %v\nstdout: %s\nstderr: %s", ctx.Err(), result.stdout, result.stderr)
	}
	if err != nil {
		if _, ok := err.(*exec.ExitError); !ok {
			t.Fatalf("run storage info CLI: %v", err)
		}
	}
	return result
}

func decodeStorageInfoSuccess(t *testing.T, stdout string) storageInfoData {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(stdout))
	var result struct {
		OK   bool            `json:"ok"`
		Data storageInfoData `json:"data"`
	}
	require.NoError(t, dec.Decode(&result))
	require.True(t, result.OK)
	require.ErrorIs(t, dec.Decode(new(any)), io.EOF, "stdout must contain one JSON envelope")
	return result.Data
}

func assertStorageInfoError(t *testing.T, got storageInfoCLIResult) {
	t.Helper()
	assert.Equal(t, 4, got.code)
	assert.Empty(t, got.stdout, "errors must not write a success response")
	dec := json.NewDecoder(strings.NewReader(got.stderr))
	var result common.Result
	require.NoError(t, dec.Decode(&result))
	assert.False(t, result.OK)
	assert.Equal(t, common.ErrCodeOther, result.Error)
	assert.NotEmpty(t, result.Message)
	require.ErrorIs(t, dec.Decode(new(any)), io.EOF, "stderr must contain one error envelope")
}

func TestStorageInfoCLIMissingDatabaseJSONModesDoNotCreateHome(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		env  []string
	}{
		{name: "flag", args: []string{"storage", "info", "--json"}},
		{name: "current environment", args: []string{"storage", "info"}, env: []string{"HYPHAE_OUTPUT=json"}},
		{name: "legacy environment", args: []string{"storage", "info"}, env: []string{"AGENT_SPEAKER_OUTPUT=json"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := filepath.Join(t.TempDir(), "not-created-home")
			got := runStorageInfoCLI(t, home, tc.env, tc.args...)
			require.Zero(t, got.code, got.stderr)
			assert.Empty(t, got.stderr)
			data := decodeStorageInfoSuccess(t, got.stdout)
			assert.Equal(t, filepath.Join(home, ".hyphae", "messages.db"), data.Path)
			assert.False(t, data.Exists)
			assert.Zero(t, data.SizeBytes)
			assert.Empty(t, data.Mode)
			assert.Zero(t, data.MessageCount)
			assert.NotNil(t, data.Tables)
			assert.Empty(t, data.Tables)
			assert.NoDirExists(t, home)
		})
	}
}

func TestStorageInfoCLIEmptyPopulatedAndHumanOutput(t *testing.T) {
	t.Run("empty database via legacy environment and special path", func(t *testing.T) {
		home := filepath.Join(t.TempDir(), "home with ?hash#percent%")
		createStorageInfoDatabase(t, home, 0)
		got := runStorageInfoCLI(t, home, []string{"AGENT_SPEAKER_OUTPUT=json"}, "storage", "info")
		require.Zero(t, got.code, got.stderr)
		assert.Empty(t, got.stderr)
		data := decodeStorageInfoSuccess(t, got.stdout)
		assert.Equal(t, filepath.Join(home, ".hyphae", "messages.db"), data.Path)
		assert.True(t, data.Exists)
		assert.Positive(t, data.SizeBytes)
		assert.NotEmpty(t, data.Mode)
		assert.Zero(t, data.MessageCount)
		assert.True(t, sort.StringsAreSorted(data.Tables))
		assert.Contains(t, data.Tables, "messages")
	})

	t.Run("populated database via flag and human regression", func(t *testing.T) {
		home := filepath.Join(t.TempDir(), "populated home")
		createStorageInfoDatabase(t, home, 2)
		got := runStorageInfoCLI(t, home, nil, "storage", "info", "--json")
		require.Zero(t, got.code, got.stderr)
		assert.Empty(t, got.stderr)
		data := decodeStorageInfoSuccess(t, got.stdout)
		assert.True(t, data.Exists)
		assert.EqualValues(t, 2, data.MessageCount)
		assert.Contains(t, data.Tables, "messages")

		human := runStorageInfoCLI(t, home, nil, "storage", "info")
		require.Zero(t, human.code, human.stderr)
		assert.Contains(t, human.stdout, "💾 Storage Information")
		assert.Contains(t, human.stdout, "Database: "+filepath.Join(home, ".hyphae", "messages.db"))
		assert.Contains(t, human.stdout, "Size:     ")
		assert.Contains(t, human.stdout, "Mode:     ")
		assert.Contains(t, human.stdout, "Messages: 2")
		assert.Contains(t, human.stdout, "📊 Tables:")
		assert.Contains(t, human.stdout, "   - messages")
	})
}

func TestStorageInfoCLIRejectsNondirectoryHome(t *testing.T) {
	homeFile := filepath.Join(t.TempDir(), "home-file")
	before := []byte("home file stays intact")
	require.NoError(t, os.WriteFile(homeFile, before, 0600))
	got := runStorageInfoCLI(t, homeFile, nil, "storage", "info", "--json")
	assertStorageInfoError(t, got)
	after, err := os.ReadFile(homeFile)
	require.NoError(t, err)
	assert.Equal(t, before, after)
}

func TestStorageInfoCLIRejectsCorruptUnsupportedAndNonregularDatabase(t *testing.T) {
	t.Run("corrupt file", func(t *testing.T) {
		home := t.TempDir()
		dbPath := filepath.Join(makeStorageInfoDir(t, home), "messages.db")
		before := []byte("not a sqlite database")
		require.NoError(t, os.WriteFile(dbPath, before, 0600))
		got := runStorageInfoCLI(t, home, nil, "storage", "info", "--json")
		assertStorageInfoError(t, got)
		after, err := os.ReadFile(dbPath)
		require.NoError(t, err)
		assert.Equal(t, before, after)
	})

	t.Run("unsupported schema is not migrated", func(t *testing.T) {
		home := t.TempDir()
		dbPath := filepath.Join(makeStorageInfoDir(t, home), "messages.db")
		db, err := sql.Open("sqlite", dbPath)
		require.NoError(t, err)
		_, err = db.Exec("CREATE TABLE unrelated (value TEXT)")
		require.NoError(t, err)
		require.NoError(t, db.Close())
		got := runStorageInfoCLI(t, home, nil, "storage", "info", "--json")
		assertStorageInfoError(t, got)
		check, err := sql.Open("sqlite", dbPath)
		require.NoError(t, err)
		defer check.Close()
		var messages int
		require.NoError(t, check.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='messages'").Scan(&messages))
		assert.Zero(t, messages)
	})

	t.Run("directory at database path", func(t *testing.T) {
		home := t.TempDir()
		dbPath := filepath.Join(makeStorageInfoDir(t, home), "messages.db")
		require.NoError(t, os.Mkdir(dbPath, 0700))
		got := runStorageInfoCLI(t, home, nil, "storage", "info", "--json")
		assertStorageInfoError(t, got)
		entries, err := os.ReadDir(dbPath)
		require.NoError(t, err)
		assert.Empty(t, entries)
	})
}

func createStorageInfoDatabase(t *testing.T, home string, messages int) {
	t.Helper()
	t.Setenv("HOME", home)
	db, err := storage.InitDB()
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = db.Close()
		storage.DB = nil
	})
	for i := 0; i < messages; i++ {
		_, err = db.Exec(`INSERT INTO messages (id, sender_npub, recipient_npub, created_at, received_at) VALUES (?, ?, ?, ?, ?)`,
			fmt.Sprintf("id-%d", i), "sender", "recipient", i+1, i+1)
		require.NoError(t, err)
	}
	require.NoError(t, db.Close())
	storage.DB = nil
}

func makeStorageInfoDir(t *testing.T, home string) string {
	t.Helper()
	dir := filepath.Join(home, ".hyphae")
	require.NoError(t, os.MkdirAll(dir, 0700))
	return dir
}

package identity

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/iDoris-ai/hyphae/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type passwordCheckFixture struct {
	Encrypted    bool   `json:"encrypted"`
	Salt         string `json:"salt,omitempty"`
	Verification string `json:"verification,omitempty"`
}

type homeTreeEntry struct {
	Path    string
	Mode    os.FileMode
	ModTime int64
	Size    int64
	SHA256  string
}

func writePasswordCheckFixture(t *testing.T, home, password string, legacy bool) {
	t.Helper()
	directory := filepath.Join(home, KeyStoreDirName)
	require.NoError(t, os.MkdirAll(directory, 0700))
	var salt, verification string
	var err error
	if legacy {
		salt, verification, err = createLegacyVerification(password)
	} else {
		salt, verification, err = createVerification(password)
	}
	require.NoError(t, err)
	data, err := json.Marshal(&types.KeyStore{
		Identities:   map[string]*types.Identity{},
		Contacts:     map[string]*types.Contact{},
		Encrypted:    true,
		Salt:         salt,
		Verification: verification,
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(directory, KeyStoreFile), data, 0600))
	require.NoError(t, os.WriteFile(filepath.Join(directory, "messages.db"), []byte("fixture-history"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(directory, "outbox.json"), []byte("fixture-outbox"), 0600))
	nested := filepath.Join(directory, "unrelated")
	require.NoError(t, os.Mkdir(nested, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(nested, "keep.txt"), []byte("unchanged"), 0600))
}

func snapshotHomeTree(t *testing.T, home string) []homeTreeEntry {
	t.Helper()
	var entries []homeTreeEntry
	err := filepath.WalkDir(home, func(path string, _ os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(home, path)
		if err != nil {
			return err
		}
		entry := homeTreeEntry{Path: rel, Mode: info.Mode(), ModTime: info.ModTime().UnixNano(), Size: info.Size()}
		if info.Mode().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			hash := sha256.Sum256(data)
			entry.SHA256 = hex.EncodeToString(hash[:])
		}
		entries = append(entries, entry)
		return nil
	})
	require.NoError(t, err)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return entries
}

func assertCheckPasswordSuccess(t *testing.T, result cliResult, secret string) {
	t.Helper()
	require.Zero(t, result.code, "stdout: %s\nstderr: %s", result.stdout, result.stderr)
	assert.Empty(t, result.stderr)
	assert.NotContains(t, result.stdout, secret)
	assert.NotContains(t, result.stdout, "nsec")
	assert.NotContains(t, result.stdout, "identity")
	var envelope struct {
		OK   bool `json:"ok"`
		Data struct {
			Valid     bool `json:"valid"`
			Encrypted bool `json:"encrypted"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal([]byte(result.stdout), &envelope))
	assert.True(t, envelope.OK)
	assert.True(t, envelope.Data.Valid)
	assert.True(t, envelope.Data.Encrypted)
	assert.Equal(t, "{\"ok\":true,\"data\":{\"encrypted\":true,\"valid\":true}}\n", result.stdout)
}

func assertCheckPasswordError(t *testing.T, result cliResult, code int, errorCode, secret string) {
	t.Helper()
	require.Equal(t, code, result.code, "stdout: %s\nstderr: %s", result.stdout, result.stderr)
	assert.Empty(t, result.stdout)
	assert.NotContains(t, result.stderr, secret)
	var envelope struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	require.NoError(t, json.Unmarshal([]byte(result.stderr), &envelope))
	assert.False(t, envelope.OK)
	assert.Equal(t, errorCode, envelope.Error)
}

func TestCheckPasswordCLIReadOnlySuccessAndOutputModes(t *testing.T) {
	const password = " padded password  "
	home := t.TempDir()
	writePasswordCheckFixture(t, home, password, false)
	before := snapshotHomeTree(t, home)

	checks := []struct {
		name string
		args []string
		env  []string
		in   string
	}{
		{name: "json flag after command", args: []string{"identity", "check-password", "--password-stdin", "--json"}, in: password + "\r\n"},
		{name: "json flag before command", args: []string{"--json", "identity", "check-password", "--password-stdin"}, in: password + "\n"},
		{name: "current json environment", args: []string{"identity", "check-password", "--password-stdin"}, env: []string{"HYPHAE_OUTPUT=json"}, in: password + "\n"},
		{name: "legacy json environment", args: []string{"identity", "check-password", "--password-stdin"}, env: []string{"AGENT_SPEAKER_OUTPUT=json"}, in: password + "\n"},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			result := runIdentityCLI(t, home, strings.NewReader(check.in), check.env, check.args...)
			assertCheckPasswordSuccess(t, result, password)
		})
	}
	human := runIdentityCLI(t, home, strings.NewReader(password+"\n"), nil, "identity", "check-password", "--password-stdin")
	require.Zero(t, human.code, "stdout: %s\nstderr: %s", human.stdout, human.stderr)
	assert.Equal(t, passwordCheckSuccessMessage+"\n", human.stdout)
	assert.NotContains(t, human.stdout, password)
	assert.Empty(t, human.stderr)
	require.Equal(t, before, snapshotHomeTree(t, home), "password checks must leave all HOME files and metadata unchanged")
}

func TestCheckPasswordCLIRemovesOnlyOneTrailingLF(t *testing.T) {
	password := "password containing a newline\n"
	home := t.TempDir()
	writePasswordCheckFixture(t, home, password, false)
	result := runIdentityCLI(t, home, strings.NewReader(password+"\n"), nil,
		"identity", "check-password", "--password-stdin", "--json")
	assertCheckPasswordSuccess(t, result, password)
}

func TestCheckPasswordCLIAcceptsLegacyVerifierWithoutMigrating(t *testing.T) {
	const password = "legacy padded "
	home := t.TempDir()
	writePasswordCheckFixture(t, home, password, true)
	before := snapshotHomeTree(t, home)
	result := runIdentityCLI(t, home, strings.NewReader(password+"\n"), nil,
		"identity", "check-password", "--password-stdin", "--json")
	assertCheckPasswordSuccess(t, result, password)
	require.Equal(t, before, snapshotHomeTree(t, home), "legacy-token verification must not migrate or write files")
}

func TestCheckPasswordCLIAcceptsExactRawInputLimit(t *testing.T) {
	checks := []struct {
		name     string
		password string
		input    string
	}{
		{name: "4096 password bytes", password: strings.Repeat("x", maxPasswordStdinBytes), input: strings.Repeat("x", maxPasswordStdinBytes)},
		{name: "line ending is byte 4096", password: strings.Repeat("x", maxPasswordStdinBytes-1), input: strings.Repeat("x", maxPasswordStdinBytes-1) + "\n"},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			home := t.TempDir()
			writePasswordCheckFixture(t, home, check.password, false)
			before := snapshotHomeTree(t, home)
			result := runIdentityCLI(t, home, strings.NewReader(check.input), nil,
				"identity", "check-password", "--password-stdin", "--json")
			assertCheckPasswordSuccess(t, result, check.password)
			require.Equal(t, before, snapshotHomeTree(t, home))
		})
	}
}

func TestCheckPasswordCLIFailureCategoriesAndNoWrites(t *testing.T) {
	const password = "secret-for-failure-tests"
	validHome := t.TempDir()
	writePasswordCheckFixture(t, validHome, password, false)
	validBefore := snapshotHomeTree(t, validHome)

	cases := []struct {
		name      string
		home      string
		stdin     io.Reader
		args      []string
		wantExit  int
		wantError string
	}{
		{name: "wrong password", home: validHome, stdin: strings.NewReader("wrong secret\n"), args: []string{"identity", "check-password", "--password-stdin", "--json"}, wantExit: 3, wantError: "auth_error"},
		{name: "empty input", home: validHome, stdin: strings.NewReader("\r\n"), args: []string{"identity", "check-password", "--password-stdin", "--json"}, wantExit: 3, wantError: "auth_error"},
		{name: "oversized input", home: validHome, stdin: strings.NewReader(strings.Repeat("x", maxPasswordStdinBytes+1)), args: []string{"identity", "check-password", "--password-stdin", "--json"}, wantExit: 3, wantError: "auth_error"},
	}
	for _, check := range cases {
		t.Run(check.name, func(t *testing.T) {
			result := runIdentityCLI(t, check.home, check.stdin, nil, check.args...)
			assertCheckPasswordError(t, result, check.wantExit, check.wantError, password)
			if check.name == "wrong password" {
				assert.NotContains(t, result.stderr, "wrong secret")
			}
			require.Equal(t, validBefore, snapshotHomeTree(t, validHome))
		})
	}

	missingFlag := runIdentityCLIWithoutStdinRead(t, validHome, "identity", "check-password", "--json")
	assertCheckPasswordError(t, missingFlag, 1, "user_error", password)
	require.Equal(t, validBefore, snapshotHomeTree(t, validHome))
}

func TestCheckPasswordCLIRejectsMissingPlainAndMalformedFiles(t *testing.T) {
	const password = "file-categories-password"
	root := t.TempDir()

	t.Run("missing keystore and no directory creation", func(t *testing.T) {
		home := filepath.Join(root, "absent-home")
		result := runIdentityCLI(t, home, strings.NewReader(password+"\n"), nil,
			"identity", "check-password", "--password-stdin", "--json")
		assertCheckPasswordError(t, result, 3, "auth_error", password)
		_, err := os.Stat(home)
		assert.ErrorIs(t, err, os.ErrNotExist)
	})

	t.Run("plain keystore", func(t *testing.T) {
		home := t.TempDir()
		directory := filepath.Join(home, KeyStoreDirName)
		require.NoError(t, os.Mkdir(directory, 0700))
		plainData, err := json.Marshal(&types.KeyStore{Identities: map[string]*types.Identity{}, Contacts: map[string]*types.Contact{}})
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(directory, KeyStoreFile), plainData, 0600))
		before := snapshotHomeTree(t, home)
		result := runIdentityCLI(t, home, strings.NewReader(password+"\n"), nil,
			"identity", "check-password", "--password-stdin", "--json")
		assertCheckPasswordError(t, result, 3, "auth_error", password)
		require.Equal(t, before, snapshotHomeTree(t, home))
	})

	for _, tc := range []struct {
		name string
		data []byte
	}{
		{name: "malformed JSON", data: []byte(`{"encrypted":`)},
		{name: "null root", data: []byte(`null`)},
		{name: "array root", data: []byte(`[]`)},
		{name: "scalar root", data: []byte(`7`)},
		{name: "missing encrypted field", data: []byte(`{}`)},
		{name: "wrong typed encrypted field", data: []byte(`{"encrypted":"false"}`)},
		{name: "invalid salt base64", data: mustJSONFixture(t, passwordCheckFixture{Encrypted: true, Salt: "%%%", Verification: "%%%"})},
		{name: "short salt", data: mustJSONFixture(t, passwordCheckFixture{Encrypted: true, Salt: base64.StdEncoding.EncodeToString(make([]byte, 15)), Verification: "%%%"})},
		{name: "invalid token base64", data: mustJSONFixture(t, passwordCheckFixture{Encrypted: true, Salt: base64.StdEncoding.EncodeToString(make([]byte, passwordCheckSaltBytes)), Verification: "%%%"})},
		{name: "short token", data: mustJSONFixture(t, passwordCheckFixture{Encrypted: true, Salt: base64.StdEncoding.EncodeToString(make([]byte, passwordCheckSaltBytes)), Verification: base64.StdEncoding.EncodeToString(make([]byte, passwordCheckMinTokenBytes-1))})},
		{name: "missing verifier", data: mustJSONFixture(t, map[string]any{"encrypted": true, "salt": base64.StdEncoding.EncodeToString(make([]byte, passwordCheckSaltBytes))})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			directory := filepath.Join(home, KeyStoreDirName)
			require.NoError(t, os.Mkdir(directory, 0700))
			require.NoError(t, os.WriteFile(filepath.Join(directory, KeyStoreFile), tc.data, 0600))
			before := snapshotHomeTree(t, home)
			result := runIdentityCLI(t, home, strings.NewReader(password+"\n"), nil,
				"identity", "check-password", "--password-stdin", "--json")
			assertCheckPasswordError(t, result, 4, "other_error", password)
			require.Equal(t, before, snapshotHomeTree(t, home))
		})
	}
}

func TestCheckPasswordCLIRequiresHomeAndDoesNotUseWorkingDirectory(t *testing.T) {
	const password = "home-is-required"
	work := t.TempDir()
	writePasswordCheckFixture(t, work, password, false)
	before := snapshotHomeTree(t, work)
	result := runIdentityCLIWithoutHome(t, work, strings.NewReader(password+"\n"),
		"identity", "check-password", "--password-stdin", "--json")
	assertCheckPasswordError(t, result, 4, "other_error", password)
	require.Equal(t, before, snapshotHomeTree(t, work), "unset HOME must not make the command read or modify cwd/.hyphae")
}

func mustJSONFixture(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	return data
}

func runIdentityCLIWithoutHome(t *testing.T, work string, stdin io.Reader, args ...string) cliResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, cliBinary, args...)
	cmd.Dir = work
	env := cleanCLIEnv(os.Environ(), "", nil)
	filtered := env[:0]
	for _, item := range env {
		if !strings.HasPrefix(item, "HOME=") {
			filtered = append(filtered, item)
		}
	}
	cmd.Env = filtered
	cmd.Stdin = stdin
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	result := cliResult{stdout: stdout.String(), stderr: stderr.String()}
	if err == nil {
		return result
	}
	if ctx.Err() != nil {
		t.Fatalf("CLI timed out: %v\nstdout: %s\nstderr: %s", ctx.Err(), result.stdout, result.stderr)
	}
	if exit, ok := err.(*exec.ExitError); ok {
		result.code = exit.ExitCode()
		return result
	}
	t.Fatalf("run CLI: %v", err)
	return result
}

func runIdentityCLIWithoutStdinRead(t *testing.T, home string, args ...string) cliResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, cliBinary, args...)
	cmd.Env = cleanCLIEnv(os.Environ(), home, nil)
	stdin, err := cmd.StdinPipe()
	require.NoError(t, err)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	require.NoError(t, cmd.Start())
	err = cmd.Wait()
	_ = stdin.Close()
	result := cliResult{stdout: stdout.String(), stderr: stderr.String()}
	if ctx.Err() != nil {
		t.Fatalf("CLI blocked reading stdin although --password-stdin was missing")
	}
	if err == nil {
		return result
	}
	if exit, ok := err.(*exec.ExitError); ok {
		result.code = exit.ExitCode()
		return result
	}
	t.Fatalf("run CLI: %v", err)
	return result
}

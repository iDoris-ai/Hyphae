package identity

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fiatjaf.com/nostr"
	"github.com/iDoris-ai/hyphae/internal/common"
)

func TestContactAddInvalidPublicKeysAreUserErrorsBeforeStorage(t *testing.T) {
	privateNsec := common.EncodeNsec(nostr.Generate())
	invalidKeys := []struct {
		name  string
		value string
	}{
		{name: "malformed npub", value: "npub1invalid-checksum"},
		{name: "wrong length hex", value: strings.Repeat("a", 62)},
		{name: "nonhex", value: strings.Repeat("g", 64)},
		{name: "private nsec", value: privateNsec},
	}
	jsonModes := []struct {
		name string
		env  []string
		flag bool
	}{
		{name: "flag", flag: true},
		{name: "current env", env: []string{"HYPHAE_OUTPUT=json"}},
		{name: "legacy env", env: []string{"AGENT_SPEAKER_OUTPUT=json"}},
	}

	for _, mode := range jsonModes {
		for _, tc := range invalidKeys {
			t.Run(mode.name+"/"+tc.name, func(t *testing.T) {
				home := t.TempDir()
				args := []string{"contact", "add", "--nickname", "bad", "--npub", tc.value}
				if mode.flag {
					args = append([]string{"--json"}, args...)
				}
				result := runIdentityCLI(t, home, nil, mode.env, args...)
				if result.code != common.ExitUserError || strings.TrimSpace(result.stdout) != "" {
					t.Fatalf("invalid key should fail with empty stdout and exit 1: %#v", result)
				}
				lines := strings.Split(strings.TrimSpace(result.stderr), "\n")
				if len(lines) != 1 {
					t.Fatalf("expected one JSON error envelope, got: %q", result.stderr)
				}
				var response common.Result
				if err := json.Unmarshal([]byte(lines[0]), &response); err != nil {
					t.Fatalf("invalid JSON error envelope: %v: %s", err, result.stderr)
				}
				if response.OK || response.Error != common.ErrCodeUser || response.Message != "--npub must be a valid public key" {
					t.Fatalf("unexpected error response: %#v", response)
				}
				if strings.Contains(result.stderr, tc.value) {
					t.Fatalf("error echoed invalid key material: %s", result.stderr)
				}
				if _, err := os.Stat(filepath.Join(home, KeyStoreDirName)); !os.IsNotExist(err) {
					t.Fatalf("invalid key touched the HOME keystore path: %v", err)
				}
			})
		}
	}
}

func TestContactAddInvalidPublicKeyLeavesExistingKeystoreUntouched(t *testing.T) {
	home := t.TempDir()
	created := runIdentityCLI(t, home, nil, nil, "identity", "create", "--nickname", "alice", "--json")
	if created.code != 0 {
		t.Fatalf("create fixture identity: %#v", created)
	}
	path := filepath.Join(home, KeyStoreDirName, KeyStoreFile)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	result := runIdentityCLI(t, home, nil, nil, "--json", "contact", "add", "--nickname", "bad", "--npub", "nsec1not-a-public-key")
	if result.code != common.ExitUserError || strings.TrimSpace(result.stdout) != "" || !strings.Contains(result.stderr, `"error":"user_error"`) {
		t.Fatalf("invalid key did not produce a clean user_error: %#v", result)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Fatal("invalid public key changed the existing keystore")
	}
}

func TestContactAddValidNpubAndHexAreCanonicalized(t *testing.T) {
	home := t.TempDir()
	created := runIdentityCLI(t, home, nil, nil, "identity", "create", "--nickname", "alice", "--json")
	var identityResult struct {
		Data struct {
			Npub string `json:"npub"`
		} `json:"data"`
	}
	if created.code != 0 || json.Unmarshal([]byte(created.stdout), &identityResult) != nil || identityResult.Data.Npub == "" {
		t.Fatalf("create fixture identity: %#v", created)
	}
	pubkey, err := common.ParsePublicKey(identityResult.Data.Npub)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		nickname string
		input    string
	}{
		{nickname: "from-npub", input: identityResult.Data.Npub},
		{nickname: "from-hex", input: pubkey.Hex()},
	} {
		result := runIdentityCLI(t, home, nil, nil, "--json", "contact", "add", "--nickname", tc.nickname, "--npub", tc.input)
		var response struct {
			OK   bool `json:"ok"`
			Data struct {
				Nickname string `json:"nickname"`
				Npub     string `json:"npub"`
			} `json:"data"`
		}
		if result.code != 0 || strings.TrimSpace(result.stderr) != "" || json.Unmarshal([]byte(result.stdout), &response) != nil || !response.OK {
			t.Fatalf("valid public key rejected: %#v", result)
		}
		if response.Data.Nickname != tc.nickname || response.Data.Npub != identityResult.Data.Npub {
			t.Fatalf("key was not canonicalized to npub: %#v", response)
		}
	}
	var stored struct {
		Contacts map[string]struct {
			Npub string `json:"npub"`
		} `json:"contacts"`
	}
	data, err := os.ReadFile(filepath.Join(home, KeyStoreDirName, KeyStoreFile))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatal(err)
	}
	if len(stored.Contacts) != 2 || stored.Contacts["from-npub"].Npub != identityResult.Data.Npub || stored.Contacts["from-hex"].Npub != identityResult.Data.Npub {
		t.Fatalf("stored contacts were not canonicalized: %#v", stored.Contacts)
	}
}

func TestContactAddHumanInvalidPublicKeyIsUserError(t *testing.T) {
	home := t.TempDir()
	invalid := strings.Repeat("z", 64)
	result := runIdentityCLI(t, home, nil, nil, "contact", "add", "--nickname", "bad", "--npub", invalid)
	if result.code != common.ExitUserError || strings.TrimSpace(result.stdout) != "" || !strings.Contains(result.stderr, "Error: --npub must be a valid public key") || strings.Contains(result.stderr, invalid) {
		t.Fatalf("human invalid-key error changed or echoed input: %#v", result)
	}
	if _, err := os.Stat(filepath.Join(home, KeyStoreDirName)); !os.IsNotExist(err) {
		t.Fatalf("human invalid-key path touched HOME: %v", err)
	}
}

func TestContactAddValidKeyStorageFailureRemainsOtherError(t *testing.T) {
	home := t.TempDir()
	key := nostr.Generate().Public()
	validNpub := common.EncodeNpub(key)
	dir := filepath.Join(home, KeyStoreDirName)
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, KeyStoreFile), 0700); err != nil {
		t.Fatal(err)
	}
	result := runIdentityCLI(t, home, nil, nil, "--json", "contact", "add", "--nickname", "valid", "--npub", validNpub)
	if result.code != common.ExitOtherError || strings.TrimSpace(result.stdout) != "" {
		t.Fatalf("storage failure should remain other_error with empty stdout: %#v", result)
	}
	var response common.Result
	if err := json.Unmarshal([]byte(result.stderr), &response); err != nil {
		t.Fatalf("invalid JSON error envelope: %v: %s", err, result.stderr)
	}
	if response.OK || response.Error != common.ErrCodeOther || !strings.Contains(response.Message, "failed to load keystore") {
		t.Fatalf("valid key storage error was incorrectly classified: %#v", response)
	}
}

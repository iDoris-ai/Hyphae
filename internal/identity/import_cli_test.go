package identity

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/nip49"
	"github.com/btcsuite/btcd/btcutil/bech32"
	"github.com/iDoris-ai/hyphae/internal/common"
)

// fakeNcryptsec builds a well-formed bech32 "ncryptsec" with an arbitrary payload.
func fakeNcryptsec(t *testing.T, payload []byte) string {
	t.Helper()
	bits, err := bech32.ConvertBits(payload, 8, 5, true)
	if err != nil {
		t.Fatal(err)
	}
	s, err := bech32.EncodeM("ncryptsec", bits)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// NIP-49 appendix vector: passphrase "nostr" opens this ncryptsec.
const (
	nip49VectorNcryptsec = "ncryptsec1qgg9947rlpvqu76pj5ecreduf9jxhselq2nae2kghhvd5g7dgjtcxfqtd67p9m0w57lspw8gsq6yphnm8623nsl8xn9j4jdzz84zm3frztj3z7s35vpzmqf6ksu8r89qk5z2zxfmu5gv8th8wclt0h4p"
	nip49VectorHex       = "3501454135014541350145413501453fefb02227e449e57cf4d3a3ce05378683"
)

// logn 1 is enough for the CLI tests; scrypt cost is not part of the import contract.
func mustNcryptsec(t *testing.T, sk nostr.SecretKey, passphrase string) string {
	t.Helper()
	encoded, err := nip49.Encrypt(sk, passphrase, 1, nip49.ClientDoesNotTrackThisData)
	if err != nil {
		t.Fatalf("nip49.Encrypt: %v", err)
	}
	return encoded
}

func importData(t *testing.T, result cliResult) map[string]any {
	t.Helper()
	data, ok := decodeSuccess(t, result)["data"].(map[string]any)
	if !ok {
		t.Fatalf("import data is not an object: %s", result.stdout)
	}
	return data
}

func assertExactImport(t *testing.T, data map[string]any, nickname, npub string) {
	t.Helper()
	if len(data) != 2 || data["nickname"] != nickname || data["npub"] != npub {
		t.Fatalf("import data = %#v, want only nickname=%s npub=%s", data, nickname, npub)
	}
}

func listIdentities(t *testing.T, home string) []any {
	t.Helper()
	raw := decodeSuccess(t, runIdentityCLI(t, home, nil, nil, "identity", "list", "--json"))["data"]
	entries, ok := raw.([]any)
	if !ok {
		t.Fatalf("identity list data = %#v", raw)
	}
	return entries
}

func assertSingleIdentity(t *testing.T, home, nickname, npub string) {
	t.Helper()
	entries := listIdentities(t, home)
	if len(entries) != 1 {
		t.Fatalf("identity list has %d entries: %#v", len(entries), entries)
	}
	entry := entries[0].(map[string]any)
	if entry["nickname"] != nickname || entry["npub"] != npub {
		t.Fatalf("remaining identity = %#v, want %s %s", entry, nickname, npub)
	}
}

func assertNoKeystore(t *testing.T, home string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(home, KeyStoreDirName)); !os.IsNotExist(err) {
		t.Fatalf("rejection wrote %s: %v", KeyStoreDirName, err)
	}
}

func decodeCLIError(t *testing.T, result cliResult, exit int, code string) {
	t.Helper()
	if result.code != exit || strings.TrimSpace(result.stdout) != "" {
		t.Fatalf("exit=%d want %d\nstdout: %s\nstderr: %s", result.code, exit, result.stdout, result.stderr)
	}
	var response common.Result
	if err := json.Unmarshal([]byte(result.stderr), &response); err != nil {
		t.Fatalf("stderr is not a JSON error: %v\n%s", err, result.stderr)
	}
	if response.OK || response.Error != code || response.Message == "" {
		t.Fatalf("error envelope = %#v, want error %q", response, code)
	}
}

func assertHidden(t *testing.T, result cliResult, secrets ...string) {
	t.Helper()
	for _, secret := range secrets {
		if secret != "" && (strings.Contains(result.stdout, secret) || strings.Contains(result.stderr, secret)) {
			t.Fatalf("CLI output leaked secret material\nstdout: %s\nstderr: %s", result.stdout, result.stderr)
		}
	}
}

func TestImportNIP49Vector(t *testing.T) {
	home := t.TempDir()
	sk := nostr.MustSecretKeyFromHex(nip49VectorHex)
	npub := common.EncodeNpub(sk.Public())
	result := runIdentityCLI(t, home, strings.NewReader("nostr\n"+nip49VectorNcryptsec+"\n"), nil,
		"identity", "import", "--nickname", "alice", "--ncryptsec-stdin", "--json")
	assertExactImport(t, importData(t, result), "alice", npub)
	assertHidden(t, result, nip49VectorNcryptsec, "nostr", nip49VectorHex)
	assertSingleIdentity(t, home, "alice", npub)
}

func TestImportNsecStdinRoundTrip(t *testing.T) {
	home := t.TempDir()
	sk := nostr.Generate()
	nsec := common.EncodeNsec(sk)
	npub := common.EncodeNpub(sk.Public())
	result := runIdentityCLI(t, home, strings.NewReader(nsec+"\n"), nil,
		"identity", "import", "--nickname", "alice", "--nsec-stdin", "--json")
	assertExactImport(t, importData(t, result), "alice", npub)
	assertHidden(t, result, nsec)
	assertSingleIdentity(t, home, "alice", npub)
}

func TestImportCLIRejections(t *testing.T) {
	sk := nostr.Generate()
	nsec := common.EncodeNsec(sk)
	passphrase := "backup-secret"
	ncrypt := mustNcryptsec(t, sk, passphrase)
	wrongPass := "not-the-passphrase"

	tests := []struct {
		name    string
		stdin   string
		args    []string
		exit    int
		code    string
		hidden  []string
		noStore bool
	}{
		{
			name:    "wrong passphrase",
			stdin:   wrongPass + "\n" + nip49VectorNcryptsec + "\n",
			args:    []string{"identity", "import", "--nickname", "alice", "--ncryptsec-stdin", "--json"},
			exit:    common.ExitAuthError,
			code:    common.ErrCodeBadBackupPassword,
			hidden:  []string{nip49VectorNcryptsec, wrongPass},
			noStore: true,
		},
		{
			name:    "garbage nsec",
			stdin:   "hello\n",
			args:    []string{"identity", "import", "--nickname", "alice", "--nsec-stdin", "--json"},
			exit:    common.ExitUserError,
			code:    common.ErrCodeInvalidKey,
			hidden:  []string{"hello"},
			noStore: true,
		},
		{
			name:    "garbage ncryptsec",
			stdin:   "backup-secret\nnsec1xxx\n",
			args:    []string{"identity", "import", "--nickname", "alice", "--ncryptsec-stdin", "--json"},
			exit:    common.ExitUserError,
			code:    common.ErrCodeInvalidKey,
			hidden:  []string{"nsec1xxx", "backup-secret"},
			noStore: true,
		},
		{
			name:    "truncated ncryptsec payload (would panic nip49)",
			stdin:   "pw\n" + fakeNcryptsec(t, []byte{0x02, 0x10, 1, 2, 3}) + "\n",
			args:    []string{"identity", "import", "--nickname", "alice", "--ncryptsec-stdin", "--json"},
			exit:    common.ExitUserError,
			code:    common.ErrCodeInvalidKey,
			noStore: true,
		},
		{
			name:    "ncryptsec logn too large (scrypt memory bomb)",
			stdin:   "pw\n" + fakeNcryptsec(t, append([]byte{0x02, 0x40}, make([]byte, 89)...)) + "\n",
			args:    []string{"identity", "import", "--nickname", "alice", "--ncryptsec-stdin", "--json"},
			exit:    common.ExitUserError,
			code:    common.ErrCodeInvalidKey,
			noStore: true,
		},
		{
			name:    "whitespace padding hides a second value past the stdin cap",
			stdin:   nsec + "\n" + strings.Repeat(" ", maxImportStdinBytes) + "\nextra\n",
			args:    []string{"identity", "import", "--nickname", "alice", "--nsec-stdin", "--json"},
			exit:    common.ExitUserError,
			code:    common.ErrCodeUser,
			hidden:  []string{nsec},
			noStore: true,
		},
		{
			name:    "both key flags",
			stdin:   nsec + "\n",
			args:    []string{"identity", "import", "--nickname", "alice", "--ncryptsec-stdin", "--nsec-stdin", "--json"},
			exit:    common.ExitUserError,
			code:    common.ErrCodeUser,
			hidden:  []string{nsec},
			noStore: true,
		},
		{
			name:    "neither key flag",
			stdin:   nsec + "\n",
			args:    []string{"identity", "import", "--nickname", "alice", "--json"},
			exit:    common.ExitUserError,
			code:    common.ErrCodeUser,
			hidden:  []string{nsec},
			noStore: true,
		},
		{
			name:    "extra nsec line",
			stdin:   nsec + "\ntrailing\n",
			args:    []string{"identity", "import", "--nickname", "alice", "--nsec-stdin", "--json"},
			exit:    common.ExitUserError,
			code:    common.ErrCodeUser,
			hidden:  []string{nsec},
			noStore: true,
		},
		{
			name:    "extra ncryptsec line",
			stdin:   passphrase + "\n" + ncrypt + "\ntrailing\n",
			args:    []string{"identity", "import", "--nickname", "alice", "--ncryptsec-stdin", "--json"},
			exit:    common.ExitUserError,
			code:    common.ErrCodeUser,
			hidden:  []string{ncrypt, passphrase},
			noStore: true,
		},
		{
			name:    "nickname newline",
			stdin:   nsec + "\n",
			args:    []string{"identity", "import", "--nickname", "bad\nname", "--nsec-stdin", "--json"},
			exit:    common.ExitUserError,
			code:    common.ErrCodeUser,
			hidden:  []string{nsec},
			noStore: true,
		},
		{
			name:    "nickname tab",
			stdin:   nsec + "\n",
			args:    []string{"identity", "import", "--nickname", "bad\tname", "--nsec-stdin", "--json"},
			exit:    common.ExitUserError,
			code:    common.ErrCodeUser,
			hidden:  []string{nsec},
			noStore: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			result := runIdentityCLI(t, home, strings.NewReader(tc.stdin), nil, tc.args...)
			decodeCLIError(t, result, tc.exit, tc.code)
			assertHidden(t, result, tc.hidden...)
			if tc.noStore {
				assertNoKeystore(t, home)
			}
		})
	}
}

func TestImportDuplicateNpubAndNickname(t *testing.T) {
	home := t.TempDir()
	sk := nostr.Generate()
	nsec := common.EncodeNsec(sk)
	npub := common.EncodeNpub(sk.Public())
	other := common.EncodeNsec(nostr.Generate())

	first := runIdentityCLI(t, home, strings.NewReader(nsec+"\n"), nil,
		"identity", "import", "--nickname", "alice", "--nsec-stdin", "--json")
	assertExactImport(t, importData(t, first), "alice", npub)

	dupNpub := runIdentityCLI(t, home, strings.NewReader(nsec+"\n"), nil,
		"identity", "import", "--nickname", "bob", "--nsec-stdin", "--json")
	decodeCLIError(t, dupNpub, common.ExitUserError, common.ErrCodeNpubExists)
	assertHidden(t, dupNpub, nsec)
	assertSingleIdentity(t, home, "alice", npub)

	dupName := runIdentityCLI(t, home, strings.NewReader(other+"\n"), nil,
		"identity", "import", "--nickname", "alice", "--nsec-stdin", "--json")
	decodeCLIError(t, dupName, common.ExitUserError, common.ErrCodeNicknameExists)
	assertHidden(t, dupName, other)
	assertSingleIdentity(t, home, "alice", npub)
}

func TestImportEncryptedKeystore(t *testing.T) {
	home := t.TempDir()
	created := runIdentityCLI(t, home, strings.NewReader("pw\n"), nil,
		"identity", "create", "--nickname", "base", "--password-stdin", "--json")
	base := decodeSuccess(t, created)["data"].(map[string]any)
	if base["nickname"] != "base" || base["encrypted"] != true {
		t.Fatalf("create base: %#v", base)
	}
	baseNpub, _ := base["npub"].(string)

	sk := nostr.Generate()
	nsec := common.EncodeNsec(sk)
	npub := common.EncodeNpub(sk.Public())
	passphrase := "backup-secret"
	ncrypt := mustNcryptsec(t, sk, passphrase)
	importArgs := []string{"identity", "import", "--nickname", "alice", "--ncryptsec-stdin", "--json"}

	locked := runIdentityCLI(t, home, strings.NewReader(passphrase+"\n"+ncrypt+"\n"), nil, importArgs...)
	decodeCLIError(t, locked, common.ExitAuthError, common.ErrCodeKeystoreLocked)
	assertHidden(t, locked, ncrypt, passphrase, nsec)

	wrong := runIdentityCLI(t, home, strings.NewReader("wrong-keystore-secret\n"+passphrase+"\n"+ncrypt+"\n"), nil,
		"identity", "import", "--nickname", "alice", "--ncryptsec-stdin", "--password-stdin", "--json")
	decodeCLIError(t, wrong, common.ExitAuthError, common.ErrCodeKeystoreLocked)
	assertHidden(t, wrong, ncrypt, passphrase, nsec, "wrong-keystore-secret")
	assertSingleIdentity(t, home, "base", baseNpub)

	ok := runIdentityCLI(t, home, strings.NewReader("pw\n"+passphrase+"\n"+ncrypt+"\n"), nil,
		"identity", "import", "--nickname", "alice", "--ncryptsec-stdin", "--password-stdin", "--json")
	assertExactImport(t, importData(t, ok), "alice", npub)
	assertHidden(t, ok, nsec, ncrypt, passphrase)

	disk, err := os.ReadFile(filepath.Join(home, KeyStoreDirName, KeyStoreFile))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(disk), nsec) {
		t.Fatal("keystore.json contains the imported plaintext nsec")
	}
	var store struct {
		Identities map[string]struct {
			Nsec string `json:"nsec"`
		} `json:"identities"`
	}
	if err := json.Unmarshal(disk, &store); err != nil {
		t.Fatal(err)
	}
	for name, id := range store.Identities {
		if strings.HasPrefix(id.Nsec, "nsec1") {
			t.Fatalf("identity %s stored a plaintext nsec", name)
		}
	}

	entries := listIdentities(t, home)
	if len(entries) != 2 {
		t.Fatalf("identity list has %d entries: %#v", len(entries), entries)
	}
	for _, item := range entries {
		entry := item.(map[string]any)
		if entry["encrypted"] != true {
			t.Fatalf("identity not stored in an encrypted keystore: %#v", entry)
		}
	}
	if identityDefault(entries, "alice") || !identityDefault(entries, "base") {
		t.Fatalf("encrypted import changed the default: %#v", entries)
	}
}

func TestImportDefaultFlag(t *testing.T) {
	home := t.TempDir()
	decodeSuccess(t, runIdentityCLI(t, home, nil, nil, "identity", "create", "--nickname", "base", "--json"))

	sk := nostr.Generate()
	nsec := common.EncodeNsec(sk)
	npub := common.EncodeNpub(sk.Public())
	result := runIdentityCLI(t, home, strings.NewReader(nsec+"\n"), nil,
		"identity", "import", "--nickname", "alice", "--nsec-stdin", "--default", "--json")
	assertExactImport(t, importData(t, result), "alice", npub)
	assertHidden(t, result, nsec)

	entries := listIdentities(t, home)
	if len(entries) != 2 || !identityDefault(entries, "alice") || identityDefault(entries, "base") {
		t.Fatalf("default flag did not stick: %#v", entries)
	}
}

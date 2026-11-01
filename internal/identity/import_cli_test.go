package identity

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

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
	// Imports into an unencrypted keystore legitimately print the plaintext
	// warning to stderr; any other stderr output is still a failure.
	result.stderr = strings.Replace(result.stderr, plaintextKeystoreWarning+"\n", "", 1)
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
			name:    "ncryptsec logn 20 is above the 512 MiB cap",
			stdin:   "pw\n" + fakeNcryptsec(t, append([]byte{0x02, 20}, make([]byte, 89)...)) + "\n",
			args:    []string{"identity", "import", "--nickname", "alice", "--ncryptsec-stdin", "--json"},
			exit:    common.ExitUserError,
			code:    common.ErrCodeInvalidKey,
			noStore: true,
		},
		{
			name:    "ncryptsec logn 0 (scrypt N=1) is invalid, not a bad passphrase",
			stdin:   "pw\n" + fakeNcryptsec(t, append([]byte{0x02, 0}, make([]byte, 89)...)) + "\n",
			args:    []string{"identity", "import", "--nickname", "alice", "--ncryptsec-stdin", "--json"},
			exit:    common.ExitUserError,
			code:    common.ErrCodeInvalidKey,
			noStore: true,
		},
		{
			name:    "all-zero secret key",
			stdin:   common.EncodeNsec(nostr.SecretKey{}) + "\n",
			args:    []string{"identity", "import", "--nickname", "alice", "--nsec-stdin", "--default", "--json"},
			exit:    common.ExitUserError,
			code:    common.ErrCodeInvalidKey,
			noStore: true,
		},
		{
			name:    "secret key equal to the curve order n",
			stdin:   common.EncodeNsec(secretKeyFromHex(t, "FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFEBAAEDCE6AF48A03BBFD25E8CD0364141")) + "\n",
			args:    []string{"identity", "import", "--nickname", "alice", "--nsec-stdin", "--json"},
			exit:    common.ExitUserError,
			code:    common.ErrCodeInvalidKey,
			noStore: true,
		},
		{
			name:    "secret key n+1 (would wrap to 1)",
			stdin:   common.EncodeNsec(secretKeyFromHex(t, "FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFEBAAEDCE6AF48A03BBFD25E8CD0364142")) + "\n",
			args:    []string{"identity", "import", "--nickname", "alice", "--nsec-stdin", "--json"},
			exit:    common.ExitUserError,
			code:    common.ErrCodeInvalidKey,
			noStore: true,
		},
		{
			name:    "all-ff secret key",
			stdin:   common.EncodeNsec(secretKeyFromHex(t, "FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFF")) + "\n",
			args:    []string{"identity", "import", "--nickname", "alice", "--nsec-stdin", "--json"},
			exit:    common.ExitUserError,
			code:    common.ErrCodeInvalidKey,
			noStore: true,
		},
		{
			name:    "all-zero key inside an otherwise valid ncryptsec",
			stdin:   "backup-secret\n" + mustNcryptsec(t, nostr.SecretKey{}, "backup-secret") + "\n",
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
			name:    "carriage return inside a line",
			stdin:   "ab\rcd\n",
			args:    []string{"identity", "import", "--nickname", "alice", "--nsec-stdin", "--json"},
			exit:    common.ExitUserError,
			code:    common.ErrCodeUser,
			hidden:  []string{"ab", "cd"},
			noStore: true,
		},
		{
			name:    "two carriage returns before the newline",
			stdin:   nsec + "\r\r\n",
			args:    []string{"identity", "import", "--nickname", "alice", "--nsec-stdin", "--json"},
			exit:    common.ExitUserError,
			code:    common.ErrCodeUser,
			hidden:  []string{nsec},
			noStore: true,
		},
		{
			name:    "lone carriage return at EOF without newline",
			stdin:   nsec + "\r",
			args:    []string{"identity", "import", "--nickname", "alice", "--nsec-stdin", "--json"},
			exit:    common.ExitUserError,
			code:    common.ErrCodeUser,
			hidden:  []string{nsec},
			noStore: true,
		},
		{
			name:    "carriage return inside the backup passphrase",
			stdin:   "pass\rword\n" + ncrypt + "\n",
			args:    []string{"identity", "import", "--nickname", "alice", "--ncryptsec-stdin", "--json"},
			exit:    common.ExitUserError,
			code:    common.ErrCodeUser,
			hidden:  []string{ncrypt, "pass", "word"},
			noStore: true,
		},
		{
			name:    "CRLF-only line is an empty value",
			stdin:   "\r\n",
			args:    []string{"identity", "import", "--nickname", "alice", "--nsec-stdin", "--json"},
			exit:    common.ExitUserError,
			code:    common.ErrCodeUser,
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

// Concurrent imports serialise on the keystore lock: different keys must all
// land (no lost update), and the same key under different nicknames must be
// stored exactly once (no duplicate-check TOCTOU).
func TestImportConcurrentProcesses(t *testing.T) {
	const n = 5
	run := func(home string, stdins, nicks []string) []cliResult {
		results := make([]cliResult, n)
		var wg sync.WaitGroup
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				results[i] = runIdentityCLI(t, home, strings.NewReader(stdins[i]+"\n"), nil,
					"identity", "import", "--nickname", nicks[i], "--nsec-stdin", "--json")
			}(i)
		}
		wg.Wait()
		return results
	}

	t.Run("different keys all stored", func(t *testing.T) {
		home := t.TempDir()
		var stdins, nicks []string
		for i := 0; i < n; i++ {
			stdins = append(stdins, common.EncodeNsec(nostr.Generate()))
			nicks = append(nicks, "id"+string(rune('a'+i)))
		}
		for _, r := range run(home, stdins, nicks) {
			importData(t, r)
		}
		if got := len(listIdentities(t, home)); got != n {
			t.Fatalf("stored %d identities, want %d (lost update)", got, n)
		}
	})

	t.Run("same key stored once", func(t *testing.T) {
		home := t.TempDir()
		nsec := common.EncodeNsec(nostr.Generate())
		var stdins, nicks []string
		for i := 0; i < n; i++ {
			stdins = append(stdins, nsec)
			nicks = append(nicks, "dup"+string(rune('a'+i)))
		}
		ok := 0
		for _, r := range run(home, stdins, nicks) {
			if r.code == 0 {
				ok++
			} else {
				decodeCLIError(t, r, common.ExitUserError, common.ErrCodeNpubExists)
			}
		}
		if ok != 1 || len(listIdentities(t, home)) != 1 {
			t.Fatalf("successes=%d stored=%d, want exactly 1", ok, len(listIdentities(t, home)))
		}
	})
}

// --default is committed together with the identity: a rejected import must not
// move the default, and an accepted one must set it in the same keystore write.
func TestImportDefaultIsAtomicWithImport(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	ks, err := LoadKeyStore()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CreateIdentity(ks, "base"); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportIdentityWithPassword(ks, "base", "", nostr.Generate(), true); err == nil {
		t.Fatal("duplicate nickname import succeeded")
	}
	if ks, _ = LoadKeyStore(); ks.DefaultIdentity != "base" {
		t.Fatalf("rejected import moved default to %q", ks.DefaultIdentity)
	}
	if _, err := ImportIdentityWithPassword(ks, "alice", "", nostr.Generate(), true); err != nil {
		t.Fatal(err)
	}
	if fresh, _ := LoadKeyStore(); fresh.DefaultIdentity != "alice" {
		t.Fatalf("default = %q, want alice", fresh.DefaultIdentity)
	}
}

// A single CRLF or LF terminator, or none at EOF, is accepted.
func TestImportAcceptsSingleLineTerminators(t *testing.T) {
	for name, term := range map[string]string{"LF": "\n", "CRLF": "\r\n", "none": ""} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			sk := nostr.Generate()
			result := runIdentityCLI(t, home, strings.NewReader(common.EncodeNsec(sk)+term), nil,
				"identity", "import", "--nickname", "alice", "--nsec-stdin", "--json")
			assertExactImport(t, importData(t, result), "alice", common.EncodeNpub(sk.Public()))
		})
	}
}

func secretKeyFromHex(t *testing.T, h string) nostr.SecretKey {
	t.Helper()
	b, err := hex.DecodeString(h)
	if err != nil || len(b) != 32 {
		t.Fatalf("bad hex key %q", h)
	}
	var sk nostr.SecretKey
	copy(sk[:], b)
	return sk
}

func TestImportDuplicateViaNcryptsec(t *testing.T) {
	home := t.TempDir()
	sk := nostr.Generate()
	npub := common.EncodeNpub(sk.Public())
	ncrypt := mustNcryptsec(t, sk, "backup-secret")
	otherNcrypt := mustNcryptsec(t, nostr.Generate(), "backup-secret")
	args := func(nick string) []string {
		return []string{"identity", "import", "--nickname", nick, "--ncryptsec-stdin", "--json"}
	}
	first := runIdentityCLI(t, home, strings.NewReader("backup-secret\n"+ncrypt+"\n"), nil, args("alice")...)
	assertExactImport(t, importData(t, first), "alice", npub)

	dupNpub := runIdentityCLI(t, home, strings.NewReader("backup-secret\n"+ncrypt+"\n"), nil, args("bob")...)
	decodeCLIError(t, dupNpub, common.ExitUserError, common.ErrCodeNpubExists)
	assertHidden(t, dupNpub, ncrypt, "backup-secret")

	dupName := runIdentityCLI(t, home, strings.NewReader("backup-secret\n"+otherNcrypt+"\n"), nil, args("alice")...)
	decodeCLIError(t, dupName, common.ExitUserError, common.ErrCodeNicknameExists)
	assertHidden(t, dupName, otherNcrypt, "backup-secret")
	assertSingleIdentity(t, home, "alice", npub)
}

// A wrong keystore password must fail before the (expensive) backup scrypt:
// with both passwords wrong the answer is keystore_locked, not bad_backup_password.
func TestImportChecksKeystorePasswordBeforeBackupPassphrase(t *testing.T) {
	home := t.TempDir()
	runIdentityCLI(t, home, strings.NewReader("pw\n"), nil,
		"identity", "create", "--nickname", "base", "--password-stdin", "--json")
	ncrypt := mustNcryptsec(t, nostr.Generate(), "backup-secret")
	res := runIdentityCLI(t, home, strings.NewReader("wrong-keystore\nwrong-backup\n"+ncrypt+"\n"), nil,
		"identity", "import", "--nickname", "alice", "--ncryptsec-stdin", "--password-stdin", "--json")
	decodeCLIError(t, res, common.ExitAuthError, common.ErrCodeKeystoreLocked)
	assertHidden(t, res, ncrypt, "wrong-keystore", "wrong-backup")
}

func countWarnings(stderr string) int { return strings.Count(stderr, "unencrypted") }

// F5: an import that leaves the nsec in plaintext on disk says so, once, on
// stderr only, for both import modes, and stdout stays a pure JSON envelope.
func TestImportPlaintextKeystoreWarning(t *testing.T) {
	sk1, sk2 := nostr.Generate(), nostr.Generate()
	cases := []struct {
		name  string
		stdin string
		flag  string
		sk    nostr.SecretKey
	}{
		{"nsec-stdin", common.EncodeNsec(sk1) + "\n", "--nsec-stdin", sk1},
		{"ncryptsec-stdin", "pp\n" + mustNcryptsec(t, sk2, "pp") + "\n", "--ncryptsec-stdin", sk2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			result := runIdentityCLI(t, home, strings.NewReader(tc.stdin), nil,
				"identity", "import", "--nickname", "alice", tc.flag, "--json")
			if result.code != 0 {
				t.Fatalf("exit=%d stderr=%s", result.code, result.stderr)
			}
			if result.stderr != plaintextKeystoreWarning+"\n" || countWarnings(result.stderr) != 1 {
				t.Fatalf("stderr = %q, want exactly the one-line warning", result.stderr)
			}
			if !strings.Contains(result.stderr, "identity change-password") {
				t.Fatalf("warning does not name the real command: %q", result.stderr)
			}
			if strings.Contains(result.stdout, "unencrypted") || strings.Contains(result.stdout, "warning") {
				t.Fatalf("warning leaked into stdout: %s", result.stdout)
			}
			var env map[string]any
			if err := json.Unmarshal([]byte(result.stdout), &env); err != nil || env["ok"] != true {
				t.Fatalf("stdout is not a pure JSON envelope: %v %s", err, result.stdout)
			}
			assertHidden(t, result, common.EncodeNsec(tc.sk))
		})
	}

	t.Run("second import into the same unencrypted keystore warns once per run", func(t *testing.T) {
		home := t.TempDir()
		for _, nick := range []string{"a", "b"} {
			r := runIdentityCLI(t, home, strings.NewReader(common.EncodeNsec(nostr.Generate())+"\n"), nil,
				"identity", "import", "--nickname", nick, "--nsec-stdin", "--json")
			if r.code != 0 || r.stderr != plaintextKeystoreWarning+"\n" {
				t.Fatalf("%s: exit=%d stderr=%q", nick, r.code, r.stderr)
			}
		}
	})

	t.Run("failed import prints no warning, only the error envelope", func(t *testing.T) {
		home := t.TempDir()
		r := runIdentityCLI(t, home, strings.NewReader("hello\n"), nil,
			"identity", "import", "--nickname", "alice", "--nsec-stdin", "--json")
		decodeCLIError(t, r, common.ExitUserError, common.ErrCodeInvalidKey)
		if countWarnings(r.stderr) != 0 {
			t.Fatalf("warning on a failed import: %q", r.stderr)
		}
	})
}

// An encrypted keystore (existing, or created by this very import through
// --password-stdin) must not get the plaintext warning.
func TestImportNoWarningForEncryptedKeystore(t *testing.T) {
	t.Run("existing encrypted keystore", func(t *testing.T) {
		home := t.TempDir()
		decodeSuccess(t, runIdentityCLI(t, home, strings.NewReader("pw\n"), nil,
			"identity", "create", "--nickname", "base", "--password-stdin", "--json"))
		r := runIdentityCLI(t, home, strings.NewReader("pw\n"+common.EncodeNsec(nostr.Generate())+"\n"), nil,
			"identity", "import", "--nickname", "alice", "--nsec-stdin", "--password-stdin", "--json")
		if r.code != 0 || r.stderr != "" {
			t.Fatalf("exit=%d stderr=%q, want clean success", r.code, r.stderr)
		}
	})
	t.Run("fresh keystore encrypted by the import itself", func(t *testing.T) {
		home := t.TempDir()
		r := runIdentityCLI(t, home, strings.NewReader("pw\n"+common.EncodeNsec(nostr.Generate())+"\n"), nil,
			"identity", "import", "--nickname", "alice", "--nsec-stdin", "--password-stdin", "--json")
		if r.code != 0 || r.stderr != "" {
			t.Fatalf("exit=%d stderr=%q, want clean success", r.code, r.stderr)
		}
	})
}

// F4: on a terminal nothing is read (no echo, no wait for Ctrl-D) and the user
// is told to pipe the values in. The terminal check is forced through the
// package variable instead of a real pty.
func TestImportRefusesTerminalStdin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pr.Close()
	defer pw.Close()
	nsec := common.EncodeNsec(nostr.Generate())
	if _, err := pw.WriteString(nsec + "\n"); err != nil {
		t.Fatal(err)
	}
	// Writer stays open: a read that is not guarded would block, not hit EOF.
	oldStdin, oldCheck := os.Stdin, importStdinIsTerminal
	os.Stdin, importStdinIsTerminal = pr, func() bool { return true }
	defer func() { os.Stdin, importStdinIsTerminal = oldStdin, oldCheck }()

	// Bounded so that a regression (a read that blocks on the open pipe) fails
	// instead of hanging the suite.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- importCommand().Run(ctx, []string{"import", "--nickname", "alice", "--nsec-stdin"})
	}()
	select {
	case err = <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("import blocked reading stdin on a terminal")
	}
	var exit *common.ExitError
	if !errors.As(err, &exit) || exit.Code != common.ErrCodeUser || !strings.Contains(exit.Error(), "pipe") {
		t.Fatalf("err = %v, want user_error telling the user to pipe", err)
	}
	if strings.Contains(exit.Error(), nsec) {
		t.Fatal("error leaked the secret")
	}
	assertNoKeystore(t, home)
	// Nothing was consumed from stdin: closing the writer makes the read end
	// non-blocking (EOF), and the whole original input must still be there.
	pw.Close()
	left, _ := io.ReadAll(pr)
	if string(left) != nsec+"\n" {
		t.Fatalf("stdin was read before the terminal check (%d of %d bytes left)", len(left), len(nsec)+1)
	}
}

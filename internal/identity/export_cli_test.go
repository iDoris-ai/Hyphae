package identity

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fiatjaf.com/nostr/nip49"
	"github.com/iDoris-ai/hyphae/internal/common"
)

func exportData(t *testing.T, r cliResult) map[string]any {
	t.Helper()
	return decodeSuccess(t, r)["data"].(map[string]any)
}

func createExportFixture(t *testing.T, password string) (home, npub string) {
	t.Helper()
	home = t.TempDir()
	args := []string{"identity", "create", "--nickname", "alice", "--json"}
	var stdin *strings.Reader
	if password != "" {
		args = append(args, "--password-stdin")
		stdin = strings.NewReader(password + "\n")
	} else {
		stdin = strings.NewReader("")
	}
	created := exportData(t, runIdentityCLI(t, home, stdin, nil, args...))
	return home, created["npub"].(string)
}

func TestExportJSONUnencrypted(t *testing.T) {
	home, npub := createExportFixture(t, "")
	data := exportData(t, runIdentityCLI(t, home, nil, nil, "identity", "export", "-n", "alice", "--json"))
	if data["nickname"] != "alice" || data["npub"] != npub || !strings.HasPrefix(data["nsec"].(string), "nsec1") {
		t.Fatalf("unexpected envelope: %#v", data)
	}
}

func TestExportEncryptedPasswordStdin(t *testing.T) {
	home, npub := createExportFixture(t, "pw")
	data := exportData(t, runIdentityCLI(t, home, strings.NewReader("pw\r\n"), nil, "identity", "export", "-n", "alice", "--json", "--password-stdin"))
	nsec, _ := data["nsec"].(string)
	if data["npub"] != npub || !strings.HasPrefix(nsec, "nsec1") {
		t.Fatalf("unexpected envelope: %#v", data)
	}

	wrong := runIdentityCLI(t, home, strings.NewReader("bad\n"), nil, "identity", "export", "-n", "alice", "--json", "--password-stdin")
	if wrong.code != 3 || strings.Contains(wrong.stdout+wrong.stderr, nsec) || !strings.Contains(wrong.stderr, `"error":"auth_error"`) {
		t.Fatalf("wrong password: %#v", wrong)
	}
	none := runIdentityCLI(t, home, nil, nil, "identity", "export", "-n", "alice", "--json")
	if none.code != 3 || strings.TrimSpace(none.stdout) != "" || !strings.Contains(none.stderr, "--password-stdin") {
		t.Fatalf("missing password: %#v", none)
	}
	empty := runIdentityCLI(t, home, strings.NewReader("\n"), nil, "identity", "export", "-n", "alice", "--json", "--password-stdin")
	if empty.code != 3 || strings.Contains(empty.stdout, "nsec1") {
		t.Fatalf("empty password: %#v", empty)
	}
}

func TestExportNcryptsecStdinLineOrder(t *testing.T) {
	for _, encrypted := range []bool{false, true} {
		pw, stdinPrefix, extra := "", "", []string{}
		if encrypted {
			pw, stdinPrefix, extra = "pw", "pw\n", []string{"--password-stdin"}
		}
		home, _ := createExportFixture(t, pw)
		plain := exportData(t, runIdentityCLI(t, home, strings.NewReader(stdinPrefix), nil, append([]string{"identity", "export", "-n", "alice", "--json"}, extra...)...))["nsec"].(string)

		args := append([]string{"identity", "export", "-n", "alice", "--json", "--format", "ncryptsec"}, extra...)
		res := runIdentityCLI(t, home, strings.NewReader(stdinPrefix+"backup pass\n"), nil, args...)
		data := exportData(t, res)
		enc, _ := data["ncryptsec"].(string)
		if _, has := data["nsec"]; has || !strings.HasPrefix(enc, "ncryptsec1") || strings.Contains(res.stdout+res.stderr, plain) {
			t.Fatalf("encrypted=%v unexpected output: %#v", encrypted, res)
		}
		sk, err := nip49.Decrypt(enc, "backup pass")
		if err != nil {
			t.Fatalf("decrypt backup: %v", err)
		}
		if common.EncodeNsec(sk) != plain {
			t.Fatal("decrypted backup does not match the stored nsec")
		}
		if _, err := nip49.Decrypt(enc, "wrong"); err == nil {
			t.Fatal("wrong passphrase decrypted the backup")
		}
		for _, in := range []string{stdinPrefix, stdinPrefix + "\n"} {
			bad := runIdentityCLI(t, home, strings.NewReader(in), nil, args...)
			if bad.code == 0 || strings.Contains(bad.stdout+bad.stderr, "ncryptsec1") {
				t.Fatalf("missing/empty passphrase accepted: %#v", bad)
			}
		}
	}
}

func TestExportOutFile(t *testing.T) {
	home, npub := createExportFixture(t, "")
	dir := t.TempDir()
	out := filepath.Join(dir, "key.txt")
	res := runIdentityCLI(t, home, nil, nil, "identity", "export", "-n", "alice", "--json", "--out", out)
	data := exportData(t, res)
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	secret := strings.TrimSuffix(string(got), "\n")
	if !strings.HasPrefix(secret, "nsec1") || data["npub"] != npub || strings.Contains(res.stdout+res.stderr, secret) {
		t.Fatalf("out file / stdout wrong: %q %#v", got, res)
	}
	if info, _ := os.Stat(out); info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", info.Mode().Perm())
	}

	again := runIdentityCLI(t, home, nil, nil, "identity", "export", "-n", "alice", "--json", "--out", out)
	after, _ := os.ReadFile(out)
	if again.code != 1 || string(after) != string(got) || strings.Contains(again.stdout+again.stderr, secret) {
		t.Fatalf("existing target was not refused: %#v", again)
	}

	victim := filepath.Join(dir, "victim.txt")
	if err := os.WriteFile(victim, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, target := range map[string]string{"live": victim, "dangling": filepath.Join(dir, "nowhere")} {
		link := filepath.Join(dir, name+".link")
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		if r := runIdentityCLI(t, home, nil, nil, "identity", "export", "-n", "alice", "--json", "--out", link); r.code == 0 {
			t.Fatalf("%s symlink accepted", name)
		}
	}
	if b, _ := os.ReadFile(victim); string(b) != "keep" {
		t.Fatal("symlink target was overwritten")
	}
	if _, err := os.Lstat(filepath.Join(dir, "nowhere")); err == nil {
		t.Fatal("dangling symlink target was created")
	}
}

func TestExportInvalidFormatAndDefaultOutput(t *testing.T) {
	home, npub := createExportFixture(t, "")
	bad := runIdentityCLI(t, home, nil, nil, "identity", "export", "-n", "alice", "--format", "hex")
	if bad.code == 0 || strings.Contains(bad.stdout, "nsec1") {
		t.Fatalf("invalid format accepted: %#v", bad)
	}
	def := runIdentityCLI(t, home, nil, nil, "identity", "export", "-n", "alice")
	for _, want := range []string{"WARNING", "Identity: alice", "Npub:     " + npub, "Nsec:     nsec1"} {
		if !strings.Contains(def.stdout, want) {
			t.Fatalf("default output lost %q: %s", want, def.stdout)
		}
	}
}

func TestExportPassphraseStripsOnlyOneCRLF(t *testing.T) {
	home, _ := createExportFixture(t, "")
	res := runIdentityCLI(t, home, strings.NewReader("abc\r\r\n"), nil, "identity", "export", "-n", "alice", "--json", "--format", "ncryptsec")
	enc := exportData(t, res)["ncryptsec"].(string)
	if _, err := nip49.Decrypt(enc, "abc\r"); err != nil {
		t.Fatalf("only the CRLF terminator may be stripped: %v", err)
	}
}

// NIP-49 specification test vector (password "nostr").
func TestNIP49SpecVector(t *testing.T) {
	const ncrypt = "ncryptsec1qgg9947rlpvqu76pj5ecreduf9jxhselq2nae2kghhvd5g7dgjtcxfqtd67p9m0w57lspw8gsq6yphnm8623nsl8xn9j4jdzz84zm3frztj3z7s35vpzmqf6ksu8r89qk5z2zxfmu5gv8th8wclt0h4p"
	sk, err := nip49.Decrypt(ncrypt, "nostr")
	if err != nil {
		t.Fatal(err)
	}
	if got := hex.EncodeToString(sk[:]); got != "3501454135014541350145413501453fefb02227e449e57cf4d3a3ce05378683" {
		t.Fatalf("spec vector decrypted to %s", got)
	}
}

func TestExportRejectsUnterminatedStdin(t *testing.T) {
	home, _ := createExportFixture(t, "")
	res := runIdentityCLI(t, home, strings.NewReader("truncated"), nil, "identity", "export", "-n", "alice", "--json", "--format", "ncryptsec")
	if res.code == 0 || strings.Contains(res.stdout, "ncryptsec1") {
		t.Fatalf("unterminated passphrase accepted: %#v", res)
	}
}

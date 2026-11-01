package identity

import (
	"bufio"
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/nip49"
	"github.com/btcsuite/btcd/btcutil/bech32"
	"github.com/iDoris-ai/hyphae/internal/audit"
	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/urfave/cli/v3"
)

const maxImportStdinBytes = 16 * 1024

// ValidateNickname rejects empty nicknames and any control character
// (newlines, tabs, ESC, DEL, ...), which would corrupt logs and terminal output.
func ValidateNickname(nickname string) error {
	if strings.TrimSpace(nickname) == "" {
		return errors.New("--nickname is required")
	}
	for _, r := range nickname {
		if unicode.IsControl(r) {
			return errors.New("nickname must not contain control characters")
		}
	}
	return nil
}

// readImportLines reads exactly n non-empty lines from r. Extra non-blank
// content is rejected so a mis-ordered stdin cannot be silently half-used.
func readImportLines(r io.Reader, n int) ([]string, error) {
	if r == nil {
		return nil, errors.New("stdin is unavailable")
	}
	br := bufio.NewReader(io.LimitReader(r, maxImportStdinBytes+1))
	lines := make([]string, 0, n)
	total := 0
	for len(lines) < n {
		line, err := br.ReadString('\n')
		total += len(line)
		if total > maxImportStdinBytes {
			return nil, errors.New("stdin input is too large")
		}
		if err != nil && err != io.EOF {
			return nil, errors.New("failed to read stdin")
		}
		// Only one terminator ("\r\n" or "\n") is removed; any other "\r" is
		// part of the value and is rejected below, so "ab\rcd", "ab\r\r\n" and
		// an unterminated "ab\r" cannot smuggle a carriage return into a secret.
		if strings.HasSuffix(line, "\r\n") {
			line = line[:len(line)-2]
		} else {
			line = strings.TrimSuffix(line, "\n")
		}
		if line == "" {
			if err == io.EOF {
				return nil, fmt.Errorf("stdin must contain %d lines (see 'hyphae identity import --help')", n)
			}
			return nil, errors.New("stdin contains an empty line where a value is required")
		}
		if strings.Contains(line, "\r") {
			return nil, errors.New("stdin value contains a carriage return; only a single trailing \\n or \\r\\n is allowed")
		}
		lines = append(lines, line)
	}
	rest, err := io.ReadAll(br)
	if err != nil || total+len(rest) > maxImportStdinBytes {
		return nil, errors.New("stdin input is too large or unreadable")
	}
	if len(strings.TrimSpace(string(rest))) != 0 {
		return nil, fmt.Errorf("stdin has more than the expected %d lines", n)
	}
	return lines, nil
}

// Wire layout of a NIP-49 payload: version(1) logn(1) salt(16) nonce(24)
// keySecurity(1) ciphertext(48) = 91 bytes. nip49.Decrypt slices these offsets
// without checking, and logn drives scrypt's memory cost, so validate the
// structure first: malformed input is invalid_key, never a panic or a
// multi-gigabyte scrypt run, and only a failed authentication is a bad passphrase.
const (
	ncryptsecPayloadLen = 91
	ncryptsecMinLogN    = 1  // logn 0 means scrypt N=1, which nip49.Decrypt rejects with a non-authentication error
	ncryptsecMaxLogN    = 19 // NIP-49 scrypt uses r=8: memory = 128*8*2^logn bytes, so 19 is ~512 MiB (22 would be 4 GiB)
)

func validateNcryptsecStructure(s string) error {
	hrp, data, err := bech32.DecodeNoLimit(s)
	if err != nil || hrp != "ncryptsec" {
		return errors.New("input is not a valid ncryptsec")
	}
	raw, err := bech32.ConvertBits(data, 5, 8, false)
	if err != nil || len(raw) != ncryptsecPayloadLen || raw[0] != 0x02 || raw[1] < ncryptsecMinLogN || raw[1] > ncryptsecMaxLogN {
		return errors.New("input is not a valid ncryptsec")
	}
	return nil
}

// secp256k1 group order n. A private key is valid only if 1 <= k < n; zero and
// values >= n have no usable public key (signatures and NIP-44 fail later).
// Compared as raw big-endian bytes so no second copy of the key is made.
var secp256k1Order = func() (n [32]byte) {
	b, _ := hex.DecodeString("FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFEBAAEDCE6AF48A03BBFD25E8CD0364141")
	copy(n[:], b)
	return n
}()

func secretKeyInRange(sk nostr.SecretKey) bool {
	return sk != (nostr.SecretKey{}) && bytes.Compare(sk[:], secp256k1Order[:]) < 0
}

func importCommand() *cli.Command {
	return &cli.Command{
		Name:  "import",
		Usage: "Import an existing identity from a NIP-49 ncryptsec or an nsec (read from stdin)",
		Description: `Secrets are read from stdin only, never from arguments or environment.

stdin carries one value per line, in this fixed order (a line is present only
when its flag is given):
  1. keystore password   (only with --password-stdin)
  2. backup passphrase   (only with --ncryptsec-stdin)
  3. the key itself      (ncryptsec1... with --ncryptsec-stdin, nsec1... with --nsec-stdin)

Examples:
  printf '%s\n%s\n' "$PASSPHRASE" "$NCRYPTSEC" | hyphae identity import -n alice --ncryptsec-stdin --json
  printf '%s\n%s\n%s\n' "$KS_PASS" "$PASSPHRASE" "$NCRYPTSEC" | hyphae identity import -n alice --ncryptsec-stdin --password-stdin --json

JSON errors use stable codes: bad_backup_password, invalid_key, nickname_exists,
npub_exists, keystore_locked. Importing a key already present fails (npub_exists).
An empty backup passphrase is rejected, so a backup made with an empty passphrase
cannot be imported. Secret keys outside 1 <= k < n are rejected (invalid_key).`,
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "nickname", Aliases: []string{"n"}, Usage: "Nickname for the imported identity"},
			&cli.BoolFlag{Name: "ncryptsec-stdin", Usage: "Read backup passphrase then NIP-49 ncryptsec from stdin"},
			&cli.BoolFlag{Name: "nsec-stdin", Usage: "Read a plaintext nsec from stdin"},
			&cli.BoolFlag{Name: "default", Aliases: []string{"d"}, Usage: "Set as default identity"},
			&cli.BoolFlag{Name: "password-stdin", Usage: "Read the keystore password from the first stdin line"},
		},
		Action: runImport,
	}
}

func runImport(ctx context.Context, c *cli.Command) error {
	jsonMode := common.JSONMode(c)
	userErr := func(code string, msg string) error { return common.NewExitError(code, errors.New(msg)) }

	useNcrypt, useNsec, passStdin := c.Bool("ncryptsec-stdin"), c.Bool("nsec-stdin"), c.Bool("password-stdin")
	if useNcrypt == useNsec {
		return userErr(common.ErrCodeUser, "choose exactly one of --ncryptsec-stdin or --nsec-stdin")
	}
	nickname := c.String("nickname")
	if err := ValidateNickname(nickname); err != nil {
		return common.NewExitError(common.ErrCodeUser, err)
	}

	want := 1
	if passStdin {
		want++
	}
	if useNcrypt {
		want++
	}
	lines, err := readImportLines(os.Stdin, want)
	if err != nil {
		return common.NewExitError(common.ErrCodeUser, err)
	}
	var password, backupPass string
	if passStdin {
		password, lines = lines[0], lines[1:]
	}
	if useNcrypt {
		backupPass, lines = lines[0], lines[1:]
	}
	keyLine := lines[0]

	// Order matters: cheap validation first, then the keystore password (so a
	// wrong keystore password fails before the expensive backup scrypt), then
	// the decryption, then the key range check.
	var sk nostr.SecretKey
	if useNcrypt {
		if err := validateNcryptsecStructure(keyLine); err != nil {
			return common.NewExitError(common.ErrCodeInvalidKey, err)
		}
	} else {
		if !strings.HasPrefix(keyLine, "nsec1") {
			return userErr(common.ErrCodeInvalidKey, "input is not a valid nsec")
		}
		sk, err = common.ParseSecretKey(keyLine)
		if err != nil {
			return userErr(common.ErrCodeInvalidKey, "input is not a valid nsec")
		}
	}

	ks, err := LoadKeyStore()
	if err != nil {
		return fmt.Errorf("failed to load keystore: %w", err)
	}
	if ks.Encrypted {
		if password == "" {
			if jsonMode {
				return userErr(common.ErrCodeKeystoreLocked, "encrypted keystore requires --password-stdin in JSON mode")
			}
			password, err = PromptPassword("Keystore password: ")
			if err != nil {
				return userErr(common.ErrCodeKeystoreLocked, "failed to read keystore password")
			}
		}
		if err := UnlockKeyStore(ks, password); err != nil {
			return userErr(common.ErrCodeKeystoreLocked, "failed to unlock keystore")
		}
	}
	if useNcrypt {
		sk, err = nip49.Decrypt(keyLine, backupPass)
		if err != nil {
			// NIP-49 gives no way to tell a wrong passphrase from a corrupted
			// string; the AEAD tag failing is by far the common case.
			return userErr(common.ErrCodeBadBackupPassword, "failed to decrypt ncryptsec: wrong backup passphrase or corrupted backup")
		}
	}
	if !secretKeyInRange(sk) {
		return userErr(common.ErrCodeInvalidKey, "secret key is out of range (must be 1 <= k < n)")
	}
	identity, err := ImportIdentityWithPassword(ks, nickname, password, sk, c.Bool("default"))
	if err != nil {
		return err
	}
	if err := audit.LogAction(nickname, audit.ActionIdentityCreated, map[string]any{"npub": identity.Npub, "imported": true}); err != nil {
		fmt.Fprintf(os.Stderr, "warning: audit log failed: %v\n", err)
	}

	type importOutput struct {
		Nickname string `json:"nickname"`
		Npub     string `json:"npub"`
	}
	common.Emit(jsonMode, importOutput{Nickname: nickname, Npub: identity.Npub}, func() {
		fmt.Printf("Imported identity '%s'\n   Npub: %s\n", nickname, identity.Npub)
	})
	return nil
}

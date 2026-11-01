package identity

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"fiatjaf.com/nostr/nip49"
	"github.com/fatih/color"
	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/pkg/types"
	"github.com/urfave/cli/v3"
	"golang.org/x/term"
)

const (
	exportFormatNsec      = "nsec"
	exportFormatNcryptsec = "ncryptsec"
	// ncryptsecLogN is the NIP-49 scrypt cost (N = 2^16, the spec's example).
	ncryptsecLogN = 16
)

const exportDescription = `Stdin protocol (secrets are never accepted on argv). Lines are read in this order:
  1. keystore password   - only with --password-stdin AND only if the keystore is encrypted
  2. backup passphrase   - only with --format ncryptsec (always from stdin, never prompted)
Each line MUST be terminated by LF or CRLF (an unterminated last line is rejected); only the terminator is stripped.
If stdin is a terminal these secrets are prompted without echo instead.
--format nsec (default) prints the plaintext nsec; --format ncryptsec prints a NIP-49 backup.
With --out <file> the secret is written to a NEW file (mode 0600, refuses an existing path or
symlink) and is not printed; stdout/JSON then only carries nickname, npub, format and out.
--json emits {"nickname","npub","nsec"} (or "ncryptsec" instead of "nsec" for that format).`

func exportFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{Name: "nickname", Aliases: []string{"n"}, Usage: "Nickname to export"},
		&cli.BoolFlag{Name: "password-stdin", Usage: "Read the keystore password from the first stdin line (encrypted keystore only)"},
		&cli.StringFlag{Name: "format", Value: exportFormatNsec, Usage: "nsec or ncryptsec (NIP-49; backup passphrase read from stdin)"},
		&cli.StringFlag{Name: "out", Usage: "Write the secret to a new 0600 file instead of stdout"},
	}
}

// readStdinLine reads one line (LF or CRLF terminated) without trimming other whitespace.
func readStdinLine(r *bufio.Reader, what string) (string, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		// EOF without a terminator is rejected: an interrupted producer must not
		// silently yield a truncated password or backup passphrase.
		return "", fmt.Errorf("failed to read %s from stdin (a LF/CRLF-terminated line is required)", what)
	}
	line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
	if line == "" {
		return "", fmt.Errorf("%s from stdin is empty", what)
	}
	if len(line) > maxPasswordStdinBytes {
		return "", fmt.Errorf("%s from stdin exceeds %d bytes", what, maxPasswordStdinBytes)
	}
	return line, nil
}

// readSecretLine prompts without echo when stdin is a terminal, otherwise reads a stdin line.
func readSecretLine(r *bufio.Reader, what string) (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return readStdinLine(r, what)
	}
	fmt.Fprintf(os.Stderr, "%s: ", what)
	b, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil || len(b) == 0 || len(b) > maxPasswordStdinBytes {
		return "", fmt.Errorf("failed to read %s", what)
	}
	return string(b), nil
}

func unlockForExport(ks *types.KeyStore, jsonMode, passwordStdin bool, in *bufio.Reader) error {
	if !ks.Encrypted || ks.MasterKey != nil {
		return nil
	}
	var pw string
	var err error
	switch {
	case passwordStdin:
		if pw, err = readSecretLine(in, "keystore password"); err != nil {
			return common.NewExitError(common.ErrCodeAuth, err)
		}
	case jsonMode:
		return common.NewExitError(common.ErrCodeAuth, errors.New("encrypted keystore requires --password-stdin in JSON mode"))
	default:
		if pw, err = PromptPassword("Keystore password: "); err != nil {
			return common.NewExitError(common.ErrCodeAuth, errors.New("failed to read keystore password"))
		}
	}
	if err := UnlockKeyStore(ks, pw); err != nil {
		return common.NewExitError(common.ErrCodeAuth, errors.New("failed to unlock keystore"))
	}
	return nil
}

// writeNewSecretFile creates path exclusively with mode 0600 and never follows a symlink.
func writeNewSecretFile(path, secret string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|exportNoFollow, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return common.NewExitError(common.ErrCodeUser, fmt.Errorf("--out target already exists: %s", path))
		}
		var pe *os.PathError
		if errors.As(err, &pe) {
			err = pe.Err
		}
		return common.NewExitError(common.ErrCodeUser, fmt.Errorf("cannot create --out file: %v", err))
	}
	_, werr := f.WriteString(secret + "\n")
	cerr := f.Close()
	if werr != nil || cerr != nil {
		if rerr := os.Remove(path); rerr != nil {
			return common.NewExitError(common.ErrCodeOther, fmt.Errorf("failed to write --out file and could not remove the partial file %s: remove it manually", path))
		}
		return common.NewExitError(common.ErrCodeOther, errors.New("failed to write --out file"))
	}
	return nil
}

func runExport(c *cli.Command) error {
	jsonMode := common.JSONMode(c)
	format := c.String("format")
	if format != exportFormatNsec && format != exportFormatNcryptsec {
		return common.NewExitError(common.ErrCodeUser, fmt.Errorf("unsupported --format %q (use nsec or ncryptsec)", format))
	}
	// Bounded so a newline-free stream cannot exhaust memory.
	in := bufio.NewReader(io.LimitReader(os.Stdin, 4*maxPasswordStdinBytes))

	ks, err := LoadKeyStore()
	if err != nil {
		return fmt.Errorf("failed to load keystore: %w", err)
	}
	// With an unencrypted keystore there is no keystore-password line. If the
	// caller sent one anyway, it would be read as the backup passphrase and the
	// backup would be encrypted with the wrong secret, so refuse before reading
	// stdin. (--format nsec reads nothing from stdin and is unaffected.)
	if c.Bool("password-stdin") && format == exportFormatNcryptsec && !ks.Encrypted {
		return common.NewExitError(common.ErrCodeUser, errors.New("--password-stdin was given but the keystore is not encrypted; stdin then carries only the backup passphrase, so drop --password-stdin"))
	}
	if err := unlockForExport(ks, jsonMode, c.Bool("password-stdin"), in); err != nil {
		return err
	}
	identity, err := GetIdentity(ks, c.String("nickname"))
	if err != nil {
		return err
	}
	nsec := identity.Nsec
	if ks.Encrypted {
		if nsec, err = decryptWithKey(identity.Nsec, *ks.MasterKey); err != nil {
			return errors.New("failed to decrypt nsec")
		}
	}

	secret, field := nsec, "nsec"
	if format == exportFormatNcryptsec {
		sk, perr := common.ParseSecretKey(nsec)
		if perr != nil {
			return common.NewExitError(common.ErrCodeOther, errors.New("stored key is invalid"))
		}
		pass, rerr := readSecretLine(in, "backup passphrase")
		if rerr != nil {
			return common.NewExitError(common.ErrCodeUser, rerr)
		}
		if secret, err = nip49.Encrypt(sk, pass, ncryptsecLogN, nip49.ClientDoesNotTrackThisData); err != nil {
			return common.NewExitError(common.ErrCodeOther, errors.New("failed to encrypt backup"))
		}
		field = "ncryptsec"
	}

	if out := c.String("out"); out != "" {
		if !exportOutSupported {
			return common.NewExitError(common.ErrCodeUser, errors.New("--out is not supported on this platform (cannot guarantee owner-only access)"))
		}
		if err := writeNewSecretFile(out, secret); err != nil {
			return err
		}
		common.Emit(jsonMode, map[string]any{"nickname": identity.Nickname, "npub": identity.Npub, "format": format, "out": out},
			func() {
				fmt.Printf("Identity: %s\nNpub:     %s\nWrote %s to %s (mode 0600)\n", identity.Nickname, identity.Npub, format, out)
			})
		return nil
	}

	common.Emit(jsonMode, map[string]any{"nickname": identity.Nickname, "npub": identity.Npub, field: secret}, func() {
		red := color.New(color.FgRed).SprintFunc()
		fmt.Println(red("⚠️  WARNING: You are about to expose your private key!"))
		fmt.Println(red("   Never share this with anyone or store it insecurely."))
		fmt.Println()
		fmt.Printf("Identity: %s\n", identity.Nickname)
		fmt.Printf("Npub:     %s\n", identity.Npub)
		if field == "nsec" {
			fmt.Printf("Nsec:     %s\n", secret)
		} else {
			fmt.Printf("Ncryptsec: %s\n", secret)
		}
	})
	return nil
}

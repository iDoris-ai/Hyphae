package identity

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"fiatjaf.com/nostr"
	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/pkg/types"
	"golang.org/x/term"
)

const (
	KeyStoreDirName       = ".hyphae"
	KeyStoreFile          = "keystore.json"
	maxPasswordStdinBytes = 4096
)

// KeyStoreCommandOptions controls command-specific keystore unlocking without
// changing the behavior of other existing keystore consumers.
type KeyStoreCommandOptions struct {
	JSONMode      bool
	RequireSecret bool
	PasswordStdin bool
	Stdin         io.Reader
}

// LoadKeyStoreForCommand loads the keystore and unlocks it only when a command
// needs secret keys. JSON commands never prompt; callers must opt in to stdin.
func LoadKeyStoreForCommand(options KeyStoreCommandOptions) (*types.KeyStore, error) {
	ks, err := LoadKeyStore()
	if err != nil {
		return nil, err
	}
	if !options.RequireSecret || !ks.Encrypted || ks.MasterKey != nil {
		return ks, nil
	}

	var password string
	if options.PasswordStdin {
		password, err = readPasswordStdin(options.Stdin)
		if err != nil {
			return nil, common.NewExitError(common.ErrCodeAuth, err)
		}
	} else if options.JSONMode {
		return nil, common.NewExitError(common.ErrCodeAuth, errors.New("encrypted keystore requires --password-stdin in JSON mode"))
	} else {
		password, err = PromptPassword("Keystore password: ")
		if err != nil {
			return nil, common.NewExitError(common.ErrCodeAuth, errors.New("failed to read keystore password"))
		}
	}
	if err := UnlockKeyStore(ks, password); err != nil {
		return nil, common.NewExitError(common.ErrCodeAuth, errors.New("failed to unlock keystore"))
	}
	return ks, nil
}

func readPasswordStdin(reader io.Reader) (string, error) {
	if reader == nil {
		return "", errors.New("password stdin is unavailable")
	}
	data, err := io.ReadAll(io.LimitReader(reader, maxPasswordStdinBytes+1))
	if err != nil {
		return "", errors.New("failed to read password from stdin")
	}
	if len(data) > maxPasswordStdinBytes {
		return "", errors.New("password from stdin exceeds 4096 bytes")
	}
	if bytes.HasSuffix(data, []byte("\r\n")) {
		data = data[:len(data)-2]
	} else if bytes.HasSuffix(data, []byte("\n")) {
		data = data[:len(data)-1]
	}
	if len(data) == 0 {
		return "", errors.New("password from stdin is empty")
	}
	return string(data), nil
}

// GetKeyStorePath returns the path to keystore directory
func GetKeyStorePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, KeyStoreDirName)
}

// EnsureKeyStore creates the keystore directory with proper permissions
func EnsureKeyStore() (string, error) {
	path := GetKeyStorePath()

	// Create directory with 700 permissions (only owner can read/write/execute)
	if err := os.MkdirAll(path, 0700); err != nil {
		return "", fmt.Errorf("failed to create keystore directory: %w", err)
	}

	// Ensure directory has correct permissions
	if err := os.Chmod(path, 0700); err != nil {
		return "", fmt.Errorf("failed to set keystore permissions: %w", err)
	}

	return path, nil
}

// LoadAndUnlockKeyStore loads the keystore and prompts for password if encrypted
func LoadAndUnlockKeyStore() (*types.KeyStore, error) {
	ks, err := LoadKeyStore()
	if err != nil {
		return nil, err
	}
	if ks.Encrypted && ks.MasterKey == nil {
		pw, err := PromptPassword("Keystore password: ")
		if err != nil {
			return nil, fmt.Errorf("failed to read password: %w", err)
		}
		if err := UnlockKeyStore(ks, pw); err != nil {
			return nil, fmt.Errorf("failed to unlock keystore: %w", err)
		}
	}
	return ks, nil
}

// LoadKeyStore loads the keystore from disk
func LoadKeyStore() (*types.KeyStore, error) {
	return loadKeyStoreFromDisk()
}

func keyStoreLockPath(directory string) string {
	return filepath.Join(directory, KeyStoreFile+".lock")
}

func loadKeyStoreFromDisk() (*types.KeyStore, error) {
	file := filepath.Join(GetKeyStorePath(), KeyStoreFile)
	ks := &types.KeyStore{
		Identities: make(map[string]*types.Identity),
		Contacts:   make(map[string]*types.Contact),
	}

	data, err := os.ReadFile(file)
	if err != nil {
		if os.IsNotExist(err) {
			return ks, nil
		}
		return nil, err
	}
	ks.StoreExists = true
	version := sha256.Sum256(data)
	ks.StoreVersion = hex.EncodeToString(version[:])

	if err := json.Unmarshal(data, ks); err != nil {
		return nil, fmt.Errorf("failed to parse keystore: %w", err)
	}

	return ks, nil
}

// SaveKeyStore uses compare-and-swap semantics so a caller cannot replace a
// newer on-disk keystore with a stale snapshot. Writers that need to combine
// concurrent updates should use a keystore transaction instead.
func SaveKeyStore(ks *types.KeyStore) error {
	if ks == nil {
		return errors.New("keystore is nil")
	}
	return withKeyStoreLock(func() error {
		current, err := loadKeyStoreFromDisk()
		if err != nil {
			return err
		}
		if current.StoreExists != ks.StoreExists || (current.StoreExists && current.StoreVersion != ks.StoreVersion) {
			return common.NewExitError(common.ErrCodeWriteConflict, errors.New("keystore changed since it was loaded; reload and retry"))
		}
		return saveKeyStoreLocked(ks)
	})
}

func saveKeyStoreLocked(ks *types.KeyStore) error {
	path, err := EnsureKeyStore()
	if err != nil {
		return err
	}
	file := filepath.Join(path, KeyStoreFile)
	data, err := json.MarshalIndent(ks, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal keystore: %w", err)
	}
	f, err := os.CreateTemp(path, KeyStoreFile+".*.tmp")
	if err != nil {
		return fmt.Errorf("open temp keystore: %w", err)
	}
	tmp := f.Name()
	if _, err := f.Write(data); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("write temp keystore: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("fsync temp keystore: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("close temp keystore: %w", err)
	}
	if err := os.Rename(tmp, file); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename keystore: %w", err)
	}
	ks.StoreExists = true
	version := sha256.Sum256(data)
	ks.StoreVersion = hex.EncodeToString(version[:])
	return nil
}

func runKeyStoreTransaction(ks *types.KeyStore, mutate func(*types.KeyStore) error) error {
	if ks == nil {
		return errors.New("keystore is nil")
	}
	return withKeyStoreLock(func() error {
		fresh, err := loadKeyStoreFromDisk()
		if err != nil {
			return err
		}
		if fresh.Encrypted && ks.MasterKey != nil {
			if ok, _ := verifyMasterKey(fresh.Verification, *ks.MasterKey); ok {
				fresh.MasterKey = ks.MasterKey
			}
		}
		if err := mutate(fresh); err != nil {
			return err
		}
		if err := saveKeyStoreLocked(fresh); err != nil {
			return err
		}
		*ks = *fresh
		return nil
	})
}

// UnlockKeyStore verifies the password and sets the master key on the keystore
func UnlockKeyStore(ks *types.KeyStore, password string) error {
	if !ks.Encrypted {
		return nil
	}
	oldSalt, oldVerification := ks.Salt, ks.Verification
	if err := unlockKeyStoreMemory(ks, password); err != nil {
		return err
	}
	if _, isLegacy := verifyMasterKey(oldVerification, *ks.MasterKey); isLegacy {
		upgradeVerificationToken(ks, *ks.MasterKey, oldSalt, oldVerification)
	}
	return nil
}

// requireMasterKey ensures the keystore is unlocked if encrypted
func requireMasterKey(ks *types.KeyStore) error {
	if !ks.Encrypted {
		return nil
	}
	if ks.MasterKey == nil {
		return fmt.Errorf("keystore is locked; please unlock with password first")
	}
	return nil
}

// CreateIdentity creates a new identity with the given nickname (unencrypted, for backward compatibility)
func CreateIdentity(ks *types.KeyStore, nickname string) (*types.Identity, error) {
	return CreateIdentityWithPassword(ks, nickname, "")
}

// CreateIdentityWithPassword creates a new identity with optional password encryption
func CreateIdentityWithPassword(ks *types.KeyStore, nickname, password string) (*types.Identity, error) {
	var created *types.Identity
	err := runKeyStoreTransaction(ks, func(fresh *types.KeyStore) error {
		if _, exists := fresh.Identities[nickname]; exists {
			return fmt.Errorf("identity '%s' already exists", nickname)
		}
		if fresh.Encrypted {
			if password == "" {
				return common.NewExitError(common.ErrCodeAuth, errors.New("encrypted keystore requires a password"))
			}
			if err := unlockKeyStoreMemory(fresh, password); err != nil {
				return common.NewExitError(common.ErrCodeAuth, errors.New("failed to unlock keystore"))
			}
		} else if password != "" {
			if len(fresh.Identities) != 0 {
				return common.NewExitError(common.ErrCodeUser, errors.New("encrypt the existing keystore with 'identity change-password' before creating another identity with a password"))
			}
			saltB64, verificationB64, err := createVerification(password)
			if err != nil {
				return fmt.Errorf("failed to setup encryption: %w", err)
			}
			fresh.Encrypted, fresh.Salt, fresh.Verification = true, saltB64, verificationB64
			salt, err := base64.StdEncoding.DecodeString(saltB64)
			if err != nil {
				return fmt.Errorf("invalid salt: %w", err)
			}
			key, err := deriveMasterKey(password, salt)
			if err != nil {
				return err
			}
			fresh.MasterKey = &key
		}

		sk := nostr.Generate()
		nsec := common.EncodeNsec(sk)
		if fresh.Encrypted {
			encrypted, err := encryptWithKey(nsec, *fresh.MasterKey)
			if err != nil {
				return fmt.Errorf("failed to encrypt nsec: %w", err)
			}
			nsec = encrypted
		}
		created = &types.Identity{Nickname: nickname, Npub: common.EncodeNpub(sk.Public()), Nsec: nsec, Created: int64(nostr.Now())}
		fresh.Identities[nickname] = created
		if fresh.DefaultIdentity == "" {
			fresh.DefaultIdentity = nickname
		}
		return nil
	})
	return created, err
}

// GetIdentity retrieves an identity by nickname
func GetIdentity(ks *types.KeyStore, nickname string) (*types.Identity, error) {
	if nickname == "" {
		nickname = ks.DefaultIdentity
	}

	identity, exists := ks.Identities[nickname]
	if !exists {
		return nil, fmt.Errorf("identity '%s' not found", nickname)
	}

	return identity, nil
}

// GetSecretKey gets the secret key for an identity
func GetSecretKey(ks *types.KeyStore, nickname string) (nostr.SecretKey, error) {
	identity, err := GetIdentity(ks, nickname)
	if err != nil {
		return nostr.SecretKey{}, err
	}

	nsec := identity.Nsec
	if ks.Encrypted {
		if err := requireMasterKey(ks); err != nil {
			return nostr.SecretKey{}, err
		}
		decrypted, err := decryptWithKey(nsec, *ks.MasterKey)
		if err != nil {
			return nostr.SecretKey{}, fmt.Errorf("failed to decrypt nsec: %w", err)
		}
		nsec = decrypted
	}

	return common.ParseSecretKey(nsec)
}

// GetPublicKey gets the public key for an identity
func GetPublicKey(ks *types.KeyStore, nickname string) (nostr.PubKey, error) {
	identity, err := GetIdentity(ks, nickname)
	if err != nil {
		return nostr.PubKey{}, err
	}

	return common.ParsePublicKey(identity.Npub)
}

// SetDefault sets the default identity
func SetDefault(ks *types.KeyStore, nickname string) error {
	return runKeyStoreTransaction(ks, func(fresh *types.KeyStore) error {
		if _, exists := fresh.Identities[nickname]; !exists {
			return fmt.Errorf("identity '%s' not found", nickname)
		}
		fresh.DefaultIdentity = nickname
		return nil
	})
}

// AddContact adds a contact with the default role (human). Equivalent to
// AddContactWithRole(ks, nickname, npub, types.RoleHuman).
func AddContact(ks *types.KeyStore, nickname, npub string) error {
	return AddContactWithRole(ks, nickname, npub, types.RoleHuman)
}

// AddContactWithRole adds a contact, marking whether it's a human or an
// Agent. See pkg/types.Role — this is a label, not an access-control
// mechanism.
func AddContactWithRole(ks *types.KeyStore, nickname, npub string, role types.Role) error {
	if !role.IsValid() {
		return fmt.Errorf("invalid role %q: must be %q or %q", role, types.RoleHuman, types.RoleAgent)
	}

	// Validate npub
	pk, err := common.ParsePublicKey(npub)
	if err != nil {
		return fmt.Errorf("invalid npub: %w", err)
	}

	return runKeyStoreTransaction(ks, func(fresh *types.KeyStore) error {
		if _, exists := fresh.Contacts[nickname]; exists {
			return fmt.Errorf("contact '%s' already exists", nickname)
		}
		fresh.Contacts[nickname] = &types.Contact{Nickname: nickname, Npub: common.EncodeNpub(pk), AddedAt: int64(nostr.Now()), Role: role}
		return nil
	})
}

// GetContact retrieves a contact by nickname
func GetContact(ks *types.KeyStore, nickname string) (*types.Contact, error) {
	contact, exists := ks.Contacts[nickname]
	if !exists {
		return nil, fmt.Errorf("contact '%s' not found", nickname)
	}

	return contact, nil
}

// ResolveRecipient resolves a recipient (nickname or npub) to npub
func ResolveRecipient(ks *types.KeyStore, input string) (string, error) {
	// First try to find as contact nickname
	if contact, err := GetContact(ks, input); err == nil {
		return contact.Npub, nil
	}

	// Then try as identity nickname
	if identity, err := GetIdentity(ks, input); err == nil {
		return identity.Npub, nil
	}

	// Finally, validate as npub
	if _, err := common.ParsePublicKey(input); err == nil {
		return input, nil
	}

	return "", fmt.Errorf("'%s' is not a known nickname or valid npub", input)
}

// ListIdentities lists all identities
func ListIdentities(ks *types.KeyStore) []*types.Identity {
	list := make([]*types.Identity, 0, len(ks.Identities))
	for _, identity := range ks.Identities {
		list = append(list, identity)
	}
	return list
}

// ListContacts lists all contacts
func ListContacts(ks *types.KeyStore) []*types.Contact {
	list := make([]*types.Contact, 0, len(ks.Contacts))
	for _, contact := range ks.Contacts {
		list = append(list, contact)
	}
	return list
}

// PromptPassword securely prompts for password
func PromptPassword(prompt string) (string, error) {
	if common.JSONModeFromArgs(os.Args) {
		return "", common.NewExitError(common.ErrCodeAuth, fmt.Errorf("interactive password prompts are unavailable in JSON mode"))
	}
	if prompt == "" {
		prompt = "Password: "
	}
	fmt.Fprint(os.Stderr, prompt)

	bytePassword, err := term.ReadPassword(int(syscall.Stdin))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}

	return string(bytePassword), nil
}

// PromptPasswordWithConfirm prompts for a new password twice and confirms they match
func PromptPasswordWithConfirm() (string, error) {
	pw1, err := PromptPassword("Enter password: ")
	if err != nil {
		return "", err
	}
	if pw1 == "" {
		return "", fmt.Errorf("password cannot be empty")
	}

	pw2, err := PromptPassword("Confirm password: ")
	if err != nil {
		return "", err
	}
	if pw1 != pw2 {
		return "", fmt.Errorf("passwords do not match")
	}

	return pw1, nil
}

// ChangePassword changes the keystore password and re-encrypts all nsecs
func ChangePassword(ks *types.KeyStore, oldPassword, newPassword string) error {
	return updateKeyStorePassword(ks, oldPassword, newPassword, false)
}

// EncryptKeyStore enables password protection on an unencrypted keystore.
func EncryptKeyStore(ks *types.KeyStore, password string) error {
	return updateKeyStorePassword(ks, "", password, true)
}

func updateKeyStorePassword(ks *types.KeyStore, oldPassword, newPassword string, allowEnable bool) error {
	if newPassword == "" {
		return errors.New("password cannot be empty")
	}
	return runKeyStoreTransaction(ks, func(fresh *types.KeyStore) error {
		decrypted := make(map[string]string, len(fresh.Identities))
		if fresh.Encrypted {
			if allowEnable {
				return common.NewExitError(common.ErrCodeAuth, errors.New("keystore is already encrypted"))
			}
			if err := unlockKeyStoreMemory(fresh, oldPassword); err != nil {
				return common.NewExitError(common.ErrCodeAuth, errors.New("failed to unlock keystore"))
			}
			for nickname, identity := range fresh.Identities {
				nsec, err := decryptWithKey(identity.Nsec, *fresh.MasterKey)
				if err != nil {
					return fmt.Errorf("failed to decrypt nsec for %s: %w", nickname, err)
				}
				decrypted[nickname] = nsec
			}
		} else {
			if !allowEnable {
				return errors.New("keystore is not encrypted")
			}
			for nickname, identity := range fresh.Identities {
				decrypted[nickname] = identity.Nsec
			}
		}

		saltB64, verificationB64, err := createVerification(newPassword)
		if err != nil {
			return fmt.Errorf("failed to setup new encryption: %w", err)
		}
		saltBytes, err := base64.StdEncoding.DecodeString(saltB64)
		if err != nil {
			return fmt.Errorf("invalid salt: %w", err)
		}
		newKey, err := deriveMasterKey(newPassword, saltBytes)
		if err != nil {
			return err
		}
		for nickname, nsec := range decrypted {
			encrypted, err := encryptWithKey(nsec, newKey)
			if err != nil {
				return fmt.Errorf("failed to encrypt nsec for %s: %w", nickname, err)
			}
			fresh.Identities[nickname].Nsec = encrypted
		}
		fresh.Encrypted, fresh.Salt, fresh.Verification, fresh.MasterKey = true, saltB64, verificationB64, &newKey
		return nil
	})
}

func mustDecodeB64(s string) ([]byte, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("invalid base64: %w", err)
	}
	return b, nil
}

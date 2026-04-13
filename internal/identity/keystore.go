package identity

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"fiatjaf.com/nostr"
	"github.com/jason/agent-speaker/internal/common"
	"github.com/jason/agent-speaker/pkg/types"
	"golang.org/x/term"
)

const (
	KeyStoreDirName = ".agent-speaker"
	KeyStoreFile    = "keystore.json"
)

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

// LoadKeyStore loads the keystore from disk
func LoadKeyStore() (*types.KeyStore, error) {
	path := GetKeyStorePath()
	file := filepath.Join(path, KeyStoreFile)

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

	if err := json.Unmarshal(data, ks); err != nil {
		return nil, fmt.Errorf("failed to parse keystore: %w", err)
	}

	return ks, nil
}

// SaveKeyStore saves the keystore to disk
func SaveKeyStore(ks *types.KeyStore) error {
	path, err := EnsureKeyStore()
	if err != nil {
		return err
	}

	file := filepath.Join(path, KeyStoreFile)

	data, err := json.MarshalIndent(ks, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal keystore: %w", err)
	}

	// Write with 600 permissions (only owner can read/write)
	if err := os.WriteFile(file, data, 0600); err != nil {
		return fmt.Errorf("failed to write keystore: %w", err)
	}

	return nil
}

// CreateIdentity creates a new identity with the given nickname
func CreateIdentity(ks *types.KeyStore, nickname string) (*types.Identity, error) {
	if _, exists := ks.Identities[nickname]; exists {
		return nil, fmt.Errorf("identity '%s' already exists", nickname)
	}

	// Generate new keypair
	sk := nostr.Generate()
	pk := sk.Public()

	identity := &types.Identity{
		Nickname: nickname,
		Npub:     common.EncodeNpub(pk),
		Nsec:     common.EncodeNsec(sk),
		Created:  int64(nostr.Now()),
	}

	ks.Identities[nickname] = identity

	// Set as default if first identity
	if ks.DefaultIdentity == "" {
		ks.DefaultIdentity = nickname
	}

	return identity, SaveKeyStore(ks)
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

	return common.ParseSecretKey(identity.Nsec)
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
	if _, exists := ks.Identities[nickname]; !exists {
		return fmt.Errorf("identity '%s' not found", nickname)
	}

	ks.DefaultIdentity = nickname
	return SaveKeyStore(ks)
}

// AddContact adds a contact
func AddContact(ks *types.KeyStore, nickname, npub string) error {
	if _, exists := ks.Contacts[nickname]; exists {
		return fmt.Errorf("contact '%s' already exists", nickname)
	}

	// Validate npub
	pk, err := common.ParsePublicKey(npub)
	if err != nil {
		return fmt.Errorf("invalid npub: %w", err)
	}

	ks.Contacts[nickname] = &types.Contact{
		Nickname: nickname,
		Npub:     common.EncodeNpub(pk),
		AddedAt:  int64(nostr.Now()),
	}

	return SaveKeyStore(ks)
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

// PromptPassword securely prompts for password (for future encryption)
func PromptPassword(prompt string) (string, error) {
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

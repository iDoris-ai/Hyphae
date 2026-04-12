package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"fiatjaf.com/nostr"
	"golang.org/x/term"
)

const (
	KeyStoreDirName = ".agent-speaker"
	KeyStoreFile    = "keystore.json"
	ContactsFile    = "contacts.json"
)

// KeyStore manages local identity and contacts
type KeyStore struct {
	DefaultIdentity string                     `json:"default_identity"` // nickname
	Identities      map[string]*Identity       `json:"identities"`       // nickname -> identity
	Contacts        map[string]*Contact        `json:"contacts"`         // nickname -> contact
}

// Identity represents a local user identity
type Identity struct {
	Nickname string `json:"nickname"`
	Npub     string `json:"npub"`
	Nsec     string `json:"nsec"` // encrypted or raw (for now)
	Created  int64  `json:"created"`
}

// Contact represents a remote contact
type Contact struct {
	Nickname string `json:"nickname"`
	Npub     string `json:"npub"`
	AddedAt  int64  `json:"added_at"`
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

// LoadKeyStore loads the keystore from disk
func LoadKeyStore() (*KeyStore, error) {
	path := GetKeyStorePath()
	file := filepath.Join(path, KeyStoreFile)
	
	ks := &KeyStore{
		Identities: make(map[string]*Identity),
		Contacts:   make(map[string]*Contact),
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

// Save saves the keystore to disk
func (ks *KeyStore) Save() error {
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
func (ks *KeyStore) CreateIdentity(nickname string) (*Identity, error) {
	if _, exists := ks.Identities[nickname]; exists {
		return nil, fmt.Errorf("identity '%s' already exists", nickname)
	}
	
	// Generate new keypair
	sk := nostr.Generate()
	pk := sk.Public()
	
	identity := &Identity{
		Nickname: nickname,
		Npub:     encodeNpub(pk),
		Nsec:     encodeNsec(sk),
		Created:  int64(nostr.Now()),
	}
	
	ks.Identities[nickname] = identity
	
	// Set as default if first identity
	if ks.DefaultIdentity == "" {
		ks.DefaultIdentity = nickname
	}
	
	return identity, ks.Save()
}

// GetIdentity retrieves an identity by nickname
func (ks *KeyStore) GetIdentity(nickname string) (*Identity, error) {
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
func (ks *KeyStore) GetSecretKey(nickname string) (nostr.SecretKey, error) {
	identity, err := ks.GetIdentity(nickname)
	if err != nil {
		return nostr.SecretKey{}, err
	}
	
	return parseSecretKey(identity.Nsec)
}

// GetPublicKey gets the public key for an identity
func (ks *KeyStore) GetPublicKey(nickname string) (nostr.PubKey, error) {
	identity, err := ks.GetIdentity(nickname)
	if err != nil {
		return nostr.PubKey{}, err
	}
	
	return parsePublicKey(identity.Npub)
}

// SetDefault sets the default identity
func (ks *KeyStore) SetDefault(nickname string) error {
	if _, exists := ks.Identities[nickname]; !exists {
		return fmt.Errorf("identity '%s' not found", nickname)
	}
	
	ks.DefaultIdentity = nickname
	return ks.Save()
}

// AddContact adds a contact
func (ks *KeyStore) AddContact(nickname, npub string) error {
	if _, exists := ks.Contacts[nickname]; exists {
		return fmt.Errorf("contact '%s' already exists", nickname)
	}
	
	// Validate npub
	pk, err := parsePublicKey(npub)
	if err != nil {
		return fmt.Errorf("invalid npub: %w", err)
	}
	
	ks.Contacts[nickname] = &Contact{
		Nickname: nickname,
		Npub:     encodeNpub(pk),
		AddedAt:  int64(nostr.Now()),
	}
	
	return ks.Save()
}

// GetContact retrieves a contact by nickname
func (ks *KeyStore) GetContact(nickname string) (*Contact, error) {
	contact, exists := ks.Contacts[nickname]
	if !exists {
		return nil, fmt.Errorf("contact '%s' not found", nickname)
	}
	
	return contact, nil
}

// ResolveRecipient resolves a recipient (nickname or npub) to npub
func (ks *KeyStore) ResolveRecipient(input string) (string, error) {
	// First try to find as contact nickname
	if contact, err := ks.GetContact(input); err == nil {
		return contact.Npub, nil
	}
	
	// Then try as identity nickname
	if identity, err := ks.GetIdentity(input); err == nil {
		return identity.Npub, nil
	}
	
	// Finally, validate as npub
	if _, err := parsePublicKey(input); err == nil {
		return input, nil
	}
	
	return "", fmt.Errorf("'%s' is not a known nickname or valid npub", input)
}

// ListIdentities lists all identities
func (ks *KeyStore) ListIdentities() []*Identity {
	list := make([]*Identity, 0, len(ks.Identities))
	for _, identity := range ks.Identities {
		list = append(list, identity)
	}
	return list
}

// ListContacts lists all contacts
func (ks *KeyStore) ListContacts() []*Contact {
	list := make([]*Contact, 0, len(ks.Contacts))
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

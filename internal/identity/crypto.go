package identity

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"io"
	"os"

	"github.com/iDoris-ai/hyphae/pkg/types"
	"golang.org/x/crypto/scrypt"
)

const (
	scryptN      = 32768
	scryptR      = 8
	scryptP      = 1
	scryptKeyLen = 32
	// verifyToken is encrypted and stored in a keystore's Verification field to
	// check password correctness at unlock time. It crosses a persistence
	// boundary: existing encrypted keystores carry whichever token was current
	// when they were created, so renaming/rebranding the project must keep
	// accepting legacyVerifyToken below, or a correct password on an old
	// keystore starts failing (see PR #33 review, finding B1).
	verifyToken       = "hyphae-keystore-v1"
	legacyVerifyToken = "agent-speaker-keystore-v1"
)

// deriveMasterKey derives a 32-byte key from password and salt using scrypt
func deriveMasterKey(password string, salt []byte) ([32]byte, error) {
	var key [32]byte
	derived, err := scrypt.Key([]byte(password), salt, scryptN, scryptR, scryptP, scryptKeyLen)
	if err != nil {
		return key, fmt.Errorf("failed to derive key: %w", err)
	}
	copy(key[:], derived)
	return key, nil
}

// encryptWithKey encrypts plaintext with AES-256-GCM and returns base64(nonce || ciphertext)
func encryptWithKey(plaintext string, key [32]byte) (string, error) {
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// decryptWithKey decrypts base64(nonce || ciphertext) with AES-256-GCM
func decryptWithKey(ciphertextB64 string, key [32]byte) (string, error) {
	data, err := base64.StdEncoding.DecodeString(ciphertextB64)
	if err != nil {
		return "", fmt.Errorf("invalid ciphertext: %w", err)
	}
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}
	nonce, ciphertext := data[:nonceSize], data[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("decryption failed: %w", err)
	}
	return string(plaintext), nil
}

// generateSalt generates a random 16-byte salt
func generateSalt() ([]byte, error) {
	salt := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, err
	}
	return salt, nil
}

// createVerification creates a verification token to verify password correctness
func createVerification(password string) (saltB64, verificationB64 string, err error) {
	salt, err := generateSalt()
	if err != nil {
		return "", "", err
	}
	key, err := deriveMasterKey(password, salt)
	if err != nil {
		return "", "", err
	}
	verification, err := encryptWithKey(verifyToken, key)
	if err != nil {
		return "", "", err
	}
	return base64.StdEncoding.EncodeToString(salt), verification, nil
}

// verifyMasterKey checks if the key can decrypt the verification token. It accepts
// both the current token and legacyVerifyToken so a keystore encrypted before a
// project rename keeps unlocking with its correct password; isLegacy tells the
// caller the on-disk token should be upgraded to the current one.
func verifyMasterKey(verificationB64 string, key [32]byte) (ok bool, isLegacy bool) {
	decrypted, err := decryptWithKey(verificationB64, key)
	if err != nil {
		return false, false
	}
	if subtle.ConstantTimeCompare([]byte(decrypted), []byte(verifyToken)) == 1 {
		return true, false
	}
	if subtle.ConstantTimeCompare([]byte(decrypted), []byte(legacyVerifyToken)) == 1 {
		return true, true
	}
	return false, false
}

// unlockKeyStore derives the master key and verifies it against the stored verification token
func unlockKeyStore(ks *types.KeyStore, password string) error {
	if !ks.Encrypted {
		return nil
	}
	salt, err := base64.StdEncoding.DecodeString(ks.Salt)
	if err != nil {
		return fmt.Errorf("invalid keystore salt: %w", err)
	}
	key, err := deriveMasterKey(password, salt)
	if err != nil {
		return err
	}
	ok, isLegacy := verifyMasterKey(ks.Verification, key)
	if !ok {
		return fmt.Errorf("incorrect password")
	}
	ks.MasterKey = &key
	if isLegacy {
		upgradeVerificationToken(ks, key)
	}
	return nil
}

// upgradeVerificationToken re-encrypts the verification token under the current
// verifyToken and persists it. Best-effort and side-effect-only: the caller has
// already confirmed the password is correct, so a failure here must only warn,
// never turn a successful unlock into an error — there's always a next unlock
// to retry the upgrade on.
func upgradeVerificationToken(ks *types.KeyStore, key [32]byte) {
	newVerification, err := encryptWithKey(verifyToken, key)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not prepare keystore verification-token upgrade: %v\n", err)
		return
	}
	ks.Verification = newVerification
	if err := SaveKeyStore(ks); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not persist keystore verification-token upgrade: %v\n", err)
	}
}

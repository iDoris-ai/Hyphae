package identity

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/AuraAIHQ/agent-speaker/pkg/types"
)

func TestEncryptDecryptWithKey(t *testing.T) {
	key := [32]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32}
	plaintext := "my secret nsec"

	encrypted, err := encryptWithKey(plaintext, key)
	require.NoError(t, err)
	assert.NotEqual(t, plaintext, encrypted)

	decrypted, err := decryptWithKey(encrypted, key)
	require.NoError(t, err)
	assert.Equal(t, plaintext, decrypted)
}

func TestDecryptWithKey_WrongKey(t *testing.T) {
	key := [32]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32}
	wrongKey := [32]byte{32, 31, 30, 29, 28, 27, 26, 25, 24, 23, 22, 21, 20, 19, 18, 17, 16, 15, 14, 13, 12, 11, 10, 9, 8, 7, 6, 5, 4, 3, 2, 1}
	plaintext := "my secret nsec"

	encrypted, err := encryptWithKey(plaintext, key)
	require.NoError(t, err)

	_, err = decryptWithKey(encrypted, wrongKey)
	assert.Error(t, err)
}

func TestCreateAndVerifyVerification(t *testing.T) {
	password := "super-secret-password"
	saltB64, verificationB64, err := createVerification(password)
	require.NoError(t, err)
	assert.NotEmpty(t, saltB64)
	assert.NotEmpty(t, verificationB64)

	key, err := deriveMasterKey(password, mustDecodeB64(saltB64))
	require.NoError(t, err)
	assert.True(t, verifyMasterKey(verificationB64, key))

	wrongKey := [32]byte{}
	assert.False(t, verifyMasterKey(verificationB64, wrongKey))
}

func TestUnlockKeyStore_Success(t *testing.T) {
	password := "test-password"
	saltB64, verificationB64, err := createVerification(password)
	require.NoError(t, err)

	ks := &types.KeyStore{
		Encrypted:    true,
		Salt:         saltB64,
		Verification: verificationB64,
	}

	err = unlockKeyStore(ks, password)
	require.NoError(t, err)
	assert.NotNil(t, ks.MasterKey)
}

func TestUnlockKeyStore_WrongPassword(t *testing.T) {
	password := "test-password"
	saltB64, verificationB64, err := createVerification(password)
	require.NoError(t, err)

	ks := &types.KeyStore{
		Encrypted:    true,
		Salt:         saltB64,
		Verification: verificationB64,
	}

	err = unlockKeyStore(ks, "wrong-password")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "incorrect password")
	assert.Nil(t, ks.MasterKey)
}

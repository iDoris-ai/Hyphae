package identity

import (
	"os"
	"testing"

	"github.com/AuraAIHQ/agent-speaker/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTempKeyStore(t *testing.T) string {
	tmpDir := t.TempDir()
	oldDir := KeyStoreDirName
	// Override via environment for test isolation
	os.Setenv("HOME", tmpDir)
	return oldDir
}

func TestCreateIdentityWithPassword(t *testing.T) {
	setupTempKeyStore(t)
	ks := &types.KeyStore{
		Identities: make(map[string]*types.Identity),
		Contacts:   make(map[string]*types.Contact),
	}

	identity, err := CreateIdentityWithPassword(ks, "alice", "testpass")
	require.NoError(t, err)
	assert.Equal(t, "alice", identity.Nickname)
	assert.NotEmpty(t, identity.Npub)
	assert.True(t, ks.Encrypted)
	assert.NotEmpty(t, ks.Salt)
	assert.NotEmpty(t, ks.Verification)

	// Nsec should be encrypted (not a raw nsec1 string)
	assert.NotContains(t, identity.Nsec, "nsec1")
}

func TestGetSecretKey_Encrypted(t *testing.T) {
	setupTempKeyStore(t)
	ks := &types.KeyStore{
		Identities: make(map[string]*types.Identity),
		Contacts:   make(map[string]*types.Contact),
	}

	_, err := CreateIdentityWithPassword(ks, "alice", "testpass")
	require.NoError(t, err)

	// After creation with password, keystore is already unlocked in memory
	sk, err := GetSecretKey(ks, "alice")
	require.NoError(t, err)
	assert.NotEqual(t, [32]byte{}, sk)

	// Simulate fresh load: reset MasterKey
	ks.MasterKey = nil
	_, err = GetSecretKey(ks, "alice")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "locked")

	// After unlocking, should succeed again
	err = UnlockKeyStore(ks, "testpass")
	require.NoError(t, err)
	sk, err = GetSecretKey(ks, "alice")
	require.NoError(t, err)
	assert.NotEqual(t, [32]byte{}, sk)
}

func TestGetSecretKey_Unencrypted(t *testing.T) {
	setupTempKeyStore(t)
	ks := &types.KeyStore{
		Identities: make(map[string]*types.Identity),
		Contacts:   make(map[string]*types.Contact),
	}

	_, err := CreateIdentity(ks, "bob")
	require.NoError(t, err)

	sk, err := GetSecretKey(ks, "bob")
	require.NoError(t, err)
	assert.NotEqual(t, [32]byte{}, sk)
}

func TestChangePassword(t *testing.T) {
	setupTempKeyStore(t)
	ks := &types.KeyStore{
		Identities: make(map[string]*types.Identity),
		Contacts:   make(map[string]*types.Contact),
	}

	_, err := CreateIdentityWithPassword(ks, "alice", "oldpass")
	require.NoError(t, err)

	// Unlock and get original secret key
	err = UnlockKeyStore(ks, "oldpass")
	require.NoError(t, err)
	origSK, err := GetSecretKey(ks, "alice")
	require.NoError(t, err)

	// Change password
	err = ChangePassword(ks, "oldpass", "newpass")
	require.NoError(t, err)

	// Old password should no longer work
	err = UnlockKeyStore(ks, "oldpass")
	assert.Error(t, err)

	// New password should work and yield same secret key
	err = UnlockKeyStore(ks, "newpass")
	require.NoError(t, err)
	newSK, err := GetSecretKey(ks, "alice")
	require.NoError(t, err)
	assert.Equal(t, origSK, newSK)
}

func TestLoadAndSaveKeyStore(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("HOME", tmpDir)

	ks, err := LoadKeyStore()
	require.NoError(t, err)
	assert.Empty(t, ks.Identities)

	_, err = CreateIdentity(ks, "test")
	require.NoError(t, err)

	// Reload
	ks2, err := LoadKeyStore()
	require.NoError(t, err)
	assert.Len(t, ks2.Identities, 1)
	assert.Equal(t, "test", ks2.Identities["test"].Nickname)
}

func TestEncryptUnencryptedKeystore(t *testing.T) {
	setupTempKeyStore(t)
	ks := &types.KeyStore{
		Identities: make(map[string]*types.Identity),
		Contacts:   make(map[string]*types.Contact),
	}

	_, err := CreateIdentity(ks, "alice")
	require.NoError(t, err)
	assert.False(t, ks.Encrypted)

	// Simulate change-password on unencrypted keystore
	pw := "newpass"
	saltB64, verificationB64, err := createVerification(pw)
	require.NoError(t, err)
	saltBytes, err := mustDecodeB64(saltB64)
	require.NoError(t, err)
	key, err := deriveMasterKey(pw, saltBytes)
	require.NoError(t, err)
	for _, identity := range ks.Identities {
		encrypted, err := encryptWithKey(identity.Nsec, key)
		require.NoError(t, err)
		identity.Nsec = encrypted
	}
	ks.Encrypted = true
	ks.Salt = saltB64
	ks.Verification = verificationB64
	ks.MasterKey = &key
	err = SaveKeyStore(ks)
	require.NoError(t, err)

	// Reload and verify
	ks2, err := LoadKeyStore()
	require.NoError(t, err)
	err = UnlockKeyStore(ks2, pw)
	require.NoError(t, err)
	sk, err := GetSecretKey(ks2, "alice")
	require.NoError(t, err)
	assert.NotEqual(t, [32]byte{}, sk)
}

func TestResolveRecipient(t *testing.T) {
	setupTempKeyStore(t)
	ks := &types.KeyStore{
		Identities: make(map[string]*types.Identity),
		Contacts:   make(map[string]*types.Contact),
	}

	self, err := CreateIdentity(ks, "alice")
	require.NoError(t, err)

	other, err := CreateIdentity(ks, "bob")
	require.NoError(t, err)

	err = AddContact(ks, "bobby", other.Npub)
	require.NoError(t, err)

	t.Run("resolves via contact nickname", func(t *testing.T) {
		npub, err := ResolveRecipient(ks, "bobby")
		require.NoError(t, err)
		assert.Equal(t, other.Npub, npub)
	})

	t.Run("resolves via identity nickname, not just contacts", func(t *testing.T) {
		// "bob" is an identity (e.g. a second local persona), not a contact —
		// this is the exact case that used to work for `agent msg --to` but
		// not `history conversation --with` before they shared ResolveRecipient.
		npub, err := ResolveRecipient(ks, "bob")
		require.NoError(t, err)
		assert.Equal(t, other.Npub, npub)
	})

	t.Run("resolves a raw npub not in contacts or identities", func(t *testing.T) {
		npub, err := ResolveRecipient(ks, self.Npub)
		require.NoError(t, err)
		assert.Equal(t, self.Npub, npub)
	})

	t.Run("unknown name errors clearly", func(t *testing.T) {
		_, err := ResolveRecipient(ks, "totally-unknown-name")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "totally-unknown-name")
	})
}

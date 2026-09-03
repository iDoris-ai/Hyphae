package identity

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/iDoris-ai/hyphae/pkg/types"
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

// TestUnlockKeyStore_LegacyVerificationTokenUpgrades is the golden-fixture regression
// test for the agent-speaker -> hyphae rename (PR #33 review, finding B1): a keystore
// encrypted before the rename has legacyVerifyToken baked into its on-disk
// Verification field. Renaming that constant without keeping the old value
// acceptable locked every encrypted keystore out, correct password or not. This
// builds exactly that pre-rename keystore on disk, confirms it still unlocks, and
// confirms the on-disk token gets silently upgraded so later unlocks take the
// fast (non-legacy) path.
func TestUnlockKeyStore_LegacyVerificationTokenUpgrades(t *testing.T) {
	setupTempKeyStore(t)
	password := "legacy-password"

	saltB64, verificationB64, err := createLegacyVerification(password)
	require.NoError(t, err)

	ks := &types.KeyStore{
		Encrypted:    true,
		Salt:         saltB64,
		Verification: verificationB64,
		Identities:   make(map[string]*types.Identity),
		Contacts:     make(map[string]*types.Contact),
	}
	require.NoError(t, SaveKeyStore(ks))

	loaded, err := LoadKeyStore()
	require.NoError(t, err)
	require.NoError(t, UnlockKeyStore(loaded, password), "correct password on a legacy keystore must still unlock")
	assert.NotNil(t, loaded.MasterKey)

	reloaded, err := LoadKeyStore()
	require.NoError(t, err)
	assert.NotEqual(t, verificationB64, reloaded.Verification, "verification token should have been upgraded on disk")

	require.NoError(t, UnlockKeyStore(reloaded, password), "must still unlock after the upgrade")
	assert.NotNil(t, reloaded.MasterKey)
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

func TestAddContactDefaultsToHumanRole(t *testing.T) {
	setupTempKeyStore(t)
	ks := &types.KeyStore{
		Identities: make(map[string]*types.Identity),
		Contacts:   make(map[string]*types.Contact),
	}

	other, err := CreateIdentity(ks, "bob")
	require.NoError(t, err)

	require.NoError(t, AddContact(ks, "bobby", other.Npub))
	contact, err := GetContact(ks, "bobby")
	require.NoError(t, err)
	assert.Equal(t, types.RoleHuman, contact.Role)
}

func TestAddContactWithRole(t *testing.T) {
	setupTempKeyStore(t)
	ks := &types.KeyStore{
		Identities: make(map[string]*types.Identity),
		Contacts:   make(map[string]*types.Contact),
	}

	other, err := CreateIdentity(ks, "worker-bot")
	require.NoError(t, err)

	require.NoError(t, AddContactWithRole(ks, "bot1", other.Npub, types.RoleAgent))
	contact, err := GetContact(ks, "bot1")
	require.NoError(t, err)
	assert.Equal(t, types.RoleAgent, contact.Role)
}

// TestAddContactWithRoleRejectsInvalidRole covers the defense-in-depth
// validation added at this internal boundary (Codex review finding on
// specs/m1.5/tasks/05-member-role-model.md): the CLI's own --role flag
// validation isn't the only thing standing between an arbitrary string and
// the keystore, so AddContactWithRole must reject invalid values itself too.
func TestAddContactWithRoleRejectsInvalidRole(t *testing.T) {
	setupTempKeyStore(t)
	ks := &types.KeyStore{
		Identities: make(map[string]*types.Identity),
		Contacts:   make(map[string]*types.Contact),
	}

	other, err := CreateIdentity(ks, "worker-bot")
	require.NoError(t, err)

	err = AddContactWithRole(ks, "bot1", other.Npub, types.Role("bogus"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid role")

	_, getErr := GetContact(ks, "bot1")
	assert.Error(t, getErr, "a rejected AddContactWithRole call must not persist a partial contact")
}

// TestLegacyContactWithoutRoleFieldDefaultsToHuman covers the backward-
// compatibility requirement from specs/m1.5/tasks/05-member-role-model.md:
// a keystore.json saved before the Role field existed must still deserialize
// (and behave) as a human contact, not error out or leave Role in some
// unexpected state.
func TestLegacyContactWithoutRoleFieldDefaultsToHuman(t *testing.T) {
	legacyJSON := `{
		"nickname": "old-contact",
		"npub": "npub1qqu3pa35gzz3pxpw4gry22399kf8nqu7tu4exv38ge7pnr8nwxeqfmme3y",
		"added_at": 1700000000
	}`

	var contact types.Contact
	require.NoError(t, json.Unmarshal([]byte(legacyJSON), &contact))

	assert.Equal(t, types.Role(""), contact.Role, "zero value for a legacy record with no role key")
	assert.Equal(t, "human", contact.Role.String(), "Role.String() must treat the zero value as human")
	assert.NotEqual(t, types.RoleAgent, contact.Role, "a legacy contact must never be mistaken for an agent")
}

// TestSaveKeyStore_ConcurrentWritesNeverCorrupt guards the unique-temp-file
// property of SaveKeyStore (PR #33 review, concurrency round). With a fixed
// temp name, concurrent writers open the same inode with O_TRUNC and write at
// independent offsets, so os.Rename publishes spliced garbage — measured at
// ~11% of concurrent pairs. Renaming is atomic for the directory entry, not
// for the bytes, so the temp file itself has to be unique. Every observer must
// see a complete, parseable keystore, never a torn one.
func TestSaveKeyStore_ConcurrentWritesNeverCorrupt(t *testing.T) {
	setupTempKeyStore(t)

	// Two keystores of deliberately different serialized sizes: a torn write
	// splices one into the other, which shows up as a JSON parse failure.
	small := &types.KeyStore{
		Identities: map[string]*types.Identity{"a": {Nickname: "a", Npub: "npub-a"}},
		Contacts:   make(map[string]*types.Contact),
	}
	large := &types.KeyStore{
		Identities: make(map[string]*types.Identity),
		Contacts:   make(map[string]*types.Contact),
	}
	for i := 0; i < 200; i++ {
		name := fmt.Sprintf("identity-%03d", i)
		large.Identities[name] = &types.Identity{
			Nickname: name,
			Npub:     strings.Repeat("x", 120),
			Nsec:     strings.Repeat("y", 120),
		}
	}

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ks := small
			if i%2 == 0 {
				ks = large
			}
			if err := SaveKeyStore(ks); err != nil {
				t.Errorf("SaveKeyStore failed: %v", err)
			}
		}(i)
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Concurrent reader: must never observe a torn file. A missing
			// file is fine (nothing has been renamed into place yet).
			if _, err := LoadKeyStore(); err != nil {
				t.Errorf("LoadKeyStore observed a corrupt keystore: %v", err)
			}
		}()
	}
	wg.Wait()

	final, err := LoadKeyStore()
	require.NoError(t, err, "keystore must be parseable after concurrent writes")
	assert.True(t, len(final.Identities) == 1 || len(final.Identities) == 200,
		"final keystore must be exactly one writer's complete document, got %d identities",
		len(final.Identities))

	// No temp files may be left behind.
	entries, err := os.ReadDir(GetKeyStorePath())
	require.NoError(t, err)
	for _, e := range entries {
		assert.False(t, strings.HasSuffix(e.Name(), ".tmp"),
			"leftover temp file: %s", e.Name())
	}
}

//go:build darwin || linux

package identity

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKeystoreTransactionChild(t *testing.T) {
	action := os.Getenv("HYPHAE_TEST_KEYSTORE_CHILD")
	if action == "" {
		return
	}
	ks, err := LoadKeyStore()
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println("READY")
	if _, err := bufio.NewReader(os.Stdin).ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(action, "|")
	switch parts[0] {
	case "create":
		_, err = CreateIdentityWithPassword(ks, parts[1], parts[2])
	case "contact":
		err = AddContact(ks, parts[1], parts[2])
	case "default":
		err = SetDefault(ks, parts[1])
	case "hold-lock":
		err = withKeyStoreLock(func() error {
			fmt.Println("LOCKED")
			_, err := bufio.NewReader(os.Stdin).ReadString('\n')
			return err
		})
	default:
		t.Fatalf("unknown child action")
	}
	if err != nil {
		t.Fatal(err)
	}
}

func startKeystoreChild(t *testing.T, action string) (*exec.Cmd, io.WriteCloser, *bytes.Buffer) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestKeystoreTransactionChild$")
	cmd.Env = append(os.Environ(), "HYPHAE_TEST_KEYSTORE_CHILD="+action)
	stdin, err := cmd.StdinPipe()
	require.NoError(t, err)
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	})
	reader := bufio.NewReader(stdout)
	line, err := reader.ReadString('\n')
	require.NoError(t, err)
	assert.Equal(t, "READY\n", line)
	return cmd, stdin, &stderr
}

func TestKeystoreTransactionsSerializeAcrossProcesses(t *testing.T) {
	setupTempKeyStore(t)
	ks := &types.KeyStore{Identities: map[string]*types.Identity{}, Contacts: map[string]*types.Contact{}}
	_, err := CreateIdentity(ks, "seed")
	require.NoError(t, err)
	_, err = CreateIdentity(ks, "target")
	require.NoError(t, err)
	contactNpub := common.EncodeNpub(nostr.Generate().Public())
	create, createIn, createErr := startKeystoreChild(t, "create|fresh|")
	contact, contactIn, contactErr := startKeystoreChild(t, "contact|added|"+contactNpub)
	setDefault, defaultIn, defaultErr := startKeystoreChild(t, "default|target")
	for _, in := range []interface{ Write([]byte) (int, error) }{createIn, contactIn, defaultIn} {
		_, err := in.Write([]byte("go\n"))
		require.NoError(t, err)
	}
	for _, child := range []struct {
		cmd *exec.Cmd
		err *bytes.Buffer
	}{{create, createErr}, {contact, contactErr}, {setDefault, defaultErr}} {
		require.NoError(t, child.cmd.Wait(), child.err.String())
	}
	final, err := LoadKeyStore()
	require.NoError(t, err)
	assert.Contains(t, final.Identities, "seed")
	assert.Contains(t, final.Identities, "fresh")
	assert.Contains(t, final.Contacts, "added")
	assert.Equal(t, "target", final.DefaultIdentity)
	for _, nickname := range []string{"seed", "target", "fresh"} {
		_, err := GetSecretKey(final, nickname)
		require.NoError(t, err)
	}
}

func TestEncryptedIdentityCreatesSerializeAcrossProcesses(t *testing.T) {
	setupTempKeyStore(t)
	first, firstIn, firstErr := startKeystoreChild(t, "create|alice|test-password")
	second, secondIn, secondErr := startKeystoreChild(t, "create|bob|test-password")
	_, err := firstIn.Write([]byte("go\n"))
	require.NoError(t, err)
	_, err = secondIn.Write([]byte("go\n"))
	require.NoError(t, err)
	require.NoError(t, first.Wait(), firstErr.String())
	require.NoError(t, second.Wait(), secondErr.String())
	final, err := LoadKeyStore()
	require.NoError(t, err)
	require.Contains(t, final.Identities, "alice")
	require.Contains(t, final.Identities, "bob")
	require.NoError(t, UnlockKeyStore(final, "test-password"))
	_, err = GetSecretKey(final, "alice")
	require.NoError(t, err)
	_, err = GetSecretKey(final, "bob")
	require.NoError(t, err)
}

func TestConcurrentSameNicknameCreatesCommitOnlyOnce(t *testing.T) {
	setupTempKeyStore(t)
	first, firstIn, firstErr := startKeystoreChild(t, "create|same|")
	second, secondIn, secondErr := startKeystoreChild(t, "create|same|")
	_, err := firstIn.Write([]byte("go\n"))
	require.NoError(t, err)
	_, err = secondIn.Write([]byte("go\n"))
	require.NoError(t, err)
	firstResult := first.Wait()
	secondResult := second.Wait()
	assert.True(t, (firstResult == nil) != (secondResult == nil), "exactly one create should succeed; errors: %q / %q", firstErr.String(), secondErr.String())
	final, err := LoadKeyStore()
	require.NoError(t, err)
	require.Len(t, final.Identities, 1)
	assert.Contains(t, final.Identities, "same")
}

func TestLegacyUnlockMigrationPreservesConcurrentMutations(t *testing.T) {
	setupTempKeyStore(t)
	const password = "legacy-password"
	salt, verification, err := createLegacyVerification(password)
	require.NoError(t, err)
	aliceSecret := nostr.Generate()
	saltBytes, err := base64.StdEncoding.DecodeString(salt)
	require.NoError(t, err)
	legacyKey, err := deriveMasterKey(password, saltBytes)
	require.NoError(t, err)
	aliceNsec, err := encryptWithKey(common.EncodeNsec(aliceSecret), legacyKey)
	require.NoError(t, err)
	legacy := &types.KeyStore{
		DefaultIdentity: "alice",
		Identities: map[string]*types.Identity{
			"alice": {Nickname: "alice", Npub: common.EncodeNpub(aliceSecret.Public()), Nsec: aliceNsec, Created: 1},
		},
		Contacts:     map[string]*types.Contact{},
		Encrypted:    true,
		Salt:         salt,
		Verification: verification,
	}
	require.NoError(t, SaveKeyStore(legacy))
	stale, err := LoadKeyStore()
	require.NoError(t, err)
	writer, err := LoadKeyStore()
	require.NoError(t, err)
	_, err = CreateIdentityWithPassword(writer, "bob", password)
	require.NoError(t, err)
	contactNpub := common.EncodeNpub(nostr.Generate().Public())
	require.NoError(t, AddContact(writer, "friend", contactNpub))
	require.NoError(t, SetDefault(writer, "bob"))
	require.NoError(t, UnlockKeyStore(stale, password))
	final, err := LoadKeyStore()
	require.NoError(t, err)
	require.Contains(t, final.Identities, "alice")
	require.Contains(t, final.Identities, "bob")
	assert.Contains(t, final.Contacts, "friend")
	assert.Equal(t, "bob", final.DefaultIdentity)
	assert.NotEqual(t, verification, final.Verification, "legacy verification token should be upgraded")
	require.NoError(t, UnlockKeyStore(final, password))
	for _, nickname := range []string{"alice", "bob"} {
		_, err := GetSecretKey(final, nickname)
		require.NoError(t, err)
	}
}

func TestStalePlaintextSnapshotCannotOverwriteNewEncryption(t *testing.T) {
	setupTempKeyStore(t)
	plain := &types.KeyStore{Identities: map[string]*types.Identity{}, Contacts: map[string]*types.Contact{}}
	_, err := CreateIdentity(plain, "alice")
	require.NoError(t, err)
	stale, err := LoadKeyStore()
	require.NoError(t, err)
	rotator, err := LoadKeyStore()
	require.NoError(t, err)
	require.NoError(t, EncryptKeyStore(rotator, "password"))
	err = func() error { _, err := CreateIdentityWithPassword(stale, "plaintext-leak", ""); return err }()
	var exitErr *common.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, common.ErrCodeAuth, exitErr.Code)
	final, err := LoadKeyStore()
	require.NoError(t, err)
	require.True(t, final.Encrypted)
	require.Contains(t, final.Identities, "alice")
	assert.NotContains(t, final.Identities, "plaintext-leak")
	assert.NotContains(t, final.Identities["alice"].Nsec, "nsec1")
	require.NoError(t, UnlockKeyStore(final, "password"))
	_, err = GetSecretKey(final, "alice")
	require.NoError(t, err)
}

func TestSaveKeyStoreRejectsStaleSnapshot(t *testing.T) {
	setupTempKeyStore(t)
	ks, err := LoadKeyStore()
	require.NoError(t, err)
	_, err = CreateIdentity(ks, "seed")
	require.NoError(t, err)
	first, err := LoadKeyStore()
	require.NoError(t, err)
	stale, err := LoadKeyStore()
	require.NoError(t, err)
	first.PasswordHint = "new state"
	stale.PasswordHint = "stale state"
	require.NoError(t, SaveKeyStore(first))
	err = SaveKeyStore(stale)
	var exitErr *common.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, common.ErrCodeWriteConflict, exitErr.Code)
	final, err := LoadKeyStore()
	require.NoError(t, err)
	assert.Equal(t, "new state", final.PasswordHint)
}

func TestCreateIdentityChecksSuppliedPasswordEvenWhenCallerIsUnlocked(t *testing.T) {
	setupTempKeyStore(t)
	ks := &types.KeyStore{Identities: map[string]*types.Identity{}, Contacts: map[string]*types.Contact{}}
	_, err := CreateIdentityWithPassword(ks, "alice", "correct-password")
	require.NoError(t, err)
	caller, err := LoadKeyStore()
	require.NoError(t, err)
	require.NoError(t, UnlockKeyStore(caller, "correct-password"))
	before, err := os.ReadFile(filepath.Join(GetKeyStorePath(), KeyStoreFile))
	require.NoError(t, err)
	err = func() error { _, err := CreateIdentityWithPassword(caller, "bob", "wrong-password"); return err }()
	var exitErr *common.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, common.ErrCodeAuth, exitErr.Code)
	after, err := os.ReadFile(filepath.Join(GetKeyStorePath(), KeyStoreFile))
	require.NoError(t, err)
	assert.Equal(t, before, after, "failed authentication must leave disk unchanged")
	final, err := LoadKeyStore()
	require.NoError(t, err)
	assert.NotContains(t, final.Identities, "bob")
}

func TestOldPasswordSnapshotCannotWriteAfterPasswordRotation(t *testing.T) {
	setupTempKeyStore(t)
	ks := &types.KeyStore{Identities: map[string]*types.Identity{}, Contacts: map[string]*types.Contact{}}
	_, err := CreateIdentityWithPassword(ks, "alice", "old-password")
	require.NoError(t, err)
	stale, err := LoadKeyStore()
	require.NoError(t, err)
	require.NoError(t, UnlockKeyStore(stale, "old-password"))
	rotator, err := LoadKeyStore()
	require.NoError(t, err)
	require.NoError(t, ChangePassword(rotator, "old-password", "new-password"))
	err = SaveKeyStore(stale)
	var conflictErr *common.ExitError
	require.ErrorAs(t, err, &conflictErr)
	assert.Equal(t, common.ErrCodeWriteConflict, conflictErr.Code)
	err = func() error { _, err := CreateIdentityWithPassword(stale, "stale", "old-password"); return err }()
	var exitErr *common.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, common.ErrCodeAuth, exitErr.Code)
	final, err := LoadKeyStore()
	require.NoError(t, err)
	assert.NotContains(t, final.Identities, "stale")
	require.NoError(t, UnlockKeyStore(final, "new-password"))
	_, err = GetSecretKey(final, "alice")
	require.NoError(t, err)
}

func TestKeystoreLockPermissionsAndProcessExitRelease(t *testing.T) {
	setupTempKeyStore(t)
	require.NoError(t, SaveKeyStore(&types.KeyStore{Identities: map[string]*types.Identity{}, Contacts: map[string]*types.Contact{}}))
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestKeystoreTransactionChild$")
	cmd.Env = append(os.Environ(), "HYPHAE_TEST_KEYSTORE_CHILD=hold-lock")
	stdin, err := cmd.StdinPipe()
	require.NoError(t, err)
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	})
	reader := bufio.NewReader(stdout)
	_, err = reader.ReadString('\n')
	require.NoError(t, err)
	_, err = stdin.Write([]byte("go\n"))
	require.NoError(t, err)
	line, err := reader.ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "LOCKED\n", line)
	lockPath := filepath.Join(GetKeyStorePath(), KeyStoreFile+".lock")
	info, err := os.Stat(lockPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), info.Mode().Perm())
	storeData, err := os.ReadFile(filepath.Join(GetKeyStorePath(), KeyStoreFile))
	require.NoError(t, err)
	assert.NotContains(t, string(storeData), "StoreVersion")
	assert.NotContains(t, string(storeData), "StoreExists")
	storeInfo, err := os.Stat(filepath.Join(GetKeyStorePath(), KeyStoreFile))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), storeInfo.Mode().Perm())
	dirInfo, err := os.Stat(GetKeyStorePath())
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0700), dirInfo.Mode().Perm())
	require.NoError(t, cmd.Process.Kill())
	_ = cmd.Wait()
	done := make(chan error, 1)
	go func() { done <- withKeyStoreLock(func() error { return nil }) }()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("process exit did not release keystore lock")
	}
}

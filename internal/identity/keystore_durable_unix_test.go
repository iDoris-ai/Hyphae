//go:build darwin || linux

package identity

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestKeystoreDurableChild writes a contact but parks between the temp file
// being written and the rename, so the parent can kill -9 it mid-write.
func TestKeystoreDurableChild(t *testing.T) {
	if os.Getenv("HYPHAE_TEST_KS_DURABLE_CHILD") != "1" {
		return
	}
	requireSubprocessGoEnv(t)
	ks, err := LoadKeyStore()
	require.NoError(t, err)
	beforeRenameHook = func() {
		os.Stdout.WriteString("AT_RENAME\n")
		time.Sleep(time.Hour) // not select{}: the runtime would abort on deadlock and drop the lock
	}
	_ = AddContact(ks, "victim", os.Getenv("HYPHAE_TEST_KS_DURABLE_NPUB"))
}

// startParkedWriter launches a child that holds the keystore lock with a fully
// written temp file on disk. It returns once the child has reached that point.
func startParkedWriter(t *testing.T, npub string) *exec.Cmd {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestKeystoreDurableChild$")
	cmd.Env = append(withSubprocessGoEnv(os.Environ()), "HYPHAE_TEST_KS_DURABLE_CHILD=1", "HYPHAE_TEST_KS_DURABLE_NPUB="+npub)
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	line, err := bufio.NewReader(stdout).ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "AT_RENAME\n", line)
	return cmd
}

func tempKeystores(t *testing.T) []string {
	t.Helper()
	m, err := filepath.Glob(filepath.Join(GetKeyStorePath(), KeyStoreFile+".*.tmp"))
	require.NoError(t, err)
	return m
}

func TestKilledWriterTempFileIsRemovedByNextWrite(t *testing.T) {
	setupTempKeyStore(t)
	seed, err := LoadKeyStore()
	require.NoError(t, err)
	ident, err := CreateIdentity(seed, "seed")
	require.NoError(t, err)
	require.NotEmpty(t, ident.Nsec)
	npub := common.EncodeNpub(nostr.Generate().Public())
	child := startParkedWriter(t, npub)

	temps := tempKeystores(t)
	require.Len(t, temps, 1)
	data, err := os.ReadFile(temps[0])
	require.NoError(t, err)
	require.Contains(t, string(data), ident.Nsec, "fixture must leave a plaintext-nsec temp file behind")

	require.NoError(t, child.Process.Kill()) // kill -9 equivalent
	_ = child.Wait()
	require.Len(t, tempKeystores(t), 1, "kill -9 leaves the orphan")

	// Inside the grace period the file is left alone; once old it is removed.
	ks, err := LoadKeyStore()
	require.NoError(t, err)
	require.NoError(t, AddContact(ks, "early", common.EncodeNpub(nostr.Generate().Public())))
	require.Len(t, tempKeystores(t), 1, "fresh file is within the grace period")
	old := time.Now().Add(-2 * staleTempMinAge)
	require.NoError(t, os.Chtimes(temps[0], old, old))

	ks, err = LoadKeyStore()
	require.NoError(t, err)
	require.NoError(t, AddContact(ks, "after", common.EncodeNpub(nostr.Generate().Public())))
	assert.Empty(t, tempKeystores(t), "next write must delete the orphan")
	final, err := LoadKeyStore()
	require.NoError(t, err)
	assert.Contains(t, final.Contacts, "after")
	assert.NotContains(t, final.Contacts, "victim", "the killed write never committed")
}

func TestLiveWriterTempFileIsNeverRemoved(t *testing.T) {
	setupTempKeyStore(t)
	child := startParkedWriter(t, common.EncodeNpub(nostr.Generate().Public()))
	live := tempKeystores(t)
	require.Len(t, live, 1)

	done := make(chan error, 1)
	go func() {
		ks, err := LoadKeyStore()
		if err == nil {
			err = AddContact(ks, "waiter", common.EncodeNpub(nostr.Generate().Public()))
		}
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("second writer must block on the live writer's lock, returned: %v", err)
	case <-time.After(500 * time.Millisecond):
	}
	_, err := os.Stat(live[0])
	require.NoError(t, err, "live writer's temp file must survive while it holds the lock")
	// Age it so only the lock (not the grace period) protected it so far.
	old := time.Now().Add(-2 * staleTempMinAge)
	require.NoError(t, os.Chtimes(live[0], old, old))

	require.NoError(t, child.Process.Kill())
	_ = child.Wait()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("second writer did not proceed after the lock holder died")
	}
	assert.Empty(t, tempKeystores(t))
}

func TestStaleCleanupOnlyTouchesExactRegularTempFiles(t *testing.T) {
	setupTempKeyStore(t)
	_, err := EnsureKeyStore()
	require.NoError(t, err)
	dir := GetKeyStorePath()
	outside := filepath.Join(t.TempDir(), "victim-target")
	require.NoError(t, os.WriteFile(outside, []byte("keep"), 0600))

	orphan := filepath.Join(dir, KeyStoreFile+".123456.tmp")
	require.NoError(t, os.WriteFile(orphan, []byte("{}"), 0600))
	old := time.Now().Add(-2 * staleTempMinAge)
	require.NoError(t, os.Chtimes(orphan, old, old))
	fresh := filepath.Join(dir, KeyStoreFile+".999.tmp")
	require.NoError(t, os.WriteFile(fresh, []byte("{}"), 0600))

	keep := []string{
		fresh, // within grace period (could be a pre-lock writer)
		filepath.Join(dir, KeyStoreFile+".abc.tmp"),    // non-numeric
		filepath.Join(dir, KeyStoreFile+".12.tmp.bak"), // wrong suffix
		filepath.Join(dir, "other.123.tmp"),            // wrong prefix
		filepath.Join(dir, KeyStoreFile+".bak"),        // backup
	}
	for _, p := range keep {
		require.NoError(t, os.WriteFile(p, []byte("x"), 0600))
	}
	symlink := filepath.Join(dir, KeyStoreFile+".777.tmp")
	require.NoError(t, os.Symlink(outside, symlink))
	subdir := filepath.Join(dir, KeyStoreFile+".888.tmp")
	require.NoError(t, os.Mkdir(subdir, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(subdir, "inner"), []byte("x"), 0600))

	ks, err := LoadKeyStore()
	require.NoError(t, err)
	require.NoError(t, AddContact(ks, "c", common.EncodeNpub(nostr.Generate().Public())))

	_, err = os.Lstat(orphan)
	assert.True(t, os.IsNotExist(err), "exact-name regular orphan is removed")
	for _, p := range keep {
		_, err := os.Lstat(p)
		assert.NoError(t, err, "must keep %s", filepath.Base(p))
	}
	fi, err := os.Lstat(symlink)
	require.NoError(t, err, "symlink must be left alone")
	assert.True(t, fi.Mode()&os.ModeSymlink != 0)
	got, err := os.ReadFile(outside)
	require.NoError(t, err)
	assert.Equal(t, "keep", strings.TrimSpace(string(got)))
	_, err = os.Stat(filepath.Join(subdir, "inner"))
	assert.NoError(t, err, "directory must be left alone")
}

func TestSaveSyncsDirectoryAfterRename(t *testing.T) {
	setupTempKeyStore(t)
	calls := 0
	var seen string
	orig := syncDirFn
	syncDirFn = func(dir string) {
		calls++
		data, err := os.ReadFile(filepath.Join(dir, KeyStoreFile))
		require.NoError(t, err, "keystore must already be renamed into place when the dir is synced")
		seen = string(data)
		orig(dir)
	}
	t.Cleanup(func() { syncDirFn = orig })

	ks, err := LoadKeyStore()
	require.NoError(t, err)
	npub := common.EncodeNpub(nostr.Generate().Public())
	require.NoError(t, AddContact(ks, "synced", npub))
	assert.Equal(t, 1, calls)
	assert.Contains(t, seen, npub)
	assert.True(t, ks.StoreExists)
	assert.NotEmpty(t, ks.StoreVersion)
}

func TestSyncDirIsBestEffort(t *testing.T) {
	syncDir(filepath.Join(t.TempDir(), "does-not-exist")) // must not panic
}

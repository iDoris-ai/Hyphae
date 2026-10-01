//go:build darwin || linux

package identity

import (
	"fmt"
	"os"
	"syscall"
)

// withKeyStoreLock serializes writers through a stable sibling inode. The
// lock file must remain in place: deleting it can split concurrent writers
// across different inodes. External callers must not hold this lock and then
// launch Hyphae, which would wait on the same lock.
func withKeyStoreLock(fn func() error) error {
	path, err := EnsureKeyStore()
	if err != nil {
		return err
	}
	lock, err := os.OpenFile(keyStoreLockPath(path), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	if err != nil {
		return fmt.Errorf("open keystore lock: %w", err)
	}
	defer lock.Close()
	info, err := lock.Stat()
	if err != nil {
		return fmt.Errorf("inspect keystore lock: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("keystore lock path is not a regular file")
	}
	if err := lock.Chmod(0600); err != nil {
		return fmt.Errorf("protect keystore lock: %w", err)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("lock keystore: %w", err)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	return fn()
}

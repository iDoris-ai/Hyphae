//go:build darwin || linux

package messaging

import (
	"fmt"
	"os"
	"syscall"
)

// withOutboxLock uses a stable sibling file so atomic replacement of the
// outbox itself never changes the inode used for coordination.
func withOutboxLock(path string, fn func() error) error {
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return fmt.Errorf("open outbox lock: %w", err)
	}
	defer lock.Close()
	if err := lock.Chmod(0600); err != nil {
		return fmt.Errorf("protect outbox lock: %w", err)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("lock outbox: %w", err)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	return fn()
}

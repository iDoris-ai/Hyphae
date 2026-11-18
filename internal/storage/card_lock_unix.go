//go:build darwin || linux

package storage

import (
	"fmt"
	"os"
	"syscall"
)

// WithCardLock runs fn holding an exclusive lock shared by every process
// that publishes a card (CLI and daemon), so load, publish and save happen
// as one step and concurrent writers cannot drop relays from the setting.
func WithCardLock(fn func() error) error {
	dbPath, err := GetDBPath()
	if err != nil {
		return err
	}
	lock, err := os.OpenFile(dbPath+".card.lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return fmt.Errorf("open card lock: %w", err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("lock card setting: %w", err)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	return fn()
}

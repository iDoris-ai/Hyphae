//go:build linux || darwin

package daemon

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/iDoris-ai/hyphae/internal/identity"
)

var errDaemonHomeLocked = errors.New("a daemon is already running for this home")

type daemonHomeLock struct {
	file *os.File
}

func acquireDaemonHomeLock() (*daemonHomeLock, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve home directory: %w", err)
	}
	dir := filepath.Join(home, identity.KeyStoreDirName)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("create keystore directory: %w", err)
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return nil, fmt.Errorf("set keystore directory permissions: %w", err)
	}

	path := filepath.Join(dir, "daemon.lock")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, fmt.Errorf("open daemon lock file: %w", err)
	}
	closeOnError := func(err error) (*daemonHomeLock, error) {
		_ = file.Close()
		return nil, err
	}
	if err := file.Chmod(0600); err != nil {
		return closeOnError(fmt.Errorf("set daemon lock permissions: %w", err))
	}
	info, err := file.Stat()
	if err != nil {
		return closeOnError(fmt.Errorf("inspect daemon lock file: %w", err))
	}
	if !info.Mode().IsRegular() {
		return closeOnError(fmt.Errorf("daemon lock path is not a regular file"))
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return closeOnError(errDaemonHomeLocked)
		}
		return closeOnError(fmt.Errorf("lock daemon home: %w", err))
	}
	return &daemonHomeLock{file: file}, nil
}

func (lock *daemonHomeLock) Close() error {
	if lock == nil || lock.file == nil {
		return nil
	}
	unlockErr := syscall.Flock(int(lock.file.Fd()), syscall.LOCK_UN)
	closeErr := lock.file.Close()
	lock.file = nil
	return errors.Join(unlockErr, closeErr)
}

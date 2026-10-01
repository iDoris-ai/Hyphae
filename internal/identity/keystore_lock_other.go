//go:build !darwin && !linux

package identity

import "errors"

func withKeyStoreLock(func() error) error {
	return errors.New("keystore write locking is unsupported on this platform")
}

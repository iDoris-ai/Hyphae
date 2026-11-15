//go:build !darwin && !linux

package storage

import (
	"errors"
	"fmt"
)

// WithCardLock is unsupported here, like the outbox lock.
func WithCardLock(fn func() error) error {
	return fmt.Errorf("card setting locking is unsupported on this platform: %w", errors.ErrUnsupported)
}

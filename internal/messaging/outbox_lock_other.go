//go:build !darwin && !linux

package messaging

import (
	"errors"
	"fmt"
)

func withOutboxLock(path string, fn func() error) error {
	return fmt.Errorf("outbox process locking is unsupported on this platform: %w", errors.ErrUnsupported)
}

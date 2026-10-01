//go:build !linux && !darwin

package daemon

import (
	"errors"
	"fmt"
)

var errDaemonHomeLocked = errors.New("a daemon is already running for this home")

type daemonHomeLock struct{}

func acquireDaemonHomeLock() (*daemonHomeLock, error) {
	return nil, fmt.Errorf("daemon home locking is supported only on Linux and macOS")
}

func (*daemonHomeLock) Close() error { return nil }

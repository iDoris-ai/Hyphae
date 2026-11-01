//go:build darwin || linux

package identity

import "syscall"

// exportNoFollow makes the --out open fail instead of following a symlink.
const exportNoFollow = syscall.O_NOFOLLOW

const exportOutSupported = true

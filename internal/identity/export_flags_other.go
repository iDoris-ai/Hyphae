//go:build !darwin && !linux

package identity

// exportNoFollow is unavailable on this platform; O_EXCL still refuses any
// existing target, including a symlink.
const exportNoFollow = 0

// exportOutSupported is false where 0600 does not give owner-only access (e.g. Windows ACLs).
const exportOutSupported = false

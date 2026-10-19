//go:build unix

package deployment

import (
	"os"
	"syscall"
)

func currentUID() (int, error) { return syscall.Geteuid(), nil }

func fileIdentity(info os.FileInfo) (int, uint64, error) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st == nil {
		return 0, 0, closedErr("WRONG_OWNER", "file owner is unavailable")
	}
	return int(st.Uid), uint64(st.Nlink), nil
}

func openNoFollowOS(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
}

func execBinary(argv0 string, argv []string, envv []string) error {
	if !isCleanAbs(argv0) || len(argv) == 0 || argv[0] != argv0 {
		return closedErr("PATH_UNSAFE", "refusing a non-pinned executable")
	}
	return syscall.Exec(argv0, argv, envv)
}

//go:build darwin || linux

package identity

import (
	"io"
	"os"
	"syscall"
)

var errKeystoreNotRegular = os.ErrInvalid

func readRegularKeystoreFile(path string) ([]byte, error) {
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errKeystoreNotRegular
	}
	return io.ReadAll(file)
}

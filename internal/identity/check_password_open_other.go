//go:build !darwin && !linux

package identity

import (
	"os"
)

var errKeystoreNotRegular = os.ErrInvalid

func readRegularKeystoreFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errKeystoreNotRegular
	}
	return os.ReadFile(path)
}

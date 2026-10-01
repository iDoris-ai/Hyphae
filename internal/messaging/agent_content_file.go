package messaging

import (
	"fmt"
	"io"
	"os"
	"syscall"
	"unicode/utf8"
)

const maxMessageContentFileBytes = 1 << 20

func readMessageContentFile(path string) (string, error) {
	if path == "-" {
		return "", fmt.Errorf("content file must be a regular file")
	}
	pathInfo, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("unable to read message content file")
	}
	if !pathInfo.Mode().IsRegular() {
		return "", fmt.Errorf("content file must be a regular file")
	}

	// O_NONBLOCK makes opening a FIFO safe; it has no effect for regular files.
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", fmt.Errorf("unable to read message content file")
	}
	f := os.NewFile(uintptr(fd), "message-content")
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("content file must be a regular file")
	}
	if info.Size() > maxMessageContentFileBytes {
		return "", fmt.Errorf("message content file exceeds 1 MiB")
	}
	b, err := io.ReadAll(io.LimitReader(f, maxMessageContentFileBytes+1))
	if err != nil {
		return "", fmt.Errorf("unable to read message content file")
	}
	if len(b) > maxMessageContentFileBytes {
		return "", fmt.Errorf("message content file exceeds 1 MiB")
	}
	if !utf8.Valid(b) {
		return "", fmt.Errorf("message content file must contain valid UTF-8")
	}
	return string(b), nil
}

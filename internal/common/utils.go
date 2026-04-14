package common

import (
	"fmt"
	"os"
	"syscall"

	"golang.org/x/term"
)

// TruncateString 截断字符串
func TruncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// ReadSecretKey 安全地读取密钥
func ReadSecretKey(prompt string) (string, error) {
	if prompt == "" {
		prompt = "Secret key (nsec or hex): "
	}
	fmt.Fprint(os.Stderr, prompt)

	bytePassword, err := term.ReadPassword(int(syscall.Stdin))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}

	secKey := string(bytePassword)
	if secKey == "" {
		return "", fmt.Errorf("secret key is required")
	}

	return secKey, nil
}

// PromptSecretKey 提示输入密钥（从 stdin）
func PromptSecretKey() (string, error) {
	fmt.Print("Enter your nsec or hex secret key: ")
	var secKey string
	fmt.Scanln(&secKey)
	if secKey == "" {
		return "", fmt.Errorf("secret key is required")
	}
	return secKey, nil
}

package notify

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// DesktopNotification sends a desktop notification
func DesktopNotification(title, message string) error {
	switch runtime.GOOS {
	case "darwin":
		return notifyDarwin(title, message)
	case "linux":
		return notifyLinux(title, message)
	default:
		return fmt.Errorf("desktop notifications not supported on %s", runtime.GOOS)
	}
}

// notifyDarwin sends notification on macOS
func notifyDarwin(title, message string) error {
	// Try terminal-notifier first
	if _, err := exec.LookPath("terminal-notifier"); err == nil {
		cmd := exec.Command("terminal-notifier",
			"-title", title,
			"-message", message,
			"-sound", "default")
		return cmd.Run()
	}

	// Fallback to osascript
	script := fmt.Sprintf(`display notification "%s" with title "%s" sound name "default"`,
		escapeAppleScript(message), escapeAppleScript(title))
	cmd := exec.Command("osascript", "-e", script)
	return cmd.Run()
}

// notifyLinux sends notification on Linux
func notifyLinux(title, message string) error {
	// Try notify-send
	if _, err := exec.LookPath("notify-send"); err == nil {
		cmd := exec.Command("notify-send", title, message)
		return cmd.Run()
	}
	return fmt.Errorf("notify-send not found")
}

// escapeAppleScript escapes string for AppleScript
func escapeAppleScript(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return s
}

// PlaySound plays a notification sound
func PlaySound() error {
	switch runtime.GOOS {
	case "darwin":
		cmd := exec.Command("afplay", "/System/Library/Sounds/Glass.aiff")
		return cmd.Run()
	default:
		return nil
	}
}

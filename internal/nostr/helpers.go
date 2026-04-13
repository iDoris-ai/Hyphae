package nostr

import (
	"strconv"
)

// MustAtoi converts string to int, panics on error
func MustAtoi(s string) int {
	i, _ := strconv.Atoi(s)
	return i
}

package daemon

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsAutoReplyMessage(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"[auto-reply] bob received your message", true},
		{"[auto-reply] alice received your message: hello", true},
		{"Hello, how are you?", false},
		{"", false},
		{"[auto-reply]", false}, // too short
		{"not [auto-reply] prefix", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, isAutoReplyMessage(tt.input))
		})
	}
}

package wireevent

import (
	"strings"
	"testing"

	"fiatjaf.com/nostr"
)

func TestClassify30078RecognizesProfileAndMessages(t *testing.T) {
	tests := []struct {
		name string
		tags nostr.Tags
		want Class
	}{
		{
			name: "profile",
			tags: nostr.Tags{{"d", ProfileDTag}, {"c", ProfileCategory}},
			want: ClassProfile,
		},
		{
			name: "new namespaced message",
			tags: messageTags(MessageDPrefix + strings.Repeat("a", 16)),
			want: ClassMessage,
		},
		{
			name: "namespaced auto-reply token",
			tags: messageTags(MessageDPrefix + strings.Repeat("a", 32)),
			want: ClassMessage,
		},
		{
			name: "namespaced full hash token",
			tags: messageTags(MessageDPrefix + strings.Repeat("a", 64)),
			want: ClassMessage,
		},
		{
			name: "legacy 16-hex d message",
			tags: messageTags(strings.Repeat("b", 16)),
			want: ClassMessage,
		},
		{
			name: "legacy auto-reply 32-hex d message",
			tags: messageTags(strings.Repeat("c", 32)),
			want: ClassMessage,
		},
		{
			name: "legacy message without d",
			tags: nostr.Tags{{"c", MessageCategory}, {"v", MessageVersion}, {"p", strings.Repeat("d", 64)}},
			want: ClassMessage,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Classify30078(tt.tags)
			if err != nil {
				t.Fatalf("Classify30078() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("Classify30078() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestClassify30078FailsClosedOnAmbiguousOrUnknownTags(t *testing.T) {
	validP := strings.Repeat("a", 64)
	tests := []struct {
		name string
		tags nostr.Tags
	}{
		{name: "missing c", tags: nostr.Tags{{"v", MessageVersion}, {"p", validP}}},
		{name: "unknown c namespace", tags: nostr.Tags{{"c", "future"}, {"v", MessageVersion}, {"p", validP}}},
		{name: "duplicate c", tags: nostr.Tags{{"c", MessageCategory}, {"c", MessageCategory}, {"v", MessageVersion}, {"p", validP}}},
		{name: "duplicate d", tags: nostr.Tags{{"c", MessageCategory}, {"v", MessageVersion}, {"p", validP}, {"d", strings.Repeat("a", 16)}, {"d", strings.Repeat("b", 16)}}},
		{name: "duplicate version", tags: nostr.Tags{{"c", MessageCategory}, {"v", MessageVersion}, {"v", MessageVersion}, {"p", validP}}},
		{name: "duplicate recipient", tags: nostr.Tags{{"c", MessageCategory}, {"v", MessageVersion}, {"p", validP}, {"p", validP}}},
		{name: "malformed classification tag", tags: nostr.Tags{{"c", MessageCategory, "extra"}, {"v", MessageVersion}, {"p", validP}}},
		{name: "message with profile namespace", tags: nostr.Tags{{"c", MessageCategory}, {"v", MessageVersion}, {"p", validP}, {"d", ProfileDTag}}},
		{name: "unknown d namespace", tags: nostr.Tags{{"c", MessageCategory}, {"v", MessageVersion}, {"p", validP}, {"d", "agent-other:abc"}}},
		{name: "malformed namespaced d", tags: nostr.Tags{{"c", MessageCategory}, {"v", MessageVersion}, {"p", validP}, {"d", MessageDPrefix + "not-hex"}}},
		{name: "profile with message tags", tags: nostr.Tags{{"c", ProfileCategory}, {"d", ProfileDTag}, {"v", MessageVersion}}},
		{name: "profile with recipient", tags: nostr.Tags{{"c", ProfileCategory}, {"d", ProfileDTag}, {"p", validP}}},
		{name: "profile with duplicate c", tags: nostr.Tags{{"c", ProfileCategory}, {"c", ProfileCategory}, {"d", ProfileDTag}}},
		{name: "message recipient not 32-byte hex", tags: nostr.Tags{{"c", MessageCategory}, {"v", MessageVersion}, {"p", "recipient"}}},
		{name: "message missing version", tags: nostr.Tags{{"c", MessageCategory}, {"p", validP}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, err := Classify30078(tt.tags); err == nil {
				t.Fatalf("Classify30078() = %v, want error", got)
			}
		})
	}
}

func messageTags(d string) nostr.Tags {
	return nostr.Tags{
		{"p", strings.Repeat("e", 64)},
		{"c", MessageCategory},
		{"z", "zstd"},
		{"v", MessageVersion},
		{"d", d},
		{"enc", "nip44"},
	}
}

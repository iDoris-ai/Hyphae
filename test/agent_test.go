package main

import (
	"strings"
	"testing"

	"fiatjaf.com/nostr"
	"github.com/stretchr/testify/assert"
)

// TestAgentCmdRegistration tests that agent commands are properly registered
func TestAgentCmdRegistration(t *testing.T) {
	assert.NotNil(t, agentCmd, "agentCmd should be registered")
	assert.Equal(t, "agent", agentCmd.Name)
	assert.Equal(t, "agent-specific nostr communication tools", agentCmd.Usage)

	// Check subcommands
	subcommands := []string{"msg", "query", "relay", "timeline"}
	for _, name := range subcommands {
		found := false
		for _, cmd := range agentCmd.Commands {
			if cmd.Name == name {
				found = true
				break
			}
		}
		assert.True(t, found, "Subcommand %s should be registered", name)
	}
}

// TestDecodeNpub tests npub decoding
func TestDecodeNpub(t *testing.T) {
	tests := []struct {
		name    string
		npub    string
		wantErr bool
	}{
		{
			name:    "valid npub",
			npub:    "npub180cvv07tjdrrgpa0j7j7tmnyl2yr6yr7l8j4s3evf6u64th6gkwsyjh6w",
			wantErr: false,
		},
		{
			name:    "invalid npub prefix",
			npub:    "nsec180cvv07tjdrrgpa0j7j7tmnyl2yr6yr7l8j4s3evf6u64th6gkwsyjh6w",
			wantErr: true,
		},
		{
			name:    "invalid npub",
			npub:    "npubinvalid",
			wantErr: true,
		},
		{
			name:    "empty string",
			npub:    "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := decodeNpub(tt.npub)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.NotEmpty(t, result)
				assert.Len(t, result, 64) // hex pubkey is 64 chars
			}
		})
	}
}

// TestDefaultRelays tests default relay configuration
func TestDefaultRelays(t *testing.T) {
	assert.NotEmpty(t, defaultRelays, "defaultRelays should not be empty")
	
	expectedRelays := []string{
		"wss://relay.damus.io",
		"wss://nos.lol",
		"wss://relay.nostr.band",
	}
	
	assert.Equal(t, expectedRelays, defaultRelays, "Default relays should match expected")
	
	// Verify all relays use wss://
	for _, relay := range defaultRelays {
		assert.True(t, strings.HasPrefix(relay, "ws"), "Relay %s should use ws", relay)
	}
}

// TestAgentConstants tests constant definitions
func TestAgentConstants(t *testing.T) {
	assert.Equal(t, 30078, AgentKind, "AgentKind should be 30078")
	assert.Equal(t, "v1", AgentVersion, "AgentVersion should be v1")
	assert.Equal(t, "zstd", CompressTag, "CompressTag should be zstd")
	assert.Equal(t, "agent", AgentTag, "AgentTag should be agent")
}

// TestCompressText tests compression function
func TestCompressText(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "simple text",
			input: "Hello, World!",
		},
		{
			name:  "empty string",
			input: "",
		},
		{
			name:  "json content",
			input: `{"kind":30078,"content":"test"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := compressText(tt.input)
			assert.NoError(t, err)
			// For now compressText is a passthrough
			assert.Equal(t, tt.input, result)
		})
	}
}

// TestAgentMsgCmdFlags tests msg command flags
func TestAgentMsgCmdFlags(t *testing.T) {
	cmd := agentMsgCmd
	
	// Check required flags exist
	flagNames := make(map[string]bool)
	for _, flag := range cmd.Flags {
		flagNames[flag.Names()[0]] = true
	}
	
	assert.True(t, flagNames["to"], "should have 'to' flag")
	assert.True(t, flagNames["relay"], "should have 'relay' flag")
	assert.True(t, flagNames["compress"], "should have 'compress' flag")
}

// TestAgentQueryCmdFlags tests query command flags
func TestAgentQueryCmdFlags(t *testing.T) {
	cmd := agentQueryCmd
	
	flagNames := make(map[string]bool)
	for _, flag := range cmd.Flags {
		flagNames[flag.Names()[0]] = true
	}
	
	assert.True(t, flagNames["kinds"], "should have 'kinds' flag")
	assert.True(t, flagNames["authors"], "should have 'authors' flag")
	assert.True(t, flagNames["relay"], "should have 'relay' flag")
	assert.True(t, flagNames["limit"], "should have 'limit' flag")
	assert.True(t, flagNames["decompress"], "should have 'decompress' flag")
}

// TestAgentRelayCmdSubcommands tests relay subcommands
func TestAgentRelayCmdSubcommands(t *testing.T) {
	cmd := agentRelayCmd
	
	assert.Len(t, cmd.Commands, 2, "relay command should have 2 subcommands")
	
	subcommandNames := make(map[string]bool)
	for _, sub := range cmd.Commands {
		subcommandNames[sub.Name] = true
	}
	
	assert.True(t, subcommandNames["start"], "should have 'start' subcommand")
	assert.True(t, subcommandNames["status"], "should have 'status' subcommand")
}

// TestAgentTimelineCmdAliases tests timeline aliases
func TestAgentTimelineCmdAliases(t *testing.T) {
	cmd := agentTimelineCmd
	
	assert.Contains(t, cmd.Aliases, "tl", "timeline should have 'tl' alias")
}

// BenchmarkDecodeNpub benchmarks npub decoding
func BenchmarkDecodeNpub(b *testing.B) {
	npub := "npub180cvv07tjdrrgpa0j7j7tmnyl2yr6yr7l8j4s3evf6u64th6gkwsyjh6w"
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := decodeNpub(npub)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// TestKind30078Specific tests agent-specific kind 30078
func TestKind30078Specific(t *testing.T) {
	assert.Equal(t, 30078, AgentKind, "AgentKind should be 30078")
	
	// Verify it's in the valid kind range for application-specific
	assert.True(t, AgentKind >= 30000 && AgentKind < 40000, 
		"AgentKind should be in application-specific range (30000-39999)")
}

// TestAgentEventTags tests agent event tag structure
func TestAgentEventTags(t *testing.T) {
	// Create an event with agent tags
	ev := &nostr.Event{
		Kind:      AgentKind,
		Content:   "test content",
		CreatedAt: nostr.Now(),
		Tags: nostr.Tags{
			{"c", AgentTag},
			{"z", CompressTag},
			{"p", "79be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798"},
		},
	}
	
	// Verify tags
	hasAgentTag := false
	hasCompressTag := false
	hasPTag := false
	
	for _, tag := range ev.Tags {
		if len(tag) >= 2 {
			switch tag[0] {
			case "c":
				if tag[1] == AgentTag {
					hasAgentTag = true
				}
			case "z":
				if tag[1] == CompressTag {
					hasCompressTag = true
				}
			case "p":
				hasPTag = true
			}
		}
	}
	
	assert.True(t, hasAgentTag, "Should have agent tag")
	assert.True(t, hasCompressTag, "Should have compression tag")
	assert.True(t, hasPTag, "Should have p tag")
}

package main

import (
	"encoding/hex"
	stdjson "encoding/json"
	"strings"
	"testing"

	"fiatjaf.com/nostr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// NAK Regression Tests
// These tests ensure nak original functionality still works
// ============================================================================

func testCall(t *testing.T, cmd string) string {
	var output strings.Builder
	stdout = func(a ...any) {
		output.WriteString(strings.TrimSpace(strings.Join(convertToStrings(a), " ")))
		output.WriteString("\n")
	}
	err := app.Run(t.Context(), strings.Split(cmd, " "))
	require.NoError(t, err)
	return strings.TrimSpace(output.String())
}

func convertToStrings(a []any) []string {
	result := make([]string, len(a))
	for i, v := range a {
		result[i] = toString(v)
	}
	return result
}

func toString(v any) string {
	switch val := v.(type) {
	case string:
		return val
	case nostr.ID:
		return val.String()
	case nostr.PubKey:
		return val.String()
	case int:
		return string(rune(val))
	default:
		return ""
	}
}

// TestNakEventBasic tests basic event generation
func TestNakEventBasic(t *testing.T) {
	output := testCall(t, "nak event --ts 1699485669")

	var evt nostr.Event
	err := stdjson.Unmarshal([]byte(output), &evt)
	require.NoError(t, err)

	assert.Equal(t, nostr.Kind(1), evt.Kind)
	assert.Equal(t, nostr.Timestamp(1699485669), evt.CreatedAt)
	assert.Equal(t, "hello from the nostr army knife", evt.Content)
	assert.Equal(t, "36d88cf5fcc449f2390a424907023eda7a74278120eebab8d02797cd92e7e29c", evt.ID.Hex())
	assert.Equal(t, "79be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798", evt.PubKey.Hex())
	assert.Equal(t, "68e71a192e8abcf8582a222434ac823ecc50607450ebe8cc4c145eb047794cc382dc3f888ce879d2f404f5ba6085a47601360a0fa2dd4b50d317bd0c6197c2c2", hex.EncodeToString(evt.Sig[:]))
}

// TestNakEventComplex tests complex event generation
func TestNakEventComplex(t *testing.T) {
	output := testCall(t, "nak event --ts 1699485669 -k 11 -c skjdbaskd --sec 17 -t t=spam -e 36d88cf5fcc449f2390a424907023eda7a74278120eebab8d02797cd92e7e29c -t r=https://abc.def?name=foobar;nothing")

	var evt nostr.Event
	err := stdjson.Unmarshal([]byte(output), &evt)
	require.NoError(t, err)

	assert.Equal(t, nostr.Kind(11), evt.Kind)
	assert.Equal(t, "skjdbaskd", evt.Content)
	assert.Len(t, evt.Tags, 3)
}

// TestNakKeyGenerate tests key generation
func TestNakKeyGenerate(t *testing.T) {
	output := testCall(t, "nak key generate")
	
	// Should output a 64-char hex string
	assert.Len(t, output, 64, "Key generate should output 64 char hex")
	_, err := hex.DecodeString(output)
	assert.NoError(t, err, "Output should be valid hex")
}

// TestNakKeyPublic tests public key derivation
func TestNakKeyPublic(t *testing.T) {
	// Generate a key first
	sec := testCall(t, "nak key generate")
	
	// Get public key
	output := testCall(t, "nak key public "+sec)
	
	// Should output a 64-char hex string
	assert.Len(t, output, 64, "Key public should output 64 char hex")
	_, err := hex.DecodeString(output)
	assert.NoError(t, err, "Output should be valid hex")
}

// TestNakEncodeNpub tests npub encoding
func TestNakEncodeNpub(t *testing.T) {
	// Known pubkey -> npub conversion
	pubkey := "79be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798"
	output := testCall(t, "nak encode npub "+pubkey)
	
	assert.True(t, strings.HasPrefix(output, "npub1"), "Should output npub")
	assert.Len(t, output, 63, "npub should be 63 chars")
}

// TestNakDecodeNpubRegression tests npub decoding
func TestNakDecodeNpubRegression(t *testing.T) {
	npub := "npub17csken27ysty9s3vrl2e9t995qlpct2epdnya9gxavxlv3xe6umsrw27qe"
	output := testCall(t, "nak decode "+npub)
	
	// Should output JSON with pubkey
	var result map[string]interface{}
	err := stdjson.Unmarshal([]byte(output), &result)
	require.NoError(t, err)
	
	assert.NotEmpty(t, result, "Should decode to non-empty result")
}

// TestNakFilterBasic tests basic filter creation
func TestNakFilterBasic(t *testing.T) {
	output := testCall(t, "nak filter -k 1 --limit 10")
	
	var filter nostr.Filter
	err := stdjson.Unmarshal([]byte(output), &filter)
	require.NoError(t, err)
	
	assert.Contains(t, filter.Kinds, nostr.Kind(1))
	assert.Equal(t, 10, filter.Limit)
}

// TestNakFilterComplex tests complex filter creation
func TestNakFilterComplex(t *testing.T) {
	output := testCall(t, "nak filter -k 1 -k 30078 -a 79be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798 --since 1000000000")
	
	var filter nostr.Filter
	err := stdjson.Unmarshal([]byte(output), &filter)
	require.NoError(t, err)
	
	assert.Contains(t, filter.Kinds, nostr.Kind(1))
	assert.Contains(t, filter.Kinds, nostr.Kind(30078))
	assert.Equal(t, nostr.Timestamp(1000000000), filter.Since)
}

// TestNakCountBasic tests count command
func TestNakCountBasic(t *testing.T) {
	// Just test that it produces valid output without error
	output := testCall(t, "nak count -k 1 --limit 10")
	
	var filter nostr.Filter
	err := stdjson.Unmarshal([]byte(output), &filter)
	require.NoError(t, err)
	
	assert.Equal(t, 10, filter.Limit)
}

// TestNakMetadata tests metadata event
func TestNakMetadata(t *testing.T) {
	output := testCall(t, "nak metadata --name testuser --about test --picture https://example.com/pic.jpg --ts 1699485669")
	
	var evt nostr.Event
	err := stdjson.Unmarshal([]byte(output), &evt)
	require.NoError(t, err)
	
	assert.Equal(t, nostr.Kind(0), evt.Kind)
	
	var metadata map[string]interface{}
	err = stdjson.Unmarshal([]byte(evt.Content), &metadata)
	require.NoError(t, err)
	
	assert.Equal(t, "testuser", metadata["name"])
	assert.Equal(t, "test", metadata["about"])
	assert.Equal(t, "https://example.com/pic.jpg", metadata["picture"])
}

// ============================================================================
// Integration Tests - NAK + Agent Features
// ============================================================================

// TestAgentEventWithCompression tests creating an agent event with compression
func TestAgentEventWithCompression(t *testing.T) {
	testContent := "This is a test message for agent"
	
	// Test the compressText function directly
	compressed, err := compressText(testContent)
	require.NoError(t, err)
	
	// Compressed content should be base64 encoded and different from original
	assert.NotEqual(t, testContent, compressed)
	assert.Greater(t, len(compressed), 0)
	
	// Test decompression
	decompressed, err := decompressText(compressed)
	require.NoError(t, err)
	assert.Equal(t, testContent, decompressed)
}

// TestNakAgentEventTags tests agent event tag structure
func TestNakAgentEventTags(t *testing.T) {
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

// TestNakKind30078Specific tests agent-specific kind 30078
func TestNakKind30078Specific(t *testing.T) {
	assert.Equal(t, 30078, AgentKind, "AgentKind should be 30078")
	
	// Verify it's in the valid kind range for application-specific
	assert.True(t, AgentKind >= 30000 && AgentKind < 40000, 
		"AgentKind should be in application-specific range (30000-39999)")
}

// BenchmarkEventGeneration benchmarks event generation
func BenchmarkEventGeneration(b *testing.B) {
	for i := 0; i < b.N; i++ {
		testCall(&testing.T{}, "nak event --ts 1699485669")
	}
}

// BenchmarkKeyGeneration benchmarks key generation
func BenchmarkKeyGeneration(b *testing.B) {
	for i := 0; i < b.N; i++ {
		testCall(&testing.T{}, "nak key generate")
	}
}

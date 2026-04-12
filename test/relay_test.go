// Package main provides live tests against relay.aastar.io
// NO MOCK - All tests use real relay
package main

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const relayURL = "wss://relay.aastar.io"

// getRelay returns a connected relay or skips test
func getRelay(t *testing.T) *nostr.Relay {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	
	relay, err := nostr.RelayConnect(ctx, relayURL)
	if err != nil {
		t.Skipf("Cannot connect to %s: %v", relayURL, err)
	}
	return relay
}

// generateKey generates a test key pair
func generateKey(t *testing.T) (string, string) {
	// Use environment variable or generate
	secKey := os.Getenv("TEST_SECRET_KEY")
	if secKey == "" {
		// Generate random for testing
		return nostr.GenerateKey()
	}
	pubKey, err := nostr.GetPublicKey(secKey)
	require.NoError(t, err)
	return pubKey, secKey
}

// TestRelayConnection tests basic connectivity
func TestRelayConnection(t *testing.T) {
	relay := getRelay(t)
	defer relay.Close()
	
	assert.NotNil(t, relay)
	t.Logf("✅ Connected to %s", relayURL)
}

// TestRelayNIP11 tests NIP-11 relay info
func TestRelayNIP11(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	
	info, err := nostr.GetRelayInformation(ctx, relayURL)
	if err != nil {
		t.Skipf("NIP-11 not available: %v", err)
	}
	
	assert.NotEmpty(t, info.Name)
	assert.Contains(t, info.Software, "strfry")
	t.Logf("✅ Relay: %s (%s)", info.Name, info.Description)
	t.Logf("   Supported NIPs: %v", info.SupportedNIPs)
}

// TestPublishTextNote tests publishing a kind 1 event
func TestPublishTextNote(t *testing.T) {
	relay := getRelay(t)
	defer relay.Close()
	
	pubKey, secKey := generateKey(t)
	
	ev := nostr.Event{
		Kind:      1,
		Content:   fmt.Sprintf("Live test message %d", time.Now().Unix()),
		CreatedAt: nostr.Now(),
		Tags:      nostr.Tags{},
	}
	
	err := ev.Sign(secKey)
	require.NoError(t, err)
	
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	
	status := relay.Publish(ctx, ev)
	require.NoError(t, status.Error)
	
	t.Logf("✅ Published kind 1 event: %s", ev.ID)
}

// TestPublishAgentMessage tests publishing a kind 30078 agent message
func TestPublishAgentMessage(t *testing.T) {
	relay := getRelay(t)
	defer relay.Close()
	
	pubKey, secKey := generateKey(t)
	
	// Compress content
	content := "This is a live agent message from acceptance test"
	compressed, err := compressText(content)
	require.NoError(t, err)
	
	ev := nostr.Event{
		Kind:      30078,
		Content:   compressed,
		CreatedAt: nostr.Now(),
		Tags: nostr.Tags{
			{"c", "agent"},
			{"z", "zstd"},
			{"p", pubKey}, // Self-reference for test
		},
	}
	
	err = ev.Sign(secKey)
	require.NoError(t, err)
	
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	
	status := relay.Publish(ctx, ev)
	require.NoError(t, status.Error)
	
	t.Logf("✅ Published kind 30078 agent event: %s", ev.ID)
	
	// Verify by querying
	time.Sleep(1 * time.Second)
	queryFilter := nostr.Filter{
		IDs: []string{ev.ID},
	}
	
	ctx2, cancel2 := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel2()
	
	result := relay.QuerySync(ctx2, queryFilter)
	assert.Len(t, result, 1, "Should find published event")
}

// TestQueryEvents tests querying events from relay
func TestQueryEvents(t *testing.T) {
	relay := getRelay(t)
	defer relay.Close()
	
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	
	filter := nostr.Filter{
		Kinds: []int{1},
		Limit: 5,
	}
	
	events := relay.QuerySync(ctx, filter)
	
	assert.Greater(t, len(events), 0, "Should find at least some events")
	t.Logf("✅ Queried %d kind 1 events", len(events))
	
	for _, ev := range events {
		assert.NotEmpty(t, ev.ID)
		assert.NotEmpty(t, ev.PubKey)
		assert.Equal(t, 1, ev.Kind)
	}
}

// TestQueryAgentMessages tests querying kind 30078 messages
func TestQueryAgentMessages(t *testing.T) {
	relay := getRelay(t)
	defer relay.Close()
	
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	
	filter := nostr.Filter{
		Kinds: []int{30078},
		Limit: 10,
	}
	
	events := relay.QuerySync(ctx, filter)
	t.Logf("✅ Found %d kind 30078 agent messages", len(events))
	
	for _, ev := range events {
		assert.Equal(t, 30078, ev.Kind)
		
		// Check for agent tag
		hasAgentTag := false
		for _, tag := range ev.Tags {
			if len(tag) >= 2 && tag[0] == "c" && tag[1] == "agent" {
				hasAgentTag = true
				break
			}
		}
		
		if hasAgentTag {
			t.Logf("   Event %s has agent tag", ev.ID)
		}
	}
}

// TestSubscription tests real-time subscription
func TestSubscription(t *testing.T) {
	relay := getRelay(t)
	defer relay.Close()
	
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	
	filter := nostr.Filter{
		Kinds: []int{1},
		Limit: 3,
	}
	
	sub, err := relay.Subscribe(ctx, filter)
	require.NoError(t, err)
	defer sub.Unsubscribe()
	
	received := 0
	timeout := time.After(20 * time.Second)
	
	for received < 3 {
		select {
		case ev := <-sub.Events:
			if ev == nil {
				t.Logf("✅ Subscription closed, received %d events", received)
				return
			}
			received++
			t.Logf("   Received event %d: %s", received, ev.ID)
			
		case <-timeout:
			t.Logf("✅ Subscription timeout, received %d events", received)
			return
			
		case <-ctx.Done():
			return
		}
	}
	
	t.Logf("✅ Subscription received %d events", received)
}

// TestCompressionRoundTripLive tests compression with live relay
func TestCompressionRoundTripLive(t *testing.T) {
	relay := getRelay(t)
	defer relay.Close()
	
	pubKey, secKey := generateKey(t)
	
	original := "Live compression test message"
	
	// Compress
	compressed, err := compressText(original)
	require.NoError(t, err)
	assert.NotEqual(t, original, compressed)
	
	// Publish compressed
	ev := nostr.Event{
		Kind:      30078,
		Content:   compressed,
		CreatedAt: nostr.Now(),
		Tags: nostr.Tags{
			{"c", "agent"},
			{"z", "zstd"},
			{"p", pubKey},
			{"test", "compression"},
		},
	}
	
	err = ev.Sign(secKey)
	require.NoError(t, err)
	
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	
	status := relay.Publish(ctx, ev)
	require.NoError(t, status.Error)
	
	// Query back
	time.Sleep(500 * time.Millisecond)
	queryFilter := nostr.Filter{
		IDs: []string{ev.ID},
	}
	
	ctx2, cancel2 := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel2()
	
	results := relay.QuerySync(ctx2, queryFilter)
	require.Len(t, results, 1)
	
	// Decompress
	decompressed, err := decompressText(results[0].Content)
	require.NoError(t, err)
	assert.Equal(t, original, decompressed)
	
	t.Logf("✅ Compression round-trip successful on live relay")
}

// TestMultipleQueries tests multiple concurrent queries
func TestMultipleQueries(t *testing.T) {
	relay := getRelay(t)
	defer relay.Close()
	
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	
	filters := []nostr.Filter{
		{Kinds: []int{1}, Limit: 3},
		{Kinds: []int{30078}, Limit: 3},
		{Kinds: []int{0}, Limit: 3}, // Metadata
	}
	
	results := make([][]nostr.Event, len(filters))
	
	// Query concurrently
	done := make(chan bool, len(filters))
	for i, filter := range filters {
		go func(idx int, f nostr.Filter) {
			defer func() { done <- true }()
			results[idx] = relay.QuerySync(ctx, f)
		}(i, filter)
	}
	
	for i := 0; i < len(filters); i++ {
		<-done
	}
	
	for i, events := range results {
		t.Logf("✅ Query %d (kinds %v): %d events", i+1, filters[i].Kinds, len(events))
	}
}

// TestEventVerification tests signature verification on live events
func TestEventVerification(t *testing.T) {
	relay := getRelay(t)
	defer relay.Close()
	
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	
	filter := nostr.Filter{
		Kinds: []int{1},
		Limit: 5,
	}
	
	events := relay.QuerySync(ctx, filter)
	require.Greater(t, len(events), 0, "Need events to verify")
	
	verified := 0
	for _, ev := range events {
		ok, err := ev.CheckSignature()
		if err == nil && ok {
			verified++
		}
	}
	
	t.Logf("✅ Verified %d/%d event signatures", verified, len(events))
	assert.Greater(t, verified, 0, "At least some events should have valid signatures")
}

// BenchmarkPublish benchmarks publishing to live relay
func BenchmarkPublish(b *testing.B) {
	ctx := context.Background()
	relay, err := nostr.RelayConnect(ctx, relayURL)
	if err != nil {
		b.Skipf("Cannot connect: %v", err)
	}
	defer relay.Close()
	
	_, secKey := nostr.GenerateKey()
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ev := nostr.Event{
			Kind:      1,
			Content:   fmt.Sprintf("Benchmark %d", i),
			CreatedAt: nostr.Now(),
		}
		ev.Sign(secKey)
		
		ctx2, cancel := context.WithTimeout(ctx, 5*time.Second)
		relay.Publish(ctx2, ev)
		cancel()
	}
}

// BenchmarkQuery benchmarks querying from live relay
func BenchmarkQuery(b *testing.B) {
	ctx := context.Background()
	relay, err := nostr.RelayConnect(ctx, relayURL)
	if err != nil {
		b.Skipf("Cannot connect: %v", err)
	}
	defer relay.Close()
	
	filter := nostr.Filter{
		Kinds: []int{1},
		Limit: 10,
	}
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ctx2, cancel := context.WithTimeout(ctx, 5*time.Second)
		relay.QuerySync(ctx2, filter)
		cancel()
	}
}

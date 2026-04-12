package main

import (
	"strings"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"github.com/stretchr/testify/assert"
)

// TestFilterConstruction tests filter construction
func TestFilterConstruction(t *testing.T) {
	// Test building a complex filter using nostr.Filter
	filter := nostr.Filter{
		Kinds:   []nostr.Kind{1, 30078},
		Since:   nostr.Timestamp(time.Now().Add(-time.Hour).Unix()),
		Until:   nostr.Timestamp(time.Now().Unix()),
		Limit:   100,
	}
	
	// Verify filter fields
	assert.NotEmpty(t, filter.Kinds)
	assert.Equal(t, 100, filter.Limit)
}

// TestCompressionRoundTrip tests compression/decompression round trip
func TestCompressionRoundTrip(t *testing.T) {
	original := "This is a message that will be compressed and then decompressed"
	
	// Compress
	compressed, err := compressText(original)
	assert.NoError(t, err)
	
	// Compressed should be different from original (base64 encoded)
	assert.NotEqual(t, original, compressed)
	
	// Decompress and verify
	decompressed, err := decompressText(compressed)
	assert.NoError(t, err)
	assert.Equal(t, original, decompressed)
}

// TestRelayURLValidation tests relay URL validation
func TestRelayURLValidation(t *testing.T) {
	validURLs := []string{
		"wss://relay.aastar.io",
		"wss://relay.aastar.io",
		"wss://relay.aastar.io",
		"ws://localhost:7777",
	}
	
	invalidURLs := []string{
		"http://relay.damus.io",  // http not wss
		"relay.damus.io",          // no protocol
		"",                         // empty
	}
	
	for _, url := range validURLs {
		assert.True(t, strings.HasPrefix(url, "ws"), "Valid URL: %s", url)
	}
	
	for _, url := range invalidURLs {
		if url != "" {
			assert.False(t, strings.HasPrefix(url, "ws"), "Invalid URL should not start with ws: %s", url)
		}
	}
}

// TestMultipleEventKinds tests handling of multiple event kinds
func TestMultipleEventKinds(t *testing.T) {
	kinds := []nostr.Kind{
		0,   // Metadata
		1,   // Text note
		3,   // Contacts
		AgentKind,
	}
	
	for _, kind := range kinds {
		evt := &nostr.Event{
			Kind:      kind,
			Content:   "test",
			CreatedAt: nostr.Now(),
		}
		
		// Just verify the event is created correctly
		assert.Equal(t, kind, evt.Kind)
	}
}

// TestTimestampHandling tests timestamp handling
func TestTimestampHandling(t *testing.T) {
	now := time.Now()
	timestamp := nostr.Timestamp(now.Unix())
	
	evt := &nostr.Event{
		Kind:      1,
		Content:   "Timestamp test",
		CreatedAt: timestamp,
	}
	
	// Verify timestamp is reasonable
	assert.True(t, evt.CreatedAt > 0)
	assert.True(t, int64(evt.CreatedAt) <= now.Add(time.Minute).Unix())
}

// ============================================================================
// Mock Tests (for offline testing)
// ============================================================================

// MockRelay is a mock relay for testing
type MockRelay struct {
	URL        string
	Events     []*nostr.Event
	Subscribed bool
}

// NewMockRelay creates a new mock relay
func NewMockRelay(url string) *MockRelay {
	return &MockRelay{
		URL:    url,
		Events: make([]*nostr.Event, 0),
	}
}

// Publish simulates publishing an event
func (r *MockRelay) Publish(evt *nostr.Event) error {
	r.Events = append(r.Events, evt)
	return nil
}

// Subscribe simulates subscribing to events
func (r *MockRelay) Subscribe(filter nostr.Filter) []*nostr.Event {
	r.Subscribed = true
	result := make([]*nostr.Event, 0)
	
	for _, evt := range r.Events {
		if matchesFilter(evt, filter) {
			result = append(result, evt)
		}
	}
	
	return result
}

// matchesFilter checks if an event matches a filter
func matchesFilter(evt *nostr.Event, filter nostr.Filter) bool {
	// Check kind
	if len(filter.Kinds) > 0 {
		kindMatch := false
		for _, k := range filter.Kinds {
			if k == evt.Kind {
				kindMatch = true
				break
			}
		}
		if !kindMatch {
			return false
		}
	}
	
	// Check author
	if len(filter.Authors) > 0 {
		authorMatch := false
		for _, a := range filter.Authors {
			if a == evt.PubKey {
				authorMatch = true
				break
			}
		}
		if !authorMatch {
			return false
		}
	}
	
	return true
}

// TestMockRelay tests the mock relay implementation
func TestMockRelay(t *testing.T) {
	relay := NewMockRelay("wss://mock.relay")
	
	// Create and publish events
	for i := 0; i < 5; i++ {
		evt := &nostr.Event{
			Kind:      1,
			Content:   "Test message",
			CreatedAt: nostr.Now(),
		}
		
		err := relay.Publish(evt)
		assert.NoError(t, err)
	}
	
	assert.Len(t, relay.Events, 5)
	
	// Test subscription
	filter := nostr.Filter{
		Kinds: []nostr.Kind{1},
	}
	
	results := relay.Subscribe(filter)
	assert.Len(t, results, 5)
}

// BenchmarkMockRelay benchmarks mock relay operations
func BenchmarkMockRelay(b *testing.B) {
	relay := NewMockRelay("wss://mock.relay")
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		evt := &nostr.Event{
			Kind:      1,
			Content:   "Benchmark",
			CreatedAt: nostr.Now(),
		}
		relay.Publish(evt)
	}
}

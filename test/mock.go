// Package main provides mock implementations for testing
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"fiatjaf.com/nostr"
	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
)

// MockRelay simulates a Nostr relay for testing
type MockRelay struct {
	URL        string
	Events     []nostr.Event
	subscribers map[string]*MockSubscription
	mu         sync.RWMutex
	connected  bool
}

// MockSubscription simulates a subscription
type MockSubscription struct {
	ID      string
	Filter  nostr.Filter
	Events  chan nostr.Event
	Closed  chan struct{}
	mu      sync.Mutex
}

// NewMockRelay creates a new mock relay
func NewMockRelay(url string) *MockRelay {
	return &MockRelay{
		URL:         url,
		Events:      []nostr.Event{},
		subscribers: make(map[string]*MockSubscription),
		connected:   true,
	}
}

// Publish adds an event to the relay
func (m *MockRelay) Publish(ctx context.Context, ev nostr.Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	if !m.connected {
		return fmt.Errorf("relay not connected")
	}
	
	// Validate event
	if !ev.CheckSignature() {
		return fmt.Errorf("invalid signature")
	}
	
	m.Events = append(m.Events, ev)
	
	// Notify subscribers
	for _, sub := range m.subscribers {
		if matchesFilter(ev, sub.Filter) {
			select {
			case sub.Events <- ev:
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
	
	return nil
}

// Query returns events matching the filter
func (m *MockRelay) Query(ctx context.Context, filter nostr.Filter) ([]nostr.Event, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	
	var results []nostr.Event
	for _, ev := range m.Events {
		if matchesFilter(ev, filter) {
			results = append(results, ev)
		}
		if filter.Limit > 0 && len(results) >= filter.Limit {
			break
		}
	}
	
	return results, nil
}

// Subscribe creates a new subscription
func (m *MockRelay) Subscribe(ctx context.Context, filter nostr.Filter) *MockSubscription {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	sub := &MockSubscription{
		ID:     generateID(),
		Filter: filter,
		Events: make(chan nostr.Event, 100),
		Closed: make(chan struct{}),
	}
	
	m.subscribers[sub.ID] = sub
	
	// Send existing matching events
	for _, ev := range m.Events {
		if matchesFilter(ev, filter) {
			select {
			case sub.Events <- ev:
			default:
			}
		}
	}
	
	return sub
}

// CloseSubscription closes a subscription
func (m *MockRelay) CloseSubscription(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	if sub, ok := m.subscribers[id]; ok {
		sub.mu.Lock()
		close(sub.Closed)
		close(sub.Events)
		sub.mu.Unlock()
		delete(m.subscribers, id)
	}
}

// matchesFilter checks if an event matches a filter
func matchesFilter(ev nostr.Event, filter nostr.Filter) bool {
	// Check kinds
	if len(filter.Kinds) > 0 {
		found := false
		for _, k := range filter.Kinds {
			if ev.Kind == k {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	
	// Check authors
	if len(filter.Authors) > 0 {
		found := false
		for _, author := range filter.Authors {
			if ev.PubKey == author {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	
	// Check IDs
	if len(filter.IDs) > 0 {
		found := false
		for _, id := range filter.IDs {
			if ev.ID == id {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	
	// Check since
	if filter.Since > 0 && ev.CreatedAt < filter.Since {
		return false
	}
	
	// Check until
	if filter.Until > 0 && ev.CreatedAt > filter.Until {
		return false
	}
	
	// Check tags
	for tagName, values := range filter.Tags {
		found := false
		evTagValues := ev.Tags.GetAll([]string{tagName})
		for _, v := range values {
			for _, evVal := range evTagValues {
				if len(evVal) > 1 && evVal[1] == v {
					found = true
					break
				}
			}
			if found {
				break
			}
		}
		if !found {
			return false
		}
	}
	
	return true
}

// Disconnect simulates a disconnection
func (m *MockRelay) Disconnect() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.connected = false
	
	// Close all subscriptions
	for id, sub := range m.subscribers {
		sub.mu.Lock()
		close(sub.Closed)
		sub.mu.Unlock()
		delete(m.subscribers, id)
	}
}

// Reconnect simulates a reconnection
func (m *MockRelay) Reconnect() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.connected = true
}

// MockKeyer implements nostr.Keyer for testing
type MockKeyer struct {
	PrivateKey *btcec.PrivateKey
	PublicKey  string
}

// NewMockKeyer creates a new mock keyer
func NewMockKeyer() (*MockKeyer, error) {
	privKey, err := btcec.NewPrivateKey()
	if err != nil {
		return nil, err
	}
	
	pubKey := schnorr.SerializePubKey(privKey.PubKey())
	
	return &MockKeyer{
		PrivateKey: privKey,
		PublicKey:  hex.EncodeToString(pubKey),
	}, nil
}

// SignEvent signs an event
func (m *MockKeyer) SignEvent(ctx context.Context, ev *nostr.Event) error {
	ev.PubKey = m.PublicKey
	
	hash := ev.GetRawHash()
	sig, err := schnorr.Sign(m.PrivateKey, hash[:])
	if err != nil {
		return err
	}
	
	ev.Sig = hex.EncodeToString(sig)
	return nil
}

// GetPublicKey returns the public key
func (m *MockKeyer) GetPublicKey(ctx context.Context) (string, error) {
	return m.PublicKey, nil
}

// Encrypt implements nip04 encryption (mock)
func (m *MockKeyer) Encrypt(ctx context.Context, plaintext string, recipient string) (string, error) {
	return plaintext, nil // Mock: no actual encryption
}

// Decrypt implements nip04 decryption (mock)
func (m *MockKeyer) Decrypt(ctx context.Context, ciphertext string, sender string) (string, error) {
	return ciphertext, nil // Mock: no actual decryption
}

// GenerateTestEvent creates a test event
func GenerateTestEvent(keyer *MockKeyer, kind int, content string, tags nostr.Tags) (*nostr.Event, error) {
	ev := &nostr.Event{
		Kind:      kind,
		Content:   content,
		Tags:      tags,
		CreatedAt: nostr.Now(),
	}
	
	ctx := context.Background()
	if err := keyer.SignEvent(ctx, ev); err != nil {
		return nil, err
	}
	
	return ev, nil
}

// TestHelper provides common test utilities
type TestHelper struct {
	MockRelay *MockRelay
	Keyer     *MockKeyer
	Events    []*nostr.Event
}

// NewTestHelper creates a new test helper
func NewTestHelper() (*TestHelper, error) {
	relay := NewMockRelay("wss://relay.test")
	keyer, err := NewMockKeyer()
	if err != nil {
		return nil, err
	}
	
	return &TestHelper{
		MockRelay: relay,
		Keyer:     keyer,
		Events:    []*nostr.Event{},
	}, nil
}

// CreateAgentMessage creates an agent message event
func (th *TestHelper) CreateAgentMessage(recipientPubkey string, content string, compressed bool) (*nostr.Event, error) {
	tags := nostr.Tags{
		{"p", recipientPubkey},
		{"c", "agent"},
	}
	
	if compressed {
		tags = append(tags, nostr.Tag{"z", "zstd"})
	}
	
	return GenerateTestEvent(th.Keyer, 30078, content, tags)
}

// CreateTextNote creates a text note event
func (th *TestHelper) CreateTextNote(content string) (*nostr.Event, error) {
	return GenerateTestEvent(th.Keyer, 1, content, nostr.Tags{})
}

// PublishToRelay publishes an event to the mock relay
func (th *TestHelper) PublishToRelay(ev *nostr.Event) error {
	ctx := context.Background()
	return th.MockRelay.Publish(ctx, *ev)
}

// generateID generates a random ID
func generateID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// AssertEventExists checks if an event exists in the relay
func AssertEventExists(relay *MockRelay, eventID string) bool {
	relay.mu.RLock()
	defer relay.mu.RUnlock()
	
	for _, ev := range relay.Events {
		if ev.ID == eventID {
			return true
		}
	}
	return false
}

// CountEvents counts events matching a filter
func CountEvents(relay *MockRelay, filter nostr.Filter) int {
	relay.mu.RLock()
	defer relay.mu.RUnlock()
	
	count := 0
	for _, ev := range relay.Events {
		if matchesFilter(ev, filter) {
			count++
		}
	}
	return count
}

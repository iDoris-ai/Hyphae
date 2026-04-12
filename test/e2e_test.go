// Package main provides end-to-end tests for agent-speaker
// These tests require a running relay or use mock relays
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
	"github.com/stretchr/testify/suite"
)

// E2ETestSuite is the test suite for end-to-end tests
type E2ETestSuite struct {
	suite.Suite
	relayURL string
	sys      *nostr.System
	ctx      context.Context
	cancel   context.CancelFunc
}

// SetupSuite runs once before all tests
func (s *E2ETestSuite) SetupSuite() {
	s.ctx, s.cancel = context.WithTimeout(context.Background(), 5*time.Minute)

	// Use our own relay
	s.relayURL = os.Getenv("TEST_RELAY_URL")
	if s.relayURL == "" {
		s.relayURL = "wss://relay.aastar.io"
	}

	// Create system
	s.sys = &nostr.System{
		Pool: nostr.NewSimplePool(s.ctx),
	}

	s.T().Logf("Using relay: %s", s.relayURL)
}

// TearDownSuite runs once after all tests
func (s *E2ETestSuite) TearDownSuite() {
	s.cancel()
}

// TestRelayConnection tests basic relay connection
func (s *E2ETestSuite) TestRelayConnection() {
	t := s.T()

	ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
	defer cancel()

	// Try to connect to relay
	relay, err := s.sys.Pool.EnsureRelay(s.relayURL)
	if err != nil {
		t.Skipf("Cannot connect to relay %s: %v", s.relayURL, err)
	}

	assert.NotNil(t, relay)
	t.Logf("Successfully connected to relay: %s", s.relayURL)
}

// TestPublishAndQuery tests publishing and querying events
func (s *E2ETestSuite) TestPublishAndQuery() {
	t := s.T()

	// Skip if no relay available
	relay, err := s.sys.Pool.EnsureRelay(s.relayURL)
	if err != nil {
		t.Skipf("Cannot connect to relay: %v", err)
	}

	ctx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
	defer cancel()

	// Query for recent kind 1 events
	filter := nostr.Filter{
		Kinds: []int{1},
		Limit: 5,
	}

	events := s.sys.Pool.FetchMany(ctx, []string{s.relayURL}, filter, nostr.SubscriptionOptions{})

	count := 0
	for event := range events {
		assert.NotEmpty(t, event.ID)
		assert.NotEmpty(t, event.PubKey)
		count++
	}

	t.Logf("Found %d events", count)
	assert.Greater(t, count, 0, "Should find at least some events")
}

// TestSubscription tests WebSocket subscription
func (s *E2ETestSuite) TestSubscription() {
	t := s.T()

	relay, err := s.sys.Pool.EnsureRelay(s.relayURL)
	if err != nil {
		t.Skipf("Cannot connect to relay: %v", err)
	}

	ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
	defer cancel()

	// Create subscription
	filter := nostr.Filter{
		Kinds: []int{1},
		Limit: 1,
	}

	// Test that we can create a subscription
	sub := s.sys.Pool.SubscribeMany(ctx, []string{s.relayURL}, filter, nostr.SubscriptionOptions{})
	assert.NotNil(t, sub)

	// Wait for at most one event or timeout
	select {
	case event, ok := <-sub.Events:
		if ok {
			assert.NotEmpty(t, event.ID)
			t.Logf("Received event: %s", event.ID)
		}
	case <-ctx.Done():
		t.Log("Subscription timeout (expected)")
	}
}

// TestCompression tests zstd compression/decompression
func (s *E2ETestSuite) TestCompression() {
	t := s.T()

	original := "This is a test message that will be compressed using zstd algorithm."

	// Compress
	compressed, err := compressText(original)
	require.NoError(t, err)
	assert.NotEmpty(t, compressed)
	assert.NotEqual(t, original, compressed)

	// Decompress
	decompressed, err := decompressText(compressed)
	require.NoError(t, err)
	assert.Equal(t, original, decompressed)

	t.Logf("Original: %d bytes, Compressed: %d bytes", len(original), len(compressed))
}

// TestTaskEngine tests the task engine
func (s *E2ETestSuite) TestTaskEngine() {
	t := s.T()

	// Create a mock keyer (this would need to be a real keyer in full tests)
	// For now, test the task creation logic

	engine := NewTaskEngine(s.sys, []string{s.relayURL}, nil)
	require.NotNil(t, engine)

	// Create a task
	task := engine.CreateTask(TaskMarketing, "Test marketing task", TaskRequirements{
		Capabilities: []string{"social-media"},
		MaxBudget:    1000,
		Currency:     "CNY",
	})

	assert.NotEmpty(t, task.ID)
	assert.Equal(t, TaskMarketing, task.Type)
	assert.Equal(t, TaskCreated, task.State)
	assert.Equal(t, "Test marketing task", task.Description)

	// Verify task is stored
	retrieved, ok := engine.GetTask(task.ID)
	assert.True(t, ok)
	assert.Equal(t, task.ID, retrieved.ID)
}

// TestBackgroundScheduler tests the background scheduler
func (s *E2ETestSuite) TestBackgroundScheduler() {
	t := s.T()

	scheduler := NewBackgroundScheduler(s.sys, []string{s.relayURL}, nil)
	require.NotNil(t, scheduler)

	// Create a test task
	task := &BackgroundTask{
		ID:   "test-task-1",
		Name: "Test Discovery",
		Type: BGDiscovery,
		Schedule: BackgroundSchedule{
			Type:     ScheduleInterval,
			Interval: 60,
		},
		Conditions: []MatchCondition{
			{
				Kind: 0,
				Tags: []string{"blogger"},
			},
		},
		Actions: []BGAction{
			{
				Type:     "add_to_contact",
				Category: "bloggers",
			},
		},
	}

	// Register task
	err := scheduler.RegisterTask(task)
	require.NoError(t, err)

	// Verify task is stored
	retrieved, ok := scheduler.GetTask(task.ID)
	assert.True(t, ok)
	assert.Equal(t, task.Name, retrieved.Name)
	assert.Equal(t, BGActive, retrieved.Status)

	// Start scheduler
	scheduler.Start()
	time.Sleep(100 * time.Millisecond) // Let it start

	// Stop scheduler
	scheduler.Stop()
}

// TestChatMessage tests chat message structure
func (s *E2ETestSuite) TestChatMessage() {
	t := s.T()

	msg := ChatMessage{
		ID:        "test-id",
		From:      "pubkey1",
		Content:   "Hello",
		Timestamp: time.Now(),
		IsMe:      true,
	}

	assert.Equal(t, "test-id", msg.ID)
	assert.Equal(t, "Hello", msg.Content)
	assert.True(t, msg.IsMe)
}

// MockRelay is a mock relay for testing without real network
type MockRelay struct {
	Events []nostr.Event
}

func (m *MockRelay) Publish(ctx context.Context, ev nostr.Event) error {
	m.Events = append(m.Events, ev)
	return nil
}

func (m *MockRelay) Query(ctx context.Context, filter nostr.Filter) ([]nostr.Event, error) {
	var results []nostr.Event
	for _, ev := range m.Events {
		if matchesFilter(ev, filter) {
			results = append(results, ev)
		}
	}
	return results, nil
}

func matchesFilter(event nostr.Event, filter nostr.Filter) bool {
	if len(filter.Kinds) > 0 {
		found := false
		for _, k := range filter.Kinds {
			if event.Kind == k {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// TestMockRelay tests the mock relay
func TestMockRelay(t *testing.T) {
	relay := &MockRelay{}

	ctx := context.Background()

	// Publish some events
	events := []nostr.Event{
		{ID: "1", Kind: 1, Content: "Hello"},
		{ID: "2", Kind: 1, Content: "World"},
		{ID: "3", Kind: 30078, Content: "Agent message"},
	}

	for _, ev := range events {
		err := relay.Publish(ctx, ev)
		require.NoError(t, err)
	}

	assert.Len(t, relay.Events, 3)

	// Query for kind 1
	filter := nostr.Filter{Kinds: []int{1}}
	results, err := relay.Query(ctx, filter)
	require.NoError(t, err)
	assert.Len(t, results, 2)

	// Query for kind 30078
	filter = nostr.Filter{Kinds: []int{30078}}
	results, err = relay.Query(ctx, filter)
	require.NoError(t, err)
	assert.Len(t, results, 1)
}

// TestFullWorkflow simulates a full workflow
func TestFullWorkflow(t *testing.T) {
	t.Skip("Skipping full workflow test - requires full setup")

	// This would test:
	// 1. Alice starts chat with Bob
	// 2. Alice sends message
	// 3. Bob receives message
	// 4. Alice delegates task
	// 5. Task discovery
	// 6. Task completion
}

// Run the test suite
func TestE2E(t *testing.T) {
	// Only run E2E tests if explicitly enabled
	if os.Getenv("RUN_E2E_TESTS") != "1" {
		t.Skip("Skipping E2E tests. Set RUN_E2E_TESTS=1 to run")
	}

	suite.Run(t, new(E2ETestSuite))
}

// TestMain runs setup and teardown for all tests
func TestMain(m *testing.M) {
	// Setup code here

	code := m.Run()

	// Teardown code here

	os.Exit(code)
}

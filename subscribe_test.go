// Package main provides tests for subscription management
package main

import (
	"context"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"github.com/stretchr/testify/assert"
)

// TestSubscriptionManagerCreation tests creating a subscription manager
func TestSubscriptionManagerCreation(t *testing.T) {
	sys := &nostr.System{
		Pool: nostr.NewSimplePool(context.Background()),
	}

	filter := nostr.Filter{
		Kinds: []int{AgentKind},
		Limit: 10,
	}

	handler := func(event nostr.Event) {}

	sm := NewSubscriptionManager(sys, []string{"wss://relay.aastar.io"}, filter, handler)

	assert.NotNil(t, sm)
	assert.False(t, sm.IsRunning())
}

// TestHeartbeatManagerCreation tests creating a heartbeat manager
func TestHeartbeatManagerCreation(t *testing.T) {
	sys := &nostr.System{
		Pool: nostr.NewSimplePool(context.Background()),
	}

	hm := NewHeartbeatManager(sys, []string{"wss://relay.aastar.io"}, 30*time.Second)

	assert.NotNil(t, hm)
	assert.False(t, hm.IsRunning())
	assert.Equal(t, "online", hm.status)
}

// TestHeartbeatStatusSetting tests setting heartbeat status
func TestHeartbeatStatusSetting(t *testing.T) {
	sys := &nostr.System{
		Pool: nostr.NewSimplePool(context.Background()),
	}

	hm := NewHeartbeatManager(sys, []string{"wss://relay.aastar.io"}, 30*time.Second)

	hm.SetStatus("busy")
	assert.Equal(t, "busy", hm.status)

	hm.SetStatus("away")
	assert.Equal(t, "away", hm.status)
}

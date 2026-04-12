// Package main provides WebSocket subscription management for agent communication
package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"fiatjaf.com/nostr"
)

// SubscriptionManager manages WebSocket subscriptions to relays
type SubscriptionManager struct {
	sys       *nostr.System
	relays    []string
	filter    nostr.Filter
	handler   func(nostr.Event)
	sub       *nostr.Subscription
	mu        sync.RWMutex
	running   bool
	cancelFunc context.CancelFunc
}

// NewSubscriptionManager creates a new subscription manager
func NewSubscriptionManager(sys *nostr.System, relays []string, filter nostr.Filter, handler func(nostr.Event)) *SubscriptionManager {
	return &SubscriptionManager{
		sys:     sys,
		relays:  relays,
		filter:  filter,
		handler: handler,
		running: false,
	}
}

// Start begins the subscription
func (sm *SubscriptionManager) Start(ctx context.Context) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if sm.running {
		return fmt.Errorf("subscription already running")
	}

	ctx, sm.cancelFunc = context.WithCancel(ctx)

	// Subscribe to events
	sub := sm.sys.Pool.SubscribeMany(ctx, sm.relays, sm.filter, nostr.SubscriptionOptions{})
	sm.sub = sub
	sm.running = true

	// Start event processing goroutine
	go sm.processEvents(ctx)

	return nil
}

// processEvents processes incoming events
func (sm *SubscriptionManager) processEvents(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-sm.sub.Events:
			if !ok {
				return
			}
			if sm.handler != nil {
				sm.handler(event)
			}
		}
	}
}

// Stop ends the subscription
func (sm *SubscriptionManager) Stop() {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if !sm.running {
		return
	}

	if sm.cancelFunc != nil {
		sm.cancelFunc()
	}

	sm.running = false
}

// IsRunning returns whether the subscription is active
func (sm *SubscriptionManager) IsRunning() bool {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.running
}

// SubscribeToPeer subscribes to events from a specific peer
func SubscribeToPeer(ctx context.Context, sys *nostr.System, relays []string, peerPubkey string, handler func(nostr.Event)) (*SubscriptionManager, error) {
	filter := nostr.Filter{
		Tags: nostr.TagMap{
			"p": []string{peerPubkey},
		},
		Kinds: []int{AgentKind, 1}, // Agent messages and regular notes
	}

	sm := NewSubscriptionManager(sys, relays, filter, handler)
	if err := sm.Start(ctx); err != nil {
		return nil, err
	}

	return sm, nil
}

// HeartbeatManager manages periodic heartbeat/status updates
type HeartbeatManager struct {
	sys      *nostr.System
	relays   []string
	interval time.Duration
	status   string
	mu       sync.RWMutex
	running  bool
	stopChan chan struct{}
}

// NewHeartbeatManager creates a new heartbeat manager
func NewHeartbeatManager(sys *nostr.System, relays []string, interval time.Duration) *HeartbeatManager {
	return &HeartbeatManager{
		sys:      sys,
		relays:   relays,
		interval: interval,
		status:   "online",
		stopChan: make(chan struct{}),
	}
}

// Start begins sending periodic heartbeats
func (hm *HeartbeatManager) Start(ctx context.Context, keyer nostr.Keyer) {
	hm.mu.Lock()
	if hm.running {
		hm.mu.Unlock()
		return
	}
	hm.running = true
	hm.mu.Unlock()

	ticker := time.NewTicker(hm.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-hm.stopChan:
			return
		case <-ticker.C:
			hm.sendHeartbeat(ctx, keyer)
		}
	}
}

// sendHeartbeat sends a single heartbeat event
func (hm *HeartbeatManager) sendHeartbeat(ctx context.Context, keyer nostr.Keyer) {
	hm.mu.RLock()
	status := hm.status
	hm.mu.RUnlock()

	ev := &nostr.Event{
		Kind:      30311, // Status update kind
		Content:   fmt.Sprintf(`{"status":"%s","timestamp":%d}`, status, time.Now().Unix()),
		Tags:      nostr.Tags{{"c", AgentTag}},
		CreatedAt: nostr.Now(),
	}

	if err := keyer.SignEvent(ctx, ev); err != nil {
		return
	}

	for _, url := range hm.relays {
		relay, err := hm.sys.Pool.EnsureRelay(url)
		if err != nil {
			continue
		}
		relay.Publish(ctx, *ev)
	}
}

// Stop stops the heartbeat
func (hm *HeartbeatManager) Stop() {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	if !hm.running {
		return
	}

	close(hm.stopChan)
	hm.running = false
}

// SetStatus updates the current status
func (hm *HeartbeatManager) SetStatus(status string) {
	hm.mu.Lock()
	defer hm.mu.Unlock()
	hm.status = status
}

// IsRunning returns whether heartbeat is active
func (hm *HeartbeatManager) IsRunning() bool {
	hm.mu.RLock()
	defer hm.mu.RUnlock()
	return hm.running
}

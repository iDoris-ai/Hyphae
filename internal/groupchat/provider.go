package groupchat

import (
	"errors"
	"fmt"
	"sync"
)

// ErrGroupProviderNotRegistered is the miss for a provider name.
var ErrGroupProviderNotRegistered = errors.New("group send provider is not registered")

// GroupSendToken is the lease Dispatch may hand to a provider.
// ExpiresAt is a unix second. At that second, or later, Dispatch keeps the token.
type GroupSendToken struct {
	ID        string
	ExpiresAt int64
}

// GroupSendCall is the value a provider Send method receives.
type GroupSendCall struct {
	Token    GroupSendToken
	Decision ReconcileDecision
}

// GroupSendProvider sends one call.
// Send takes the call value and returns an error. It has no dialer argument.
type GroupSendProvider interface {
	Send(call GroupSendCall) error
}

// GroupProviderRegistry stores providers by name.
type GroupProviderRegistry struct {
	mu        sync.RWMutex
	providers map[string]GroupSendProvider
}

// NewGroupProviderRegistry returns an empty registry.
func NewGroupProviderRegistry() *GroupProviderRegistry {
	return &GroupProviderRegistry{providers: map[string]GroupSendProvider{}}
}

// Register stores provider under name.
// An empty name or a nil provider is refused.
func (r *GroupProviderRegistry) Register(name string, provider GroupSendProvider) error {
	if r == nil {
		return ErrGroupProviderNotRegistered
	}
	if name == "" || provider == nil {
		return fmt.Errorf("group send provider name and value are required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers[name] = provider
	return nil
}

// Lookup returns the provider stored under name.
func (r *GroupProviderRegistry) Lookup(name string) (GroupSendProvider, error) {
	if r == nil {
		return nil, ErrGroupProviderNotRegistered
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	provider := r.providers[name]
	if provider == nil {
		return nil, ErrGroupProviderNotRegistered
	}
	return provider, nil
}

// Dispatch calls the named provider for a live token and a decision other than hold.
// A missing name returns ErrGroupProviderNotRegistered.
// The bool reports whether Send ran.
func (r *GroupProviderRegistry) Dispatch(name string, token GroupSendToken, now int64, decision ReconcileDecision) (bool, error) {
	provider, err := r.Lookup(name)
	if err != nil {
		return false, err
	}
	if decision == ReconcileHold || now < 0 || now >= token.ExpiresAt {
		return false, nil
	}
	if err := provider.Send(GroupSendCall{Token: token, Decision: decision}); err != nil {
		return true, err
	}
	return true, nil
}

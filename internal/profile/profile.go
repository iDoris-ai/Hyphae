package profile

import (
	"encoding/json"
	"fmt"

	"fiatjaf.com/nostr"
	"github.com/iDoris-ai/hyphae/internal/wireevent"
	"github.com/iDoris-ai/hyphae/pkg/types"
)

const (
	// ProfileKind is the nostr kind for agent profiles (Kind 30078 extension)
	ProfileKind = wireevent.Kind30078
	// ProfileTag marks this as a profile event
	ProfileTag = wireevent.ProfileCategory
	// ProfileDTag is the 'd' tag value for parameterized replaceable events
	ProfileDTag = wireevent.ProfileDTag
)

// ProfileToEvent converts an AgentProfile to a nostr Event
func ProfileToEvent(profile *types.AgentProfile, pubkey nostr.PubKey) (*nostr.Event, error) {
	if err := profile.Validate(); err != nil {
		return nil, err
	}

	content, err := json.Marshal(profile)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal profile: %w", err)
	}

	event := &nostr.Event{
		CreatedAt: nostr.Now(),
		Kind:      ProfileKind,
		Tags: nostr.Tags{
			{"d", ProfileDTag},
			{"c", ProfileTag},
		},
		Content: string(content),
		PubKey:  pubkey,
	}

	return event, nil
}

// EventToProfile converts a nostr Event to an AgentProfile
func EventToProfile(event *nostr.Event) (*types.AgentProfile, error) {
	if event.Kind != ProfileKind {
		return nil, fmt.Errorf("expected kind %d, got %d", ProfileKind, event.Kind)
	}
	class, err := wireevent.Classify30078(event.Tags)
	if err != nil {
		return nil, fmt.Errorf("classify profile event: %w", err)
	}
	if class != wireevent.ClassProfile {
		return nil, fmt.Errorf("kind 30078 event is not an agent profile")
	}

	profile, err := types.AgentProfileFromJSON([]byte(event.Content))
	if err != nil {
		return nil, err
	}

	profile.UpdatedAt = int64(event.CreatedAt)
	return profile, nil
}

// IsProfileEvent checks if a nostr event is an agent profile event
func IsProfileEvent(event *nostr.Event) bool {
	if event == nil || event.Kind != ProfileKind {
		return false
	}
	class, err := wireevent.Classify30078(event.Tags)
	return err == nil && class == wireevent.ClassProfile
}

// BuildFilter creates a nostr filter for agent profile events
func BuildFilter(authors []nostr.PubKey, limit int) nostr.Filter {
	filter := nostr.Filter{
		Kinds: []nostr.Kind{ProfileKind},
		Tags:  nostr.TagMap{"c": []string{ProfileTag}, "d": []string{ProfileDTag}},
	}
	if len(authors) > 0 {
		filter.Authors = authors
	}
	if limit > 0 {
		filter.Limit = limit
	}
	return filter
}

// NewAgentProfile creates a new agent profile with defaults
func NewAgentProfile(name string) *types.AgentProfile {
	return &types.AgentProfile{
		Name:         name,
		Version:      "1.0",
		Availability: types.AvailabilityAvailable,
		Capabilities: make([]types.Capability, 0),
	}
}

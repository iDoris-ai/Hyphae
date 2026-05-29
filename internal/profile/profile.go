package profile

import (
	"encoding/json"
	"fmt"

	"fiatjaf.com/nostr"
	"github.com/AuraAIHQ/agent-speaker/pkg/types"
)

const (
	// ProfileKind is the nostr kind for agent profiles (Kind 30078 extension)
	ProfileKind = 30078
	// ProfileTag marks this as a profile event
	ProfileTag = "profile"
	// ProfileDTag is the 'd' tag value for parameterized replaceable events
	ProfileDTag = "agent-profile"
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

	// Verify it's a profile event
	isProfile := false
	for _, tag := range event.Tags {
		if len(tag) >= 2 && tag[0] == "c" && tag[1] == ProfileTag {
			isProfile = true
			break
		}
	}
	if !isProfile {
		return nil, fmt.Errorf("event missing profile tag")
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
	if event.Kind != ProfileKind {
		return false
	}
	for _, tag := range event.Tags {
		if len(tag) >= 2 && tag[0] == "c" && tag[1] == ProfileTag {
			return true
		}
	}
	return false
}

// BuildFilter creates a nostr filter for agent profile events
func BuildFilter(authors []nostr.PubKey, limit int) nostr.Filter {
	filter := nostr.Filter{
		Kinds: []nostr.Kind{ProfileKind},
		Tags:  nostr.TagMap{"c": []string{ProfileTag}},
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

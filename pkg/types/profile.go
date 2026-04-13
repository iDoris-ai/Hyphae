// Package types provides agent profile type definitions
package types

import (
	"encoding/json"
	"fmt"
)

// AgentProfile represents an agent's public profile published on nostr
type AgentProfile struct {
	Name         string        `json:"name"`
	Description  string        `json:"description,omitempty"`
	Capabilities []Capability  `json:"capabilities,omitempty"`
	Availability string        `json:"availability,omitempty"`
	RateSheet    *RateSheet    `json:"rate_sheet,omitempty"`
	Contact      *ContactInfo  `json:"contact,omitempty"`
	Version      string        `json:"version,omitempty"`
	UpdatedAt    int64         `json:"updated_at"`
}

// Capability represents a service or skill the agent offers
type Capability struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
}

// RateSheet represents the agent's pricing structure
type RateSheet struct {
	Currency string      `json:"currency,omitempty"`
	Rates    []RateEntry `json:"rates,omitempty"`
}

// RateEntry represents a single pricing tier
type RateEntry struct {
	Service     string  `json:"service"`
	Unit        string  `json:"unit,omitempty"`
	Price       float64 `json:"price"`
	Description string  `json:"description,omitempty"`
}

// ContactInfo represents how to reach the agent
type ContactInfo struct {
	Email    string `json:"email,omitempty"`
	Website  string `json:"website,omitempty"`
	Relay    string `json:"relay,omitempty"`
	NostrDMs bool   `json:"nostr_dms,omitempty"`
}

// StoredProfile represents a profile stored in the local database
type StoredProfile struct {
	Npub         string        `json:"npub"`
	Name         string        `json:"name"`
	Description  string        `json:"description,omitempty"`
	Availability string        `json:"availability,omitempty"`
	UpdatedAt    int64         `json:"updated_at"`
	DiscoveredAt int64         `json:"discovered_at"`
	Profile      *AgentProfile `json:"profile"`
}

// Availability constants
const (
	AvailabilityAvailable   = "available"
	AvailabilityBusy        = "busy"
	AvailabilityAway        = "away"
	AvailabilityOffline     = "offline"
)

// Validate checks if the profile is valid
func (p *AgentProfile) Validate() error {
	if p.Name == "" {
		return fmt.Errorf("profile name is required")
	}

	validAvailability := map[string]bool{
		AvailabilityAvailable: true,
		AvailabilityBusy:      true,
		AvailabilityAway:      true,
		AvailabilityOffline:   true,
		"":                    true,
	}
	if !validAvailability[p.Availability] {
		return fmt.Errorf("invalid availability: %s", p.Availability)
	}

	return nil
}

// ToJSON serializes the profile to JSON
func (p *AgentProfile) ToJSON() ([]byte, error) {
	return json.Marshal(p)
}

// AgentProfileFromJSON deserializes a profile from JSON
func AgentProfileFromJSON(data []byte) (*AgentProfile, error) {
	var p AgentProfile
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("invalid profile JSON: %w", err)
	}
	return &p, nil
}

// HasCapability checks if the agent has a specific capability
func (p *AgentProfile) HasCapability(name string) bool {
	for _, c := range p.Capabilities {
		if c.Name == name {
			return true
		}
	}
	return false
}

// AddCapability adds a capability to the profile
func (p *AgentProfile) AddCapability(cap Capability) {
	if !p.HasCapability(cap.Name) {
		p.Capabilities = append(p.Capabilities, cap)
	}
}

// RemoveCapability removes a capability from the profile
func (p *AgentProfile) RemoveCapability(name string) {
	newCaps := make([]Capability, 0, len(p.Capabilities))
	for _, c := range p.Capabilities {
		if c.Name != name {
			newCaps = append(newCaps, c)
		}
	}
	p.Capabilities = newCaps
}

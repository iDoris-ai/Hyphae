// Package types provides agent profile type definitions
package types

import (
	"encoding/json"
	"fmt"
	"strings"
)

// AgentProfile represents an agent's public profile published on nostr
type AgentProfile struct {
	Name         string       `json:"name"`
	Mode         ProfileMode  `json:"mode,omitempty"`
	Tags         []string     `json:"tags,omitempty"`
	Description  string       `json:"description,omitempty"`
	Capabilities []Capability `json:"capabilities,omitempty"`
	Availability string       `json:"availability,omitempty"`
	RateSheet    *RateSheet   `json:"rate_sheet,omitempty"`
	Contact      *ContactInfo `json:"contact,omitempty"`
	// Rating is a self-declared score (structured mode only, e.g. "how I'd
	// rate my own reliability"), not a computed/third-party reputation
	// score -- there's no reputation system to compute one from yet (see
	// docs/protocol-v2.md's CityRep discussion for where that's headed).
	// A pointer distinguishes "no rating set" from "rated 0".
	Rating    *float64 `json:"rating,omitempty"`
	Version   string   `json:"version,omitempty"`
	UpdatedAt int64    `json:"updated_at"`
}

// ProfileMode marks how much of a profile's structure was published: simple
// (name only), tagged (name + free-form tags), or structured (the full
// capabilities/rate-sheet/availability schema that existed before this
// field). The zero value ("") means a legacy profile published before
// ProfileMode existed -- see Effective().
type ProfileMode string

const (
	ModeSimple     ProfileMode = "simple"
	ModeTagged     ProfileMode = "tagged"
	ModeStructured ProfileMode = "structured"
)

// Effective returns m, or ModeStructured if m is the zero value -- legacy
// profiles (published before this field existed) only ever had the full
// structured shape, so that's the correct backward-compatible default.
func (m ProfileMode) Effective() ProfileMode {
	if m == "" {
		return ModeStructured
	}
	return m
}

// IsValid reports whether m is one of the three explicitly recognized
// modes. Like Role.IsValid, the zero value is deliberately NOT valid here --
// callers must pick a mode explicitly; "" is only meaningful as the
// deserialization default handled by Effective().
func (m ProfileMode) IsValid() bool {
	return m == ModeSimple || m == ModeTagged || m == ModeStructured
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
	AvailabilityAvailable = "available"
	AvailabilityBusy      = "busy"
	AvailabilityAway      = "away"
	AvailabilityOffline   = "offline"
)

// Validate checks if the profile is valid
func (p *AgentProfile) Validate() error {
	if p.Name == "" {
		return fmt.Errorf("profile name is required")
	}

	if p.Mode != "" && !p.Mode.IsValid() {
		return fmt.Errorf("invalid mode %q: must be %q, %q, or %q", p.Mode, ModeSimple, ModeTagged, ModeStructured)
	}

	// Defense in depth: the CLI's `profile publish` rejects mode/flag
	// mismatches before building the profile, but a profile loaded via
	// --json-file skips that path entirely, so the schema-per-mode
	// constraint has to be enforced here too, not just at the flag layer.
	// Every field outside {name, mode, tags} counts as "structured" here
	// (including a non-nil RateSheet with no rates, and Availability/Version
	// set to anything) -- the simple/tagged JSON schemas in
	// specs/m1.5/tasks/06-profile-register-mode-schema.md are exhaustive,
	// not illustrative, so partial structured data must be rejected too.
	hasStructuredFields := p.Description != "" || p.Availability != "" || p.Version != "" ||
		len(p.Capabilities) > 0 || p.RateSheet != nil || p.Contact != nil || p.Rating != nil
	switch p.Mode.Effective() {
	case ModeSimple:
		if len(p.Tags) > 0 || hasStructuredFields {
			return fmt.Errorf("mode %q only supports 'name', but tagged/structured fields are set", ModeSimple)
		}
	case ModeTagged:
		if hasStructuredFields {
			return fmt.Errorf("mode %q only supports 'name' and 'tags', but structured fields are set", ModeTagged)
		}
		if !hasNonBlankTag(p.Tags) {
			return fmt.Errorf("mode %q requires at least one non-blank tag (use %q if you don't have any yet)", ModeTagged, ModeSimple)
		}
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

// hasNonBlankTag reports whether tags contains at least one entry that isn't
// empty or all-whitespace. The CLI's own --tags flag is pre-cleaned via
// cleanTags before it ever reaches Validate, but --json-file input skips
// that entirely, so {"tags": [" "]} must not count as "has a tag".
func hasNonBlankTag(tags []string) bool {
	for _, t := range tags {
		if strings.TrimSpace(t) != "" {
			return true
		}
	}
	return false
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

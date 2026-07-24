package types

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProfileModeEffective(t *testing.T) {
	assert.Equal(t, ModeStructured, ProfileMode("").Effective())
	assert.Equal(t, ModeSimple, ModeSimple.Effective())
	assert.Equal(t, ModeTagged, ModeTagged.Effective())
	assert.Equal(t, ModeStructured, ModeStructured.Effective())
}

func TestProfileModeIsValid(t *testing.T) {
	assert.True(t, ModeSimple.IsValid())
	assert.True(t, ModeTagged.IsValid())
	assert.True(t, ModeStructured.IsValid())
	assert.False(t, ProfileMode("").IsValid(), "zero value must not be a valid explicit choice")
	assert.False(t, ProfileMode("bogus").IsValid())
}

func TestAgentProfileValidateByMode(t *testing.T) {
	tests := []struct {
		name    string
		profile *AgentProfile
		wantErr bool
		errMsg  string
	}{
		{
			name:    "simple mode with only name is valid",
			profile: &AgentProfile{Name: "Alice", Mode: ModeSimple},
		},
		{
			name:    "simple mode with tags is rejected",
			profile: &AgentProfile{Name: "Alice", Mode: ModeSimple, Tags: []string{"dev"}},
			wantErr: true,
			errMsg:  `mode "simple" only supports 'name'`,
		},
		{
			name:    "simple mode with description is rejected",
			profile: &AgentProfile{Name: "Alice", Mode: ModeSimple, Description: "hi"},
			wantErr: true,
			errMsg:  `mode "simple" only supports 'name'`,
		},
		{
			name:    "tagged mode with name and tags is valid",
			profile: &AgentProfile{Name: "Alice", Mode: ModeTagged, Tags: []string{"dev", "go"}},
		},
		{
			// Codex review round 2 on task 06: tagged mode with zero tags is
			// indistinguishable from simple mode, defeating the point of
			// picking "tagged" -- require at least one.
			name:    "tagged mode with no tags is rejected",
			profile: &AgentProfile{Name: "Alice", Mode: ModeTagged},
			wantErr: true,
			errMsg:  `mode "tagged" requires at least one non-blank tag`,
		},
		{
			// Codex review round 3: the CLI's --tags flag is pre-cleaned via
			// cleanTags, but --json-file input reaches Validate directly, so
			// blank/whitespace-only tags must be rejected here too, not just
			// an empty slice.
			name:    "tagged mode with only blank/whitespace tags is rejected",
			profile: &AgentProfile{Name: "Alice", Mode: ModeTagged, Tags: []string{"", "   "}},
			wantErr: true,
			errMsg:  `mode "tagged" requires at least one non-blank tag`,
		},
		{
			name: "tagged mode with capabilities is rejected",
			profile: &AgentProfile{Name: "Alice", Mode: ModeTagged, Tags: []string{"dev"},
				Capabilities: []Capability{{Name: "seo"}}},
			wantErr: true,
			errMsg:  `mode "tagged" only supports 'name' and 'tags'`,
		},
		{
			name: "structured mode allows the full schema",
			profile: &AgentProfile{Name: "Alice", Mode: ModeStructured, Tags: []string{"dev"},
				Description: "full profile", Capabilities: []Capability{{Name: "seo"}},
				RateSheet: &RateSheet{Rates: []RateEntry{{Service: "audit", Price: 50}}}},
		},
		{
			name:    "legacy profile with no mode field is treated as structured",
			profile: &AgentProfile{Name: "Alice", Description: "old profile", Capabilities: []Capability{{Name: "seo"}}},
		},
		{
			// Codex review finding on task 06: availability/version/an empty-
			// but-non-nil RateSheet must also be rejected for simple/tagged --
			// the schema in the task spec is exhaustive ({name, mode[, tags]}),
			// not just "no capabilities/description/contact".
			name:    "simple mode with availability set is rejected",
			profile: &AgentProfile{Name: "Alice", Mode: ModeSimple, Availability: AvailabilityBusy},
			wantErr: true,
			errMsg:  `mode "simple" only supports 'name'`,
		},
		{
			name:    "tagged mode with version set is rejected",
			profile: &AgentProfile{Name: "Alice", Mode: ModeTagged, Tags: []string{"dev"}, Version: "1.0"},
			wantErr: true,
			errMsg:  `mode "tagged" only supports 'name' and 'tags'`,
		},
		{
			// Task 07 adds Rating to AgentProfile for discover filtering; it's
			// a structured-only field like Availability/Version/RateSheet.
			name: "tagged mode with rating set is rejected",
			profile: &AgentProfile{Name: "Alice", Mode: ModeTagged, Tags: []string{"dev"},
				Rating: func() *float64 { v := 4.5; return &v }()},
			wantErr: true,
			errMsg:  `mode "tagged" only supports 'name' and 'tags'`,
		},
		{
			name:    "tagged mode with an empty but non-nil RateSheet is rejected",
			profile: &AgentProfile{Name: "Alice", Mode: ModeTagged, Tags: []string{"dev"}, RateSheet: &RateSheet{Currency: "USD"}},
			wantErr: true,
			errMsg:  `mode "tagged" only supports 'name' and 'tags'`,
		},
		{
			name:    "invalid mode string is rejected",
			profile: &AgentProfile{Name: "Alice", Mode: ProfileMode("bogus")},
			wantErr: true,
			errMsg:  "invalid mode",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.profile.Validate()
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestLegacyProfileJSONWithoutModeFieldDefaultsToStructured covers the
// backward-compatibility requirement from
// specs/m1.5/tasks/06-profile-register-mode-schema.md: a Kind 0 profile
// event published before the mode field existed must still deserialize (and
// be treated as structured) without erroring.
func TestLegacyProfileJSONWithoutModeFieldDefaultsToStructured(t *testing.T) {
	legacyJSON := `{
		"name": "Old SEO Bot",
		"description": "published before mode existed",
		"availability": "available",
		"capabilities": [{"name": "seo"}],
		"version": "1.0",
		"updated_at": 1700000000
	}`

	var profile AgentProfile
	require.NoError(t, json.Unmarshal([]byte(legacyJSON), &profile))

	assert.Equal(t, ProfileMode(""), profile.Mode, "zero value for a legacy record with no mode key")
	assert.Equal(t, ModeStructured, profile.Mode.Effective())
	require.NoError(t, profile.Validate(), "legacy structured fields must not be rejected once Effective() treats this as structured")
}

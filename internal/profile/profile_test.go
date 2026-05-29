package profile

import (
	"testing"

	"fiatjaf.com/nostr"
	"github.com/AuraAIHQ/agent-speaker/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewAgentProfile(t *testing.T) {
	p := NewAgentProfile("Test Agent")
	assert.Equal(t, "Test Agent", p.Name)
	assert.Equal(t, "1.0", p.Version)
	assert.Equal(t, types.AvailabilityAvailable, p.Availability)
	assert.Empty(t, p.Capabilities)
}

func TestProfileToEvent(t *testing.T) {
	sk := nostr.Generate()
	pk := sk.Public()

	profile := &types.AgentProfile{
		Name:         "SEO Bot",
		Description:  "I optimize websites",
		Availability: types.AvailabilityAvailable,
		Capabilities: []types.Capability{
			{Name: "seo", Description: "Search engine optimization", Tags: []string{"marketing"}},
		},
		RateSheet: &types.RateSheet{
			Currency: "USD",
			Rates: []types.RateEntry{
				{Service: "audit", Unit: "page", Price: 50},
			},
		},
		Version: "1.0",
	}

	event, err := ProfileToEvent(profile, pk)
	require.NoError(t, err)
	assert.Equal(t, nostr.Kind(ProfileKind), event.Kind)
	assert.Equal(t, pk, event.PubKey)

	// Verify tags
	hasDTag := false
	hasCTag := false
	for _, tag := range event.Tags {
		if len(tag) >= 2 && tag[0] == "d" && tag[1] == ProfileDTag {
			hasDTag = true
		}
		if len(tag) >= 2 && tag[0] == "c" && tag[1] == ProfileTag {
			hasCTag = true
		}
	}
	assert.True(t, hasDTag, "event should have d tag")
	assert.True(t, hasCTag, "event should have c tag")
}

func TestProfileToEvent_InvalidProfile(t *testing.T) {
	sk := nostr.Generate()
	pk := sk.Public()

	profile := &types.AgentProfile{
		Name:         "",
		Availability: types.AvailabilityAvailable,
	}

	_, err := ProfileToEvent(profile, pk)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "name is required")
}

func TestEventToProfile(t *testing.T) {
	sk := nostr.Generate()
	pk := sk.Public()

	profile := &types.AgentProfile{
		Name:         "SEO Bot",
		Description:  "I optimize websites",
		Availability: types.AvailabilityBusy,
		Version:      "2.0",
	}

	event, err := ProfileToEvent(profile, pk)
	require.NoError(t, err)

	result, err := EventToProfile(event)
	require.NoError(t, err)
	assert.Equal(t, "SEO Bot", result.Name)
	assert.Equal(t, "I optimize websites", result.Description)
	assert.Equal(t, types.AvailabilityBusy, result.Availability)
	assert.Equal(t, "2.0", result.Version)
}

func TestEventToProfile_WrongKind(t *testing.T) {
	sk := nostr.Generate()
	pk := sk.Public()

	event := &nostr.Event{
		CreatedAt: nostr.Now(),
		Kind:      1,
		Tags:      nostr.Tags{{"c", ProfileTag}},
		Content:   `{"name":"Test"}`,
		PubKey:    pk,
	}
	event.Sign(sk)

	_, err := EventToProfile(event)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "expected kind")
}

func TestEventToProfile_MissingTag(t *testing.T) {
	sk := nostr.Generate()
	pk := sk.Public()

	event := &nostr.Event{
		CreatedAt: nostr.Now(),
		Kind:      ProfileKind,
		Tags:      nostr.Tags{{"d", ProfileDTag}},
		Content:   `{"name":"Test"}`,
		PubKey:    pk,
	}
	event.Sign(sk)

	_, err := EventToProfile(event)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "missing profile tag")
}

func TestIsProfileEvent(t *testing.T) {
	profileEvent := &nostr.Event{
		Kind:    ProfileKind,
		Tags:    nostr.Tags{{"c", ProfileTag}, {"d", ProfileDTag}},
		Content: `{"name":"Test"}`,
	}
	assert.True(t, IsProfileEvent(profileEvent))

	wrongKind := &nostr.Event{
		Kind:    1,
		Tags:    nostr.Tags{{"c", ProfileTag}},
		Content: `{"name":"Test"}`,
	}
	assert.False(t, IsProfileEvent(wrongKind))

	missingTag := &nostr.Event{
		Kind:    ProfileKind,
		Tags:    nostr.Tags{{"d", ProfileDTag}},
		Content: `{"name":"Test"}`,
	}
	assert.False(t, IsProfileEvent(missingTag))
}

func TestBuildFilter(t *testing.T) {
	filter := BuildFilter(nil, 0)
	assert.Equal(t, []nostr.Kind{ProfileKind}, filter.Kinds)
	assert.Equal(t, nostr.TagMap{"c": []string{ProfileTag}}, filter.Tags)
	assert.Empty(t, filter.Authors)
	assert.Zero(t, filter.Limit)

	pk := nostr.Generate().Public()
	filter2 := BuildFilter([]nostr.PubKey{pk}, 10)
	assert.Equal(t, []nostr.PubKey{pk}, filter2.Authors)
	assert.Equal(t, 10, filter2.Limit)
}

func TestAgentProfileValidate(t *testing.T) {
	tests := []struct {
		name    string
		profile *types.AgentProfile
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid profile",
			profile: &types.AgentProfile{Name: "Test", Availability: types.AvailabilityAvailable},
			wantErr: false,
		},
		{
			name:    "missing name",
			profile: &types.AgentProfile{Name: ""},
			wantErr: true,
			errMsg:  "name is required",
		},
		{
			name:    "invalid availability",
			profile: &types.AgentProfile{Name: "Test", Availability: "invalid"},
			wantErr: true,
			errMsg:  "invalid availability",
		},
		{
			name:    "empty availability is valid",
			profile: &types.AgentProfile{Name: "Test", Availability: ""},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.profile.Validate()
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestAgentProfileHasCapability(t *testing.T) {
	p := &types.AgentProfile{
		Capabilities: []types.Capability{
			{Name: "seo"},
			{Name: "writing"},
		},
	}

	assert.True(t, p.HasCapability("seo"))
	assert.True(t, p.HasCapability("writing"))
	assert.False(t, p.HasCapability("coding"))
}

func TestAgentProfileAddCapability(t *testing.T) {
	p := &types.AgentProfile{Capabilities: []types.Capability{}}
	p.AddCapability(types.Capability{Name: "seo"})
	assert.Len(t, p.Capabilities, 1)

	// Duplicate should not be added
	p.AddCapability(types.Capability{Name: "seo"})
	assert.Len(t, p.Capabilities, 1)

	p.AddCapability(types.Capability{Name: "writing"})
	assert.Len(t, p.Capabilities, 2)
}

func TestAgentProfileRemoveCapability(t *testing.T) {
	p := &types.AgentProfile{
		Capabilities: []types.Capability{
			{Name: "seo"},
			{Name: "writing"},
			{Name: "coding"},
		},
	}

	p.RemoveCapability("writing")
	assert.Len(t, p.Capabilities, 2)
	assert.True(t, p.HasCapability("seo"))
	assert.True(t, p.HasCapability("coding"))
	assert.False(t, p.HasCapability("writing"))

	// Removing non-existent should not panic
	p.RemoveCapability("design")
	assert.Len(t, p.Capabilities, 2)
}

func TestAgentProfileToJSON(t *testing.T) {
	p := &types.AgentProfile{Name: "Test", Version: "1.0"}
	data, err := p.ToJSON()
	require.NoError(t, err)
	assert.Contains(t, string(data), `"name":"Test"`)
}

func TestAgentProfileFromJSON(t *testing.T) {
	data := []byte(`{"name":"Test Agent","version":"1.0","availability":"available","capabilities":[{"name":"seo"}]}`)
	p, err := types.AgentProfileFromJSON(data)
	require.NoError(t, err)
	assert.Equal(t, "Test Agent", p.Name)
	assert.Equal(t, "1.0", p.Version)
	assert.True(t, p.HasCapability("seo"))
}

func TestAgentProfileFromJSON_Invalid(t *testing.T) {
	_, err := types.AgentProfileFromJSON([]byte(`{invalid`))
	assert.Error(t, err)
}

func TestParseCapability(t *testing.T) {
	cap := parseCapability("seo:Search optimization")
	assert.Equal(t, "seo", cap.Name)
	assert.Equal(t, "Search optimization", cap.Description)

	cap2 := parseCapability("writing")
	assert.Equal(t, "writing", cap2.Name)
	assert.Empty(t, cap2.Description)
}

func TestParseRateEntry(t *testing.T) {
	tests := []struct {
		input    string
		expected types.RateEntry
	}{
		{
			input:    "audit",
			expected: types.RateEntry{Service: "audit"},
		},
		{
			input:    "audit:page",
			expected: types.RateEntry{Service: "audit", Unit: "page"},
		},
		{
			input:    "audit:page:50",
			expected: types.RateEntry{Service: "audit", Unit: "page", Price: 50},
		},
		{
			input:    "audit:page:50:Full SEO audit",
			expected: types.RateEntry{Service: "audit", Unit: "page", Price: 50, Description: "Full SEO audit"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := parseRateEntry(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestSplitRateString(t *testing.T) {
	parts := splitRateString("a:b:c:d")
	assert.Equal(t, []string{"a", "b", "c", "d"}, parts)

	parts2 := splitRateString("single")
	assert.Equal(t, []string{"single"}, parts2)

	parts3 := splitRateString("")
	assert.Equal(t, []string{""}, parts3)
}

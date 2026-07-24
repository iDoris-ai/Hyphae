package profile

import (
	"testing"

	"github.com/AuraAIHQ/agent-speaker/pkg/types"
	"github.com/stretchr/testify/assert"
)

func ratingPtr(v float64) *float64 { return &v }
func intPtr(v int) *int            { return &v }

func structuredProfile() *types.AgentProfile {
	return &types.AgentProfile{
		Name: "SEO Bot",
		Mode: types.ModeStructured,
		Capabilities: []types.Capability{
			{Name: "seo"},
			{Name: "writing"},
		},
		RateSheet: &types.RateSheet{
			Currency: "USD",
			Rates: []types.RateEntry{
				{Service: "audit", Price: 200},
			},
		},
		Rating:       ratingPtr(4.8),
		Availability: types.AvailabilityAvailable,
	}
}

func TestDiscoverFilterIsZero(t *testing.T) {
	assert.True(t, DiscoverFilter{}.IsZero())
	assert.False(t, DiscoverFilter{Capability: "seo"}.IsZero())
	assert.False(t, DiscoverFilter{OnlineOnly: true}.IsZero())
	assert.False(t, DiscoverFilter{PriceMin: intPtr(1)}.IsZero())
	assert.False(t, DiscoverFilter{RatingMin: ratingPtr(1)}.IsZero())
}

// TestDiscoverFilterMatches_NoFilterMatchesEverything covers acceptance
// criterion 2: not passing any filter flag must behave like the pre-existing
// discover (everything matches).
func TestDiscoverFilterMatches_NoFilterMatchesEverything(t *testing.T) {
	simple := &types.AgentProfile{Name: "Simple Agent", Mode: types.ModeSimple}
	structured := structuredProfile()

	f := DiscoverFilter{}
	assert.True(t, f.Matches(simple))
	assert.True(t, f.Matches(structured))
}

func TestDiscoverFilterMatches_Capability(t *testing.T) {
	structured := structuredProfile()

	assert.True(t, DiscoverFilter{Capability: "seo"}.Matches(structured))
	assert.False(t, DiscoverFilter{Capability: "coding"}.Matches(structured))
}

// TestDiscoverFilterMatches_SimpleTaggedExcludedFromCapabilityFilter covers
// acceptance criterion 3: simple/tagged profiles have no capabilities, so a
// --capability filter must exclude them without erroring or panicking.
func TestDiscoverFilterMatches_SimpleTaggedExcludedFromCapabilityFilter(t *testing.T) {
	simple := &types.AgentProfile{Name: "Simple Agent", Mode: types.ModeSimple}
	tagged := &types.AgentProfile{Name: "Tagged Agent", Mode: types.ModeTagged, Tags: []string{"dev"}}

	f := DiscoverFilter{Capability: "seo"}
	assert.NotPanics(t, func() {
		assert.False(t, f.Matches(simple))
		assert.False(t, f.Matches(tagged))
	})
}

func TestDiscoverFilterMatches_PriceRange(t *testing.T) {
	structured := structuredProfile() // has a single rate at price 200

	tests := []struct {
		name string
		f    DiscoverFilter
		want bool
	}{
		{"within range", DiscoverFilter{PriceMin: intPtr(100), PriceMax: intPtr(300)}, true},
		{"below range", DiscoverFilter{PriceMin: intPtr(250)}, false},
		{"above range", DiscoverFilter{PriceMax: intPtr(150)}, false},
		{"min only, satisfied", DiscoverFilter{PriceMin: intPtr(200)}, true},
		{"max only, satisfied", DiscoverFilter{PriceMax: intPtr(200)}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.f.Matches(structured))
		})
	}

	simple := &types.AgentProfile{Name: "Simple Agent", Mode: types.ModeSimple}
	assert.False(t, DiscoverFilter{PriceMin: intPtr(0)}.Matches(simple), "no rate sheet must not match a price filter")
}

// TestDiscoverFilterMatches_PriceIsNotTruncatedToInt covers a Codex review
// finding: comparing rate.Price (float64) against int bounds must not
// truncate the price first, or a fractional price could wrongly satisfy a
// boundary it doesn't actually meet.
func TestDiscoverFilterMatches_PriceIsNotTruncatedToInt(t *testing.T) {
	profile := &types.AgentProfile{
		Name: "Fractional Price Bot",
		Mode: types.ModeStructured,
		RateSheet: &types.RateSheet{
			Rates: []types.RateEntry{{Service: "audit", Price: 100.99}},
		},
	}

	assert.False(t, DiscoverFilter{PriceMax: intPtr(100)}.Matches(profile),
		"100.99 truncated to int(100) would wrongly satisfy price-max 100")
	assert.True(t, DiscoverFilter{PriceMax: intPtr(101)}.Matches(profile))
	assert.True(t, DiscoverFilter{PriceMin: intPtr(100)}.Matches(profile))
}

// TestDiscoverFilterMatches_MalformedProfileGatedByMode covers a Codex
// review finding: relay data isn't trusted to have gone through our own
// Validate(), so a profile declaring mode=simple/tagged but still carrying
// capabilities/rate_sheet/rating (a malformed or adversarial event) must be
// excluded from structured-only filters based on its declared Mode, not
// merely because the fields happen to be present.
func TestDiscoverFilterMatches_MalformedProfileGatedByMode(t *testing.T) {
	malformedSimple := &types.AgentProfile{
		Name:         "Malformed Simple",
		Mode:         types.ModeSimple,
		Capabilities: []types.Capability{{Name: "seo"}},
		RateSheet:    &types.RateSheet{Rates: []types.RateEntry{{Service: "audit", Price: 200}}},
		Rating:       ratingPtr(5.0),
	}

	assert.False(t, DiscoverFilter{Capability: "seo"}.Matches(malformedSimple))
	assert.False(t, DiscoverFilter{PriceMin: intPtr(0)}.Matches(malformedSimple))
	assert.False(t, DiscoverFilter{RatingMin: ratingPtr(0)}.Matches(malformedSimple))
	// OnlineOnly isn't a structured-only filter -- Availability applies
	// regardless of mode, so it should still evaluate normally.
	malformedSimple.Availability = types.AvailabilityAvailable
	assert.True(t, DiscoverFilter{OnlineOnly: true}.Matches(malformedSimple))
}

func TestDiscoverFilterMatches_RatingMin(t *testing.T) {
	structured := structuredProfile() // rating 4.8

	assert.True(t, DiscoverFilter{RatingMin: ratingPtr(4.5)}.Matches(structured))
	assert.False(t, DiscoverFilter{RatingMin: ratingPtr(4.9)}.Matches(structured))

	noRating := &types.AgentProfile{Name: "No Rating Agent", Mode: types.ModeStructured}
	assert.False(t, DiscoverFilter{RatingMin: ratingPtr(0)}.Matches(noRating), "unset rating must not match a rating-min filter, even rating-min 0")
}

func TestDiscoverFilterMatches_OnlineOnly(t *testing.T) {
	available := &types.AgentProfile{Name: "A", Availability: types.AvailabilityAvailable}
	busy := &types.AgentProfile{Name: "B", Availability: types.AvailabilityBusy}
	unset := &types.AgentProfile{Name: "C"}

	f := DiscoverFilter{OnlineOnly: true}
	assert.True(t, f.Matches(available))
	assert.False(t, f.Matches(busy))
	assert.False(t, f.Matches(unset))
}

// TestDiscoverFilterMatches_Combined covers AND semantics across multiple
// simultaneously-set filters.
func TestDiscoverFilterMatches_Combined(t *testing.T) {
	structured := structuredProfile()

	allSatisfied := DiscoverFilter{
		Capability: "seo",
		PriceMin:   intPtr(100),
		PriceMax:   intPtr(300),
		RatingMin:  ratingPtr(4.0),
		OnlineOnly: true,
	}
	assert.True(t, allSatisfied.Matches(structured))

	oneUnsatisfied := DiscoverFilter{
		Capability: "seo",
		RatingMin:  ratingPtr(4.9), // structured's rating is 4.8, fails this one
		OnlineOnly: true,
	}
	assert.False(t, oneUnsatisfied.Matches(structured))
}

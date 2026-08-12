package profile

import (
	"testing"

	"github.com/iDoris-ai/hyphae/pkg/types"
	"github.com/stretchr/testify/assert"
)

func floatPtr(v float64) *float64 { return &v }

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
		Rating:       floatPtr(4.8),
		Availability: types.AvailabilityAvailable,
	}
}

func TestDiscoverFilterIsZero(t *testing.T) {
	assert.True(t, DiscoverFilter{}.IsZero())
	assert.False(t, DiscoverFilter{Capability: "seo"}.IsZero())
	assert.False(t, DiscoverFilter{OnlineOnly: true}.IsZero())
	assert.False(t, DiscoverFilter{PriceMin: floatPtr(1)}.IsZero())
	assert.False(t, DiscoverFilter{RatingMin: floatPtr(1)}.IsZero())
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
		{"within range", DiscoverFilter{PriceMin: floatPtr(100), PriceMax: floatPtr(300)}, true},
		{"below range", DiscoverFilter{PriceMin: floatPtr(250)}, false},
		{"above range", DiscoverFilter{PriceMax: floatPtr(150)}, false},
		{"min only, satisfied", DiscoverFilter{PriceMin: floatPtr(200)}, true},
		{"max only, satisfied", DiscoverFilter{PriceMax: floatPtr(200)}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.f.Matches(structured))
		})
	}

	simple := &types.AgentProfile{Name: "Simple Agent", Mode: types.ModeSimple}
	assert.False(t, DiscoverFilter{PriceMin: floatPtr(0)}.Matches(simple), "no rate sheet must not match a price filter")
}

// TestDiscoverFilterMatches_FractionalPriceBounds covers fractional prices
// on both sides of the comparison: PriceMin/PriceMax are now *float64 (the
// CLI flags are cli.FloatFlag, not cli.IntFlag), matching RateEntry.Price's
// own float64 type, so a boundary like --price-max 100.99 can be expressed
// and compared exactly instead of only ever being able to pass a whole
// integer bound against a fractional price.
func TestDiscoverFilterMatches_FractionalPriceBounds(t *testing.T) {
	profile := &types.AgentProfile{
		Name: "Fractional Price Bot",
		Mode: types.ModeStructured,
		RateSheet: &types.RateSheet{
			Rates: []types.RateEntry{{Service: "audit", Price: 100.99}},
		},
	}

	assert.False(t, DiscoverFilter{PriceMax: floatPtr(100)}.Matches(profile))
	assert.False(t, DiscoverFilter{PriceMax: floatPtr(100.98)}.Matches(profile))
	assert.True(t, DiscoverFilter{PriceMax: floatPtr(100.99)}.Matches(profile), "an exact fractional boundary must match")
	assert.True(t, DiscoverFilter{PriceMax: floatPtr(101)}.Matches(profile))
	assert.True(t, DiscoverFilter{PriceMin: floatPtr(100)}.Matches(profile))
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
		Rating:       floatPtr(5.0),
	}

	assert.False(t, DiscoverFilter{Capability: "seo"}.Matches(malformedSimple))
	assert.False(t, DiscoverFilter{PriceMin: floatPtr(0)}.Matches(malformedSimple))
	assert.False(t, DiscoverFilter{RatingMin: floatPtr(0)}.Matches(malformedSimple))
	// OnlineOnly isn't a structured-only filter -- Availability applies
	// regardless of mode, so it should still evaluate normally.
	malformedSimple.Availability = types.AvailabilityAvailable
	assert.True(t, DiscoverFilter{OnlineOnly: true}.Matches(malformedSimple))
}

func TestDiscoverFilterMatches_RatingMin(t *testing.T) {
	structured := structuredProfile() // rating 4.8

	assert.True(t, DiscoverFilter{RatingMin: floatPtr(4.5)}.Matches(structured))
	assert.False(t, DiscoverFilter{RatingMin: floatPtr(4.9)}.Matches(structured))

	noRating := &types.AgentProfile{Name: "No Rating Agent", Mode: types.ModeStructured}
	assert.False(t, DiscoverFilter{RatingMin: floatPtr(0)}.Matches(noRating), "unset rating must not match a rating-min filter, even rating-min 0")
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
		PriceMin:   floatPtr(100),
		PriceMax:   floatPtr(300),
		RatingMin:  floatPtr(4.0),
		OnlineOnly: true,
	}
	assert.True(t, allSatisfied.Matches(structured))

	oneUnsatisfied := DiscoverFilter{
		Capability: "seo",
		RatingMin:  floatPtr(4.9), // structured's rating is 4.8, fails this one
		OnlineOnly: true,
	}
	assert.False(t, oneUnsatisfied.Matches(structured))
}

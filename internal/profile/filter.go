package profile

import "github.com/AuraAIHQ/agent-speaker/pkg/types"

// DiscoverFilter narrows `profile discover` results. Every field is
// optional; the filters that are set combine with AND semantics. The zero
// value (IsZero() == true) imposes no constraint at all, matching the
// pre-existing discover behavior with no filter flags.
type DiscoverFilter struct {
	Capability string
	PriceMin   *int
	PriceMax   *int
	RatingMin  *float64
	OnlineOnly bool
}

// IsZero reports whether f has no filters set.
func (f DiscoverFilter) IsZero() bool {
	return f.Capability == "" && f.PriceMin == nil && f.PriceMax == nil && f.RatingMin == nil && !f.OnlineOnly
}

// Matches reports whether profile satisfies every constraint set on f.
// simple/tagged-mode profiles carry no capabilities, rate sheet, or rating,
// so a --capability/--price-*/--rating-min filter simply excludes them --
// that's not an error, it's the documented behavior for those modes.
func (f DiscoverFilter) Matches(profile *types.AgentProfile) bool {
	if f.Capability != "" && !profile.HasCapability(f.Capability) {
		return false
	}
	if (f.PriceMin != nil || f.PriceMax != nil) && !matchesPriceRange(profile, f.PriceMin, f.PriceMax) {
		return false
	}
	if f.RatingMin != nil && (profile.Rating == nil || *profile.Rating < *f.RatingMin) {
		return false
	}
	if f.OnlineOnly && profile.Availability != types.AvailabilityAvailable {
		return false
	}
	return true
}

// matchesPriceRange reports whether profile has at least one rate entry
// whose price falls within [min, max] (either bound may be nil, meaning
// unbounded on that side). A profile with no rate sheet (simple/tagged
// mode, or a structured profile that just didn't set any rates) never
// matches a price filter.
func matchesPriceRange(profile *types.AgentProfile, min, max *int) bool {
	if profile.RateSheet == nil {
		return false
	}
	for _, rate := range profile.RateSheet.Rates {
		price := int(rate.Price)
		if min != nil && price < *min {
			continue
		}
		if max != nil && price > *max {
			continue
		}
		return true
	}
	return false
}

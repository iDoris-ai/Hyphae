package profile

import "github.com/iDoris-ai/hyphae/pkg/types"

// DiscoverFilter narrows `profile discover` results. Every field is
// optional; the filters that are set combine with AND semantics. The zero
// value (IsZero() == true) imposes no constraint at all, matching the
// pre-existing discover behavior with no filter flags.
type DiscoverFilter struct {
	Capability string
	PriceMin   *float64
	PriceMax   *float64
	RatingMin  *float64
	OnlineOnly bool
}

// IsZero reports whether f has no filters set.
func (f DiscoverFilter) IsZero() bool {
	return f.Capability == "" && f.PriceMin == nil && f.PriceMax == nil && f.RatingMin == nil && !f.OnlineOnly
}

// Matches reports whether profile satisfies every constraint set on f.
// Capability/price/rating are structured-only fields (per
// specs/m1.5/tasks/07-profile-discover-filters.md's design: "Capability 只对
// structured 模式的 profile 生效") -- a non-structured profile is excluded
// from those filters based on its declared Mode, not merely on whether it
// happens to carry the field. Relay data isn't trusted to have gone through
// our own Validate(): a malformed/adversarial event could declare
// mode:"simple" while still carrying capabilities/rate_sheet/rating, and
// checking Mode explicitly (instead of only checking field presence) keeps
// that from being treated as a structured match.
func (f DiscoverFilter) Matches(profile *types.AgentProfile) bool {
	wantsStructuredFilter := f.Capability != "" || f.PriceMin != nil || f.PriceMax != nil || f.RatingMin != nil
	if wantsStructuredFilter && profile.Mode.Effective() != types.ModeStructured {
		return false
	}

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
// unbounded on that side). A profile with no rate sheet never matches a
// price filter.
func matchesPriceRange(profile *types.AgentProfile, min, max *float64) bool {
	if profile.RateSheet == nil {
		return false
	}
	for _, rate := range profile.RateSheet.Rates {
		if min != nil && rate.Price < *min {
			continue
		}
		if max != nil && rate.Price > *max {
			continue
		}
		return true
	}
	return false
}

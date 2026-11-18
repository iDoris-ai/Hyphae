package friend

import (
	"context"

	"fiatjaf.com/nostr"

	"github.com/iDoris-ai/hyphae/internal/card"
	"github.com/iDoris-ai/hyphae/internal/storage"
	"github.com/iDoris-ai/hyphae/pkg/types"
)

// RefreshAfter is how old a live card may get before the daemon republishes
// it; with a 30-day lifetime a daemon that runs weekly keeps the card live.
const RefreshAfter = 7 * 24 * 60 * 60

// RefreshResult says what Refresh did.
type RefreshResult struct {
	// Published is true when a relay accepted a new card.
	Published bool
	// FirstTime is true when this is the identity's first accepted card:
	// it was public by default and is now findable, so tell the person.
	FirstTime bool
	Card      storage.CardSetting
}

// Refresh publishes the identity's card when it is public and has no card
// newer than RefreshAfter, and resends the tombstone when a hide reached no
// relay while the old card is still live. It holds the card lock, so it never
// races `hyphae friend visibility`.
func Refresh(ctx context.Context, id *types.Identity, sk nostr.SecretKey, relays []string, now int64) (RefreshResult, error) {
	var res RefreshResult
	err := storage.WithCardLock(func() error {
		db, err := storage.OpenDB()
		if err != nil {
			return err
		}
		defer func() { _ = db.Close() }()
		s, err := storage.LoadCardSetting(db, id.Npub)
		if err != nil {
			return err
		}
		due := s.Visible && s.ExpiresAt-now <= card.Lifetime-RefreshAfter
		retryHide := !s.Visible && s.ExpiresAt > now
		if !due && !retryHide {
			res.Card = s
			return nil
		}
		first := s.Visible && s.PublishedAt == 0
		s, o, err := publishAndSave(ctx, db, id.Npub, sk, relays, withDefaults(s, id.Nickname))
		res.Card = s
		for _, r := range o.results {
			res.Published = res.Published || r.OK
		}
		res.FirstTime = first && res.Published
		return err
	})
	return res, err
}

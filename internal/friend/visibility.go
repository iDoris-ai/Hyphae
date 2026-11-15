// Package friend implements the `hyphae friend` commands: directory-card
// visibility and tags, search, and the stranger rules
// (docs/search/discover-friends.md).
package friend

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"slices"
	"time"

	"fiatjaf.com/nostr"
	"github.com/urfave/cli/v3"

	"github.com/iDoris-ai/hyphae/internal/card"
	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/internal/identity"
	"github.com/iDoris-ai/hyphae/internal/storage"
	"github.com/iDoris-ai/hyphae/internal/wireevent"
)

// Cmd is the `hyphae friend` command group.
var Cmd = NewCmd()

// NewCmd builds a fresh tree: flags keep parsed values between in-process runs.
func NewCmd() *cli.Command {
	return &cli.Command{
		Name:     "friend",
		Usage:    "Find people on your relay and manage strangers and friends",
		Commands: []*cli.Command{visibilityCmd(), tagsCmd()},
	}
}

func commonFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{Name: "as", Aliases: []string{"a"}, Usage: "Your nickname", Required: true},
		&cli.StringSliceFlag{Name: "relay", Aliases: []string{"r"}, Usage: "Relay URLs"},
		&cli.BoolFlag{Name: "password-stdin", Usage: "Read the keystore password from stdin"},
	}
}

func visibilityCmd() *cli.Command {
	return &cli.Command{
		Name:      "visibility",
		Usage:     "Set whether others can find you: public (default) or hidden",
		ArgsUsage: "public|hidden",
		Flags: append(commonFlags(),
			&cli.StringFlag{Name: "name", Usage: "Name on your card (default: your nickname)"},
			&cli.StringSliceFlag{Name: "tags", Usage: "Tags on your card, comma-separated (default: " + card.DefaultTag + ")"},
			&cli.StringFlag{Name: "about", Usage: "One line about you"},
		),
		Action: func(ctx context.Context, c *cli.Command) error {
			switch arg := c.Args().First(); arg {
			case "public", "hidden":
				return apply(ctx, c, false, func(s *storage.CardSetting) error {
					s.Visible = arg == "public"
					return mergeFlags(c, s)
				})
			default:
				return common.NewExitError(common.ErrCodeUser, fmt.Errorf("want public or hidden, got %q", arg))
			}
		},
	}
}

func tagsCmd() *cli.Command {
	return &cli.Command{
		Name:  "tags",
		Usage: "Set the tags on your card; republished only if you are already public",
		Flags: append(commonFlags(),
			&cli.StringSliceFlag{Name: "set", Usage: "Tags, comma-separated", Required: true},
		),
		Action: func(ctx context.Context, c *cli.Command) error {
			return apply(ctx, c, true, func(s *storage.CardSetting) error { return setTags(c, "set", s) })
		},
	}
}

func mergeFlags(c *cli.Command, s *storage.CardSetting) error {
	if c.IsSet("name") {
		s.Name = c.String("name")
	}
	if c.IsSet("about") {
		s.About = c.String("about")
	}
	if c.IsSet("tags") {
		return setTags(c, "tags", s)
	}
	return nil
}

// setTags normalizes the tags given in flag. A set flag always yields at
// least one value (an empty one is rejected by NormalizeTag).
func setTags(c *cli.Command, flag string, s *storage.CardSetting) error {
	tags, err := card.NormalizeTags(c.StringSlice(flag))
	if err != nil {
		return common.NewExitErrorWithData(common.ErrCodeInvalidTag, err, map[string]any{"reason": err.Error()})
	}
	s.Tags = tags
	return nil
}

// withDefaults fills the name and tags a fresh identity gets.
func withDefaults(s storage.CardSetting, nickname string) storage.CardSetting {
	if s.Name == "" {
		s.Name = nickname
	}
	if len(s.Tags) == 0 {
		s.Tags = []string{card.DefaultTag}
	}
	return s
}

type cardView struct {
	Visibility string   `json:"visibility"`
	Name       string   `json:"name"`
	Tags       []string `json:"tags"`
	About      string   `json:"about,omitempty"`
	ExpiresAt  int64    `json:"expires_at"`
}

func view(s storage.CardSetting) cardView {
	vis := map[bool]string{true: "public", false: "hidden"}[s.Visible]
	return cardView{Visibility: vis, Name: s.Name, Tags: s.Tags, About: s.About, ExpiresAt: s.ExpiresAt}
}

// apply loads the setting, lets mutate change it, publishes the card (or the
// tombstone when hidden) and stores the result, all under the card lock so a
// concurrent run cannot drop relays. The setting is saved even when no relay
// accepted the event; the command then fails with network_error. With
// onlyIfPublic, a setting whose card is not live (never published, hidden
// or expired) is saved without publishing.
func apply(ctx context.Context, c *cli.Command, onlyIfPublic bool, mutate func(*storage.CardSetting) error) error {
	jsonMode := common.JSONMode(c)
	ks, err := identity.LoadKeyStoreForCommand(identity.KeyStoreCommandOptions{
		JSONMode: jsonMode, RequireSecret: true, PasswordStdin: c.Bool("password-stdin"), Stdin: os.Stdin,
	})
	if err != nil {
		return err
	}
	me, err := identity.GetIdentity(ks, c.String("as"))
	if err != nil {
		return err
	}
	sk, err := identity.GetSecretKey(ks, me.Nickname)
	if err != nil {
		return err
	}
	relays, err := common.ResolveRelays(c)
	if err != nil {
		return err
	}
	return storage.WithCardLock(func() error {
		db, err := storage.OpenDB()
		if err != nil {
			return err
		}
		defer func() { _ = db.Close() }()
		s, err := storage.LoadCardSetting(db, me.Npub)
		if err != nil {
			return err
		}
		// Republish only a card that is live now. Public is the default, but
		// the first card goes out from `visibility public` or the daemon,
		// never as a side effect of editing tags.
		publishNow := !onlyIfPublic || (s.Visible && s.PublishedAt > 0 && s.ExpiresAt > int64(nostr.Now()))
		if err := mutate(&s); err != nil {
			return err
		}
		s = withDefaults(s, me.Nickname)
		if c.IsSet("name") || c.IsSet("about") {
			// Check edits now, even when hiding: a bad name saved while hidden
			// would only fail at the next `public`.
			if err := (card.Card{Name: s.Name, Tags: s.Tags, About: s.About}).Validate(); err != nil {
				return common.NewExitError(common.ErrCodeUser, err)
			}
		}
		if !publishNow {
			if err := storage.SaveCardSetting(db, me.Npub, s); err != nil {
				return err
			}
			common.Emit(jsonMode, struct {
				cardView
				Published   bool `json:"published"`
				PublishedTo int  `json:"published_to"`
			}{view(s), false, 0}, func() {
				fmt.Printf("Saved tags %v; not published. Run `hyphae friend visibility public --as %s` to publish.\n",
					s.Tags, me.Nickname)
			})
			return nil
		}
		return publishSetting(ctx, jsonMode, db, me.Npub, sk, relays, s)
	})
}

func publishSetting(ctx context.Context, jsonMode bool, db *sql.DB, npub string, sk nostr.SecretKey,
	relays []string, s storage.CardSetting) error {
	// A replacement must be strictly newer: relays keep the lower event ID on
	// a created_at tie, so a toggle within the same second would be lost.
	now := max(nostr.Now(), nostr.Timestamp(s.PublishedAt+1))
	if s.Visible && int64(now) > int64(nostr.Now())+card.MaxClockSkew {
		// Readers reject a card dated this far ahead; a hide still goes out.
		return common.NewExitError(common.ErrCodeUser, fmt.Errorf(
			"your last card is dated %s, ahead of this computer's clock; check the clock and retry after that time",
			time.Unix(s.PublishedAt, 0).Format(time.DateTime)))
	}

	var events []nostr.Event
	targets := relays
	if s.Visible {
		ev, err := card.Card{Name: s.Name, Tags: s.Tags, About: s.About}.Event(sk.Public(), now)
		if err != nil {
			return common.NewExitError(common.ErrCodeUser, err)
		}
		events = append(events, *ev)
	} else {
		// Hide everywhere the card ever went, not only on today's relays.
		targets = union(s.Relays, relays)
		events = append(events, *card.Tombstone(sk.Public(), now), deletion(sk.Public(), now))
	}
	for i := range events {
		if err := events[i].Sign(sk); err != nil {
			return fmt.Errorf("sign: %w", err)
		}
	}
	results, okURLs, touched := publish(ctx, targets, events)
	ok := len(okURLs)
	if touched {
		// Some relay holds an event at now, so the next one must be newer.
		s.PublishedAt = max(s.PublishedAt, int64(now))
	}
	var missed []string // relays that still hold the visible card
	if s.Visible && ok > 0 {
		s.ExpiresAt = int64(now) + card.Lifetime
		s.Relays = union(s.Relays, okURLs)
	} else if !s.Visible {
		for _, url := range s.Relays {
			if !slices.Contains(okURLs, url) {
				missed = append(missed, url)
			}
		}
		if (len(missed) == 0 && ok > 0) || s.ExpiresAt <= int64(nostr.Now()) {
			// ExpiresAt > 0 while hidden marks an unfinished hide (the daemon
			// resends it); an expired card needs no hide.
			s.ExpiresAt = 0
		}
	}
	if err := storage.SaveCardSetting(db, npub, s); err != nil {
		return err
	}
	out := struct {
		cardView
		Relays      []relayResult `json:"relays"`
		PublishedTo int           `json:"published_to"`
	}{view(s), results, ok}
	if !s.Visible && s.ExpiresAt > 0 {
		return common.NewExitErrorWithData(common.ErrCodeNetwork, fmt.Errorf(
			"%d relay(s) holding your card did not accept the hide (%v); the card stays findable there until %s unless you retry",
			len(missed), missed, time.Unix(s.ExpiresAt, 0).Format(time.DateTime)), out)
	}
	if ok == 0 {
		return common.NewExitErrorWithData(common.ErrCodeNetwork, errors.New("no relay accepted the card"), out)
	}
	common.Emit(jsonMode, out, func() {
		fmt.Printf("Visibility: %s, name %s, tags %v; sent to %d/%d relays.\n",
			out.Visibility, out.Name, out.Tags, ok, len(targets))
		if s.Visible {
			fmt.Printf("Card expires %s unless republished (the daemon does this while running).\n",
				time.Unix(s.ExpiresAt, 0).Format(time.DateTime))
		} else {
			fmt.Println("Hidden: new searches will not find you. Copies others already fetched cannot be recalled.")
		}
	})
	return nil
}

// deletion is the NIP-09 request for the card's address; relays that honour
// it drop the card, others still see the tombstone.
func deletion(pk nostr.PubKey, now nostr.Timestamp) nostr.Event {
	addr := fmt.Sprintf("%d:%s:%s", wireevent.Kind30078, pk.Hex(), wireevent.CardCategory)
	return nostr.Event{Kind: nostr.KindDeletion, CreatedAt: now, PubKey: pk,
		Tags: nostr.Tags{{"a", addr}, {"k", fmt.Sprint(wireevent.Kind30078)}}}
}

func union(a, b []string) []string {
	out := slices.Clone(a)
	for _, x := range b {
		if !slices.Contains(out, x) {
			out = append(out, x)
		}
	}
	return out
}

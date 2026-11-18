package friend

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"slices"
	"time"

	"fiatjaf.com/nostr"
	"github.com/urfave/cli/v3"

	"github.com/iDoris-ai/hyphae/internal/card"
	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/internal/identity"
	"github.com/iDoris-ai/hyphae/internal/relayquery"
	"github.com/iDoris-ai/hyphae/internal/social/contact"
	"github.com/iDoris-ai/hyphae/internal/storage"
	"github.com/iDoris-ai/hyphae/internal/wireevent"
	"github.com/iDoris-ai/hyphae/pkg/types"
)

// MaxFindResults caps one search, and is also the per-relay fetch size so
// ranking sees every candidate a relay returns.
const MaxFindResults = 100

type findResult struct {
	Npub          string   `json:"npub"`
	Name          string   `json:"name"`
	Tags          []string `json:"tags"`
	About         string   `json:"about,omitempty"`
	Matched       []string `json:"matched"`
	Relation      string   `json:"relation"`
	CardUpdatedAt int64    `json:"card_updated_at"`
}

func findCmd() *cli.Command {
	return &cli.Command{
		Name:  "find",
		Usage: "Find public people on your relays by tag (any tag matches)",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "as", Aliases: []string{"a"}, Usage: "Your nickname", Required: true},
			&cli.StringSliceFlag{Name: "relay", Aliases: []string{"r"}, Usage: "Relay URLs"},
			&cli.StringSliceFlag{Name: "tag", Aliases: []string{"t"}, Usage: "Tag to match (repeatable)", Value: []string{card.DefaultTag}},
			&cli.IntFlag{Name: "limit", Usage: fmt.Sprintf("Most results to show (1..%d)", MaxFindResults), Value: 20},
			&cli.IntFlag{Name: "timeout", Usage: "Seconds to wait for each relay", Value: 10},
		},
		Action: find,
	}
}

func find(ctx context.Context, c *cli.Command) error {
	jsonMode := common.JSONMode(c)
	limit := c.Int("limit")
	if limit < 1 || limit > MaxFindResults {
		return common.NewExitError(common.ErrCodeUser, fmt.Errorf("--limit must be 1..%d", MaxFindResults))
	}
	filter, err := card.Filter(c.StringSlice("tag"), MaxFindResults)
	if err != nil {
		return common.NewExitErrorWithData(common.ErrCodeInvalidTag, err, map[string]any{"reason": err.Error()})
	}
	ks, err := identity.LoadKeyStoreForCommand(identity.KeyStoreCommandOptions{JSONMode: jsonMode, Stdin: os.Stdin})
	if err != nil {
		return err
	}
	me, err := identity.GetIdentity(ks, c.String("as"))
	if err != nil {
		return err
	}
	relays, err := common.ResolveRelays(c)
	if err != nil {
		return err
	}
	newest, failed := fetchCards(ctx, relays, filter, time.Duration(c.Int("timeout"))*time.Second)
	if len(failed) == len(relays) {
		return common.NewExitErrorWithData(common.ErrCodeNetwork, fmt.Errorf("no relay answered"),
			map[string]any{"failed_relays": failed})
	}
	db, err := storage.OpenDB()
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	results, err := rank(newest, filter.Tags["t"], nostr.Now(), func(npub string) (string, error) {
		return relationOf(storage.NewRelationStore(db), ks, me.Npub, npub)
	})
	if err != nil {
		return err
	}
	if len(results) > limit {
		results = results[:limit]
	}
	out := map[string]any{"results": results, "failed_relays": failed}
	common.Emit(jsonMode, out, func() {
		if len(results) == 0 {
			fmt.Println("No one found. Try another tag.")
		}
		for _, r := range results {
			fmt.Printf("%s  %s  %v  [%s]\n    %s\n", r.Name, r.Npub, r.Tags, r.Relation, r.About)
		}
		for _, f := range failed {
			fmt.Fprintf(os.Stderr, "relay %s did not answer\n", f)
		}
	})
	return nil
}

// fetchCards queries every relay by tag, then looks up each author found
// without the tag filter, so a newer card without the tag or a tombstone on
// any relay wins over an old card elsewhere, and drops authors whose card was
// deleted (NIP-09) on any relay. Comparing created_at only compares one
// author's own events.
func fetchCards(ctx context.Context, relays []string, f nostr.Filter, timeout time.Duration) (map[nostr.PubKey]nostr.Event, []string) {
	newest := map[nostr.PubKey]nostr.Event{}
	failed := []string{}
	keep := func(ev nostr.Event) {
		if old, ok := newest[ev.PubKey]; !ok || ev.CreatedAt > old.CreatedAt {
			newest[ev.PubKey] = ev
		}
	}
	for _, url := range relays {
		page, err := relayquery.FetchWithTimeout(ctx, url, f, timeout)
		if err != nil {
			failed = append(failed, url)
			continue
		}
		for _, ev := range page.Events {
			keep(ev)
		}
	}
	if len(newest) == 0 {
		return newest, failed
	}
	authors := make([]nostr.PubKey, 0, len(newest))
	addrs := make([]string, 0, len(newest))
	for pk := range newest {
		authors = append(authors, pk)
		addrs = append(addrs, fmt.Sprintf("%d:%s:%s", wireevent.Kind30078, pk.Hex(), wireevent.CardCategory))
	}
	byAuthor := nostr.Filter{Kinds: f.Kinds, Authors: authors,
		Tags: nostr.TagMap{"d": {wireevent.CardCategory}}, Limit: len(authors)}
	deletions := nostr.Filter{Kinds: []nostr.Kind{nostr.KindDeletion}, Authors: authors,
		Tags: nostr.TagMap{"a": addrs}, Limit: MaxFindResults}
	deletedAt := map[nostr.PubKey]nostr.Timestamp{}
	for _, url := range relays {
		if slices.Contains(failed, url) {
			continue
		}
		cards, err := relayquery.FetchWithTimeout(ctx, url, byAuthor, timeout)
		if err == nil {
			for _, ev := range cards.Events {
				keep(ev)
			}
		}
		dels, err2 := relayquery.FetchWithTimeout(ctx, url, deletions, timeout)
		if err2 == nil {
			for _, ev := range dels.Events {
				deletedAt[ev.PubKey] = max(deletedAt[ev.PubKey], ev.CreatedAt)
			}
		}
		if err != nil || err2 != nil {
			failed = append(failed, url)
		}
	}
	for pk, at := range deletedAt {
		if ev, ok := newest[pk]; ok && at >= ev.CreatedAt {
			delete(newest, pk)
		}
	}
	return newest, failed
}

// rank parses each author's newest card, drops invalid or expired cards and
// people relation reports as "" (self, blocked), and orders the rest by
// number of matched tags, then card time (display order only).
func rank(newest map[nostr.PubKey]nostr.Event, want []string, now nostr.Timestamp,
	relation func(npub string) (string, error)) ([]findResult, error) {
	results := []findResult{}
	for pk, ev := range newest {
		cd, err := card.Parse(&ev, now)
		if err != nil {
			continue
		}
		npub := common.EncodeNpub(pk)
		rel, err := relation(npub)
		if err != nil {
			return nil, err
		}
		if rel == "" {
			continue
		}
		matched := []string{}
		for _, t := range cd.Tags {
			if slices.Contains(want, t) {
				matched = append(matched, t)
			}
		}
		if len(matched) == 0 {
			continue // the newest card no longer has the tag
		}
		results = append(results, findResult{Npub: npub, Name: cd.Name, Tags: cd.Tags, About: cd.About,
			Matched: matched, Relation: rel, CardUpdatedAt: int64(ev.CreatedAt)})
	}
	slices.SortFunc(results, func(a, b findResult) int {
		return cmp.Or(cmp.Compare(len(b.Matched), len(a.Matched)),
			cmp.Compare(b.CardUpdatedAt, a.CardUpdatedAt), cmp.Compare(a.Npub, b.Npub))
	})
	return results, nil
}

// relationOf returns "friend" or "stranger", or "" for one of my own
// identities or someone I blocked.
func relationOf(store *storage.RelationStore, ks *types.KeyStore, me, npub string) (string, error) {
	for _, id := range ks.Identities {
		if id.Npub == npub {
			return "", nil
		}
	}
	p, err := store.Get(me, npub)
	if err != nil {
		return "", err
	}
	switch {
	case p.Relation.State == contact.Blocked:
		return "", nil
	case p.Relation.State == contact.Friend:
		return "friend", nil
	}
	for _, ct := range ks.Contacts {
		if ct.Npub == npub {
			return "friend", nil
		}
	}
	return "stranger", nil
}

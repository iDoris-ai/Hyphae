package friend

import (
	"context"
	"fmt"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/internal/social/contact"
	"github.com/iDoris-ai/hyphae/internal/storage"
)

type listEntry struct {
	Npub     string `json:"npub"`
	Nickname string `json:"nickname,omitempty"`
	Relation string `json:"relation"`
	Sent     int    `json:"sent"`
	Received int    `json:"received"`
	// CanSend is how many more messages you may send; -1 means no limit.
	CanSend int `json:"can_send"`
}

func listCmd() *cli.Command {
	return &cli.Command{
		Name:  "list",
		Usage: "Friends and the strangers you talked with, with what you can still send",
		Flags: []cli.Flag{asFlag()},
		Action: func(ctx context.Context, c *cli.Command) error {
			env, done, err := openPeerEnv(c)
			if err != nil {
				return err
			}
			defer done()
			rows, err := env.store.List(env.me.Npub)
			if err != nil {
				return err
			}
			byNpub := map[string]storage.PeerRelation{}
			for _, p := range rows {
				byNpub[p.Peer] = p
			}
			out := []listEntry{}
			seen := map[string]bool{}
			for _, ct := range env.ks.Contacts {
				p := byNpub[ct.Npub]
				e := listEntry{Npub: ct.Npub, Nickname: ct.Nickname, Relation: "friend", CanSend: -1}
				if p.Relation.State == contact.Blocked {
					e.Relation, e.CanSend = "blocked", 0
				}
				out, seen[ct.Npub] = append(out, e), true
			}
			for _, p := range rows {
				if seen[p.Peer] {
					continue
				}
				e := listEntry{Npub: p.Peer, Relation: p.Relation.State.String(), Sent: p.Relation.Sent, Received: p.Relation.Received}
				switch p.Relation.State {
				case contact.Stranger:
					e.CanSend = max(0, contact.PerStrangerLimit-p.Relation.Sent)
				case contact.Friend:
					e.CanSend = -1
				}
				out = append(out, e)
			}
			s, err := storage.LoadCardSetting(env.db, env.me.Npub)
			if err != nil {
				return err
			}
			q, err := env.store.Quota(env.me.Npub, time.Now().Unix())
			if err != nil {
				return err
			}
			data := map[string]any{"peers": out, "card_expires_at": s.ExpiresAt, "quota": quotaView(q)}
			common.Emit(env.jsonMode, data, func() {
				for _, e := range out {
					fmt.Printf("%-9s %s %s", e.Relation, e.Npub, e.Nickname)
					if e.Relation == "stranger" {
						fmt.Printf("  (can send %d more)", e.CanSend)
					}
					fmt.Println()
				}
				fmt.Printf("New strangers today: %d of %d left.\n", q.Remaining, contact.DailyLimit)
				if s.Visible && s.ExpiresAt > 0 {
					fmt.Printf("Your card expires %s.\n", time.Unix(s.ExpiresAt, 0).Format(time.DateTime))
				}
			})
			return nil
		},
	}
}

func quotaView(q contact.Quota) map[string]any {
	return map[string]any{"daily_limit": contact.DailyLimit, "used": q.Used, "remaining": q.Remaining,
		"resets_at": q.ResetsAt, "per_stranger_limit": contact.PerStrangerLimit}
}

func quotaCmd() *cli.Command {
	return &cli.Command{
		Name:  "quota",
		Usage: "How many new strangers you can still start talking to today",
		Flags: []cli.Flag{asFlag()},
		Action: func(ctx context.Context, c *cli.Command) error {
			env, done, err := openPeerEnv(c)
			if err != nil {
				return err
			}
			defer done()
			q, err := env.store.Quota(env.me.Npub, time.Now().Unix())
			if err != nil {
				return err
			}
			common.Emit(env.jsonMode, quotaView(q), func() {
				fmt.Printf("New strangers: %d used, %d of %d left.\n", q.Used, q.Remaining, contact.DailyLimit)
				if q.ResetsAt > 0 {
					fmt.Printf("Next slot frees at %s.\n", time.Unix(q.ResetsAt, 0).Format(time.DateTime))
				}
			})
			return nil
		},
	}
}

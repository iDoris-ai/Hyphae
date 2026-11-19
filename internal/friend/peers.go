package friend

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode"

	"github.com/urfave/cli/v3"

	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/internal/identity"
	"github.com/iDoris-ai/hyphae/internal/social/contact"
	"github.com/iDoris-ai/hyphae/internal/storage"
	"github.com/iDoris-ai/hyphae/pkg/types"
)

// peerEnv is what every pending/ignore/block/list/quota run needs.
type peerEnv struct {
	jsonMode bool
	ks       *types.KeyStore
	me       *types.Identity
	db       *sql.DB
	store    *storage.RelationStore
}

func openPeerEnv(c *cli.Command) (*peerEnv, func(), error) {
	jsonMode := common.JSONMode(c)
	ks, err := identity.LoadKeyStoreForCommand(identity.KeyStoreCommandOptions{JSONMode: jsonMode, Stdin: os.Stdin})
	if err != nil {
		return nil, nil, err
	}
	me, err := identity.GetIdentity(ks, c.String("as"))
	if err != nil {
		return nil, nil, err
	}
	db, err := storage.OpenDB()
	if err != nil {
		return nil, nil, err
	}
	return &peerEnv{jsonMode, ks, me, db, storage.NewRelationStore(db)}, func() { _ = db.Close() }, nil
}

func asFlag() cli.Flag {
	return &cli.StringFlag{Name: "as", Aliases: []string{"a"}, Usage: "Your nickname", Required: true}
}

func npubFlag() cli.Flag {
	return &cli.StringFlag{Name: "npub", Aliases: []string{"p"}, Usage: "The peer's npub (or hex key)", Required: true}
}

// peerArg returns the canonical npub of --npub.
func peerArg(c *cli.Command) (string, error) {
	pk, err := common.ParsePublicKey(c.String("npub"))
	if err != nil {
		return "", common.NewExitError(common.ErrCodeInvalidKey, err)
	}
	return common.EncodeNpub(pk), nil
}

func isContactNpub(ks *types.KeyStore, npub string) bool {
	for _, ct := range ks.Contacts {
		if ct.Npub == npub {
			return true
		}
	}
	return false
}

type pendingEntry struct {
	Npub         string `json:"npub"`
	FirstMessage string `json:"first_message"`
	Received     int    `json:"received"`
	Suppressed   int    `json:"suppressed"`
	FirstSeenAt  int64  `json:"first_seen_at"`
}

func pendingCmd() *cli.Command {
	return &cli.Command{
		Name:  "pending",
		Usage: "Strangers who wrote to you and wait for accept, ignore or block",
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
			out := []pendingEntry{}
			for _, p := range rows {
				if p.Relation.State != contact.Stranger || p.Relation.Received == 0 || p.Ignored || isContactNpub(env.ks, p.Peer) {
					continue
				}
				first, err := storage.FirstIncoming(env.db, env.me.Npub, p.Peer)
				if err != nil {
					return err
				}
				out = append(out, pendingEntry{Npub: p.Peer, FirstMessage: first, Received: p.Relation.Received,
					Suppressed: max(0, p.Relation.Received-contact.PerStrangerLimit), FirstSeenAt: p.FirstContactAt})
			}
			common.Emit(env.jsonMode, map[string]any{"pending": out}, func() {
				if len(out) == 0 {
					fmt.Println("No pending strangers.")
				}
				for _, e := range out {
					fmt.Printf("%s  %d message(s)", e.Npub, e.Received)
					if e.Suppressed > 0 {
						fmt.Printf(", %d not shown", e.Suppressed)
					}
					fmt.Printf("\n    %s\n", printable(e.FirstMessage))
				}
			})
			return nil
		},
	}
}

// relationCmd builds ignore, block and unblock: one Update on the stored
// relation, plus extra (block removes the peer from contacts).
func relationCmd(name, usage string, fn func(storage.PeerRelation, int64) (storage.PeerRelation, error),
	extra func(*peerEnv, string) (map[string]any, error)) *cli.Command {
	return &cli.Command{
		Name:  name,
		Usage: usage,
		Flags: []cli.Flag{asFlag(), npubFlag()},
		Action: func(ctx context.Context, c *cli.Command) error {
			peer, err := peerArg(c)
			if err != nil {
				return err
			}
			env, done, err := openPeerEnv(c)
			if err != nil {
				return err
			}
			defer done()
			if peer == env.me.Npub {
				return common.NewExitError(common.ErrCodeUser, errors.New("that is you"))
			}
			now := time.Now().Unix()
			p, err := env.store.Update(env.me.Npub, peer, now, func(p storage.PeerRelation) (storage.PeerRelation, error) {
				return fn(p, now)
			})
			if err != nil {
				if errors.Is(err, contact.ErrNotStranger) || errors.Is(err, contact.ErrNotBlocked) {
					return common.NewExitErrorWithData(common.ErrCodeUser, err, map[string]any{"peer": peer})
				}
				return err
			}
			out := map[string]any{"npub": peer, "relation": p.Relation.State.String()}
			if extra != nil {
				more, err := extra(env, peer)
				if err != nil {
					return err
				}
				for k, v := range more {
					out[k] = v
				}
			}
			common.Emit(env.jsonMode, out, func() { fmt.Printf("%s: %s\n", peer, out["relation"]) })
			return nil
		},
	}
}

func ignoreCmd() *cli.Command {
	return relationCmd("ignore", "Clear a stranger's request; they stay a stranger", func(p storage.PeerRelation, _ int64) (storage.PeerRelation, error) {
		if p.Relation.State != contact.Stranger {
			return p, contact.ErrNotStranger
		}
		p.Ignored = true
		return p, nil
	}, nil)
}

func blockCmd() *cli.Command {
	return relationCmd("block", "Drop everything from a peer and stop sending to them; removes them from contacts",
		func(p storage.PeerRelation, _ int64) (storage.PeerRelation, error) {
			p.Relation = contact.Block(p.Relation)
			return p, nil
		}, func(env *peerEnv, peer string) (map[string]any, error) {
			removed, err := identity.RemoveContactsByNpub(env.ks, peer)
			if removed == nil {
				removed = []string{}
			}
			return map[string]any{"removed_contacts": removed}, err
		})
}

func unblockCmd() *cli.Command {
	return relationCmd("unblock", "Unblock a peer; they become a stranger again with the counts they had",
		func(p storage.PeerRelation, now int64) (storage.PeerRelation, error) {
			var err error
			p.Relation, err = contact.Unblock(p.Relation, now)
			return p, err
		}, nil)
}

// printable drops control characters (C0, DEL, C1) from a stranger's text
// before it reaches the terminal, so it cannot carry escape sequences.
func printable(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}

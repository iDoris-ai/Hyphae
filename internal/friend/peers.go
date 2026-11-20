package friend

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
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

// actionError maps a stranger-action error to the CLI's exit codes.
func actionError(err error, peer string) error {
	switch {
	case errors.Is(err, ErrSelf), errors.Is(err, ErrNicknameTaken):
		return common.NewExitError(common.ErrCodeUser, err)
	case errors.Is(err, contact.ErrNotStranger), errors.Is(err, contact.ErrNotBlocked),
		errors.Is(err, contact.ErrNothingToAccept):
		return common.NewExitErrorWithData(common.ErrCodeUser, err, map[string]any{"peer": peer})
	}
	return err
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
			out, err := ListPending(env.db, env.ks, env.me.Npub)
			if err != nil {
				return err
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

// relationCmd builds ignore, block and unblock around one action.
func relationCmd(name, usage string, act func(*peerEnv, string) (storage.PeerRelation, map[string]any, error)) *cli.Command {
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
			p, more, err := act(env, peer)
			if err != nil {
				return actionError(err, peer)
			}
			out := map[string]any{"npub": peer, "relation": p.Relation.State.String()}
			for k, v := range more {
				out[k] = v
			}
			common.Emit(env.jsonMode, out, func() { fmt.Printf("%s: %s\n", peer, out["relation"]) })
			return nil
		},
	}
}

func ignoreCmd() *cli.Command {
	return relationCmd("ignore", "Clear a stranger's request; they stay a stranger",
		func(env *peerEnv, peer string) (storage.PeerRelation, map[string]any, error) {
			p, err := Ignore(env.db, env.me.Npub, peer)
			return p, nil, err
		})
}

func blockCmd() *cli.Command {
	return relationCmd("block", "Drop everything from a peer and stop sending to them; removes them from contacts",
		func(env *peerEnv, peer string) (storage.PeerRelation, map[string]any, error) {
			p, removed, err := Block(env.db, env.ks, env.me.Npub, peer)
			return p, map[string]any{"removed_contacts": removed}, err
		})
}

func unblockCmd() *cli.Command {
	return relationCmd("unblock", "Unblock a peer; they become a stranger again with the counts they had",
		func(env *peerEnv, peer string) (storage.PeerRelation, map[string]any, error) {
			p, err := Unblock(env.db, env.me.Npub, peer)
			return p, nil, err
		})
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

package friend

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/internal/identity"
	"github.com/iDoris-ai/hyphae/internal/storage"
	"github.com/iDoris-ai/hyphae/pkg/types"
)

func acceptCmd() *cli.Command {
	return &cli.Command{
		Name:  "accept",
		Usage: "Accept a stranger who wrote to you: they become a contact and are told with a friend_accept",
		Flags: []cli.Flag{asFlag(), npubFlag(),
			&cli.StringFlag{Name: "nickname", Aliases: []string{"n"}, Usage: "Contact nickname (default: derived from the npub)"},
			&cli.StringSliceFlag{Name: "relay", Aliases: []string{"r"}, Usage: "Relay URLs"},
			&cli.BoolFlag{Name: "password-stdin", Usage: "Read the keystore password from stdin"},
		},
		Action: accept,
	}
}

// accept is safe to repeat: on a peer who is already a friend it skips the
// local change and only sends the friend_accept again, so a run whose publish
// failed can be retried.
func accept(ctx context.Context, c *cli.Command) error {
	jsonMode := common.JSONMode(c)
	peer, err := peerArg(c)
	if err != nil {
		return err
	}
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
	db, err := storage.OpenDB()
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	res, err := Accept(ctx, db, ks, me, sk, relays, peer, c.String("nickname"))
	out := map[string]any{"npub": peer, "relation": "friend", "nickname": res.Nickname,
		"relays": res.Relays, "published_to": res.PublishedTo}
	switch {
	case errors.Is(err, ErrNotPublished):
		return common.NewExitErrorWithData(common.ErrCodeNetwork, err, out)
	case err != nil:
		return actionError(err, peer)
	}
	common.Emit(jsonMode, out, func() {
		fmt.Printf("%s is now your friend (contact %q); told them on %d/%d relays.\n", peer, res.Nickname, res.PublishedTo, len(relays))
	})
	return nil
}

// ensureContact returns the nickname of an existing contact with npub, or
// adds one: the given nickname, else "friend-" plus the first npub
// characters, with more characters while the name is taken.
func ensureContact(ks *types.KeyStore, npub, nick string) (string, error) {
	for _, ct := range ks.Contacts {
		if ct.Npub == npub {
			return ct.Nickname, nil
		}
	}
	if nick == "" {
		body := strings.TrimPrefix(npub, "npub1")
		for n := 8; n <= len(body); n += 4 {
			nick = "friend-" + body[:n]
			if _, taken := ks.Contacts[nick]; !taken {
				break
			}
		}
	}
	if err := identity.AddContact(ks, nick, npub); err != nil {
		return "", common.NewExitError(common.ErrCodeUser, err)
	}
	return nick, nil
}

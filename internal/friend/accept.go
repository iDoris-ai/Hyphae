package friend

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"fiatjaf.com/nostr"
	"github.com/urfave/cli/v3"

	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/internal/identity"
	"github.com/iDoris-ai/hyphae/internal/messaging"
	"github.com/iDoris-ai/hyphae/internal/social/contact"
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
	peerPK, _ := common.ParsePublicKey(peer)
	db, err := storage.OpenDB()
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	if nick := c.String("nickname"); nick != "" {
		// Check before the relation changes, so a taken name leaves nothing
		// half done.
		if ct, taken := ks.Contacts[nick]; taken && ct.Npub != peer {
			return common.NewExitError(common.ErrCodeUser, fmt.Errorf("contact %q already exists", nick))
		}
	}
	_, answers, err := storage.FirstIncoming(db, me.Npub, peer)
	if err != nil {
		return err
	}
	if answers == "" {
		return common.NewExitErrorWithData(common.ErrCodeUser, contact.ErrNothingToAccept, map[string]any{"peer": peer})
	}
	_, err = storage.NewRelationStore(db).Update(me.Npub, peer, time.Now().Unix(),
		func(p storage.PeerRelation) (storage.PeerRelation, error) {
			if p.Relation.State == contact.Friend {
				return p, nil
			}
			r, err := contact.Accept(p.Relation)
			p.Relation = r
			return p, err
		})
	if err != nil {
		if errors.Is(err, contact.ErrNotStranger) || errors.Is(err, contact.ErrNothingToAccept) {
			return common.NewExitErrorWithData(common.ErrCodeUser, err, map[string]any{"peer": peer})
		}
		return err
	}
	nick, err := ensureContact(ks, peer, c.String("nickname"))
	if err != nil {
		return err
	}
	ev, err := messaging.BuildFriendAccept(sk, peerPK, answers, nostr.Now())
	if err != nil {
		return err
	}
	results, okURLs, _ := publish(ctx, relays, []nostr.Event{*ev})
	out := map[string]any{"npub": peer, "relation": "friend", "nickname": nick,
		"relays": results, "published_to": len(okURLs)}
	if len(okURLs) == 0 {
		return common.NewExitErrorWithData(common.ErrCodeNetwork,
			errors.New("accepted locally, but no relay took the friend_accept; run accept again to resend"), out)
	}
	common.Emit(jsonMode, out, func() {
		fmt.Printf("%s is now your friend (contact %q); told them on %d/%d relays.\n", peer, nick, len(okURLs), len(relays))
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

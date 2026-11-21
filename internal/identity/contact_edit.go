package identity

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/iDoris-ai/hyphae/internal/audit"
	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/pkg/types"
)

// ErrContactNotFound: no contact has that nickname or npub.
var ErrContactNotFound = errors.New("contact not found")

// ErrContactAmbiguous: the npub is saved under several nicknames; name one.
var ErrContactAmbiguous = errors.New("npub is saved under several nicknames; give the nickname")

// ErrContactExists: the new nickname belongs to another contact.
var ErrContactExists = errors.New("contact nickname already exists")

// matchContacts returns the nicknames target names: the contact with that
// nickname, or every contact saved with that npub (or hex key).
func matchContacts(ks *types.KeyStore, target string) []string {
	if _, ok := ks.Contacts[target]; ok {
		return []string{target}
	}
	pk, err := common.ParsePublicKey(target)
	if err != nil {
		return nil
	}
	npub := common.EncodeNpub(pk)
	var out []string
	for nick, c := range ks.Contacts {
		if c.Npub == npub {
			out = append(out, nick)
		}
	}
	sort.Strings(out)
	return out
}

// RenameContact gives the contact target (nickname or npub) the nickname
// newName and returns the renamed contact and its old nickname.
func RenameContact(ks *types.KeyStore, target, newName string) (types.Contact, string, error) {
	var renamed types.Contact
	var old string
	err := runKeyStoreTransaction(ks, func(fresh *types.KeyStore) error {
		nicks := matchContacts(fresh, target)
		switch {
		case len(nicks) == 0:
			return ErrContactNotFound
		case len(nicks) > 1:
			return ErrContactAmbiguous
		}
		old = nicks[0]
		c := *fresh.Contacts[old]
		if old == newName {
			renamed = c
			return nil
		}
		if _, taken := fresh.Contacts[newName]; taken {
			return fmt.Errorf("%w: %q", ErrContactExists, newName)
		}
		delete(fresh.Contacts, old)
		c.Nickname = newName
		fresh.Contacts[newName] = &c
		renamed = c
		return nil
	})
	return renamed, old, err
}

// RemoveContact deletes the contact target names (a nickname, or every
// nickname saved with that npub) and returns the removed nicknames. Messages
// are keyed by npub and stay; the friend/stranger relation is unchanged.
func RemoveContact(ks *types.KeyStore, target string) ([]string, string, error) {
	var removed []string
	var npub string
	err := runKeyStoreTransaction(ks, func(fresh *types.KeyStore) error {
		removed = matchContacts(fresh, target)
		if len(removed) == 0 {
			return ErrContactNotFound
		}
		npub = fresh.Contacts[removed[0]].Npub
		for _, nick := range removed {
			delete(fresh.Contacts, nick)
		}
		return nil
	})
	return removed, npub, err
}

func contactEditError(err error) error {
	if errors.Is(err, ErrContactExists) {
		return common.NewExitError(common.ErrCodeNicknameExists, err)
	}
	if errors.Is(err, ErrContactNotFound) || errors.Is(err, ErrContactAmbiguous) {
		return common.NewExitError(common.ErrCodeUser, err)
	}
	return err
}

func logContactAction(ks *types.KeyStore, action audit.Action, details map[string]any) {
	actor := ks.DefaultIdentity
	if actor == "" {
		actor = "unknown"
	}
	if err := audit.LogAction(actor, action, details); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  audit log failed: %v\n", err)
	}
}

var contactRenameCmd = &cli.Command{
	Name:      "rename",
	Usage:     "Rename a contact: rename <nickname|npub> <new-nickname>",
	ArgsUsage: "<nickname|npub> <new-nickname>",
	Action: func(ctx context.Context, c *cli.Command) error {
		jsonMode := common.JSONMode(c)
		target, newName := c.Args().Get(0), strings.TrimSpace(c.Args().Get(1))
		if c.Args().Len() != 2 || target == "" || newName == "" {
			return common.NewExitError(common.ErrCodeUser, fmt.Errorf("usage: contact rename <nickname|npub> <new-nickname>"))
		}
		ks, err := LoadKeyStore()
		if err != nil {
			return fmt.Errorf("failed to load keystore: %w", err)
		}
		ct, old, err := RenameContact(ks, target, newName)
		if err != nil {
			return contactEditError(err)
		}
		logContactAction(ks, audit.ActionContactRenamed, map[string]any{"old": old, "nickname": ct.Nickname, "npub": ct.Npub})
		common.Emit(jsonMode, map[string]string{
			"old_nickname": old, "nickname": ct.Nickname, "npub": ct.Npub, "role": ct.Role.String(),
		}, func() { fmt.Printf("✅ Renamed contact '%s' to '%s'\n", old, ct.Nickname) })
		return nil
	},
}

var contactRemoveCmd = &cli.Command{
	Name:      "remove",
	Usage:     "Remove a contact by nickname or npub; chat history is kept",
	ArgsUsage: "<nickname|npub>",
	Action: func(ctx context.Context, c *cli.Command) error {
		jsonMode := common.JSONMode(c)
		target := c.Args().Get(0)
		if c.Args().Len() != 1 || target == "" {
			return common.NewExitError(common.ErrCodeUser, fmt.Errorf("usage: contact remove <nickname|npub>"))
		}
		ks, err := LoadKeyStore()
		if err != nil {
			return fmt.Errorf("failed to load keystore: %w", err)
		}
		removed, npub, err := RemoveContact(ks, target)
		if err != nil {
			return contactEditError(err)
		}
		logContactAction(ks, audit.ActionContactRemoved, map[string]any{"removed": removed, "npub": npub})
		common.Emit(jsonMode, map[string]any{"removed": removed, "npub": npub}, func() {
			fmt.Printf("✅ Removed contact %s (chat history kept)\n", strings.Join(removed, ", "))
		})
		return nil
	},
}

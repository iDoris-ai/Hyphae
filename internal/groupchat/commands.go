package groupchat

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"time"

	"fiatjaf.com/nostr"
	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/internal/identity"
	"github.com/iDoris-ai/hyphae/internal/storage"
	"github.com/urfave/cli/v3"
)

// Cmd is the encrypted group fanout command. send persists the fanout and the
// outbox entries. It does not dial a relay.
var Cmd = &cli.Command{
	Name:  "groupchat",
	Usage: "Persist encrypted group fanout",
	Commands: []*cli.Command{
		createCmd,
		inviteCmd,
		acceptCmd,
		sendCmd,
	},
}

var sendCmd = &cli.Command{
	Name:  "send",
	Usage: "Prepare and enqueue one group message",
	Flags: []cli.Flag{
		&cli.StringFlag{Name: "group-id", Required: true, Usage: "Active group id"},
		&cli.StringFlag{Name: "body", Required: true, Usage: "Message body"},
		&cli.StringFlag{Name: "logical-id", Usage: "Stable send key; generated when empty"},
		&cli.IntFlag{Name: "max-retries", Value: 10, Usage: "Retry budget stored with the fanout"},
		&cli.StringFlag{Name: "identity", Usage: "Sender nickname; default identity when empty"},
		&cli.BoolFlag{Name: "password-stdin", Usage: "Read an encrypted keystore password from stdin"},
	},
	Action: func(ctx context.Context, c *cli.Command) error {
		report, err := sendMessage(c)
		if err != nil {
			return err
		}
		common.Emit(common.JSONMode(c), report, func() {
			fmt.Printf("queued %d logical %s\n", len(report.Recipients), report.LogicalID)
			for _, recipient := range report.Recipients {
				fmt.Printf("%s %s %s\n", recipient.RecipientNpub, recipient.QueueID, recipient.EventID)
			}
		})
		return nil
	},
}

func sendMessage(c *cli.Command) (FanoutReport, error) {
	nickname := c.String("identity")
	groupID := c.String("group-id")
	logicalID := c.String("logical-id")
	body := c.String("body")
	maxRetries := c.Int("max-retries")
	if logicalID == "" {
		id, err := NewOpaqueID()
		if err != nil {
			return FanoutReport{}, err
		}
		logicalID = id
	}
	ks, err := identity.LoadKeyStoreForCommand(identity.KeyStoreCommandOptions{
		JSONMode:      common.JSONMode(c),
		RequireSecret: true,
		PasswordStdin: c.Bool("password-stdin"),
		Stdin:         os.Stdin,
	})
	if err != nil {
		return FanoutReport{}, err
	}
	sk, err := identity.GetSecretKey(ks, nickname)
	if err != nil {
		return FanoutReport{}, err
	}
	db, err := storage.OpenDB()
	if err != nil {
		return FanoutReport{}, err
	}
	defer db.Close()
	report, err := enqueueOwned(db, sk, groupID, logicalID, body, maxRetries)
	if err != nil {
		return FanoutReport{}, fmt.Errorf("logical %s: %w", logicalID, err)
	}
	return report, nil
}

func enqueueOwned(db *sql.DB, sk nostr.SecretKey, groupID, logicalID, body string, maxRetries int) (FanoutReport, error) {
	store, err := NewStore(db)
	if err != nil {
		return FanoutReport{}, err
	}
	return store.EnqueueMessageFanout(sk, groupID, logicalID, body, nostr.Timestamp(time.Now().Unix()), maxRetries)
}

func localIdentityFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{Name: "identity", Usage: "Local nickname; default identity when empty"},
		&cli.BoolFlag{Name: "password-stdin", Usage: "Read an encrypted keystore password from stdin"},
	}
}

func emitGroupID(c *cli.Command, view groupIDView) error {
	common.Emit(common.JSONMode(c), view, func() { fmt.Println(view.GroupID) })
	return nil
}

var createCmd = &cli.Command{
	Name:  "create",
	Usage: "Create a group and hand local invitations to co-located invitees",
	Flags: append([]cli.Flag{
		&cli.StringFlag{Name: "name", Required: true, Usage: "Group name"},
		&cli.StringSliceFlag{Name: "invitee", Required: true, Usage: "Invitee npub"},
	}, localIdentityFlags()...),
	Action: func(ctx context.Context, c *cli.Command) error {
		view, err := createGroupCommand(c)
		if err != nil {
			return err
		}
		return emitGroupID(c, view)
	},
}

var inviteCmd = &cli.Command{
	Name:  "invite",
	Usage: "Show group invitations",
	Commands: []*cli.Command{
		{
			Name:  "list",
			Usage: "List invitations stored for one group",
			Flags: append([]cli.Flag{
				&cli.StringFlag{Name: "group", Required: true, Usage: "Group id"},
			}, localIdentityFlags()...),
			Action: func(ctx context.Context, c *cli.Command) error {
				entries, err := listInvitesCommand(c)
				if err != nil {
					return err
				}
				common.Emit(common.JSONMode(c), map[string]any{"invites": entries}, func() {
					for _, invite := range entries {
						fmt.Println(invite.InviteeNpub)
					}
				})
				return nil
			},
		},
	},
}

var acceptCmd = &cli.Command{
	Name:  "accept",
	Usage: "Accept an invitation and print the group id when it is active",
	Flags: append([]cli.Flag{
		&cli.StringFlag{Name: "invite", Required: true, Usage: "Invitation id"},
	}, localIdentityFlags()...),
	Action: func(ctx context.Context, c *cli.Command) error {
		view, err := acceptGroupCommand(c)
		if err != nil {
			return err
		}
		return emitGroupID(c, view)
	},
}

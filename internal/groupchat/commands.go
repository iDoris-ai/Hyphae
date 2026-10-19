package groupchat

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"fiatjaf.com/nostr"
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
	},
	Action: func(ctx context.Context, c *cli.Command) error {
		report, err := sendMessage(c.String("identity"), c.String("group-id"), c.String("logical-id"), c.String("body"), c.Int("max-retries"))
		if err != nil {
			return err
		}
		fmt.Printf("queued %d logical %s\n", len(report.Recipients), report.LogicalID)
		for _, recipient := range report.Recipients {
			fmt.Printf("%s %s %s\n", recipient.RecipientNpub, recipient.QueueID, recipient.EventID)
		}
		return nil
	},
}

func sendMessage(nickname, groupID, logicalID, body string, maxRetries int) (FanoutReport, error) {
	if logicalID == "" {
		id, err := NewOpaqueID()
		if err != nil {
			return FanoutReport{}, err
		}
		logicalID = id
	}
	ks, err := identity.LoadKeyStore()
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
	return enqueueOwned(db, sk, groupID, logicalID, body, maxRetries)
}

func enqueueOwned(db *sql.DB, sk nostr.SecretKey, groupID, logicalID, body string, maxRetries int) (FanoutReport, error) {
	store, err := NewStore(db)
	if err != nil {
		return FanoutReport{}, err
	}
	return store.EnqueueMessageFanout(sk, groupID, logicalID, body, nostr.Timestamp(time.Now().Unix()), maxRetries)
}

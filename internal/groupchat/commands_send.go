package groupchat

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"fiatjaf.com/nostr"
	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/pkg/types"
	"github.com/urfave/cli/v3"
)

const cliSendProviderName = "group"

type cliSendProvider struct {
	store      *Store
	sk         nostr.SecretKey
	groupID    string
	logicalID  string
	body       string
	maxRetries int
	report     FanoutReport
}

func (p *cliSendProvider) Send(call GroupSendCall) error {
	if call.Decision == ReconcileHold || call.Token.ExpiresAt <= time.Now().Unix() {
		return fmt.Errorf("group send provider held")
	}
	report, err := p.store.EnqueueMessageFanout(p.sk, p.groupID, p.logicalID, p.body, nostr.Timestamp(time.Now().Unix()), p.maxRetries)
	if err != nil {
		return err
	}
	if recipient := os.Getenv("HYPHAE_FAIL_GROUP_RECIPIENT"); recipient != "" {
		key := FanoutKey{LocalNpub: common.EncodeNpub(p.sk.Public()), GroupID: p.groupID, EnvelopeType: EnvelopeMessage, SendKey: p.logicalID}
		if err := exhaustQueuedRecipient(p.store, key, recipient); err != nil {
			return err
		}
		report, err = p.store.LoadFanoutReport(key)
		if err != nil {
			return err
		}
	}
	p.report = report
	if report.Failed > 0 {
		return fmt.Errorf("partial fanout queued %d failed %d", report.Queued, report.Failed)
	}
	return nil
}

func enqueueThroughProvider(store *Store, sk nostr.SecretKey, groupID, logicalID, body string, maxRetries int) (FanoutReport, error) {
	provider := &cliSendProvider{store: store, sk: sk, groupID: groupID, logicalID: logicalID, body: body, maxRetries: maxRetries}
	registry := NewGroupProviderRegistry()
	if err := registry.Register(cliSendProviderName, provider); err != nil {
		return FanoutReport{}, err
	}
	now := time.Now().Unix()
	ran, err := registry.Dispatch(cliSendProviderName, GroupSendToken{ID: logicalID, ExpiresAt: now + 60}, now, ReconcileDecision("enqueue"))
	if err != nil {
		// on a partial fanout the report still says who was queued and who failed
		return provider.report, err
	}
	if !ran {
		return FanoutReport{}, fmt.Errorf("group send provider did not run")
	}
	return provider.report, nil
}

// exhaustQueuedRecipient records one publish failure for a queued recipient
// through reserve, start, and commit. A max retry budget of one leaves the row failed.
func exhaustQueuedRecipient(store *Store, key FanoutKey, recipient string) error {
	local, err := canonicalNpub(key.LocalNpub)
	if err != nil {
		return err
	}
	recipient, err = canonicalNpub(recipient)
	if err != nil {
		return err
	}
	key.LocalNpub = local
	var eventJSON string
	err = store.db.QueryRow(`SELECT event_json FROM groupchat_fanout
		WHERE local_npub=? AND group_id=? AND envelope_type=? AND send_key=? AND recipient_npub=?`,
		local, key.GroupID, key.EnvelopeType, key.SendKey, recipient).Scan(&eventJSON)
	if err != nil {
		return err
	}
	token, err := store.reserveFanoutAttemptFrozen(key, recipient, eventJSON)
	if err != nil {
		return err
	}
	if err := store.startFanoutAttempt(token, 1, time.Now().Unix()); err != nil {
		return err
	}
	return store.commitFanoutFailure(token)
}

var retryCmd = &cli.Command{
	Name:  "retry",
	Usage: "Requeue a failed recipient through a new queue id",
	Flags: append([]cli.Flag{
		&cli.StringFlag{Name: "group-id", Required: true, Usage: "Active group id"},
		&cli.StringFlag{Name: "logical-id", Required: true, Usage: "Frozen send key"},
		&cli.StringFlag{Name: "recipient", Usage: "Failed recipient npub; every failed recipient when empty"},
	}, localIdentityFlags()...),
	Action: func(ctx context.Context, c *cli.Command) error {
		report, err := retryCommand(c)
		if err != nil {
			return err
		}
		common.Emit(common.JSONMode(c), report, func() {
			for _, recipient := range report.Recipients {
				fmt.Printf("%s %s %s\n", recipient.EventID, recipient.QueueID, recipient.State)
			}
		})
		return nil
	},
}

func retryCommand(c *cli.Command) (FanoutReport, error) {
	session, err := openSession(c)
	if err != nil {
		return FanoutReport{}, err
	}
	defer session.db.Close()
	key := FanoutKey{LocalNpub: session.me, GroupID: c.String("group-id"), EnvelopeType: EnvelopeMessage, SendKey: c.String("logical-id")}
	return session.store.RequeueFailed(key, c.String("recipient"))
}

type statusView struct {
	GroupID     string                    `json:"group_id"`
	LogicalID   string                    `json:"logical_id"`
	Queued      int                       `json:"queued"`
	Failed      int                       `json:"failed"`
	Partial     bool                      `json:"partial"`
	Recipients  []RecipientDelivery       `json:"recipients"`
	Bookkeeping []FanoutBookkeepingReport `json:"bookkeeping"`
}

var statusCmd = &cli.Command{
	Name:  "status",
	Usage: "Read queued and failed counts for one frozen group send",
	Flags: append([]cli.Flag{
		&cli.StringFlag{Name: "group-id", Required: true, Usage: "Active group id"},
		&cli.StringFlag{Name: "logical-id", Required: true, Usage: "Frozen send key"},
	}, localIdentityFlags()...),
	Action: func(ctx context.Context, c *cli.Command) error {
		view, err := statusCommand(c)
		if err != nil {
			return err
		}
		common.Emit(common.JSONMode(c), view, func() {
			if view.Partial {
				fmt.Printf("partial queued %d failed %d\n", view.Queued, view.Failed)
				return
			}
			fmt.Printf("queued %d failed %d\n", view.Queued, view.Failed)
		})
		return nil
	},
}

func statusCommand(c *cli.Command) (statusView, error) {
	session, err := openSession(c)
	if err != nil {
		return statusView{}, err
	}
	defer session.db.Close()
	key := FanoutKey{LocalNpub: session.me, GroupID: c.String("group-id"), EnvelopeType: EnvelopeMessage, SendKey: c.String("logical-id")}
	report, err := session.store.LoadFanoutReport(key)
	if err != nil {
		return statusView{}, err
	}
	books, err := session.store.InspectFanoutBookkeeping(key)
	if err != nil {
		return statusView{}, err
	}
	return statusView{
		GroupID: key.GroupID, LogicalID: key.SendKey,
		Queued: report.Queued, Failed: report.Failed, Partial: report.Queued > 0 && report.Failed > 0,
		Recipients: report.Recipients, Bookkeeping: books,
	}, nil
}

var historyCmd = &cli.Command{
	Name:  "history",
	Usage: "Read local group history",
	Flags: append([]cli.Flag{
		&cli.StringFlag{Name: "group-id", Required: true, Usage: "Active group id"},
		&cli.IntFlag{Name: "limit", Value: 20, Usage: "Maximum messages"},
		&cli.StringFlag{Name: "before-id", Usage: "Message id: return the messages just older than it"},
		&cli.StringFlag{Name: "after-id", Usage: "Message id: return the messages just newer than it"},
	}, localIdentityFlags()...),
	Action: func(ctx context.Context, c *cli.Command) error {
		messages, err := historyCommand(c)
		if err != nil {
			return err
		}
		common.Emit(common.JSONMode(c), map[string]any{"messages": messages}, func() {
			for _, message := range messages {
				fmt.Println(message.Plaintext)
			}
		})
		return nil
	},
}

func historyCommand(c *cli.Command) ([]types.GroupMessage, error) {
	session, err := openSession(c)
	if err != nil {
		return nil, err
	}
	defer session.db.Close()
	messages, err := session.store.GroupMessagesPage(session.me, c.String("group-id"), c.Int("limit"), c.String("before-id"), c.String("after-id"))
	if errors.Is(err, ErrCursorNotFound) || errors.Is(err, ErrConflictingCursors) {
		return nil, common.NewExitError(common.ErrCodeUser, err)
	}
	return messages, err
}

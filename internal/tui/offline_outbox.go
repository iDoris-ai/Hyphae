package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"fiatjaf.com/nostr"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/internal/identity"
	"github.com/iDoris-ai/hyphae/internal/messaging"
	"github.com/iDoris-ai/hyphae/pkg/crypto"
	"github.com/iDoris-ai/hyphae/pkg/types"
)

const outboxPollInterval = 500 * time.Millisecond

type outboxSendRequest struct {
	requestID uint64
	content   string
}

type outboxDeliveryUpdate struct {
	requestID uint64
	eventID   string
	state     messaging.AgentMessageDeliveryState
	issue     messaging.AgentMessageDeliveryIssue
}

type outboxWorkerStartedMsg struct{}

type outboxDeliveryUpdateMsg struct {
	update outboxDeliveryUpdate
}

func (m *ChatModel) startOutboxWorker() tea.Cmd {
	return func() tea.Msg {
		m.inboxMu.Lock()
		if m.inboxClosed || m.outboxStarted {
			m.inboxMu.Unlock()
			return outboxWorkerStartedMsg{}
		}
		m.outboxStarted = true
		m.inboxMu.Unlock()

		go m.runOutboxWorker()
		return outboxWorkerStartedMsg{}
	}
}

func (m *ChatModel) waitOutboxUpdate() tea.Cmd {
	return func() tea.Msg {
		select {
		case update := <-m.outboxUpdates:
			return outboxDeliveryUpdateMsg{update: update}
		case <-m.outboxCtx.Done():
			return nil
		}
	}
}

func (m *ChatModel) runOutboxWorker() {
	defer close(m.outboxDone)
	ticker := time.NewTicker(outboxPollInterval)
	defer ticker.Stop()

	// Report durable pending/failed items before attempting any network work,
	// so the user sees restored state even while a relay is offline.
	m.reportOutboxSnapshot(m.outboxCtx)
	m.retryPendingOutbox(m.outboxCtx)

	for {
		select {
		case <-m.outboxCtx.Done():
			return
		case request := <-m.outboxRequests:
			m.sendQueuedMessage(m.outboxCtx, request)
		case <-ticker.C:
			m.retryPendingOutbox(m.outboxCtx)
		}
	}
}

func (m *ChatModel) reportOutboxSnapshot(ctx context.Context) {
	outbox, err := messaging.LoadOutbox()
	if err != nil {
		m.publishOutboxUpdate(ctx, outboxDeliveryUpdate{state: messaging.AgentMessageFailed, issue: messaging.AgentMessageIssueSendFailed})
		return
	}
	sender, err := common.ParsePublicKey(m.myIdentity.Npub)
	if err != nil {
		m.publishOutboxUpdate(ctx, outboxDeliveryUpdate{state: messaging.AgentMessageFailed, issue: messaging.AgentMessageIssueSendFailed})
		return
	}
	for _, entry := range outbox.Entries {
		if !m.belongsToCurrentIdentity(entry, sender) || entry.RecipientNpub != m.contactNpub {
			continue
		}
		switch entry.Status {
		case "pending":
			m.publishOutboxUpdate(ctx, outboxDeliveryUpdate{eventID: entry.ID, state: messaging.AgentMessageQueued})
		case "failed":
			m.publishOutboxUpdate(ctx, outboxDeliveryUpdate{eventID: entry.ID, state: messaging.AgentMessageFailed, issue: messaging.AgentMessageIssueRetryExhausted})
		}
	}
}

func (m *ChatModel) retryPendingOutbox(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	outbox, err := messaging.LoadOutbox()
	if err != nil {
		m.publishOutboxUpdate(ctx, outboxDeliveryUpdate{state: messaging.AgentMessageFailed, issue: messaging.AgentMessageIssueSendFailed})
		return
	}
	sender, err := common.ParsePublicKey(m.myIdentity.Npub)
	if err != nil {
		m.publishOutboxUpdate(ctx, outboxDeliveryUpdate{state: messaging.AgentMessageFailed, issue: messaging.AgentMessageIssueSendFailed})
		return
	}
	for _, entry := range messaging.GetPendingOutbox(outbox) {
		if ctx.Err() != nil {
			return
		}
		if !m.belongsToCurrentIdentity(entry, sender) || !outboxRetryDue(entry, time.Now()) {
			continue
		}
		// Queue and history live in different stores. If the process died after
		// enqueue, reconstruct history from the original signed event before
		// publishing; no new signature or encryption nonce is generated.
		if err := m.restoreOutboxHistory(entry); err != nil {
			if entry.RecipientNpub == m.contactNpub {
				m.publishOutboxUpdate(ctx, outboxDeliveryUpdate{eventID: entry.ID, state: messaging.AgentMessageQueued, issue: messaging.AgentMessageIssueHistoryNotStored})
			}
			continue
		}
		result, sendErr := messaging.AttemptSend(ctx, outbox, entry, m.relays, relayDialTimeout)
		if !result.Attempted && result.Superseded {
			continue
		}
		if entry.RecipientNpub == m.contactNpub {
			m.publishOutboxUpdate(ctx, outboxDeliveryUpdate{
				eventID: entry.ID,
				state:   retryDeliveryState(result),
				issue:   retryDeliveryIssue(result, sendErr),
			})
		}
	}
}

func (m *ChatModel) restoreOutboxHistory(entry types.OutboxEntry) error {
	var event nostr.Event
	if err := json.Unmarshal([]byte(entry.EventJSON), &event); err != nil {
		return err
	}
	if !event.CheckID() || !event.VerifySignature() {
		return fmt.Errorf("invalid queued event signature")
	}
	if err := messaging.ValidateAgentMessageEvent(&event); err != nil {
		return err
	}
	stored, err := m.store.GetMessage(entry.ID)
	if err != nil {
		return err
	}
	if stored != nil && (!stored.IsEncrypted || stored.Plaintext != "") {
		return nil
	}
	ks, err := identity.LoadKeyStore()
	if err != nil {
		return err
	}
	secret, err := identity.GetSecretKey(ks, m.myIdentity.Nickname)
	if err != nil {
		return err
	}
	recipient, err := common.ParsePublicKey(entry.RecipientNpub)
	if err != nil {
		return err
	}
	for _, tag := range event.Tags {
		if len(tag) >= 2 && tag[0] == "p" && tag[1] != recipient.Hex() {
			return fmt.Errorf("queued event recipient does not match queue metadata")
		}
	}
	// NIP-44's conversation key is symmetric. Decode with the sender key and
	// recipient public key; keep the actual event untouched for retry/storage.
	decodeEvent := event
	decodeEvent.PubKey = recipient
	plaintext, encrypted, err := messaging.DecodeMessageContent(&decodeEvent, secret)
	if err != nil {
		return err
	}
	return m.store.StoreOutgoingMessage(&event, entry.RecipientNpub, plaintext, encrypted)
}

func (m *ChatModel) belongsToCurrentIdentity(entry types.OutboxEntry, sender nostr.PubKey) bool {
	if entry.Status == "sent" {
		return false
	}
	var event nostr.Event
	if err := json.Unmarshal([]byte(entry.EventJSON), &event); err != nil {
		return false
	}
	return event.PubKey == sender && event.ID.Hex() == entry.ID
}

func outboxRetryDue(entry types.OutboxEntry, now time.Time) bool {
	if entry.LastAttempt <= 0 {
		return true
	}
	backoff := time.Duration(entry.RetryCount*entry.RetryCount) * time.Second
	if backoff > 5*time.Minute {
		backoff = 5 * time.Minute
	}
	return now.Sub(time.Unix(entry.LastAttempt, 0)) >= backoff
}

func (m *ChatModel) sendQueuedMessage(ctx context.Context, request outboxSendRequest) {
	update := outboxDeliveryUpdate{requestID: request.requestID, state: messaging.AgentMessageFailed, issue: messaging.AgentMessageIssueSendFailed}
	defer func() { m.publishOutboxUpdate(ctx, update) }()

	recipient, err := common.ParsePublicKey(m.contactNpub)
	if err != nil {
		return
	}
	ks, err := identity.LoadKeyStore()
	if err != nil {
		return
	}
	senderSK, err := identity.GetSecretKey(ks, m.myIdentity.Nickname)
	if err != nil {
		return
	}
	encrypted, err := crypto.EncryptMessage(request.content, senderSK, recipient)
	if err != nil {
		return
	}
	compressed, err := messaging.CompressText(encrypted)
	if err != nil {
		return
	}
	createdAt := nostr.Now()
	dTag, err := messaging.NewAgentMessageDTag(compressed, createdAt)
	if err != nil {
		return
	}
	event := &nostr.Event{
		CreatedAt: createdAt,
		Kind:      messaging.AgentKind,
		Tags: nostr.Tags{
			{"p", common.PubKeyToHex(recipient)},
			{"c", messaging.AgentTag},
			{"z", messaging.CompressTag},
			{"v", messaging.AgentVersion},
			{"d", dTag},
			{"enc", "nip44"},
		},
		Content: compressed,
		PubKey:  senderSK.Public(),
	}
	if err := messaging.ValidateAgentMessageEvent(event); err != nil {
		return
	}
	event.Sign(senderSK)
	update.eventID = event.ID.Hex()

	result, _ := messaging.SendQueuedAgentMessage(ctx, event, m.contactNpub, request.content, true, m.relays, relayDialTimeout)
	update.state = result.State
	update.issue = result.Issue
}

func (m *ChatModel) publishOutboxUpdate(ctx context.Context, update outboxDeliveryUpdate) {
	select {
	case m.outboxUpdates <- update:
	case <-ctx.Done():
	}
}

func retryDeliveryState(result messaging.SendResult) messaging.AgentMessageDeliveryState {
	switch {
	case result.Sent:
		return messaging.AgentMessageRelayAccepted
	case result.Queued:
		return messaging.AgentMessageQueued
	default:
		return messaging.AgentMessageFailed
	}
}

func retryDeliveryIssue(result messaging.SendResult, err error) messaging.AgentMessageDeliveryIssue {
	switch {
	case result.QueueStateUnknown:
		return messaging.AgentMessageIssueQueueStateUnknown
	case result.Superseded:
		return messaging.AgentMessageIssueQueueSuperseded
	case result.MarkedFailed:
		return messaging.AgentMessageIssueRetryExhausted
	case err != nil && result.Sent:
		return messaging.AgentMessageIssueOutboxBookkeepingFailed
	case err != nil || !result.Attempted:
		return messaging.AgentMessageIssueSendFailed
	default:
		return messaging.AgentMessageIssueNone
	}
}

func formatOutboxStatus(update outboxDeliveryUpdate) string {
	id := update.eventID
	if len(id) > 12 {
		id = id[:12]
	}
	if id != "" {
		id = " • " + id
	}
	switch update.state {
	case messaging.AgentMessageQueued:
		if update.issue == messaging.AgentMessageIssueHistoryNotStored {
			return "Outbox: queued for retry" + id + " (not delivered; awaiting relay ACK) • local history recovery pending"
		}
		if update.issue != messaging.AgentMessageIssueNone {
			return "Outbox: queued for retry" + id + " (not delivered; awaiting relay ACK) • relay unavailable"
		}
		return "Outbox: queued for retry" + id + " (not delivered; awaiting relay ACK)"
	case messaging.AgentMessageRelayAccepted:
		status := "Outbox: relay accepted" + id
		switch update.issue {
		case messaging.AgentMessageIssueQueueStateUnknown:
			return status + " • queue state unknown; recipient delivery/read not confirmed"
		case messaging.AgentMessageIssueHistoryNotStored:
			return status + " • local history not stored; recipient delivery/read not confirmed"
		case messaging.AgentMessageIssueOutboxBookkeepingFailed:
			return status + " • local history/outbox bookkeeping needs attention; recipient delivery/read not confirmed"
		default:
			return status + " (recipient delivery/read not confirmed)"
		}
	default:
		if update.eventID == "" && update.requestID == 0 {
			return "Outbox: failed • pending state could not be read"
		}
		status := "Outbox: failed"
		if update.eventID != "" {
			status += id
		}
		switch update.issue {
		case messaging.AgentMessageIssueQueueStateUnknown:
			return status + " • queue state unknown; verify before resending"
		case messaging.AgentMessageIssueHistoryNotStored:
			return status + " • local history could not be saved; text remains in input"
		case messaging.AgentMessageIssueQueueSuperseded:
			return status + " • queue entry changed; text remains in input"
		case messaging.AgentMessageIssueRetryExhausted:
			return status + " • retry limit reached; outbox entry retained"
		default:
			return status + " • durable queue was not confirmed; text remains in input"
		}
	}
}

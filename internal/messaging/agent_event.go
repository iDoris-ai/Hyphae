package messaging

import (
	"fmt"

	"fiatjaf.com/nostr"
	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/pkg/crypto"
)

// BuildAgentMessageEvent encrypts, compresses, and signs a new agent message.
// Each call generates fresh encryption and d-tag nonces; retries must reuse the
// returned signed event rather than construct a new one.
func BuildAgentMessageEvent(senderSK nostr.SecretKey, recipientPK nostr.PubKey, plaintext string, createdAt nostr.Timestamp) (*nostr.Event, error) {
	encrypted, err := crypto.EncryptMessage(plaintext, senderSK, recipientPK)
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt: %w", err)
	}
	compressed, err := CompressText(encrypted)
	if err != nil {
		return nil, fmt.Errorf("failed to compress message: %w", err)
	}
	dTag, err := NewAgentMessageDTag(compressed, createdAt)
	if err != nil {
		return nil, fmt.Errorf("failed to derive d tag: %w", err)
	}
	event := &nostr.Event{
		CreatedAt: createdAt,
		Kind:      AgentKind,
		Tags: nostr.Tags{
			{"p", common.PubKeyToHex(recipientPK)},
			{"c", AgentTag},
			{"z", CompressTag},
			{"v", AgentVersion},
			// Give each addressable event its own relay coordinate.
			{"d", dTag},
			{"enc", "nip44"},
		},
		Content: compressed,
		PubKey:  senderSK.Public(),
	}
	if err := ValidateAgentMessageEvent(event); err != nil {
		return nil, fmt.Errorf("validate outgoing message tags: %w", err)
	}
	if err := event.Sign(senderSK); err != nil {
		return nil, fmt.Errorf("failed to sign event: %w", err)
	}
	return event, nil
}

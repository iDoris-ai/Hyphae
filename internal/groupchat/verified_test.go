package groupchat

import (
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/internal/messaging"
	"github.com/iDoris-ai/hyphae/pkg/crypto"
	"github.com/stretchr/testify/require"
)

func TestVerifyIncomingChecksSignatureEncryptionAndRecipient(t *testing.T) {
	alice, bob, carol := nostr.Generate(), nostr.Generate(), nostr.Generate()
	payload, err := Encode(Envelope{Type: EnvelopeMessage, Version: Version,
		GroupID: mustOpaque(t), LogicalID: mustOpaque(t), Body: "private"})
	require.NoError(t, err)
	event := testAgentEvent(t, payload, alice, bob, true)
	verified, err := VerifyIncoming(event, bob)
	require.NoError(t, err)
	require.Equal(t, common.EncodeNpub(alice.Public()), verified.SenderNpub())
	require.Equal(t, common.EncodeNpub(bob.Public()), verified.RecipientNpub())
	require.True(t, verified.IsEncrypted())
	require.NotContains(t, event.Content, ReservedPrefix)

	_, err = VerifyIncoming(event, carol)
	require.Error(t, err)
	mutated := event
	mutated.Content += "x"
	_, err = VerifyIncoming(mutated, bob)
	require.Error(t, err)
	badSignature := event
	badSignature.CreatedAt++
	badSignature.ID = badSignature.GetID()
	require.True(t, badSignature.CheckID(), "fixture must isolate signature verification")
	require.False(t, badSignature.VerifySignature())
	_, err = VerifyIncoming(badSignature, bob)
	require.Error(t, err)

	duplicateRecipient := event
	duplicateRecipient.Tags = cloneTestTags(event.Tags)
	duplicateRecipient.Tags = append(duplicateRecipient.Tags, nostr.Tag{"p", common.PubKeyToHex(carol.Public())})
	require.NoError(t, duplicateRecipient.Sign(alice))
	_, err = VerifyIncoming(duplicateRecipient, bob)
	require.Error(t, err)
	misroutedTag := event
	misroutedTag.Tags = cloneTestTags(event.Tags)
	for i := range misroutedTag.Tags {
		if len(misroutedTag.Tags[i]) == 2 && misroutedTag.Tags[i][0] == "p" {
			misroutedTag.Tags[i][1] = common.PubKeyToHex(carol.Public())
		}
	}
	require.NoError(t, misroutedTag.Sign(alice))
	_, err = VerifyIncoming(misroutedTag, bob)
	require.Error(t, err, "ciphertext recipient alone cannot override signed p routing")

	badClassifierTag := event
	badClassifierTag.Tags = cloneTestTags(event.Tags)
	badClassifierTag.Tags = append(badClassifierTag.Tags, nostr.Tag{"d", "agent-profile"})
	require.NoError(t, badClassifierTag.Sign(alice))
	_, err = VerifyIncoming(badClassifierTag, bob)
	require.Error(t, err, "shared kind-30078 classifier must reject conflicting tags")

	plain := testAgentEvent(t, payload, alice, bob, false)
	_, err = VerifyIncoming(plain, bob)
	require.Error(t, err, "group messages require NIP-44 encryption")
}

func cloneTestTags(tags nostr.Tags) nostr.Tags {
	copyTags := make(nostr.Tags, len(tags))
	for i := range tags {
		copyTags[i] = append(nostr.Tag(nil), tags[i]...)
	}
	return copyTags
}

// testAgentEvent uses the existing NIP-44 and compression functions to build
// signed fixtures; it intentionally does not provide a production crypto path.
func testAgentEvent(t *testing.T, plaintext string, sender, recipient nostr.SecretKey, encrypted bool) nostr.Event {
	t.Helper()
	content := plaintext
	if encrypted {
		var err error
		content, err = crypto.EncryptMessage(content, sender, recipient.Public())
		require.NoError(t, err)
	}
	compressed, err := messaging.CompressText(content)
	require.NoError(t, err)
	dTag, err := messaging.FormatAgentMessageDTag("0123456789abcdef")
	require.NoError(t, err)
	tags := nostr.Tags{
		{"p", common.PubKeyToHex(recipient.Public())},
		{"c", messaging.AgentTag},
		{"z", messaging.CompressTag},
		{"v", messaging.AgentVersion},
		{"d", dTag},
	}
	if encrypted {
		tags = append(tags, nostr.Tag{"enc", "nip44"})
	}
	event := nostr.Event{CreatedAt: nostr.Timestamp(time.Now().Unix()), Kind: nostr.Kind(messaging.AgentKind),
		PubKey: sender.Public(), Tags: tags, Content: compressed}
	require.NoError(t, event.Sign(sender))
	return event
}

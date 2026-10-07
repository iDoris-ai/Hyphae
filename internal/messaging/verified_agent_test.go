package messaging

import (
	"testing"

	"fiatjaf.com/nostr"
	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/pkg/crypto"
	"github.com/stretchr/testify/require"
)

func TestVerifiedAgentMessageClassifiesReservedAndOrdinaryPayloads(t *testing.T) {
	sender, recipient := nostr.Generate(), nostr.Generate()
	cases := []struct {
		name      string
		body      string
		expected  AgentMessageRoute
		encrypted bool
	}{
		{name: "ordinary text", body: "hello", expected: AgentRouteDirect, encrypted: true},
		{name: "ordinary JSON", body: `{"type":"message"}`, expected: AgentRouteDirect, encrypted: false},
		{name: "group v1", body: "hyphae.group/v1\n{}", expected: AgentRouteReservedGroup, encrypted: true},
		{name: "unknown group version", body: "hyphae.group/v99\n{}", expected: AgentRouteReservedGroup, encrypted: true},
		{name: "broken reserved magic", body: "hyphae.group/no-version-separator", expected: AgentRouteReservedGroup, encrypted: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			message, err := VerifyAgentMessage(makeVerifiedAgentEvent(t, tc.body, sender, recipient, tc.encrypted), recipient)
			require.NoError(t, err)
			require.True(t, message.Valid())
			require.Equal(t, tc.expected, message.Route())
			require.Equal(t, tc.body, message.Plaintext())
			require.Equal(t, tc.encrypted, message.IsEncrypted())
			require.Equal(t, common.EncodeNpub(sender.Public()), message.SenderNpub())
			require.Equal(t, common.EncodeNpub(recipient.Public()), message.RecipientNpub())
		})
	}
	var zero VerifiedAgentMessage
	require.False(t, zero.Valid())
	require.Equal(t, AgentRouteInvalid, zero.Route())
}

func TestVerifyAgentMessageRejectsInvalidIdentityRoutingAndCiphertext(t *testing.T) {
	sender, recipient, stranger := nostr.Generate(), nostr.Generate(), nostr.Generate()
	valid := makeVerifiedAgentEvent(t, "hello", sender, recipient, true)

	t.Run("invalid event id", func(t *testing.T) {
		event := valid
		event.Content += "tamper"
		_, err := VerifyAgentMessage(event, recipient)
		require.Error(t, err)
	})
	t.Run("invalid signature", func(t *testing.T) {
		event := valid
		event.CreatedAt++
		event.ID = event.GetID()
		require.True(t, event.CheckID())
		require.False(t, event.VerifySignature())
		_, err := VerifyAgentMessage(event, recipient)
		require.Error(t, err)
	})
	t.Run("wrong recipient key", func(t *testing.T) {
		_, err := VerifyAgentMessage(valid, stranger)
		require.Error(t, err)
	})
	t.Run("missing p", func(t *testing.T) {
		event := valid
		event.Tags = cloneVerifiedTags(valid.Tags)
		event.Tags = withoutTag(event.Tags, "p")
		require.NoError(t, event.Sign(sender))
		_, err := VerifyAgentMessage(event, recipient)
		require.Error(t, err)
	})
	t.Run("wrong p", func(t *testing.T) {
		event := valid
		event.Tags = cloneVerifiedTags(valid.Tags)
		for i := range event.Tags {
			if len(event.Tags[i]) > 0 && event.Tags[i][0] == "p" {
				event.Tags[i] = nostr.Tag{"p", common.PubKeyToHex(stranger.Public())}
			}
		}
		require.NoError(t, event.Sign(sender))
		_, err := VerifyAgentMessage(event, recipient)
		require.Error(t, err)
	})
	t.Run("duplicate p", func(t *testing.T) {
		event := valid
		event.Tags = cloneVerifiedTags(valid.Tags)
		event.Tags = append(event.Tags, nostr.Tag{"p", common.PubKeyToHex(stranger.Public())})
		require.NoError(t, event.Sign(sender))
		_, err := VerifyAgentMessage(event, recipient)
		require.Error(t, err)
	})
	t.Run("profile classifier", func(t *testing.T) {
		event := nostr.Event{CreatedAt: 1, Kind: nostr.Kind(AgentKind), PubKey: sender.Public(),
			Tags: nostr.Tags{{"c", "profile"}, {"d", "agent-profile"}}, Content: "profile"}
		require.NoError(t, event.Sign(sender))
		_, err := VerifyAgentMessage(event, recipient)
		require.Error(t, err)
	})
	t.Run("encrypted corruption", func(t *testing.T) {
		event := valid
		event.Content = "not-valid-base64"
		require.NoError(t, event.Sign(sender))
		_, err := VerifyAgentMessage(event, recipient)
		require.Error(t, err)
	})
	t.Run("zero recipient key", func(t *testing.T) {
		_, err := VerifyAgentMessage(valid, nostr.SecretKey{})
		require.Error(t, err)
	})
}

func makeVerifiedAgentEvent(t *testing.T, body string, sender, recipient nostr.SecretKey, encrypted bool) nostr.Event {
	t.Helper()
	content := body
	if encrypted {
		var err error
		content, err = crypto.EncryptMessage(body, sender, recipient.Public())
		require.NoError(t, err)
	}
	compressed, err := CompressText(content)
	require.NoError(t, err)
	dTag, err := FormatAgentMessageDTag("0123456789abcdef")
	require.NoError(t, err)
	tags := nostr.Tags{{"p", common.PubKeyToHex(recipient.Public())}, {"c", AgentTag},
		{"z", CompressTag}, {"v", AgentVersion}, {"d", dTag}}
	if encrypted {
		tags = append(tags, nostr.Tag{"enc", "nip44"})
	}
	event := nostr.Event{CreatedAt: 1, Kind: nostr.Kind(AgentKind), PubKey: sender.Public(), Tags: tags, Content: compressed}
	require.NoError(t, event.Sign(sender))
	return event
}

func cloneVerifiedTags(tags nostr.Tags) nostr.Tags {
	cloned := make(nostr.Tags, len(tags))
	for i := range tags {
		cloned[i] = append(nostr.Tag(nil), tags[i]...)
	}
	return cloned
}

func withoutTag(tags nostr.Tags, name string) nostr.Tags {
	filtered := make(nostr.Tags, 0, len(tags))
	for _, tag := range tags {
		if len(tag) == 0 || tag[0] != name {
			filtered = append(filtered, tag)
		}
	}
	return filtered
}

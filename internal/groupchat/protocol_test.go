package groupchat

import (
	"strings"
	"testing"

	"fiatjaf.com/nostr"
	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/stretchr/testify/require"
)

func TestEnvelopeCodecStrictTypedAndBounded(t *testing.T) {
	alice, bob := common.EncodeNpub(nostr.Generate().Public()), common.EncodeNpub(nostr.Generate().Public())
	groupID, err := NewOpaqueID()
	require.NoError(t, err)
	hash, members, err := CanonicalRosterHash(groupID, alice, "planning", []string{alice, bob})
	require.NoError(t, err)
	envelope := Envelope{Type: EnvelopeInvite, Version: Version, GroupID: groupID, CreatorNpub: alice,
		InviteID: mustOpaque(t), RosterHash: hash, InviteeNpub: bob, Name: "planning", Members: members}
	encoded, err := Encode(envelope)
	require.NoError(t, err)
	decoded, err := Decode(encoded)
	require.NoError(t, err)
	require.Equal(t, envelope, decoded)
	boundary := encoded + strings.Repeat(" ", MaxEnvelope-len(encoded))
	require.Len(t, boundary, MaxEnvelope)
	_, err = Decode(boundary)
	require.NoError(t, err, "exactly 32 KiB is within the envelope limit")
	_, err = Decode(boundary + " ")
	require.ErrorContains(t, err, "exceeds")

	_, err = Decode(`{"type":"message","body":"normal json"}`)
	require.ErrorIs(t, err, ErrNotEnvelope)
	_, err = Decode(ReservedPrefix + "missing-version-separator")
	require.Error(t, err, "malformed reserved magic must fail closed")
	_, err = Decode("hyphae.group/v9\n{}")
	require.ErrorIs(t, err, ErrUnsupportedVersion)
	_, err = Decode(MagicV1 + `{"type":"unknown","version":1,"group_id":"00000000000000000000000000000000"}`)
	require.Error(t, err, "unknown envelope type must be rejected")
	_, err = Decode(MagicV1 + `{"type":"message","type":"message"}`)
	require.Error(t, err)
	_, err = Decode(MagicV1 + `{"type":"message","version":1,"group_id":"00000000000000000000000000000000","logical_id":"00000000000000000000000000000000","body":"x","extra":true}`)
	require.Error(t, err)
	_, err = Decode(MagicV1 + `{"type":"message","version":1,"group_id":"00000000000000000000000000000000","logical_id":"00000000000000000000000000000000","body":"x"} {}`)
	require.Error(t, err)
	invalidUTF8 := MagicV1 + `{"type":"message","version":1,"group_id":"00000000000000000000000000000000","logical_id":"00000000000000000000000000000000","body":"` + string([]byte{0xff}) + `"}`
	_, err = Decode(invalidUTF8)
	require.Error(t, err, "JSON decoder must not normalize invalid UTF-8")
	tooLarge := MagicV1 + strings.Repeat(" ", MaxEnvelope)
	_, err = Decode(tooLarge)
	require.ErrorContains(t, err, "exceeds")

	bad := envelope
	bad.Members = []string{alice, alice}
	_, err = Encode(bad)
	require.Error(t, err)
	bad = envelope
	bad.Type, bad.Members, bad.Name, bad.InviteID, bad.InviteeNpub = EnvelopeMessage, nil, "", "", ""
	bad.CreatorNpub, bad.RosterHash, bad.LogicalID, bad.Body = "", "", mustOpaque(t), "hello"
	_, err = Encode(bad)
	require.NoError(t, err)
	bad.Body = "bad\x00body"
	_, err = Encode(bad)
	require.Error(t, err)
	bad.Body = string([]byte{0xff})
	_, err = Encode(bad)
	require.Error(t, err, "encoder must reject invalid UTF-8")
	bad.Body = strings.Repeat("好", MaxBodyRunes+1)
	_, err = Encode(bad)
	require.Error(t, err)
}

func TestRosterLimitsAndRandomOpaqueIDs(t *testing.T) {
	keys := make([]string, MaxMembers+1)
	for i := range keys {
		keys[i] = common.EncodeNpub(nostr.Generate().Public())
	}
	groupID, err := NewOpaqueID()
	require.NoError(t, err)
	first, err := NewOpaqueID()
	require.NoError(t, err)
	second, err := NewOpaqueID()
	require.NoError(t, err)
	require.NotEqual(t, first, second)
	_, _, err = CanonicalRosterHash(groupID, keys[0], "limits", keys[:MaxMembers+1])
	require.Error(t, err)
	_, _, err = CanonicalRosterHash(groupID, keys[0], "limits", keys[:1])
	require.Error(t, err)
}

func mustOpaque(t *testing.T) string {
	t.Helper()
	id, err := NewOpaqueID()
	require.NoError(t, err)
	return id
}

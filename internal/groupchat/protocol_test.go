package groupchat

import (
	"encoding/json"
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
	require.ErrorContains(t, err, "malformed group envelope magic")
	_, err = Decode("hyphae.group/v9\n{}")
	require.ErrorIs(t, err, ErrUnsupportedVersion)
	_, err = Decode(MagicV1 + `{"type":"unknown","version":1,"group_id":"00000000000000000000000000000000"}`)
	require.Error(t, err, "unknown envelope type must be rejected")
	_, err = Decode(MagicV1 + `{"TYPE":"message","Version":1,"Group_ID":"00000000000000000000000000000000","Logical_ID":"00000000000000000000000000000000","Body":"hello"}`)
	require.Error(t, err, "case-variant field names must be rejected")
	_, err = Decode(MagicV1 + `{"type":"message","version":1,"group_id":"00000000000000000000000000000000","logical_id":"00000000000000000000000000000000","body":"first","BODY":"last"}`)
	require.Error(t, err, "case-variant duplicates must be rejected")
	_, err = Decode(MagicV1 + `{"type":"message","type":"message"}`)
	require.ErrorContains(t, err, "duplicate group envelope field")
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

func TestInviteRosterHashAndMembershipGuards(t *testing.T) {
	alice := common.EncodeNpub(nostr.Generate().Public())
	bob := common.EncodeNpub(nostr.Generate().Public())
	carol := common.EncodeNpub(nostr.Generate().Public())
	groupID := mustOpaque(t)
	inviteID := mustOpaque(t)
	hash, roster, err := CanonicalRosterHash(groupID, alice, "planning", []string{alice, bob, carol})
	require.NoError(t, err)
	valid := Envelope{Type: EnvelopeInvite, Version: Version, GroupID: groupID, CreatorNpub: alice,
		InviteID: inviteID, RosterHash: hash, InviteeNpub: bob, Name: "planning", Members: roster}

	wrongHash := strings.Repeat("0", 64)
	if wrongHash == hash {
		wrongHash = strings.Repeat("1", 64)
	}
	mutatedHash := valid
	mutatedHash.RosterHash = wrongHash
	err = ValidateEnvelope(mutatedHash)
	require.ErrorContains(t, err, "hash does not match")
	raw, err := json.Marshal(mutatedHash)
	require.NoError(t, err)
	_, err = Decode(MagicV1 + string(raw))
	require.ErrorContains(t, err, "hash does not match")
	_, err = Encode(mutatedHash)
	require.ErrorContains(t, err, "hash does not match")

	outsideHash, outsideRoster, err := CanonicalRosterHash(groupID, alice, "planning", []string{alice, carol})
	require.NoError(t, err)
	outside := valid
	outside.RosterHash, outside.Members, outside.InviteeNpub = outsideHash, outsideRoster, bob
	err = ValidateEnvelope(outside)
	require.ErrorContains(t, err, "non-creator roster member")

	selfHash, selfRoster, err := CanonicalRosterHash(groupID, alice, "planning", []string{alice, bob})
	require.NoError(t, err)
	self := valid
	self.RosterHash, self.Members, self.InviteeNpub = selfHash, selfRoster, alice
	err = ValidateEnvelope(self)
	require.ErrorContains(t, err, "non-creator roster member")

	_, _, err = CanonicalRosterHash(groupID, alice, "planning", []string{bob, carol})
	require.ErrorContains(t, err, "creator must be in roster")
	creatorOutside := valid
	creatorOutside.Members, creatorOutside.RosterHash = []string{bob, carol}, wrongHash
	err = ValidateEnvelope(creatorOutside)
	require.ErrorContains(t, err, "creator must be in roster")
}

func TestControlEnvelopeRoundTripsAndRejectsEveryForbiddenField(t *testing.T) {
	creator := common.EncodeNpub(nostr.Generate().Public())
	invitee := common.EncodeNpub(nostr.Generate().Public())
	groupID, inviteID := mustOpaque(t), mustOpaque(t)
	hash := strings.Repeat("a", 64)
	for _, kind := range []EnvelopeType{EnvelopeAccept, EnvelopeDecline, EnvelopeCancel} {
		base := Envelope{Type: kind, Version: Version, GroupID: groupID, CreatorNpub: creator,
			InviteID: inviteID, RosterHash: hash, InviteeNpub: invitee}
		encoded, err := Encode(base)
		require.NoError(t, err, "legal %s envelope encodes", kind)
		decoded, err := Decode(encoded)
		require.NoError(t, err, "legal %s envelope decodes", kind)
		require.Equal(t, base, decoded)

		for field, mutate := range map[string]func(*Envelope){
			"name":       func(e *Envelope) { e.Name = "extra" },
			"members":    func(e *Envelope) { e.Members = []string{invitee} },
			"logical_id": func(e *Envelope) { e.LogicalID = mustOpaque(t) },
			"body":       func(e *Envelope) { e.Body = "extra" },
		} {
			bad := base
			mutate(&bad)
			require.ErrorContains(t, ValidateEnvelope(bad), "unrelated fields", "%s forbids %s", kind, field)
		}
	}

	activationHash, members, err := CanonicalRosterHash(groupID, creator, "planning", []string{creator, invitee})
	require.NoError(t, err)
	activation := Envelope{Type: EnvelopeActivate, Version: Version, GroupID: groupID, CreatorNpub: creator,
		InviteID: inviteID, RosterHash: activationHash, InviteeNpub: invitee, Name: "planning", Members: members}
	encoded, err := Encode(activation)
	require.NoError(t, err)
	decoded, err := Decode(encoded)
	require.NoError(t, err)
	require.Equal(t, activation, decoded)
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

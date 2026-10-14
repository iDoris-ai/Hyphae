package groupchat

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Proof-gap (test-only, paired): EXACT 47d8daf missing-invite_id fixture.
// Baseline 127f460: NewStore falsely healthy, CreateGroup then fails (RED).
// Candidate 47d8daf: NewStore refuses with missing invite_id (GREEN).
func TestProofGapNewStoreMembersMissingInviteID(t *testing.T) {
	db := openTestDB(t)
	_, err := db.Exec(`CREATE TABLE groupchat_members (
		local_npub TEXT NOT NULL,
		group_id TEXT NOT NULL,
		npub TEXT NOT NULL,
		state TEXT NOT NULL,
		is_creator INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY(local_npub, group_id, npub)
	)`)
	require.NoError(t, err)

	_, newStoreErr := NewStore(db)
	alice := newMember(t)
	bob := newMember(t)
	_, createErr := (&Store{db: db}).CreateGroup("pg-group", alice.npub, []string{bob.npub})
	t.Logf("PROOFGAP groupchat newStoreErr=%v createGroupErr=%v", newStoreErr, createErr)

	require.Error(t, createErr, "semantic negative: CreateGroup must fail on members table lacking invite_id")
	require.Contains(t, createErr.Error(), "invite_id")
	require.Error(t, newStoreErr, "NewStore must refuse (127f460 is falsely healthy here)")
	require.Contains(t, newStoreErr.Error(), "missing required column(s) invite_id")
}

// Positive control (both SHAs): nullable TEXT invite_id is healthy and
// CreateGroup succeeds on the same otherwise-identical members DDL.
func TestProofGapNewStoreAcceptsNullableInviteIDControl(t *testing.T) {
	db := openTestDB(t)
	_, err := db.Exec(`CREATE TABLE groupchat_members (
		local_npub TEXT NOT NULL,
		group_id TEXT NOT NULL,
		npub TEXT NOT NULL,
		invite_id TEXT,
		state TEXT NOT NULL,
		is_creator INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY(local_npub, group_id, npub)
	)`)
	require.NoError(t, err)
	store, err := NewStore(db)
	require.NoError(t, err, "nullable invite_id members table must be accepted")
	alice := newMember(t)
	bob := newMember(t)
	draft, err := store.CreateGroup("pg-healthy", alice.npub, []string{bob.npub})
	require.NoError(t, err)
	require.Equal(t, "pg-healthy", draft.Group.Name)
	t.Logf("PROOFGAP groupchat nullable-invite_id control NewStore=ok CreateGroup=ok")
}

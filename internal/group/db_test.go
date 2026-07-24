package group

import (
	"fmt"
	"os"
	"sync"
	"testing"

	"github.com/AuraAIHQ/agent-speaker/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestDB(t *testing.T) (*DB, func()) {
	tempDir := t.TempDir()
	os.Setenv("HOME", tempDir)

	db, err := NewDB()
	require.NoError(t, err)

	cleanup := func() {
		os.RemoveAll(tempDir)
		os.Unsetenv("HOME")
	}

	return db, cleanup
}

func TestNewDB(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	assert.NotNil(t, db)
	assert.NotNil(t, db.db)
}

func TestCreateGroup(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	group, err := db.CreateGroup("Test Group", "A test group", "npub1creator", []string{"npub1alice", "npub1bob"}, nil)
	require.NoError(t, err)
	assert.NotNil(t, group)
	assert.Equal(t, "Test Group", group.Name)
	assert.Equal(t, "A test group", group.Description)
	assert.Equal(t, "npub1creator", group.Creator)
	assert.Len(t, group.Members, 2)
	assert.NotEmpty(t, group.ID)
	assert.Greater(t, group.CreatedAt, int64(0))
}

func TestGetGroup(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Create group
	created, err := db.CreateGroup("Get Test", "Testing get", "npub1creator", []string{"npub1member"}, nil)
	require.NoError(t, err)

	// Get group
	group, err := db.GetGroup(created.ID)
	require.NoError(t, err)
	assert.NotNil(t, group)
	assert.Equal(t, created.ID, group.ID)
	assert.Equal(t, "Get Test", group.Name)

	// Get non-existent group
	group, err = db.GetGroup("nonexistent")
	require.NoError(t, err)
	assert.Nil(t, group)
}

func TestGetGroupMembers(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	members := []string{"npub1alice", "npub1bob", "npub1charlie"}
	group, err := db.CreateGroup("Members Test", "", "npub1creator", members, nil)
	require.NoError(t, err)

	retrievedMembers, err := db.GetGroupMembers(group.ID)
	require.NoError(t, err)
	assert.Len(t, retrievedMembers, 3)
}

func TestGetGroupsForUser(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	alice := "npub1alice"
	bob := "npub1bob"

	// Create groups - include creator in members
	_, err := db.CreateGroup("Group 1", "", alice, []string{alice, bob}, nil)
	require.NoError(t, err)

	_, err = db.CreateGroup("Group 2", "", bob, []string{bob, alice}, nil)
	require.NoError(t, err)

	_, err = db.CreateGroup("Group 3", "", "npub1other", []string{"npub1other", "npub1other2"}, nil)
	require.NoError(t, err)

	// Get groups for alice
	groups, err := db.GetGroupsForUser(alice)
	require.NoError(t, err)
	assert.Len(t, groups, 2)
}

func TestAddMember(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	group, err := db.CreateGroup("Add Member Test", "", "npub1creator", []string{"npub1existing"}, nil)
	require.NoError(t, err)

	// Add new member
	err = db.AddMember(group.ID, "npub1new", types.RoleHuman)
	require.NoError(t, err)

	// Verify
	members, err := db.GetGroupMembers(group.ID)
	require.NoError(t, err)
	assert.Len(t, members, 2)

	// Add duplicate (should not error)
	err = db.AddMember(group.ID, "npub1new", types.RoleHuman)
	require.NoError(t, err)
}

func TestRemoveMember(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	alice := "npub1alice"
	bob := "npub1bob"
	charlie := "npub1charlie"

	group, err := db.CreateGroup("Remove Test", "", alice, []string{alice, bob, charlie}, nil)
	require.NoError(t, err)

	// Remove bob
	err = db.RemoveMember(group.ID, bob)
	require.NoError(t, err)

	// Verify
	members, err := db.GetGroupMembers(group.ID)
	require.NoError(t, err)
	assert.Len(t, members, 2) // alice and charlie remain
	assert.Contains(t, members, alice)
	assert.Contains(t, members, charlie)
	assert.NotContains(t, members, bob)
}

func TestIsMember(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	alice := "npub1alice"
	bob := "npub1bob"

	group, err := db.CreateGroup("Is Member Test", "", alice, []string{alice, bob}, nil)
	require.NoError(t, err)

	// Check alice is member
	isMember, err := db.IsMember(group.ID, alice)
	require.NoError(t, err)
	assert.True(t, isMember)

	// Check bob is member
	isMember, err = db.IsMember(group.ID, bob)
	require.NoError(t, err)
	assert.True(t, isMember)

	// Check non-member
	isMember, err = db.IsMember(group.ID, "npub1stranger")
	require.NoError(t, err)
	assert.False(t, isMember)
}

func TestDeleteGroup(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	group, err := db.CreateGroup("Delete Test", "", "npub1creator", []string{"npub1creator", "npub1member"}, nil)
	require.NoError(t, err)

	// Delete
	err = db.DeleteGroup(group.ID)
	require.NoError(t, err)

	// Verify deleted
	g, err := db.GetGroup(group.ID)
	require.NoError(t, err)
	assert.Nil(t, g)
}

func TestStoreGroupMessage(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	group, err := db.CreateGroup("Message Test", "", "npub1creator", []string{"npub1creator", "npub1member"}, nil)
	require.NoError(t, err)

	msg := &types.GroupMessage{
		ID:          "msg-1",
		EventID:     "event-1",
		GroupID:     group.ID,
		Sender:      "npub1sender",
		Content:     "encrypted",
		Plaintext:   "Hello group",
		CreatedAt:   1234567890,
		IsEncrypted: false,
	}

	err = db.StoreGroupMessage(msg)
	require.NoError(t, err)

	// Retrieve messages
	messages, err := db.GetGroupMessages(group.ID, 10)
	require.NoError(t, err)
	assert.Len(t, messages, 1)
	assert.Equal(t, "Hello group", messages[0].Plaintext)
}

func TestGetGroupMessages(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	group, err := db.CreateGroup("Messages Test", "", "npub1creator", []string{"npub1creator", "npub1member"}, nil)
	require.NoError(t, err)

	// Store multiple messages with unique IDs
	for i := 0; i < 5; i++ {
		msg := &types.GroupMessage{
			ID:        fmt.Sprintf("msg-%s-%d", group.ID[:8], i),
			EventID:   fmt.Sprintf("event-%s-%d", group.ID[:8], i),
			GroupID:   group.ID,
			Sender:    "npub1sender",
			Plaintext: fmt.Sprintf("Message %d", i),
			CreatedAt: int64(1000 + i),
		}
		err := db.StoreGroupMessage(msg)
		require.NoError(t, err)
	}

	// Get all messages
	messages, err := db.GetGroupMessages(group.ID, 10)
	require.NoError(t, err)
	assert.Len(t, messages, 5)

	// Get limited messages
	messages, err = db.GetGroupMessages(group.ID, 3)
	require.NoError(t, err)
	assert.Len(t, messages, 3)
}

func TestGroupIsMemberMethod(t *testing.T) {
	g := &types.Group{
		Members: []string{"npub1alice", "npub1bob"},
	}

	assert.True(t, g.IsMember("npub1alice"))
	assert.True(t, g.IsMember("npub1bob"))
	assert.False(t, g.IsMember("npub1charlie"))
}

func TestGroupAddMemberMethod(t *testing.T) {
	g := &types.Group{
		Members: []string{"npub1alice"},
	}

	g.AddMember("npub1bob")
	assert.Len(t, g.Members, 2)

	// Add duplicate
	g.AddMember("npub1bob")
	assert.Len(t, g.Members, 2)
}

func TestGroupRemoveMemberMethod(t *testing.T) {
	g := &types.Group{
		Members: []string{"npub1alice", "npub1bob", "npub1charlie"},
	}

	g.RemoveMember("npub1bob")
	assert.Len(t, g.Members, 2)
	assert.Contains(t, g.Members, "npub1alice")
	assert.Contains(t, g.Members, "npub1charlie")
	assert.NotContains(t, g.Members, "npub1bob")
}

func TestCreateGroupWithRoles(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	roles := map[string]types.Role{
		"npub1creator": types.RoleHuman,
		"npub1bot":     types.RoleAgent,
		// "npub1alice" deliberately omitted — should default to human.
	}

	group, err := db.CreateGroup("Role Test", "", "npub1creator", []string{"npub1creator", "npub1bot", "npub1alice"}, roles)
	require.NoError(t, err)

	members, err := db.GetGroupMembersWithRoles(group.ID)
	require.NoError(t, err)
	require.Len(t, members, 3)

	byNpub := map[string]types.Role{}
	for _, m := range members {
		byNpub[m.Npub] = m.Role
	}
	assert.Equal(t, types.RoleHuman, byNpub["npub1creator"])
	assert.Equal(t, types.RoleAgent, byNpub["npub1bot"])
	assert.Equal(t, types.RoleHuman, byNpub["npub1alice"], "member with no entry in the roles map should default to human")
}

func TestCreateGroupNilRolesDefaultsAllToHuman(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	group, err := db.CreateGroup("Nil Roles Test", "", "npub1creator", []string{"npub1creator", "npub1alice"}, nil)
	require.NoError(t, err)

	members, err := db.GetGroupMembersWithRoles(group.ID)
	require.NoError(t, err)
	for _, m := range members {
		assert.Equal(t, types.RoleHuman, m.Role)
	}
}

func TestAddMemberWithRole(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	group, err := db.CreateGroup("Add Role Test", "", "npub1creator", []string{"npub1creator"}, nil)
	require.NoError(t, err)

	require.NoError(t, db.AddMember(group.ID, "npub1bot", types.RoleAgent))
	require.NoError(t, db.AddMember(group.ID, "npub1empty-role", ""))

	members, err := db.GetGroupMembersWithRoles(group.ID)
	require.NoError(t, err)

	byNpub := map[string]types.Role{}
	for _, m := range members {
		byNpub[m.Npub] = m.Role
	}
	assert.Equal(t, types.RoleAgent, byNpub["npub1bot"])
	assert.Equal(t, types.RoleHuman, byNpub["npub1empty-role"], "empty role passed to AddMember should default to human")
}

// TestAddMemberRejectsInvalidRole and TestCreateGroupRejectsInvalidRole cover
// the defense-in-depth validation added at the group DB boundary (Codex
// review finding): only the CLI validated --role before this, so any other
// caller of these exported methods could persist an arbitrary role string.
func TestAddMemberRejectsInvalidRole(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	group, err := db.CreateGroup("Invalid Role Test", "", "npub1creator", []string{"npub1creator"}, nil)
	require.NoError(t, err)

	err = db.AddMember(group.ID, "npub1bogus", types.Role("bogus"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid role")

	members, err := db.GetGroupMembers(group.ID)
	require.NoError(t, err)
	assert.NotContains(t, members, "npub1bogus", "a rejected AddMember call must not persist the member")
}

func TestCreateGroupRejectsInvalidRole(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	roles := map[string]types.Role{"npub1bad": types.Role("bogus")}
	_, err := db.CreateGroup("Invalid Role Create Test", "", "npub1creator", []string{"npub1creator", "npub1bad"}, roles)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid role")
}

// TestGroupMembersTableMigrationIsIdempotent covers the ALTER TABLE ADD
// COLUMN path added for the role field: migrate() must be safe to run
// against a database that already has the column (e.g. NewDB() called twice
// against the same file, or an app restart), not just against a brand-new one.
func TestGroupMembersTableMigrationIsIdempotent(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// migrate() already ran once inside setupTestDB's NewDB(). Running it
	// again against the same underlying *sql.DB must not error.
	require.NoError(t, db.migrate())
	require.NoError(t, db.migrate())
}

// TestGenerateGroupID_ConcurrentSameCreatorNoCollisions is the regression
// test for the pre-existing generateGroupID collision risk recorded in
// specs/m1.5/README.md's known-issues section: 30 concurrent CreateGroup
// calls from the same creator (originally reproduced during PR #18 review,
// 2 collisions out of 9 runs) used to occasionally produce a duplicate ID,
// tripping the groups.id UNIQUE constraint. Now that generateGroupID
// appends a random suffix, none of the 30 concurrent creates should fail or
// collide.
func TestGenerateGroupID_ConcurrentSameCreatorNoCollisions(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Serialize the connection pool: this test's concern is generateGroupID's
	// collision behavior under concurrent callers, not the shared DB's
	// separate, pre-existing SQLITE_BUSY-under-contention behavior (the
	// production code doesn't set SetMaxOpenConns(1) for this DB the way
	// internal/audit does for its own hash-chain DB -- out of scope here).
	db.db.SetMaxOpenConns(1)

	const n = 30
	ids := make([]string, n)
	errs := make([]error, n)

	var wg sync.WaitGroup
	wg.Add(n)
	for i := range n {
		go func(i int) {
			defer wg.Done()
			group, err := db.CreateGroup(fmt.Sprintf("Group %d", i), "", "npub1samecreator", []string{"npub1samecreator"}, nil)
			errs[i] = err
			if group != nil {
				ids[i] = group.ID
			}
		}(i)
	}
	wg.Wait()

	seen := make(map[string]bool, n)
	for i := range n {
		require.NoError(t, errs[i])
		require.False(t, seen[ids[i]], "duplicate group ID generated: %s", ids[i])
		seen[ids[i]] = true
	}
}

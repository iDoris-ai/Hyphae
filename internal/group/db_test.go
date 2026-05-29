package group

import (
	"fmt"
	"os"
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

	group, err := db.CreateGroup("Test Group", "A test group", "npub1creator", []string{"npub1alice", "npub1bob"})
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
	created, err := db.CreateGroup("Get Test", "Testing get", "npub1creator", []string{"npub1member"})
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
	group, err := db.CreateGroup("Members Test", "", "npub1creator", members)
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
	_, err := db.CreateGroup("Group 1", "", alice, []string{alice, bob})
	require.NoError(t, err)

	_, err = db.CreateGroup("Group 2", "", bob, []string{bob, alice})
	require.NoError(t, err)

	_, err = db.CreateGroup("Group 3", "", "npub1other", []string{"npub1other", "npub1other2"})
	require.NoError(t, err)

	// Get groups for alice
	groups, err := db.GetGroupsForUser(alice)
	require.NoError(t, err)
	assert.Len(t, groups, 2)
}

func TestAddMember(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	group, err := db.CreateGroup("Add Member Test", "", "npub1creator", []string{"npub1existing"})
	require.NoError(t, err)

	// Add new member
	err = db.AddMember(group.ID, "npub1new")
	require.NoError(t, err)

	// Verify
	members, err := db.GetGroupMembers(group.ID)
	require.NoError(t, err)
	assert.Len(t, members, 2)

	// Add duplicate (should not error)
	err = db.AddMember(group.ID, "npub1new")
	require.NoError(t, err)
}

func TestRemoveMember(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	alice := "npub1alice"
	bob := "npub1bob"
	charlie := "npub1charlie"

	group, err := db.CreateGroup("Remove Test", "", alice, []string{alice, bob, charlie})
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

	group, err := db.CreateGroup("Is Member Test", "", alice, []string{alice, bob})
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

	group, err := db.CreateGroup("Delete Test", "", "npub1creator", []string{"npub1creator", "npub1member"})
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

	group, err := db.CreateGroup("Message Test", "", "npub1creator", []string{"npub1creator", "npub1member"})
	require.NoError(t, err)

	msg := &types.GroupMessage{
		ID:        "msg-1",
		EventID:   "event-1",
		GroupID:   group.ID,
		Sender:    "npub1sender",
		Content:   "encrypted",
		Plaintext: "Hello group",
		CreatedAt: 1234567890,
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

	group, err := db.CreateGroup("Messages Test", "", "npub1creator", []string{"npub1creator", "npub1member"})
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

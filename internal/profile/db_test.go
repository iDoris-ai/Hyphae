package profile

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/AuraAIHQ/agent-speaker/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestDB(t *testing.T) (*DB, string) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_profiles.db")
	db, err := NewDBWithPath(dbPath)
	require.NoError(t, err)
	return db, dbPath
}

func TestNewDBWithPath(t *testing.T) {
	db, _ := setupTestDB(t)
	defer db.Close()
	assert.NotNil(t, db)
}

func TestStoreAndGetProfile(t *testing.T) {
	db, _ := setupTestDB(t)
	defer db.Close()

	profile := &types.AgentProfile{
		Name:         "Test Agent",
		Description:  "A test agent",
		Availability: types.AvailabilityAvailable,
		Version:      "1.0",
		UpdatedAt:    1234567890,
	}

	err := db.StoreProfile("npub1test", profile)
	require.NoError(t, err)

	retrieved, err := db.GetProfile("npub1test")
	require.NoError(t, err)
	require.NotNil(t, retrieved)
	assert.Equal(t, "Test Agent", retrieved.Name)
	assert.Equal(t, "A test agent", retrieved.Description)
	assert.Equal(t, types.AvailabilityAvailable, retrieved.Availability)
}

func TestGetProfile_NotFound(t *testing.T) {
	db, _ := setupTestDB(t)
	defer db.Close()

	retrieved, err := db.GetProfile("npub1nonexistent")
	require.NoError(t, err)
	assert.Nil(t, retrieved)
}

func TestStoreProfile_UpdateExisting(t *testing.T) {
	db, _ := setupTestDB(t)
	defer db.Close()

	profile1 := &types.AgentProfile{
		Name:         "Original Name",
		Availability: types.AvailabilityAvailable,
		Version:      "1.0",
		UpdatedAt:    1000,
	}

	err := db.StoreProfile("npub1update", profile1)
	require.NoError(t, err)

	profile2 := &types.AgentProfile{
		Name:         "Updated Name",
		Availability: types.AvailabilityBusy,
		Version:      "2.0",
		UpdatedAt:    2000,
	}

	err = db.StoreProfile("npub1update", profile2)
	require.NoError(t, err)

	retrieved, err := db.GetProfile("npub1update")
	require.NoError(t, err)
	assert.Equal(t, "Updated Name", retrieved.Name)
	assert.Equal(t, types.AvailabilityBusy, retrieved.Availability)
}

func TestListProfiles(t *testing.T) {
	db, _ := setupTestDB(t)
	defer db.Close()

	profiles, err := db.ListProfiles()
	require.NoError(t, err)
	assert.Empty(t, profiles)

	err = db.StoreProfile("npub1alice", &types.AgentProfile{
		Name:         "Alice",
		Availability: types.AvailabilityAvailable,
		UpdatedAt:    1000,
	})
	require.NoError(t, err)

	err = db.StoreProfile("npub1bob", &types.AgentProfile{
		Name:         "Bob",
		Availability: types.AvailabilityBusy,
		UpdatedAt:    2000,
	})
	require.NoError(t, err)

	profiles, err = db.ListProfiles()
	require.NoError(t, err)
	assert.Len(t, profiles, 2)

	// Should be ordered by name
	assert.Equal(t, "Alice", profiles[0].Name)
	assert.Equal(t, "npub1alice", profiles[0].Npub)
	assert.Equal(t, "Bob", profiles[1].Name)
	assert.Equal(t, "npub1bob", profiles[1].Npub)
}

func TestSearchProfiles(t *testing.T) {
	db, _ := setupTestDB(t)
	defer db.Close()

	err := db.StoreProfile("npub1seo", &types.AgentProfile{
		Name:         "SEO Master",
		Description:  "Search engine optimization expert",
		Availability: types.AvailabilityAvailable,
		Capabilities: []types.Capability{{Name: "seo"}},
		UpdatedAt:    1000,
	})
	require.NoError(t, err)

	err = db.StoreProfile("npub1writer", &types.AgentProfile{
		Name:         "Content Writer",
		Description:  "Professional blog writer",
		Availability: types.AvailabilityBusy,
		Capabilities: []types.Capability{{Name: "writing"}},
		UpdatedAt:    2000,
	})
	require.NoError(t, err)

	// Search by name
	results, err := db.SearchProfiles("SEO")
	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, "SEO Master", results[0].Name)

	// Search by description
	results, err = db.SearchProfiles("blog")
	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, "Content Writer", results[0].Name)

	// Search by capability (in JSON)
	results, err = db.SearchProfiles("writing")
	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, "Content Writer", results[0].Name)

	// Search with no matches
	results, err = db.SearchProfiles("nonexistent")
	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestDeleteProfile(t *testing.T) {
	db, _ := setupTestDB(t)
	defer db.Close()

	err := db.StoreProfile("npub1delete", &types.AgentProfile{
		Name:      "To Delete",
		UpdatedAt: 1000,
	})
	require.NoError(t, err)

	err = db.DeleteProfile("npub1delete")
	require.NoError(t, err)

	retrieved, err := db.GetProfile("npub1delete")
	require.NoError(t, err)
	assert.Nil(t, retrieved)
}

func TestStoreProfile_InvalidJSON(t *testing.T) {
	db, _ := setupTestDB(t)
	defer db.Close()

	// Create a profile with a field that causes JSON marshal issues
	// Actually json.Marshal handles all our types well, so we'll test
	// the database error path by closing the DB first
	db.Close()

	profile := &types.AgentProfile{Name: "Test", UpdatedAt: 1000}
	err := db.StoreProfile("npub1test", profile)
	assert.Error(t, err)
}

func TestSearchProfiles_EmptyQuery(t *testing.T) {
	db, _ := setupTestDB(t)
	defer db.Close()

	err := db.StoreProfile("npub1test", &types.AgentProfile{
		Name:      "Test",
		UpdatedAt: 1000,
	})
	require.NoError(t, err)

	// Empty query should match everything with LIKE %%
	results, err := db.SearchProfiles("")
	require.NoError(t, err)
	assert.Len(t, results, 1)
}

func TestMigrate_CreatesIndexes(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "index_test.db")

	db, err := NewDBWithPath(dbPath)
	require.NoError(t, err)
	db.Close()

	// Re-opening should not fail (indexes use IF NOT EXISTS)
	db2, err := NewDBWithPath(dbPath)
	require.NoError(t, err)
	defer db2.Close()
}

func TestNewDBWithPath_InvalidPath(t *testing.T) {
	tmpDir := t.TempDir()
	// Create a file where a directory should be
	invalidPath := filepath.Join(tmpDir, "not_a_dir")
	os.MkdirAll(invalidPath, 0755)
	// This path points to a directory, which sqlite should reject
	dbPath := filepath.Join(invalidPath, "subdir", "test.db")

	_, err := NewDBWithPath(dbPath)
	assert.Error(t, err)
}

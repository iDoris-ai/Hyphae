package tui

import (
	"os"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/AuraAIHQ/agent-speaker/internal/identity"
	"github.com/AuraAIHQ/agent-speaker/internal/storage"
	"github.com/AuraAIHQ/agent-speaker/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestEnv(t *testing.T) func() {
	tempDir := t.TempDir()
	os.Setenv("HOME", tempDir)

	// Initialize storage
	_, err := storage.InitDB()
	require.NoError(t, err)

	// Create test identity
	ks, err := identity.LoadKeyStore()
	require.NoError(t, err)

	_, err = identity.CreateIdentity(ks, "testuser")
	if err != nil {
		// Identity might already exist
	}

	return func() {
		storage.CloseDB()
		os.RemoveAll(tempDir)
		os.Unsetenv("HOME")
	}
}

func TestNewChatModel(t *testing.T) {
	cleanup := setupTestEnv(t)
	defer cleanup()

	// Create test contact
	ks, err := identity.LoadKeyStore()
	require.NoError(t, err)

	// First create identity to use as contact
	_, err = identity.CreateIdentity(ks, "testcontact")
	require.NoError(t, err)

	// Reload to get the identity
	ks, err = identity.LoadKeyStore()
	require.NoError(t, err)

	id, _ := identity.GetIdentity(ks, "testcontact")
	if id != nil {
		// Use as contact
		err = identity.AddContact(ks, "testcontact", id.Npub)
		require.NoError(t, err)
	}

	model, err := NewChatModel("testcontact")
	require.NoError(t, err)
	assert.NotNil(t, model)
	assert.Equal(t, "testcontact", model.contactName)
	assert.NotNil(t, model.store)
	assert.NotNil(t, model.viewport)
	assert.NotNil(t, model.input)
}

func TestNewChatModelContactNotFound(t *testing.T) {
	cleanup := setupTestEnv(t)
	defer cleanup()

	_, err := NewChatModel("nonexistent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestChatModelInit(t *testing.T) {
	cleanup := setupTestEnv(t)
	defer cleanup()

	// Create test contact
	ks, err := identity.LoadKeyStore()
	require.NoError(t, err)

	_, err = identity.CreateIdentity(ks, "contactinit")
	require.NoError(t, err)

	ks, err = identity.LoadKeyStore()
	require.NoError(t, err)

	id, _ := identity.GetIdentity(ks, "contactinit")
	if id != nil {
		err = identity.AddContact(ks, "contactinit", id.Npub)
		require.NoError(t, err)
	}

	model, err := NewChatModel("contactinit")
	require.NoError(t, err)

	cmd := model.Init()
	assert.NotNil(t, cmd)
}

func TestChatModelUpdateWindowSize(t *testing.T) {
	cleanup := setupTestEnv(t)
	defer cleanup()

	ks, err := identity.LoadKeyStore()
	require.NoError(t, err)

	_, err = identity.CreateIdentity(ks, "contactws")
	require.NoError(t, err)

	ks, err = identity.LoadKeyStore()
	require.NoError(t, err)

	id, _ := identity.GetIdentity(ks, "contactws")
	if id != nil {
		err = identity.AddContact(ks, "contactws", id.Npub)
		require.NoError(t, err)
	}

	model, err := NewChatModel("contactws")
	require.NoError(t, err)

	// Simulate window resize
	newModel, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m := newModel.(*ChatModel)

	assert.Equal(t, 100, m.width)
	assert.Equal(t, 30, m.height)
}

func TestChatModelQuit(t *testing.T) {
	cleanup := setupTestEnv(t)
	defer cleanup()

	ks, err := identity.LoadKeyStore()
	require.NoError(t, err)

	_, err = identity.CreateIdentity(ks, "contactquit")
	require.NoError(t, err)

	ks, err = identity.LoadKeyStore()
	require.NoError(t, err)

	id, _ := identity.GetIdentity(ks, "contactquit")
	if id != nil {
		err = identity.AddContact(ks, "contactquit", id.Npub)
		require.NoError(t, err)
	}

	model, err := NewChatModel("contactquit")
	require.NoError(t, err)

	// Press escape to quit
	_, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	assert.NotNil(t, cmd) // Should return a quit command
}

func TestNewContactsModel(t *testing.T) {
	cleanup := setupTestEnv(t)
	defer cleanup()

	// Create test identity and contact
	ks, err := identity.LoadKeyStore()
	require.NoError(t, err)

	_, err = identity.CreateIdentity(ks, "myidentity")
	require.NoError(t, err)

	model, err := NewContactsModel()
	require.NoError(t, err)
	assert.NotNil(t, model)
	assert.GreaterOrEqual(t, len(model.identities), 1)
}

func TestContactsModelNavigation(t *testing.T) {
	cleanup := setupTestEnv(t)
	defer cleanup()

	// Create multiple identities
	ks, err := identity.LoadKeyStore()
	require.NoError(t, err)

	_, err = identity.CreateIdentity(ks, "id1")
	require.NoError(t, err)

	ks, err = identity.LoadKeyStore()
	require.NoError(t, err)

	_, err = identity.CreateIdentity(ks, "id2")
	require.NoError(t, err)

	model, err := NewContactsModel()
	require.NoError(t, err)

	// Test navigation down
	newModel, _ := model.Update(tea.KeyMsg{Type: tea.KeyDown})
	m := newModel.(*ContactsModel)
	assert.Equal(t, 1, m.cursor)

	// Test navigation up
	newModel, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = newModel.(*ContactsModel)
	assert.Equal(t, 0, m.cursor)

	// Test quit
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	assert.NotNil(t, cmd) // Should return a quit command
}

func TestFormatMessage(t *testing.T) {
	cleanup := setupTestEnv(t)
	defer cleanup()

	ks, err := identity.LoadKeyStore()
	require.NoError(t, err)

	_, err = identity.CreateIdentity(ks, "contactfmt")
	require.NoError(t, err)

	ks, err = identity.LoadKeyStore()
	require.NoError(t, err)

	id, _ := identity.GetIdentity(ks, "contactfmt")
	if id != nil {
		err = identity.AddContact(ks, "contactfmt", id.Npub)
		require.NoError(t, err)
	}

	model, err := NewChatModel("contactfmt")
	require.NoError(t, err)

	// Test incoming message
	msg := model.formatMessage(types.StoredMessage{
		Plaintext:   "Hello",
		IsIncoming:  true,
		IsEncrypted: false,
		CreatedAt:   1234567890,
	})
	assert.Contains(t, msg, "Hello")
	assert.NotContains(t, msg, "🔒")

	// Test outgoing encrypted message
	msg = model.formatMessage(types.StoredMessage{
		Plaintext:   "Secret",
		IsIncoming:  false,
		IsEncrypted: true,
		CreatedAt:   1234567890,
	})
	assert.Contains(t, msg, "Secret")
	assert.Contains(t, msg, "🔒")
}

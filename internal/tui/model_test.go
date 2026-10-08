package tui

import (
	"encoding/hex"
	"errors"
	"os"
	"sync"
	"testing"

	"fiatjaf.com/nostr"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/internal/identity"
	"github.com/iDoris-ai/hyphae/internal/messaging"
	"github.com/iDoris-ai/hyphae/internal/storage"
	"github.com/iDoris-ai/hyphae/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestEnv(t *testing.T) func() {
	messaging.ResetStoreForTest()
	tempDir := t.TempDir()
	os.Setenv("HOME", tempDir)

	// Initialize storage
	_, err := storage.InitDB()
	require.NoError(t, err)

	// Create test identity
	ks, err := identity.LoadKeyStore()
	require.NoError(t, err)

	// HOME points to a fresh t.TempDir, so the keystore is always empty
	// and CreateIdentity must succeed. A failure here means the test
	// harness is broken and downstream assertions would be misleading.
	_, err = identity.CreateIdentity(ks, "testuser")
	require.NoError(t, err)

	return func() {
		messaging.ResetStoreForTest()
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

func TestInboxDisconnectIsNonFatalAndInputRemainsUsable(t *testing.T) {
	cleanup := setupTestEnv(t)
	defer cleanup()

	ks, err := identity.LoadKeyStore()
	require.NoError(t, err)
	_, err = identity.CreateIdentity(ks, "contactoffline")
	require.NoError(t, err)
	ks, err = identity.LoadKeyStore()
	require.NoError(t, err)
	id, err := identity.GetIdentity(ks, "contactoffline")
	require.NoError(t, err)
	require.NoError(t, identity.AddContact(ks, "contactoffline", id.Npub))

	model, err := NewChatModel("contactoffline", "ws://127.0.0.1:1")
	require.NoError(t, err)
	defer model.Close()

	newModel, _ := model.Update(inboxWatchUpdateMsg{update: messaging.AgentInboxWatchUpdate{Err: errors.New("offline")}})
	chat := newModel.(*ChatModel)
	assert.Nil(t, chat.err, "relay outage is receiver state, not fatal TUI state")
	assert.Contains(t, chat.inboxStatus, "offline")
	chat.input.SetValue("still usable")
	_, cmd := chat.Update(tea.KeyMsg{Type: tea.KeyEnter})
	assert.NotNil(t, cmd, "keyboard input should still schedule a send while receiver reconnects")
}

func TestChatModelConcurrentWatcherStartIsSingleAndCloseJoins(t *testing.T) {
	cleanup := setupTestEnv(t)
	defer cleanup()

	ks, err := identity.LoadKeyStore()
	require.NoError(t, err)
	_, err = identity.CreateIdentity(ks, "contactlifecycle")
	require.NoError(t, err)
	ks, err = identity.LoadKeyStore()
	require.NoError(t, err)
	id, err := identity.GetIdentity(ks, "contactlifecycle")
	require.NoError(t, err)
	require.NoError(t, identity.AddContact(ks, id.Nickname, id.Npub))
	model, err := NewChatModel(id.Nickname, "ws://127.0.0.1:1")
	require.NoError(t, err)

	start := model.startInboxWatcher()
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			start()
		}()
	}
	wg.Wait()
	model.inboxMu.Lock()
	started := model.inboxStarted
	model.inboxMu.Unlock()
	require.True(t, started)
	require.NoError(t, model.Close())
	select {
	case <-model.inboxDone:
	default:
		t.Fatal("Close returned before the receiver goroutine stopped")
	}
}

func TestInboxWatchUpdateLoadsDurableMessageIntoView(t *testing.T) {
	cleanup := setupTestEnv(t)
	defer cleanup()

	ks, err := identity.LoadKeyStore()
	require.NoError(t, err)
	contact, err := identity.CreateIdentity(ks, "livecontact")
	require.NoError(t, err)
	ks, err = identity.LoadKeyStore()
	require.NoError(t, err)
	contact, err = identity.GetIdentity(ks, contact.Nickname)
	require.NoError(t, err)
	require.NoError(t, identity.AddContact(ks, contact.Nickname, contact.Npub))
	myIdentity, err := identity.GetIdentity(ks, "testuser")
	require.NoError(t, err)
	mySK, err := identity.GetSecretKey(ks, myIdentity.Nickname)
	require.NoError(t, err)
	contactSK, err := identity.GetSecretKey(ks, contact.Nickname)
	require.NoError(t, err)
	model, err := NewChatModel(contact.Nickname, "ws://127.0.0.1:1")
	require.NoError(t, err)
	defer model.Close()
	staleInitialRefresh, ok := model.loadMessages()().(messagesMsg)
	require.True(t, ok)
	body := "watch-to-screen-live-message"
	dTag, err := messaging.FormatAgentMessageDTag("0000000000000001")
	require.NoError(t, err)
	myPub := mySK.Public()
	event := nostr.Event{
		Kind:      messaging.AgentKind,
		CreatedAt: nostr.Now(),
		Tags: nostr.Tags{
			{"p", hex.EncodeToString(myPub[:])},
			{"c", messaging.AgentTag},
			{"v", messaging.AgentVersion},
			{"d", dTag},
		},
		Content: body,
	}
	require.NoError(t, event.Sign(contactSK))
	first, err := messaging.StoreIncomingMessageOnce(&event, myIdentity.Npub, body, false)
	require.NoError(t, err)
	require.True(t, first)

	model.inboxUpdates <- messaging.AgentInboxWatchUpdate{Connected: true}
	message := &messaging.ReceivedAgentMessage{
		EventID: event.ID.Hex(), SenderNpub: common.EncodeNpub(contactSK.Public()), Content: body,
	}
	newModel, cmd := model.Update(inboxWatchUpdateMsg{update: messaging.AgentInboxWatchUpdate{Message: message}})
	model = newModel.(*ChatModel)
	require.NotNil(t, cmd)
	batchMsg := cmd()
	commands, ok := batchMsg.(tea.BatchMsg)
	require.True(t, ok, "watch update should schedule DB refresh and next receiver update")

	var refreshed bool
	for _, command := range commands {
		if command == nil {
			continue
		}
		msg := command()
		if _, ok := msg.(messagesMsg); ok {
			newModel, _ = model.Update(msg)
			model = newModel.(*ChatModel)
			refreshed = true
			break
		}
	}
	require.True(t, refreshed, "watch update must execute its actual loadMessages command")
	assert.Contains(t, model.View(), body, "the durable live message must be rendered in the open chat")
	assert.Nil(t, model.err)
	newModel, _ = model.Update(staleInitialRefresh)
	model = newModel.(*ChatModel)
	assert.Contains(t, model.View(), body, "an older empty init query cannot overwrite the live refresh")
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

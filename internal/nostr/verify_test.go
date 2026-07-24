package nostr

import (
	"context"
	"encoding/json"
	"testing"

	"fiatjaf.com/nostr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func signedTestEvent(t *testing.T) *nostr.Event {
	t.Helper()
	sk := nostr.Generate()
	event := &nostr.Event{
		CreatedAt: nostr.Now(),
		Kind:      1,
		Content:   "hello",
		PubKey:    sk.Public(),
	}
	require.NoError(t, event.Sign(sk))
	return event
}

func TestVerifyCmd_ValidSignature(t *testing.T) {
	event := signedTestEvent(t)
	data, err := json.Marshal(event)
	require.NoError(t, err)

	out := captureStdout(t, func() {
		require.NoError(t, VerifyCmd.Run(context.Background(), []string{"verify", string(data)}))
	})
	assert.Contains(t, out, "Signature is VALID")
}

// TestVerifyCmd_TamperedContentIsInvalid covers the round-trip case the
// task spec calls for: sign an event, then flip a byte (here, mutate the
// content after signing) -- verification must fail.
func TestVerifyCmd_TamperedContentIsInvalid(t *testing.T) {
	event := signedTestEvent(t)
	event.Content = "tampered"
	data, err := json.Marshal(event)
	require.NoError(t, err)

	out := captureStdout(t, func() {
		require.NoError(t, VerifyCmd.Run(context.Background(), []string{"verify", string(data)}))
	})
	assert.Contains(t, out, "Signature is INVALID")
}

func TestVerifyCmd_EmptyEventErrors(t *testing.T) {
	err := VerifyCmd.Run(context.Background(), []string{"verify", ""})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "event JSON is required")
}

func TestVerifyCmd_MalformedJSONErrors(t *testing.T) {
	err := VerifyCmd.Run(context.Background(), []string{"verify", "{not json"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid JSON")
}

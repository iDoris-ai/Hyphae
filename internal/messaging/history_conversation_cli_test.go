package messaging

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/iDoris-ai/hyphae/internal/identity"
	"github.com/iDoris-ai/hyphae/internal/storage"
	"github.com/iDoris-ai/hyphae/pkg/types"
)

func TestHistoryConversationCLIJSONAndIdentitySelection(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	ks := &types.KeyStore{Identities: map[string]*types.Identity{}, Contacts: map[string]*types.Contact{}}
	alice, err := identity.CreateIdentity(ks, "alice")
	if err != nil {
		t.Fatal(err)
	}
	carol, err := identity.CreateIdentity(ks, "carol")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := identity.CreateIdentity(ks, "bob")
	if err != nil {
		t.Fatal(err)
	}
	if err := identity.SaveKeyStore(ks); err != nil {
		t.Fatal(err)
	}
	db, err := storage.InitDB()
	if err != nil {
		t.Fatal(err)
	}
	store := storage.NewMessageStore(db)
	fixtures := []types.StoredMessage{
		{ID: "out-new", SenderNpub: alice.Npub, RecipientNpub: bob.Npub, Content: "ciphertext-out", Plaintext: "new outgoing", CreatedAt: 30, ReceivedAt: 31, IsEncrypted: true, IsIncoming: false, Relay: "wss://relay.example"},
		{ID: "in-old", SenderNpub: bob.Npub, RecipientNpub: alice.Npub, Content: "ciphertext-in", Plaintext: "old incoming", CreatedAt: 10, ReceivedAt: 11, IsEncrypted: true, IsIncoming: true, Relay: "wss://relay.example"},
		{ID: "out-mid", SenderNpub: alice.Npub, RecipientNpub: bob.Npub, Content: "plain-content", CreatedAt: 20, ReceivedAt: 21, IsIncoming: false},
		{ID: "carol-only", SenderNpub: carol.Npub, RecipientNpub: bob.Npub, Content: "carol secret", CreatedAt: 40, ReceivedAt: 41, IsIncoming: false},
	}
	for i := range fixtures {
		if err := store.StoreMessage(&fixtures[i]); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		args []string
		env  []string
	}{
		{name: "flag", args: []string{"history", "conversation", "--with", "bob", "--json"}},
		{name: "current env", args: []string{"history", "conversation", "--with", "bob"}, env: []string{"HYPHAE_OUTPUT=json"}},
		{name: "legacy env", args: []string{"history", "conversation", "--with", "bob"}, env: []string{"AGENT_SPEAKER_OUTPUT=json"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := runHistoryConversationCLI(t, home, tc.env, tc.args...)
			if result.code != 0 || result.stderr != "" {
				t.Fatalf("exit=%d stdout=%q stderr=%q", result.code, result.stdout, result.stderr)
			}
			var response struct {
				OK   bool                  `json:"ok"`
				Data []types.StoredMessage `json:"data"`
			}
			if err := json.Unmarshal([]byte(result.stdout), &response); err != nil {
				t.Fatalf("invalid single JSON envelope: %v: %s", err, result.stdout)
			}
			if !response.OK || len(response.Data) != 3 {
				t.Fatalf("unexpected result: %#v", response)
			}
			if response.Data[0].ID != "out-new" || response.Data[1].ID != "out-mid" || response.Data[2].ID != "in-old" {
				t.Fatalf("JSON should preserve newest-first storage order: %#v", response.Data)
			}
			if response.Data[0].Content != "ciphertext-out" || response.Data[0].Plaintext != "new outgoing" || !response.Data[0].IsEncrypted || response.Data[2].IsIncoming != true {
				t.Fatalf("stored message fields were changed: %#v", response.Data)
			}
		})
	}

	empty := runHistoryConversationCLI(t, home, nil, "history", "conversation", "--with", "carol", "--json")
	if empty.code != 0 || empty.stderr != "" || !strings.Contains(empty.stdout, `"data":[]`) {
		t.Fatalf("empty JSON conversation must return [] without human text: %#v", empty)
	}

	limited := runHistoryConversationCLI(t, home, nil, "history", "conversation", "--with", "bob", "--limit", "1", "--json")
	var limitedResponse struct {
		Data []types.StoredMessage `json:"data"`
	}
	if limited.code != 0 || json.Unmarshal([]byte(limited.stdout), &limitedResponse) != nil || len(limitedResponse.Data) != 1 || limitedResponse.Data[0].ID != "out-new" {
		t.Fatalf("limit was not applied to newest-first results: %#v", limited)
	}

	asCarol := runHistoryConversationCLI(t, home, nil, "history", "conversation", "--with", "bob", "--as", "carol", "--json")
	var carolResponse struct {
		Data []types.StoredMessage `json:"data"`
	}
	if asCarol.code != 0 || json.Unmarshal([]byte(asCarol.stdout), &carolResponse) != nil || len(carolResponse.Data) != 1 || carolResponse.Data[0].ID != "carol-only" {
		t.Fatalf("--as did not isolate identity history: %#v", asCarol)
	}

	invalid := runHistoryConversationCLI(t, home, nil, "history", "conversation", "--with", "not-a-recipient", "--json")
	var errorResponse map[string]any
	if invalid.code != 1 || json.Unmarshal([]byte(invalid.stderr), &errorResponse) != nil || errorResponse["error"] != "user_error" || invalid.stdout != "" {
		t.Fatalf("invalid recipient should produce a user_error envelope: %#v", invalid)
	}

	human := runHistoryConversationCLI(t, home, nil, "history", "conversation", "--with", "bob", "--limit", "2")
	if human.code != 0 || !strings.Contains(human.stdout, "Conversation with bob (2 messages)") || !strings.Contains(human.stdout, "plain-content") || !strings.Contains(human.stdout, "new outgoing") || strings.Index(human.stdout, "plain-content") > strings.Index(human.stdout, "new outgoing") {
		t.Fatalf("human output/order changed: %#v", human)
	}
}

type historyCLIResult struct {
	stdout string
	stderr string
	code   int
}

func runHistoryConversationCLI(t *testing.T, home string, extraEnv []string, args ...string) historyCLIResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, outboxCLI, args...)
	cmd.Env = historyCLIEnv(os.Environ(), home, extraEnv)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	result := historyCLIResult{stdout: stdout.String(), stderr: stderr.String()}
	if err == nil {
		return result
	}
	if ctx.Err() != nil {
		t.Fatalf("CLI timed out: %v; stdout=%q stderr=%q", ctx.Err(), result.stdout, result.stderr)
	}
	if exit, ok := err.(*exec.ExitError); ok {
		result.code = exit.ExitCode()
		return result
	}
	t.Fatalf("run CLI: %v", err)
	return result
}

func historyCLIEnv(current []string, home string, extra []string) []string {
	env := make([]string, 0, len(current)+len(extra)+1)
	for _, item := range current {
		if strings.HasPrefix(item, "HOME=") || strings.HasPrefix(item, "HYPHAE_OUTPUT=") || strings.HasPrefix(item, "AGENT_SPEAKER_OUTPUT=") {
			continue
		}
		env = append(env, item)
	}
	env = append(env, "HOME="+home)
	return append(env, extra...)
}

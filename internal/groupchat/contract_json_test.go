package groupchat_test

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// dataKeys returns the sorted keys of the data object of a --json result, or
// of its first element when the object holds the named list.
func dataKeys(t *testing.T, raw, list string) []string {
	t.Helper()
	var env struct {
		OK   bool                       `json:"ok"`
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(raw), &env); err != nil || !env.OK {
		t.Fatalf("not an ok envelope: %v\n%s", err, raw)
	}
	obj := env.Data
	if list != "" {
		var rows []map[string]json.RawMessage
		if err := json.Unmarshal(env.Data[list], &rows); err != nil || len(rows) == 0 {
			t.Fatalf("list %q empty or invalid: %v\n%s", list, err, raw)
		}
		obj = rows[0]
	}
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func wantKeys(t *testing.T, name string, got []string, want ...string) {
	t.Helper()
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s JSON fields changed (Agent24 reads them)\n got: %v\nwant: %v", name, got, want)
	}
}

// TestCommandJSONContract pins the field names of every groupchat command a
// UI reads. A field added, renamed or dropped fails here, so the contract
// document and Agent24 are updated deliberately.
func TestCommandJSONContract(t *testing.T) {
	bin := buildHyphae(t)
	home := t.TempDir()
	createIdentity(t, bin, home, "alice")
	bob := createIdentity(t, bin, home, "bob")
	carol := createIdentity(t, bin, home, "carol")
	j := func(args ...string) string {
		return mustCLI(t, bin, home, append([]string{"--json", "groupchat"}, args...)...)
	}

	created := j("create", "--name", "contract", "--identity", "alice", "--invitee", bob.Npub, "--invitee", carol.Npub)
	wantKeys(t, "create", dataKeys(t, created, ""), "group_id")
	groupID := strings.TrimSpace(strings.SplitN(strings.SplitN(created, `"group_id":"`, 2)[1], `"`, 2)[0])

	invites := j("invite", "list", "--group", groupID, "--identity", "alice")
	wantKeys(t, "invite list", dataKeys(t, invites, "invites"), "group_id", "invite_id", "invitee_npub", "state")

	pending := j("accept", "--identity", "bob", "--invite", inviteFor(t, listInvites(t, bin, home, "alice", groupID), bob.Npub, groupID))
	wantKeys(t, "accept", dataKeys(t, pending, ""), "group_id", "state")
	active := j("accept", "--identity", "carol", "--invite", inviteFor(t, listInvites(t, bin, home, "alice", groupID), carol.Npub, groupID))
	wantKeys(t, "accept", dataKeys(t, active, ""), "group_id", "state")

	wantKeys(t, "list", dataKeys(t, j("list", "--identity", "alice"), "groups"),
		"creator_npub", "group_id", "members", "name", "state", "updated_at")
	wantKeys(t, "members", dataKeys(t, j("members", "--group-id", groupID, "--identity", "alice"), "members"),
		"is_creator", "npub", "state")

	logical := opaqueID(t)
	sendKeys := []string{"accepted", "created_at", "failed", "group_id", "logical_id", "queued", "recipients", "state"}
	sent := j("send", "--group-id", groupID, "--logical-id", logical, "--body", "hi", "--identity", "alice")
	wantKeys(t, "send", dataKeys(t, sent, ""), sendKeys...)
	recipientKeys := []string{"attempts", "max_retries", "queue_id", "recipient_npub", "relay_acks", "relay_count", "state", "event_id"}
	got := dataKeys(t, sent, "recipients")
	for _, k := range recipientKeys {
		if !contains(got, k) {
			t.Fatalf("send recipient lost field %q: %v", k, got)
		}
	}

	status := j("status", "--group-id", groupID, "--logical-id", logical, "--identity", "alice")
	wantKeys(t, "status", dataKeys(t, status, ""), "bookkeeping", "failed", "group_id", "logical_id", "partial", "queued", "recipients")

	// a partial failure: exit 4, nothing on stdout, and the error JSON carries
	// the same report shape as a successful send
	failedLogical := opaqueID(t)
	out, errOut, code := runCLIEnv(t, bin, home, []string{"HYPHAE_FAIL_GROUP_RECIPIENT=" + bob.Npub},
		"--json", "groupchat", "send", "--group-id", groupID, "--logical-id", failedLogical, "--body", "half", "--identity", "alice", "--max-retries", "1")
	if code != 4 || out != "" {
		t.Fatalf("partial send code %d stdout %q", code, out)
	}
	var failure struct {
		OK    bool            `json:"ok"`
		Error string          `json:"error"`
		Data  json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(errOut), &failure); err != nil || failure.OK || failure.Error != "other_error" {
		t.Fatalf("partial send error JSON: %v\n%s", err, errOut)
	}
	wantKeys(t, "send error data", dataKeys(t, `{"ok":true,"data":`+string(failure.Data)+`}`, ""), sendKeys...)

	retryOut := j("retry", "--group-id", groupID, "--logical-id", failedLogical, "--recipient", bob.Npub, "--identity", "alice")
	wantKeys(t, "retry", dataKeys(t, retryOut, ""), sendKeys...)

	history := j("history", "--group-id", groupID, "--identity", "alice")
	wantKeys(t, "history", dataKeys(t, history, "messages"),
		"content", "created_at", "event_id", "group_id", "id", "is_encrypted", "plaintext", "sender")

	// Caller-input mistakes are user_error (exit 1), never other_error (exit 4),
	// so a UI can tell "fix your request" from "Hyphae is broken".
	pendingOut := j("create", "--name", "pending", "--identity", "alice", "--invitee", bob.Npub)
	pendingID := strings.TrimSpace(strings.SplitN(strings.SplitN(pendingOut, `"group_id":"`, 2)[1], `"`, 2)[0])
	unknownGroup := opaqueID(t)
	unknownLogical := opaqueID(t)
	userErrors := []struct {
		name string
		args []string
		msg  string
	}{
		{"send to inactive group", []string{"send", "--group-id", pendingID, "--body", "x", "--identity", "alice"}, "group is not active"},
		{"history of inactive group", []string{"history", "--group-id", pendingID, "--identity", "alice"}, "group is not active"},
		{"send with malformed group id", []string{"send", "--group-id", "bogus", "--body", "x", "--identity", "alice"}, "invalid group ID"},
		{"send with malformed logical id", []string{"send", "--group-id", groupID, "--logical-id", "bogus", "--body", "x", "--identity", "alice"}, "invalid logical message ID"},
		{"send to unknown group", []string{"send", "--group-id", unknownGroup, "--body", "x", "--identity", "alice"}, "group not found"},
		{"members of unknown group", []string{"members", "--group-id", unknownGroup, "--identity", "alice"}, "group not found"},
		{"invite list of malformed group", []string{"invite", "list", "--group", "bogus", "--identity", "alice"}, "invalid group ID"},
		{"status of unknown logical id", []string{"status", "--group-id", groupID, "--logical-id", unknownLogical, "--identity", "alice"}, "not found"},
		{"retry of unknown logical id", []string{"retry", "--group-id", groupID, "--logical-id", unknownLogical, "--identity", "alice"}, "not found"},
		{"accept unknown invite", []string{"accept", "--invite", "nope", "--identity", "bob"}, "invitation not found"},
		{"unknown identity", []string{"list", "--identity", "ghost"}, "identity 'ghost' not found"},
		{"history limit 0", []string{"history", "--group-id", groupID, "--limit", "0", "--identity", "alice"}, "--limit must be at least 1"},
		{"history limit -1", []string{"history", "--group-id", groupID, "--limit", "-1", "--identity", "alice"}, "--limit must be at least 1"},
	}
	for _, tc := range userErrors {
		t.Run("user_error/"+tc.name, func(t *testing.T) {
			out, errOut, code := runCLI(t, bin, home, append([]string{"--json", "groupchat"}, tc.args...)...)
			var failure struct {
				OK      bool   `json:"ok"`
				Error   string `json:"error"`
				Message string `json:"message"`
			}
			if err := json.Unmarshal([]byte(errOut), &failure); err != nil {
				t.Fatalf("error JSON: %v\n%s", err, errOut)
			}
			if code != 1 || out != "" || failure.OK || failure.Error != "user_error" || !strings.Contains(failure.Message, tc.msg) {
				t.Fatalf("want exit 1 user_error containing %q, got exit %d stdout %q: %s", tc.msg, code, out, errOut)
			}
		})
	}
	// the smallest valid limit still works
	one := j("history", "--group-id", groupID, "--limit", "1", "--identity", "alice")
	wantKeys(t, "history limit 1", dataKeys(t, one, "messages"),
		"content", "created_at", "event_id", "group_id", "id", "is_encrypted", "plaintext", "sender")
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

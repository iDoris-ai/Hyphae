package groupchat_test

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestGroupSendSameLogicalIDIsIdempotent pins what Agent24 relies on: a send
// repeated with the same --logical-id and body (e.g. after a timeout that did
// persist) reuses the stored events instead of sending twice, and a different
// body under that id is refused.
func TestGroupSendSameLogicalIDIsIdempotent(t *testing.T) {
	bin := buildHyphae(t)
	home := t.TempDir()
	alice := createIdentity(t, bin, home, "alice")
	bob := createIdentity(t, bin, home, "bob")
	carol := createIdentity(t, bin, home, "carol")
	groupID := activateGroup(t, bin, home, "room", alice.Npub, bob, carol)

	logical := opaqueID(t)
	first := sendJSON(t, bin, home, nil, groupID, logical, "hello", 10)
	before := fanoutSnapshot(t, home, logical)
	again := sendJSON(t, bin, home, nil, groupID, logical, "hello", 10)
	if fanoutSnapshot(t, home, logical) != before || again.LogicalID != logical || len(again.Recipients) != len(first.Recipients) {
		t.Fatalf("resend changed the fanout:\n%s\nvs\n%s", before, fanoutSnapshot(t, home, logical))
	}
	for i := range first.Recipients {
		if again.Recipients[i].EventID != first.Recipients[i].EventID {
			t.Fatalf("resend signed a new event for %s", first.Recipients[i].RecipientNpub)
		}
	}
	if n := queryInt(t, home, `SELECT COUNT(*) FROM groupchat_messages WHERE group_id=? AND logical_id=?`, groupID, logical); n != 1 {
		t.Fatalf("history rows %d", n)
	}
	if h := historyJSON(t, bin, home, groupID); len(h) != 1 || h[0].ID != logical {
		t.Fatalf("history id must be the logical id: %#v", h)
	}

	_, stderr, code := runCLI(t, bin, home, "groupchat", "send", "--json", "--group-id", groupID, "--logical-id", logical, "--body", "other", "--identity", "alice")
	var failure struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal([]byte(stderr), &failure); err != nil || code != 4 || failure.Error != "other_error" ||
		!strings.Contains(failure.Message, "group logical message ID conflict") {
		t.Fatalf("different body: exit %d stderr %s", code, stderr)
	}
	if fanoutSnapshot(t, home, logical) != before {
		t.Fatal("the refused send changed the fanout")
	}
}

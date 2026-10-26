package groupchat_test

import (
	"strings"
	"testing"
)

type groupListData struct {
	Groups []struct {
		GroupID   string   `json:"group_id"`
		Name      string   `json:"name"`
		State     string   `json:"state"`
		Creator   string   `json:"creator_npub"`
		Members   []string `json:"members"`
		UpdatedAt int64    `json:"updated_at"`
	} `json:"groups"`
}

type membersData struct {
	Members []struct {
		Npub      string `json:"npub"`
		State     string `json:"state"`
		IsCreator bool   `json:"is_creator"`
	} `json:"members"`
}

func TestGroupListAndMembersCLI(t *testing.T) {
	bin := buildHyphae(t)
	home := t.TempDir()
	alice := createIdentity(t, bin, home, "alice")
	bob := createIdentity(t, bin, home, "bob")

	empty := mustCLI(t, bin, home, "--json", "groupchat", "list", "--identity", "alice")
	if !strings.Contains(empty, `"groups":[]`) {
		t.Fatalf("no groups must be an empty array, got %s", empty)
	}

	groupID := oneLine(t, mustCLI(t, bin, home, "groupchat", "create", "--name", "planning", "--identity", "alice", "--invitee", bob.Npub))
	listed := decodeData[groupListData](t, mustCLI(t, bin, home, "--json", "groupchat", "list", "--identity", "alice"))
	if len(listed.Groups) != 1 {
		t.Fatalf("alice groups = %+v", listed.Groups)
	}
	g := listed.Groups[0]
	if g.GroupID != groupID || g.Name != "planning" || g.State != "pending" || g.Creator != alice.Npub || len(g.Members) != 2 {
		t.Fatalf("pending group = %+v", g)
	}

	inviteID := inviteFor(t, listInvites(t, bin, home, "alice", groupID), bob.Npub, groupID)
	mustCLI(t, bin, home, "groupchat", "accept", "--identity", "bob", "--invite", inviteID)

	members := decodeData[membersData](t, mustCLI(t, bin, home, "--json", "groupchat", "members", "--group-id", groupID, "--identity", "bob"))
	if len(members.Members) != 2 || members.Members[0].Npub != alice.Npub || !members.Members[0].IsCreator || members.Members[1].Npub != bob.Npub || members.Members[1].IsCreator {
		t.Fatalf("members = %+v", members.Members)
	}
	for _, m := range members.Members {
		if m.State != "active" {
			t.Fatalf("member %s state %s after activation", m.Npub, m.State)
		}
	}
	bobGroups := decodeData[groupListData](t, mustCLI(t, bin, home, "--json", "groupchat", "list", "--identity", "bob"))
	if len(bobGroups.Groups) != 1 || bobGroups.Groups[0].State != "active" {
		t.Fatalf("bob groups = %+v", bobGroups.Groups)
	}

	_, stderr, code := runCLI(t, bin, home, "groupchat", "members", "--group-id", strings.Repeat("0", 32), "--identity", "alice")
	if code == 0 || stderr == "" {
		t.Fatalf("unknown group must fail, code %d stderr %q", code, stderr)
	}
}

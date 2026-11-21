package identity

import (
	"encoding/json"
	"strings"
	"testing"

	"fiatjaf.com/nostr"
	"github.com/iDoris-ai/hyphae/internal/common"
)

func contactJSON(t *testing.T, r cliResult) map[string]any {
	t.Helper()
	var out struct {
		OK   bool           `json:"ok"`
		Data map[string]any `json:"data"`
	}
	if r.code != 0 || json.Unmarshal([]byte(r.stdout), &out) != nil || !out.OK {
		t.Fatalf("command failed: %#v", r)
	}
	return out.Data
}

func listNicks(t *testing.T, home string) []string {
	t.Helper()
	r := runIdentityCLI(t, home, nil, nil, "--json", "contact", "list")
	var out struct {
		Data []struct {
			Nickname string `json:"nickname"`
		} `json:"data"`
	}
	if r.code != 0 || json.Unmarshal([]byte(r.stdout), &out) != nil {
		t.Fatalf("contact list: %#v", r)
	}
	var nicks []string
	for _, c := range out.Data {
		nicks = append(nicks, c.Nickname)
	}
	return nicks
}

func TestContactRenameAndRemove(t *testing.T) {
	home := t.TempDir()
	bob := common.EncodeNpub(nostr.Generate().Public())
	carol := common.EncodeNpub(nostr.Generate().Public())
	for _, args := range [][]string{
		{"contact", "add", "--nickname", "bob", "--npub", bob, "--role", "agent"},
		{"contact", "add", "--nickname", "carol", "--npub", carol},
		{"contact", "add", "--nickname", "carol2", "--npub", carol},
	} {
		if r := runIdentityCLI(t, home, nil, nil, args...); r.code != 0 {
			t.Fatalf("%v: %#v", args, r)
		}
	}

	d := contactJSON(t, runIdentityCLI(t, home, nil, nil, "--json", "contact", "rename", bob, "robert"))
	if d["old_nickname"] != "bob" || d["nickname"] != "robert" || d["npub"] != bob || d["role"] != "agent" {
		t.Fatalf("rename by npub: %v", d)
	}
	contactJSON(t, runIdentityCLI(t, home, nil, nil, "--json", "contact", "rename", "robert", "bobby"))

	for name, args := range map[string][]string{
		"unknown":   {"contact", "rename", "nobody", "x"},
		"ambiguous": {"contact", "rename", carol, "c"},
		"no args":   {"contact", "rename", "bobby"},
		"remove ?":  {"contact", "remove", "nobody"},
	} {
		r := runIdentityCLI(t, home, nil, nil, append([]string{"--json"}, args...)...)
		if r.code != common.ExitUserError || !strings.Contains(r.stderr, `"error":"user_error"`) {
			t.Fatalf("%s should be a user error: %#v", name, r)
		}
	}

	if r := runIdentityCLI(t, home, nil, nil, "--json", "contact", "rename", "bobby", "carol"); r.code != common.ExitUserError ||
		!strings.Contains(r.stderr, `"error":"nickname_exists"`) {
		t.Fatalf("taken nickname: %#v", r)
	}

	d = contactJSON(t, runIdentityCLI(t, home, nil, nil, "--json", "contact", "remove", carol))
	if got := d["removed"].([]any); len(got) != 2 || got[0] != "carol" || got[1] != "carol2" || d["npub"] != carol {
		t.Fatalf("remove by npub: %v", d)
	}
	contactJSON(t, runIdentityCLI(t, home, nil, nil, "--json", "contact", "remove", "bobby"))
	if nicks := listNicks(t, home); len(nicks) != 0 {
		t.Fatalf("contacts left: %v", nicks)
	}
}

package messaging

import (
	"database/sql"
	"encoding/json"
	"sort"
	"testing"

	"github.com/iDoris-ai/hyphae/internal/identity"
	"github.com/iDoris-ai/hyphae/internal/storage"
	"github.com/iDoris-ai/hyphae/pkg/types"
	_ "modernc.org/sqlite"
)

// Proof-gap DM positive control (test-only, paired 127f460 / 47d8daf).
//
// Standalone: it does NOT run after any candidate-only refusal assertion, so it
// executes (and must PASS) on BOTH trees. Real storage DM CRUD plus the
// compiled CLI `history conversation` (local-only, temp HOME, no relay) on a
// messages.db that also carries the EXACT 47d8daf malformed groupchat_members
// table (missing invite_id). The malformed group table must not gate ordinary
// unmanaged DM storage or the DM CLI.
func TestProofGapDMPositiveControl(t *testing.T) {
	ResetStoreForTest()
	t.Cleanup(ResetStoreForTest)

	home := t.TempDir()
	t.Setenv("HOME", home)

	ks := &types.KeyStore{Identities: map[string]*types.Identity{}, Contacts: map[string]*types.Contact{}}
	alice, err := identity.CreateIdentity(ks, "alice")
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

	// Healthy DM schema via real storage, then seed two DMs.
	db, err := storage.InitDB()
	if err != nil {
		t.Fatal(err)
	}
	seed := storage.NewMessageStore(db)
	for _, m := range []*types.StoredMessage{
		{ID: "pg-dm-keep", SenderNpub: alice.Npub, RecipientNpub: bob.Npub, Content: "c1", Plaintext: "pg-keep", CreatedAt: 10, ReceivedAt: 11, IsEncrypted: true},
		{ID: "pg-dm-delete", SenderNpub: bob.Npub, RecipientNpub: alice.Npub, Content: "c2", Plaintext: "pg-delete", CreatedAt: 12, ReceivedAt: 13, IsEncrypted: true, IsIncoming: true},
	} {
		if err := seed.StoreMessage(m); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	storage.DB = nil

	// Plant the EXACT 47d8daf missing-invite_id groupchat_members DDL.
	pgDMPlantMembersMissingInviteID(t)
	if !pgDMMembersTablePresentWithoutInviteID(t) {
		t.Fatal("precondition: malformed groupchat_members (no invite_id) must be present")
	}

	// In-process DM CRUD through the production singleton.
	ResetStoreForTest()
	got, err := GetStore()
	if err != nil {
		t.Fatalf("DM store must open despite malformed groupchat_members: %v", err)
	}
	keep, err := got.GetMessage("pg-dm-keep")
	if err != nil || keep == nil || keep.Plaintext != "pg-keep" {
		t.Fatalf("DM read failed: msg=%#v err=%v", keep, err)
	}
	if err := got.DeleteMessage("pg-dm-delete"); err != nil {
		t.Fatalf("DM delete failed: %v", err)
	}
	if err := got.StoreMessage(&types.StoredMessage{ID: "pg-dm-after", SenderNpub: alice.Npub, RecipientNpub: bob.Npub, Content: "c3", Plaintext: "pg-after", CreatedAt: 14, ReceivedAt: 15, IsEncrypted: true}); err != nil {
		t.Fatalf("DM create after malformed group table failed: %v", err)
	}
	conv, err := got.GetConversation(alice.Npub, bob.Npub, 10)
	if err != nil {
		t.Fatalf("DM conversation read failed: %v", err)
	}
	inProc := pgDMIDs(conv)
	if len(inProc) != 2 || inProc[0] != "pg-dm-after" || inProc[1] != "pg-dm-keep" {
		t.Fatalf("unexpected in-process DM conversation: %v", inProc)
	}
	ResetStoreForTest()

	// Compiled CLI (built from THIS tree by TestMain), local-only history read.
	res := runHistoryConversationCLI(t, home, nil, "history", "conversation", "--with", "bob", "--json")
	var resp struct {
		OK   bool                  `json:"ok"`
		Data []types.StoredMessage `json:"data"`
	}
	jsonErr := json.Unmarshal([]byte(res.stdout), &resp)
	cliIDs := pgDMIDs(resp.Data)
	stillMalformed := pgDMMembersTablePresentWithoutInviteID(t)
	t.Logf("PROOFGAP dm-control inProcIDs=%v cliExit=%d cliOK=%v cliIDs=%v jsonErr=%v membersStillMissingInviteID=%v stderr=%q",
		inProc, res.code, resp.OK, cliIDs, jsonErr, stillMalformed, res.stderr)
	if res.code != 0 || jsonErr != nil || !resp.OK {
		t.Fatalf("DM history CLI must succeed: exit=%d stdout=%q stderr=%q", res.code, res.stdout, res.stderr)
	}
	if len(cliIDs) != 2 || cliIDs[0] != "pg-dm-after" || cliIDs[1] != "pg-dm-keep" {
		t.Fatalf("unexpected CLI DM history: %v", cliIDs)
	}
	if !stillMalformed {
		t.Fatal("control is vacuous: groupchat_members was repaired/removed during the DM path")
	}
}

func pgDMPlantMembersMissingInviteID(t *testing.T) {
	t.Helper()
	path, err := storage.GetDBPath()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.Exec(`
		CREATE TABLE groupchat_members (
			local_npub TEXT NOT NULL,
			group_id TEXT NOT NULL,
			npub TEXT NOT NULL,
			state TEXT NOT NULL,
			is_creator INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (local_npub, group_id, npub)
		);
	`); err != nil {
		t.Fatal(err)
	}
}

func pgDMMembersTablePresentWithoutInviteID(t *testing.T) bool {
	t.Helper()
	path, err := storage.GetDBPath()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	rows, err := raw.Query(`PRAGMA table_info(groupchat_members)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	cols := 0
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			t.Fatal(err)
		}
		cols++
		if name == "invite_id" {
			return false
		}
	}
	return cols > 0
}

func pgDMIDs(ms []types.StoredMessage) []string {
	ids := make([]string, 0, len(ms))
	for _, m := range ms {
		ids = append(ids, m.ID)
	}
	sort.Strings(ids)
	return ids
}

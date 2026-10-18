package messaging

import (
	"bytes"
	"fmt"
	"reflect"
	"testing"

	"fiatjaf.com/nostr"
	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/pkg/types"
)

// typedDiffDMEntry is a compact legacy DM entry for a freshly signed event.
func typedDiffDMEntry(t *testing.T, n int, status string, relays []string) []byte {
	t.Helper()
	recipient := nostr.Generate()
	event := makeInboxEvent(t, recipient.Public(), nostr.Generate(), nostr.Timestamp(1_700_000_000+n), fmt.Sprintf("typed diff dm %d", n), nil)
	eventJSON, err := event.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	return encodeLegacyOutboxEntry(types.OutboxEntry{
		QueueID: fmt.Sprintf("%032x", 0xd1f0000+n), ID: event.ID.Hex(), EventJSON: string(eventJSON),
		RecipientNpub: common.EncodeNpub(recipient.Public()), Relays: relays, MaxRetries: 10,
		LastAttempt: 1, CreatedAt: int64(event.CreatedAt), Status: status,
	})
}

// typedDiffGroupEntry is a legacy group entry for a fresh signed event, with
// non-canonical whitespace so any re-encoding would show.
func typedDiffGroupEntry(t *testing.T, n int) []byte {
	t.Helper()
	recipient := nostr.Generate()
	event := makeInboxEvent(t, recipient.Public(), nostr.Generate(), nostr.Timestamp(1_700_000_000+n), fmt.Sprintf("typed diff group %d", n), nostr.Tags{{"enc", "nip44"}})
	eventJSON, err := event.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	raw := encodeLegacyOutboxEntry(types.OutboxEntry{
		QueueID: fmt.Sprintf("%032x", 0x6f0000+n), Route: "group", ID: event.ID.Hex(), EventJSON: string(eventJSON),
		RecipientNpub: common.EncodeNpub(recipient.Public()), MaxRetries: 10, CreatedAt: int64(event.CreatedAt), Status: "group_pending",
	})
	raw = bytes.Replace(raw, []byte(`"relays":[]`), []byte("\"relays\" :  null"), 1)
	return bytes.Replace(raw, []byte(`,"status"`), []byte(",\n\t  \"status\""), 1)
}

// typedDiffLayout builds a file from a layout: D = failed DM to delete,
// K = pending DM kept, L = legacy fixture DM (relays:null) kept,
// G = O3 group entry (failure_result), W = group entry with recovery_witness
// (same event as G, so never both), H = plain group entry with irregular
// whitespace and relays:null.
func typedDiffLayout(t *testing.T, layout string) [][]byte {
	t.Helper()
	var entries [][]byte
	for i, c := range layout {
		switch c {
		case 'D':
			entries = append(entries, typedDiffDMEntry(t, i, "failed", []string{}))
		case 'K':
			entries = append(entries, typedDiffDMEntry(t, i, "pending", []string{"wss://relay.invalid"}))
		case 'L':
			entries = append(entries, legacyFixtureEntry(t, "legacy-dm-nil-relays.json"))
		case 'G':
			entries = append(entries, bytes.TrimSpace(readF2BFixture(t, "p-o3-entry.json")))
		case 'W':
			entries = append(entries, bytes.TrimSpace(readF2BFixture(t, "p-missing-entry.json")))
		case 'H':
			entries = append(entries, typedDiffGroupEntry(t, i))
		default:
			t.Fatalf("layout %q: unknown entry %q", layout, c)
		}
	}
	return entries
}

// requireRetainedEntriesIdentical checks that the file holds exactly the
// retained original entries, in order, each with byte-identical raw objects
// (signed event_json, relays token, protocol members included).
func requireRetainedEntriesIdentical(t *testing.T, raw []byte, want [][]byte) {
	t.Helper()
	file, err := scanRawOutboxFile(raw)
	if err != nil {
		t.Fatalf("result fails the strict scan: %v\n%s", err, raw)
	}
	if len(file.Entries) != len(want) {
		t.Fatalf("result has %d entries, want %d:\n%s", len(file.Entries), len(want), raw)
	}
	for i, e := range file.Entries {
		if got := raw[e.Span.Start:e.Span.End]; !bytes.Equal(got, want[i]) {
			t.Fatalf("retained entry %d bytes changed:\n got %s\nwant %s", i, got, want[i])
		}
	}
}

// #224 (1): every contiguous deletion run becomes one non-overlapping patch,
// and every retained entry keeps its exact raw bytes.
func TestTypedDiffRemovesContiguousDeletionRuns(t *testing.T) {
	for name, layout := range map[string]string{
		"two leading before group":           "DDG",
		"leading run before two groups":      "DDDGH",
		"middle run":                         "GDDH",
		"trailing run":                       "HGDD",
		"single leading":                     "DG",
		"two leading before witness group":   "DDW",
		"adjacent and separated runs":        "DDGDDKHD",
		"every DM between groups":            "DGDDHD",
		"legacy null relays kept beside run": "DDLGDD",
		"all entries deleted":                "DDD",
		"single entry deleted":               "D",
	} {
		t.Run(name, func(t *testing.T) {
			entries := typedDiffLayout(t, layout)
			path := writeOutboxFile(t, rawOutboxDoc(entries...))
			before, err := readOutbox(path)
			if err != nil {
				t.Fatal(err)
			}
			after := cloneOutbox(before)
			after.Entries = after.Entries[:0]
			var want [][]byte
			for i, e := range before.Entries {
				if e.Status != "failed" {
					after.Entries = append(after.Entries, e)
					want = append(want, entries[i])
				}
			}
			if err := withOutboxLock(path, func() error { return persistOutboxDiffLocked(path, before, after) }); err != nil {
				t.Fatalf("layout %s: %v", layout, err)
			}
			requireRetainedEntriesIdentical(t, mustRead(t, path), want)
			reloaded, err := readOutbox(path)
			if err != nil || len(reloaded.Entries) != len(after.Entries) || (len(after.Entries) > 0 && !reflect.DeepEqual(reloaded.Entries, after.Entries)) {
				t.Fatalf("typed view after removal: %+v %v", reloaded, err)
			}
		})
	}
}

// A deletion run next to a retained DM whose fields change in the same
// transaction stays disjoint from that DM's value patches.
func TestTypedDiffDeletionRunBesideDMUpdate(t *testing.T) {
	entries := typedDiffLayout(t, "DDKDDG")
	path := writeOutboxFile(t, rawOutboxDoc(entries...))
	before, err := readOutbox(path)
	if err != nil {
		t.Fatal(err)
	}
	kept := before.Entries[2]
	kept.Status, kept.RetryCount, kept.LastAttempt = "sent", 3, 99
	after := &types.Outbox{Entries: []types.OutboxEntry{kept, before.Entries[5]}}
	if err := withOutboxLock(path, func() error { return persistOutboxDiffLocked(path, before, after) }); err != nil {
		t.Fatal(err)
	}
	raw := mustRead(t, path)
	wantKept := bytes.Replace(entries[2], []byte(`"status":"pending"`), []byte(`"status":"sent"`), 1)
	wantKept = bytes.Replace(wantKept, []byte(`"retry_count":0`), []byte(`"retry_count":3`), 1)
	wantKept = bytes.Replace(wantKept, []byte(`"last_attempt":1,`), []byte(`"last_attempt":99,`), 1)
	requireRetainedEntriesIdentical(t, raw, [][]byte{wantKept, entries[5]})
}

// The exact external #224 scenario end to end: CleanupOutbox removes two
// leading failed DMs in front of a retained protocol group entry.
func TestCleanupOutboxRemovesLeadingFailedDMsBeforeGroup(t *testing.T) {
	entries := typedDiffLayout(t, "DDG")
	path := stageHomeOutbox(t, rawOutboxDoc(entries...))
	if err := CleanupOutbox(&types.Outbox{}, 0); err != nil {
		t.Fatalf("CleanupOutbox: %v", err)
	}
	requireRetainedEntriesIdentical(t, mustRead(t, path), [][]byte{entries[2]})
}

// #224 (2): a relay difference the raw patches cannot express (nil versus
// empty, either direction) is refused with zero writes instead of reporting
// success without persisting it.
func TestTypedDiffRejectsUnsupportedRelayDifferences(t *testing.T) {
	group := bytes.TrimSpace(readF2BFixture(t, "p-o3-entry.json"))
	nilRelays := legacyFixtureEntry(t, "legacy-dm-nil-relays.json")
	emptyRelays := typedDiffDMEntry(t, 1, "pending", []string{})
	listed := typedDiffDMEntry(t, 2, "pending", []string{"wss://a.invalid"})
	for name, tc := range map[string]struct {
		dm     []byte
		relays []string
		status string
	}{
		"legacy nil to empty":             {nilRelays, []string{}, ""},
		"legacy nil to empty with status": {nilRelays, []string{}, "failed"},
		"empty to nil":                    {emptyRelays, nil, ""},
		"empty to nil with status":        {emptyRelays, nil, "failed"},
		"listed relay replaced":           {listed, []string{"wss://b.invalid"}, ""},
		"listed relay to empty":           {listed, []string{}, ""},
	} {
		t.Run(name, func(t *testing.T) {
			raw := rawOutboxDoc(tc.dm, group)
			path := stageHomeOutbox(t, raw)
			_, err := UpdateOutbox(func(ob *types.Outbox) error {
				ob.Entries[0].Relays = tc.relays
				if tc.status != "" {
					ob.Entries[0].Status = tc.status
				}
				return nil
			})
			requireTypedWriteHeld(t, name, err)
			requireOutboxUnchanged(t, path, raw)
		})
	}
}

// Relays that are left unchanged (nil, empty, listed) still allow the
// supported DM field patches, and their raw token is preserved.
func TestTypedDiffKeepsUnchangedRelays(t *testing.T) {
	group := bytes.TrimSpace(readF2BFixture(t, "p-o3-entry.json"))
	for name, dm := range map[string][]byte{
		"legacy nil": legacyFixtureEntry(t, "legacy-dm-nil-relays.json"),
		"empty":      typedDiffDMEntry(t, 1, "pending", []string{}),
		"listed":     typedDiffDMEntry(t, 2, "pending", []string{"wss://a.invalid", "wss://b.invalid"}),
	} {
		t.Run(name, func(t *testing.T) {
			path := stageHomeOutbox(t, rawOutboxDoc(dm, group))
			before, err := LoadOutbox()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := UpdateOutbox(func(ob *types.Outbox) error { ob.Entries[0].Status = "failed"; return nil }); err != nil {
				t.Fatalf("status update with unchanged relays: %v", err)
			}
			file, err := scanRawOutboxFile(mustRead(t, path))
			if err != nil {
				t.Fatal(err)
			}
			origin, _ := scanRawOutboxFile(rawOutboxDoc(dm, group))
			gotRelays := file.Raw[file.Entries[0].Values["relays"].Start:file.Entries[0].Values["relays"].End]
			wantRelays := origin.Raw[origin.Entries[0].Values["relays"].Start:origin.Entries[0].Values["relays"].End]
			if !bytes.Equal(gotRelays, wantRelays) {
				t.Fatalf("relays token %s, want %s", gotRelays, wantRelays)
			}
			if got := file.Raw[file.Entries[1].Span.Start:file.Entries[1].Span.End]; !bytes.Equal(got, group) {
				t.Fatal("group entry bytes changed")
			}
			after, _ := LoadOutbox()
			if after.Entries[0].Status != "failed" || !reflect.DeepEqual(after.Entries[0].Relays, before.Entries[0].Relays) {
				t.Fatalf("typed view: %+v", after.Entries[0])
			}
			current, err := currentOutboxAttempt(after.Entries[0])
			if err != nil || !sameOutboxAttempt(after.Entries[0], current.entry) {
				t.Fatalf("attempt lost after update: %+v %v", current.entry, err)
			}
		})
	}
}

package messaging

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCommitGroupEnqueueLockedAppendsProtocolEntry(t *testing.T) {
	group, err := ParseRawGroupEntry(legacyFixtureEntry(t, "legacy-165-group-nil-relays.json"))
	if err != nil {
		t.Fatal(err)
	}
	rec := GroupEnqueueRecord{EventJSON: group.Entry.EventJSON, RecipientNpub: group.Entry.RecipientNpub, MaxRetries: 10, CreatedAt: 1_791_539_064}
	path := filepath.Join(t.TempDir(), "outbox.json")

	queueID, existed, err := CommitGroupEnqueueLocked(path, rec)
	if err != nil || existed || len(queueID) != 32 {
		t.Fatalf("O1 on missing file: %q %v %v", queueID, existed, err)
	}
	entry, _, err := ReadGroupEntryLocked(path, group.Entry.ID)
	if err != nil || entry == nil || entry.QueueID != queueID || entry.FanoutProtocol == nil || *entry.FanoutProtocol != 1 {
		t.Fatalf("O1 entry: %+v %v", entry, err)
	}
	if entry.Entry.Status != OutboxStatusGroupPending || entry.Entry.RetryCount != 0 || entry.Entry.LastAttempt != 0 || entry.Entry.MaxRetries != 10 ||
		entry.Entry.CreatedAt != rec.CreatedAt || string(entry.Fields["relays"]) != "[]" {
		t.Fatalf("O1 entry fields: %+v relays=%s", entry.Entry, entry.Fields["relays"])
	}
	ob, err := readOutbox(path)
	if err != nil || len(ob.Entries) != 1 || ob.Entries[0].Route != OutboxRouteGroup || ob.Entries[0].QueueID != queueID {
		t.Fatalf("typed read of the O1 entry: %+v %v", ob, err)
	}

	// Idempotent: the same frozen event for the same recipient is "existed".
	before, _ := os.ReadFile(path)
	again, existed, err := CommitGroupEnqueueLocked(path, rec)
	if err != nil || !existed || again != queueID {
		t.Fatalf("re-enqueue: %q %v %v", again, existed, err)
	}
	requireOutboxBytes(t, path, before)

	// Conflicts write nothing.
	other := rec
	other.RecipientNpub = "npub1qs84sd0arvhxqn33mx7rlc9sncrjwcsgzdgyzxszcu7y4qh7w3dqw4y824"
	if _, _, err := CommitGroupEnqueueLocked(path, other); !errors.Is(err, ErrOutboxEnqueueConflict) {
		t.Fatalf("different recipient: %v", err)
	}
	requireOutboxBytes(t, path, before)
	for name, bad := range map[string]GroupEnqueueRecord{
		"no recipient": {EventJSON: rec.EventJSON, MaxRetries: 10},
		"zero budget":  {EventJSON: rec.EventJSON, RecipientNpub: rec.RecipientNpub},
		"bad event":    {EventJSON: "{", RecipientNpub: rec.RecipientNpub, MaxRetries: 10},
	} {
		if _, _, err := CommitGroupEnqueueLocked(path, bad); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	requireOutboxBytes(t, path, before)

	// Once protocol records exist, typed DM traffic still flows (F2e-2b) and
	// a typed group enqueue is refused.
	t.Setenv("HOME", filepath.Dir(path))
	dm := newDMEntry(t, "00000000000000000000000000000021")
	if err := appendOutboxEntryLocked(path, dm); err != nil {
		t.Fatalf("DM beside O1 entry: %v", err)
	}
	entry, _, err = ReadGroupEntryLocked(path, group.Entry.ID)
	if err != nil || entry.FanoutProtocol == nil {
		t.Fatalf("O1 entry lost its protocol tag: %v", err)
	}
	withRelays := GroupEnqueueRecord{EventJSON: dm.EventJSON, RecipientNpub: dm.RecipientNpub, MaxRetries: 3, CreatedAt: 1, Relays: []string{"wss://x"}}
	if _, _, err := CommitGroupEnqueueLocked(path, withRelays); !errors.Is(err, ErrOutboxEnqueueConflict) {
		t.Fatalf("DM entry already holds the event: %v", err)
	}
}

func TestCommitGroupEnqueueLockedExhaustedEntryConflicts(t *testing.T) {
	failed := exhaustedGroupEntry(t, OutboxStatusGroupFailed)
	parsed, err := ParseRawGroupEntry(failed)
	if err != nil {
		t.Fatal(err)
	}
	path := writeOutboxFile(t, rawOutboxDoc(failed))
	rec := GroupEnqueueRecord{EventJSON: parsed.Entry.EventJSON, RecipientNpub: parsed.Entry.RecipientNpub, MaxRetries: 10, CreatedAt: 1}
	if _, _, err := CommitGroupEnqueueLocked(path, rec); !errors.Is(err, ErrOutboxEnqueueConflict) {
		t.Fatalf("exhausted entry must not be re-enqueued by O1 (that is D6): %v", err)
	}
	requireOutboxUnchanged(t, path, rawOutboxDoc(failed))
}

func requireOutboxBytes(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("outbox bytes changed (%v):\n%s", err, got)
	}
}

// An active entry whose max_retries differs from the frozen record is not
// reusable: O1 refuses without adopting or rewriting the budget.
func TestCommitGroupEnqueueLockedMaxRetriesMismatchConflicts(t *testing.T) {
	group, err := ParseRawGroupEntry(legacyFixtureEntry(t, "legacy-165-group-nil-relays.json"))
	if err != nil {
		t.Fatal(err)
	}
	rec := GroupEnqueueRecord{EventJSON: group.Entry.EventJSON, RecipientNpub: group.Entry.RecipientNpub, MaxRetries: 3, CreatedAt: 7}
	path := filepath.Join(t.TempDir(), "outbox.json")
	if _, _, err := CommitGroupEnqueueLocked(path, rec); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	rec.MaxRetries++
	if _, _, err := CommitGroupEnqueueLocked(path, rec); !errors.Is(err, ErrOutboxEnqueueConflict) {
		t.Fatalf("max_retries mismatch must conflict, got %v", err)
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Fatal("conflict must not rewrite the outbox")
	}
}

func TestCheckGroupEnqueueLockedMatchesCommit(t *testing.T) {
	group, err := ParseRawGroupEntry(legacyFixtureEntry(t, "legacy-165-group-nil-relays.json"))
	if err != nil {
		t.Fatal(err)
	}
	baseRec := GroupEnqueueRecord{
		EventJSON: group.Entry.EventJSON, RecipientNpub: group.Entry.RecipientNpub, MaxRetries: 3, CreatedAt: 7,
	}

	o3Entry, err := ParseRawGroupEntry(readF2BFixture(t, "p-o3-entry.json"))
	if err != nil {
		t.Fatal(err)
	}
	o3Rec := GroupEnqueueRecord{
		EventJSON: o3Entry.Entry.EventJSON, RecipientNpub: o3Entry.Entry.RecipientNpub, MaxRetries: int64(o3Entry.Entry.MaxRetries), CreatedAt: o3Entry.Entry.CreatedAt,
	}

	exhaustedEntryRaw := exhaustedGroupEntry(t, OutboxStatusGroupFailed)
	exhaustedParsed, err := ParseRawGroupEntry(exhaustedEntryRaw)
	if err != nil {
		t.Fatal(err)
	}
	exhaustedRec := GroupEnqueueRecord{
		EventJSON: exhaustedParsed.Entry.EventJSON, RecipientNpub: exhaustedParsed.Entry.RecipientNpub, MaxRetries: 10, CreatedAt: 1,
	}

	tests := []struct {
		name        string
		setup       func(t *testing.T) (path string, rec GroupEnqueueRecord)
		wantExisted bool
		wantErrIs   error
	}{
		{
			name: "missing file",
			setup: func(t *testing.T) (string, GroupEnqueueRecord) {
				return filepath.Join(t.TempDir(), "outbox.json"), baseRec
			},
			wantExisted: false,
			wantErrIs:   nil,
		},
		{
			name: "fresh",
			setup: func(t *testing.T) (string, GroupEnqueueRecord) {
				path := filepath.Join(t.TempDir(), "outbox.json")
				if err := writeRawOutboxAtomic(path, []byte("{\n  \"entries\": []\n}\n")); err != nil {
					t.Fatal(err)
				}
				return path, baseRec
			},
			wantExisted: false,
			wantErrIs:   nil,
		},
		{
			name: "reusable",
			setup: func(t *testing.T) (string, GroupEnqueueRecord) {
				path := filepath.Join(t.TempDir(), "outbox.json")
				if _, _, err := CommitGroupEnqueueLocked(path, baseRec); err != nil {
					t.Fatal(err)
				}
				return path, baseRec
			},
			wantExisted: true,
			wantErrIs:   nil,
		},
		{
			name: "recipient mismatch",
			setup: func(t *testing.T) (string, GroupEnqueueRecord) {
				path := filepath.Join(t.TempDir(), "outbox.json")
				if _, _, err := CommitGroupEnqueueLocked(path, baseRec); err != nil {
					t.Fatal(err)
				}
				badRec := baseRec
				badRec.RecipientNpub = "npub1qs84sd0arvhxqn33mx7rlc9sncrjwcsgzdgyzxszcu7y4qh7w3dqw4y824"
				return path, badRec
			},
			wantExisted: false,
			wantErrIs:   ErrOutboxEnqueueConflict,
		},
		{
			name: "bytes mismatch",
			setup: func(t *testing.T) (string, GroupEnqueueRecord) {
				path := filepath.Join(t.TempDir(), "outbox.json")
				if _, _, err := CommitGroupEnqueueLocked(path, baseRec); err != nil {
					t.Fatal(err)
				}
				badRec := baseRec
				badRec.EventJSON = `{"id":"` + group.Entry.ID + `","kind":1,"pubkey":"0000000000000000000000000000000000000000000000000000000000000000","created_at":1,"tags":[],"content":"changed","sig":"00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000"}`
				return path, badRec
			},
			wantExisted: false,
			wantErrIs:   ErrOutboxEnqueueConflict,
		},
		{
			name: "exhausted",
			setup: func(t *testing.T) (string, GroupEnqueueRecord) {
				path := writeOutboxFile(t, rawOutboxDoc(exhaustedEntryRaw))
				return path, exhaustedRec
			},
			wantExisted: false,
			wantErrIs:   ErrOutboxEnqueueConflict,
		},
		{
			name: "max_retries mismatch",
			setup: func(t *testing.T) (string, GroupEnqueueRecord) {
				path := filepath.Join(t.TempDir(), "outbox.json")
				if _, _, err := CommitGroupEnqueueLocked(path, baseRec); err != nil {
					t.Fatal(err)
				}
				badRec := baseRec
				badRec.MaxRetries = baseRec.MaxRetries + 1
				return path, badRec
			},
			wantExisted: false,
			wantErrIs:   ErrOutboxEnqueueConflict,
		},
		{
			name: "failure_result present",
			setup: func(t *testing.T) (string, GroupEnqueueRecord) {
				path := writeOutboxFile(t, rawOutboxDoc(readF2BFixture(t, "p-o3-entry.json")))
				return path, o3Rec
			},
			wantExisted: false,
			wantErrIs:   ErrOutboxEnqueueConflict,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path, rec := tc.setup(t)

			var beforeBytes []byte
			var beforeMtime time.Time
			var fileExisted bool
			if fi, err := os.Stat(path); err == nil {
				fileExisted = true
				beforeMtime = fi.ModTime()
				b, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				beforeBytes = b
			}

			checkQ, checkExisted, checkErr := CheckGroupEnqueueLocked(path, rec)

			// Verify file is untouched
			if !fileExisted {
				if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("CheckGroupEnqueueLocked must not create file")
				}
			} else {
				fi, err := os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
				if !fi.ModTime().Equal(beforeMtime) {
					t.Fatalf("CheckGroupEnqueueLocked modified file mtime: before %v, after %v", beforeMtime, fi.ModTime())
				}
				afterBytes, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(beforeBytes, afterBytes) {
					t.Fatalf("CheckGroupEnqueueLocked modified file bytes")
				}
			}

			// Verify error class
			if tc.wantErrIs != nil {
				if !errors.Is(checkErr, tc.wantErrIs) {
					t.Fatalf("CheckGroupEnqueueLocked error = %v, want %v", checkErr, tc.wantErrIs)
				}
			} else if checkErr != nil {
				t.Fatalf("CheckGroupEnqueueLocked unexpected error = %v", checkErr)
			}
			if checkExisted != tc.wantExisted {
				t.Fatalf("CheckGroupEnqueueLocked existed = %v, want %v", checkExisted, tc.wantExisted)
			}

			// Verify CommitGroupEnqueueLocked produces matching existed & error class
			commitQ, commitExisted, commitErr := CommitGroupEnqueueLocked(path, rec)
			if tc.wantErrIs != nil {
				if !errors.Is(commitErr, tc.wantErrIs) {
					t.Fatalf("CommitGroupEnqueueLocked error = %v, want %v", commitErr, tc.wantErrIs)
				}
			} else if commitErr != nil {
				t.Fatalf("CommitGroupEnqueueLocked unexpected error = %v", commitErr)
			}
			if commitExisted != checkExisted {
				t.Fatalf("CommitGroupEnqueueLocked existed (%v) != CheckGroupEnqueueLocked existed (%v)", commitExisted, checkExisted)
			}
			if checkExisted && checkQ != commitQ {
				t.Fatalf("checkQ (%q) != commitQ (%q)", checkQ, commitQ)
			}
		})
	}
}

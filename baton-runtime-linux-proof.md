# Baton M1 startup/identity independent Linux proof

- Task: `cloud-m1-startup-proof-20261014-031037`, attempt `9d5783ed-dd49-4bb6-af80-f31fb5a3d861`
- Candidate under test: exact `042d2794fae972afa7b07086aca486fd6b93c5cc` (startup seam 4ba1a58 + bound identity 57973a4 on accepted 09ac32c)
- Host: Linux 6.12.94+ x86_64, `go1.26.0 linux/amd64` (GOTOOLCHAIN=auto, go.mod `go 1.26.0`)
- Role: independent verifier only. No production file changed on this branch; only new `_test.go` files and this report.

## Verdict: FAIL

The candidate's startup gate (`groupchat.NewStore` called from `daemon` Action and `tui.NewChatModel`) reports a healthy
group schema when an **unindexed** group table name (`groupchat_groups`, `groupchat_members`) is held by a VIEW or by a
wrong-shape table. `CREATE TABLE IF NOT EXISTS` is a silent no-op for an existing object of that name, and no
`CREATE INDEX` targets those two tables, so nothing in migrate touches them. The daemon then starts, dials the relay,
and any later group write fails. The candidate's own view/table fixtures only use `groupchat_invites`, which is indexed
(`idx_groupchat_invites_state`), so they pass for the index error, not for a physical-table health check.

Not fixed here (verifier lane, no production edits). Errors are not normalized and fixtures are not weakened.

### Minimal repro

```
HOME=$(mktemp -d); storage.OpenDB(); Close()            # healthy DM schema only
sqlite3 $HOME/.hyphae/messages.db "CREATE VIEW groupchat_groups AS SELECT 'n' AS local_npub, 'g' AS group_id"
groupchat.NewStore(storage.OpenDB())  => nil error      # expected: refusal
store.CreateGroup(...)                => "cannot modify groupchat_groups because it is a view"
```

| Proof test (new, this branch) | Observed on 042d279 |
|---|---|
| `groupchat.TestProofNewStoreRejectsNonPhysicalUnindexedGroupTables/GroupsView` | FAIL: NewStore nil; CreateGroup `cannot modify groupchat_groups because it is a view` |
| `.../MembersView` | FAIL: NewStore nil; CreateGroup `cannot modify groupchat_members because it is a view` |
| `.../GroupsWrongShapeTable` | FAIL: NewStore nil; CreateGroup `table groupchat_groups has no column named name` |
| `daemon.TestProofDaemonRefusesGroupsViewBeforeRelayDial` | FAIL: daemon ran until killed at 8s, `ok:true` status lines, relay connections = 1 (outbox bytes/mtime unchanged) |
| `tui.TestProofNewChatModelRefusesGroupsView` | FAIL: NewChatModel succeeded, GroupStore retained (0 dials at construct) |

## Commands and results

| Command | Exit | Counted top-level tests |
|---|---:|---|
| `go vet ./...` | 0 | n/a |
| `go vet -tags integration ./...` | 0 | n/a |
| `go test -race -count=1 -timeout 25m -skip 'TestProof\|TestCloud' ./...` | 0 | 740 pass, 0 fail, 0 skip (only exclusion: the 13 TestProof/TestCloud tests run separately below) |
| `go test -race -tags integration -count=1 -timeout 15m ./tests/...` | 0 | 10 pass, 0 fail, 0 skip |
| `go test -race -count=1 -run '<startup/identity/replay regex>' ./internal/{daemon,tui,groupchat,messaging}/` | 0 | 63 pass |
| `go test -race -count=1 -run TestCloud ./internal/{daemon,tui,groupchat,messaging}/` | 0 | 10 pass (prior test-first matrix from 83a377d; its 3 RED cases are now GREEN on the candidate) |
| `go test -race -count=1 -run TestProof ./internal/{daemon,tui,groupchat}/` | 1 | 3 fail (verdict evidence above) |

Focused regex: `FailClosed|CorruptDB|StartupRefusal|SameBrokenFile|HealthyGroupDDL|HealthyStartup|StorageFailure|Identity|AlicePresented|AliceMatching|BaselineUnbound|DuplicateEventID|NewFanoutAttemptHandlerFor|D6|Replay|Recovery`. No `-count=20` full race was run.

## Checklist against the handoff

| Requirement | Status |
|---|---|
| Bad fanout accounting refuses daemon/TUI before relay dial/outbox/watcher | PASS (`TestDaemonFailClosedOnInvalidLegacyFanoutCounters`, `TestTUIFailClosedOnInvalidLegacyFanoutCounters`, prior `TestCloud*Refuses*`) |
| View / wrong table on indexed `groupchat_invites` refuses | PASS (candidate tests) |
| View / wrong table / missing physical table on unindexed `groupchat_groups`/`groupchat_members` refuses | **FAIL** (above) |
| Owned handle closes without closing global DM pool | PASS (sentinel `storage.DB` asserts in candidate + `TestCloudHealthyOpenDBNewStoreAndOwnedHandleCloses`) |
| Same broken group-only schema leaves DM CLI/history usable | PASS (`TestSameBrokenFileDMCLIBaselineHealthy`, `TestCloudBrokenGroupSchemaLeavesDMHistoryAndCLIHealthy`) |
| Corrupt SQLite file fails startup | PASS (`TestDaemonJSONCorruptDBStartupRefusal`, `TestCloudNewChatModelRefusesCorruptMessagesDB`, `TestCloudDaemonRefusesCorruptMessagesDBBeforeRelayDial`) |
| Runtime processing error after healthy startup preserved | PASS (`TestDaemonJSONLocalRelayStorageFailure`, `TestCloudHealthyStartupInjectedStorageFailureAfterStart`, `TestWatchOneRelay_StorageFailureCanRecoverWithoutRestart`) |
| TUI join-before-close | PASS (`TestChatModelConcurrentWatcherStartIsSingleAndCloseJoins`, `TestTUIOutboxCloseCancelsAndJoinsInflightSend`) |
| TUI timeout leaves DB open | Code unchanged by candidate (`Close` returns "database left open" after 5s); **no test exercises the timeout branch** (gap) |
| Bound identity: Bob entry/token offered to Alice => Conflict, raw bytes/mtime/full row unchanged, foreign D6/O3 receipt unconsumed; valid matching replay works | PASS (`TestProcessAlicePresentedBob{Entry,Token,PendingD6Replay,PendingO3Replay}*`, `TestAliceMatchingEntryWorks`; snapshot helper compares outbox bytes, mtime and full fanout row) |

## Coverage gaps

1. No physical-table health check for `groupchat_groups` / `groupchat_members` (the FAIL).
2. Same class of risk for any future unindexed table; `groupchat_fanout` and `groupchat_messages`/`groupchat_invites` are only protected incidentally by their `CREATE INDEX`.
3. TUI Close timeout ("database left open") branch has no test.
4. Daemon proof uses a fake HTTP relay (counts connections); not a real relay.
5. macOS not run here (Linux only).

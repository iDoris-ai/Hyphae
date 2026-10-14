# Baton M1 schema independent Linux proof

- Task: `cloud-m1-schema-linux-20261014-105349`, attempt `be89bb80-81c5-4a20-a04e-8289b45b43d0`
- Candidate under test (frozen): exact `47d8daf824a5dcee2bfb6488da3b62ce75594aa2`
  (`127f460` physical-schema gate + `47d8daf` operational `invite_id` on `groupchat_members`)
- Earlier independent proof `ab1544d` on base `042d2794` rightly **FAIL** (VIEW/wrong-shape on unindexed tables)
- Host: `grok-bot-vm-307756233`, Linux 6.12.94+ x86_64, `go1.26.0 linux/amd64` (GOTOOLCHAIN=auto; box `go1.24.4` selects toolchain)
- Role: independent verifier only. **No production file changed.** Only carried/expanded `_test.go` proof files and this report.
- No runtime activation. No M1/S7/V7 activation. Existing groupinit `safeCandidate` does **NOT** prove V7.
- PTY uncaptured remains **UNPROVEN** (honest gap; not exercised here).

## Verdict: PASS (delivery of proof/testkit, not acceptance)

On frozen `47d8daf`, the startup gate refuses non-physical / wrong-shape / wrong-PK / missing-column group schemas (including missing operational `invite_id`) before daemon/TUI relay dial, with compiled daemon **actual exit 4** (`ExitOtherError`), zero RelayDial, and unchanged outbox. Healthy CreateGroup, legacy fanout migration, and DM history CLI isolation remain green.

| Proof area | Observed on 47d8daf |
|---|---|
| Original 3 negatives (GroupsView, MembersView, GroupsWrongShapeTable) | PASS — NewStore refuses |
| Expanded 36 VIEW/wrong-shape/no-PK/wrong-PK/missing-col/mixed-case matrix (5 tables) | PASS — 36/36 refuse |
| Nullable TEXT `invite_id` lower + mixed table name accepted; missing refuse | PASS |
| Healthy CreateGroup + legacy fanout migration + reopen | PASS |
| Compiled daemon GroupsView → exit 4, RelayDial=0, outbox unchanged | PASS (`exit status 4`) |
| Compiled daemon members missing `invite_id` (127 real-red closed) → exit 4, RelayDial=0, outbox unchanged | PASS |
| TUI NewChatModel refuses GroupsView + missing `invite_id` | PASS |
| Cloud TEST-FIRST matrix (daemon/tui/groupchat/messaging) | PASS |
| Candidate `schema_health` + failclosed invite_id / groups view tests | PASS |

### Minimal green repro (was red on 042 / invite_id gap on 127)

```
# VIEW occupying unindexed groupchat_groups
HOME=$(mktemp -d); storage.OpenDB(); Close()
sqlite3 $HOME/.hyphae/messages.db "CREATE VIEW groupchat_groups AS SELECT 'n' AS local_npub, 'g' AS group_id"
groupchat.NewStore(storage.OpenDB())  => error (expected table, found view)
compiled daemon --json ...            => exit 4, RelayDial=0, outbox untouched

# members missing invite_id (127f460 accepted; 47d8daf refuses)
sqlite3 ... "CREATE TABLE groupchat_members (..., NO invite_id, PRIMARY KEY(...))"
NewStore / daemon / NewChatModel      => refuse before dial; daemon exit 4
```

## Files carried (test-only)

From `git diff 042d2794..ab1544d` (seven `_test.go`), onto TMP based on frozen 47, then expanded for missing coverage only:

| Path | Role |
|---|---|
| `internal/groupchat/startup_proof_test.go` | Original 3 + 36 matrix + invite_id ± + healthy/legacy |
| `internal/daemon/startup_proof_test.go` | Daemon exit-4 / zero dial / outbox for view + missing invite_id |
| `internal/tui/startup_proof_test.go` | TUI refuse view + missing invite_id |
| `internal/daemon/startup_cloud_test.go` | Cloud TEST-FIRST daemon matrix |
| `internal/groupchat/startup_cloud_test.go` | Cloud NewStore / fanout isolation |
| `internal/tui/startup_cloud_test.go` | Cloud NewChatModel matrix |
| `internal/messaging/startup_cloud_test.go` | Cloud DM history CLI isolation |

## Commands and results

| Command | Exit | Notes |
|---|---:|---|
| `go vet ./...` | 0 | |
| `go vet -tags integration ./...` | 0 | |
| `go build -o hyphae ./cmd/hyphae` | 0 | |
| `go test -race -count=1 -timeout 25m -skip 'TestProof\|TestCloud' ./...` | 0 | 774 listed top-level tests excl. Proof/Cloud; all packages ok |
| `go test -race -tags integration -count=1 -timeout 15m ./tests/...` | 0 | 10 listed; packages ok |
| `go test -race -count=1 -run TestProof ./internal/{groupchat,daemon,tui}/` | 0 | 51 PASS lines (incl. subtests), 0 fail, 0 skip |
| `go test -race -count=1 -run TestCloud ./internal/{daemon,tui,groupchat,messaging}/` | 0 | leaf 58 pass across Proof+Cloud JSON recount when combined; Cloud alone packages ok |
| `go test -race -count=1 -run 'TestNewStoreRejects\|TestNewStoreAccepts\|TestValidateTableShape\|TestDaemonFailClosed\|...' ./internal/{groupchat,daemon}/` | 0 | candidate schema_health + failclosed green |

Daemon proof log excerpts (literal):

- GroupsView: `daemon err=exit status 4 timedOut=false relayConnections=0 outboxSizeSame=true outboxMtimeSame=true` message contains `expected a table, found a view`
- Missing invite_id: `exit status 4 … relay=0 outboxSame=true` message contains `missing required column(s) invite_id`

## Honest gaps / non-claims

1. **PTY uncaptured = UNPROVEN** (not run).
2. Fake HTTP/WS relay counts connections; not a real public relay.
3. macOS / Darwin not run (Linux only).
4. Delivery ≠ coordinator acceptance; no M1/S7/V7 or runtime activation.
5. groupinit `safeCandidate` is not V7 evidence.
6. TUI Close timeout ("database left open") branch still has no dedicated proof here (pre-existing gap from ab1544d report).

## Next (coordinator)

Coordinator cross-model acceptance and M1 DAG next step. Stop after this delivery.

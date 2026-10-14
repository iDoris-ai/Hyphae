# Baton startup TEST-FIRST matrix (cloud-m1-startup-20261014-023744)

Independent TEST-FIRST acceptance on base `09ac32c` (owned `storage.OpenDB`, inert `groupchat.NewStore`).
**No production file changes.** Daemon/TUI still lack group-init gate (separate AGY worker).

Owner policy under test: group table init/migration failure ⇒ refuse daemon/TUI **before** any relay dial / watcher / outbox worker / alt-screen; DM-only CLI remains usable; no automatic repair / default group / silent DM-only daemon.

## Commands and results (this attempt)

| Expectation | Command | Exit | Notes |
|---|---|---:|---|
| PASS healthy controls | `go test ./internal/groupchat/ -run 'TestCloud' -count=1` | 0 | OpenDB+NewStore healthy; broken fanout counters fail NewStore; DM `messages` still readable; owned handle close leaves other pool usable |
| PASS DM isolation | `go test ./internal/messaging/ -run 'TestCloud' -count=1` | 0 | Same broken fanout schema; DM `GetConversation` + `history conversation` CLI healthy |
| PASS + expected RED | `go test ./internal/tui/ -run 'TestCloud' -count=1` | 1 | Healthy NewChatModel PASS; corrupt messages.db refuse PASS; **broken group schema refuse FAIL (EXPECTED RED)** — NewChatModel succeeds without group init |
| PASS + expected RED | `go test ./internal/daemon/ -run 'TestCloud' -count=1` | 1 | Healthy OpenDB+NewStore PASS; injected post-start storage failure PASS; **broken group refuse FAIL (EXPECTED RED)** dialed relay connections=1; **corrupt messages.db startup refuse FAIL (EXPECTED RED)** dialed connections=1 (preload only warns) |

Per-test detail:

| Test | Package | Expected | Observed |
|---|---|---|---|
| `TestCloudHealthyOpenDBNewStoreAndOwnedHandleCloses` | groupchat | PASS | PASS |
| `TestCloudBrokenFanoutCountersMakeNewStoreFailWhileMessagesReadable` | groupchat | PASS | PASS |
| `TestCloudBrokenGroupSchemaLeavesDMHistoryAndCLIHealthy` | messaging | PASS | PASS |
| `TestCloudHealthyNewChatModelControl` | tui | PASS | PASS |
| `TestCloudNewChatModelRefusesCorruptMessagesDB` | tui | PASS | PASS |
| `TestCloudNewChatModelRefusesBrokenGroupSchemaBeforeWatcher` | tui | **RED** | FAIL — model constructed; 0 connections (no dial yet at construct) but no group-init refusal |
| `TestCloudHealthyOpenDBNewStoreControl` | daemon | PASS | PASS |
| `TestCloudHealthyStartupInjectedStorageFailureAfterStart` | daemon | PASS | PASS (hook seam; keeps post-start storage-error model; companion `TestDaemonJSONLocalRelayStorageFailure`) |
| `TestCloudDaemonRefusesBrokenGroupSchemaBeforeRelayDial` | daemon | **RED** | FAIL — Action ran, dialed fake relay (connections=1), returned nil on cancel; outbox not fail-closed |
| `TestCloudDaemonRefusesCorruptMessagesDBBeforeRelayDial` | daemon | **RED** | FAIL — Action dialed (connections=1); preload warns `file is not a database` but does not refuse before dial |

## Fixture

Broken group schema: legacy `groupchat_fanout` row with `max_retries=0` after healthy `OpenDB` DM migrate ⇒ `groupchat.NewStore` returns `legacy fanout accounting contains invalid counters`. Messages table remains readable.

## Coverage gap (exact)

Baseline has **no nonintrusive production seam** that opens `groupchat.NewStore` (or equivalent group migrate) inside `daemon.DaemonCmd.Action` or `tui.NewChatModel` before relay dial / inbox watcher / outbox worker. Tests exercise the real public APIs and record the policy gap as EXPECTED RED. Implementing the gate is out of scope for this TEST-FIRST lane (no production edits).

## Files added (test-only + this matrix)

- `internal/groupchat/startup_cloud_test.go`
- `internal/daemon/startup_cloud_test.go`
- `internal/tui/startup_cloud_test.go`
- `internal/messaging/startup_cloud_test.go`
- `baton-startup-test-matrix.md`

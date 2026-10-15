# M1 F3/F4 wiring plan — from inert primitives to `AttemptSendRouted`

- Date: 2026-10-11 (UTC+7)
- Baseline: stacked branch `m1-f3g-wiring-plan` (main `8b0b4d3` + #178/#179/#180 + F2d-2a/b/c + F2e-1 + F3-a..f + F4-a/b). Line numbers refer to that branch.
- Authority: [rev7 contract](m1-group-fanout-contract-20261008.md) §3.3–§3.6, [writer inventory](m1-f2e-writer-inventory-20261011.md). The outbox failure-result/recovery-witness schema supplement cited in earlier drafts (`m1-outbox-result-witness-schema.md`, intended §5/§7) was never published; it remains outside the rev7 contract's authority. Until it lands, the inert readers in [`outbox_result.go`](../../internal/messaging/outbox_result.go) and [`outbox_witness.go`](../../internal/messaging/outbox_witness.go) are cited here as the implementation-level reference for the wire shapes those sections would have covered — not as a normative supplement.
- Status: **plan only**. Nothing in this document is wired; every primitive below is inert (no production caller) until the slices in §4 are reviewed and merged in order.

## 1. What exists on the stack (all inert)

| Layer | Symbol | Branch | Role |
| --- | --- | --- | --- |
| messaging | `scanRawOutboxFile`, `commitRawOutboxEdit[Locked]` | F2d-2a | strict whole-file gate + span map + locked atomic replace |
| messaging | `planOutboxO3`, `planOutboxD6`, `planOutboxO2` | F2d-2b/c, F3-d | exact-condition byte patches (result+budget, epoch replace, precise removal) |
| messaging | `requireTypedOutboxRoundTrip` in `UpdateOutbox`/`SaveOutbox` | F2e-1 | typed writers refuse files that carry protocol records |
| messaging | `WithGroupOutboxLock`, `CommitGroupFailureLocked`, `CommitGroupRecoveryLocked`, `CommitGroupCleanupLocked`, `ReadGroupEntryLocked`, `ConfirmOutboxDurable` | F3-a..d, F4-a | lock-holder API consumed by groupchat |
| groupchat | `Store.reserveFanoutAttempt` (R), `Store.startFanoutAttempt` (S) | F3-c | behind `fanoutBarrierTx` |
| groupchat | `Store.commitFanoutFailure` (O3+T4), `Store.commitFanoutAccepted` (T3+O2), `Store.cleanupAcceptedFanout` | F3-a, F3-d | current-token result boundaries |
| groupchat | `fanoutBarrierTx`, `recoverFanoutFailure`, `recoverFanoutRecovery` | F3-b/c, F4-b | replay-once barrier (T4, T2'), D14, pending/conflict holds |
| groupchat | `Store.authorizeFanoutRecovery` (D6+T2') | F4-a | explicit per-recipient recovery |
| groupchat | `fanoutCrashGate` + `TestFanoutCrashHelper` | F3-e | SIGKILL evidence O3→T4, D6→T2' |

Lock order everywhere: outbox sibling flock → SQLite `BEGIN IMMEDIATE` → short work → commit → release. P (network) runs with both released.

## 2. Current production path and why it cannot stay

`attemptSendGroup` (`internal/messaging/outbox.go:~388`) drives the group route through `GroupOutboxHandler{BeforePublish, MarkRelayAccepted, RecordAttemptFailure}` (`outbox.go:305`), implemented by `groupchat.FanoutOutboxHandler` (`fanout_queue.go:~166`). Today **no production caller passes `AttemptOptions.Handlers.Group`** (daemon, TUI and `storage outbox retry` all call `AttemptSend`/`AttemptSendWithKeyStore`), so every group entry ends in `route_handler_missing`. The handler path itself is rev7-incompatible:

1. `currentOutboxAttempt` (W5) is a typed `UpdateOutbox` read-modify-write: it rewrites the whole file and would erase protocol records (now refused by F2e-1, so it would hard-fail instead).
2. `recordAttemptFailure` writes JSON first and lets the handler validate afterwards (the exact order rev7 §3.3 forbids); it has no token, so late/duplicate callbacks can mutate the row.
3. `MarkRelayAccepted` + `removeGroupOutboxEntry` are two unrelated transactions with a typed O2 that cannot check consumed records.
4. `RequeueFailed` → `queueTargets(requeue=true)` → `RequeueGroupOutboxEntry` is a typed D6 without witness, generation or barrier.

## 3. Target shape

```text
attemptSendGroup(ctx, entry, relays, opts):
  token, err := store.reserveFanoutAttempt(key, recipient)      // barrier: replay T4/T2', D14, active budget
  if pending/conflict/exhausted → return SendResult{Issue: …}, no P
  err = store.startFanoutAttempt(token, len(targets), now)       // S: attempts+1, phase started
  ok := publish(ctx, targets, frozenEvent, dialTimeout)          // P: both locks released
  if ok  → store.commitFanoutAccepted(token, now)                // T3 then precise O2 (cleanup pending ≠ failure)
  else   → store.commitFanoutFailure(token)                      // O3 then T4 (pending ≠ second O3)
  stale_attempt from either commit → SendResult{Superseded: true}, zero writes
```

- The key/recipient come from the SQLite row looked up by `entry.ID` (`UNIQUE(local_npub,event_id)`), never from the JSON entry alone; sender identity must match the row's `local_npub`.
- Targets: `entry.Relays`, falling back to default relays when `[]`/`null` (unchanged runtime choice).
- Operator recovery (`groupchat retry`): `store.authorizeFanoutRecovery(key, recipient, now)` per failed recipient, replacing `RequeueFailed`'s typed requeue.
- Read-only status (`groupchat status`, TUI): `LoadFanoutReport` plus "pending bookkeeping" when the barrier would replay; **status never writes**.

## 4. Slices (each ≤250 production lines, reviewed in order)

| Slice | Change | Gate it closes |
| --- | --- | --- |
| W-1 | `GroupOutboxHandler` → new interface `GroupAttemptHandler{Reserve, Start, Accepted, Failed}` implemented by `groupchat.Store`; `attemptSendGroup` rewritten to §3; W5 `currentOutboxAttempt` skipped for `route=group` (R replaces it) | rev7 §3.3 order |
| W-2 | Daemon (`daemon.go:292`), TUI (`offline_outbox.go:139`), `outbox retry` (`outbox_commands.go:346`) pass `Handlers.Group` built from the opened groupchat store; group entries without a store → `route_handler_missing` stays | D7/D11 |
| W-3 | `RequeueFailed` → `authorizeFanoutRecovery`; `RequeueGroupOutboxEntry` deleted; `EnqueuePrepared` (first enqueue, O1/T2) moved to the raw writer with `fanout_protocol=1` on new entries | F2e-3 / D6 |
| W-4 | `CleanupOutbox`, `outbox clear`, `removeConfirmedOutboxEntries` migrated to raw patches that never touch group entries (W10–W12 of the inventory) | §3.5 evidence retention |
| W-5 | `groupchat status` surfaces `pending bookkeeping` / `queue_conflict` / `cleanup_pending`; CLI/TUI format `publish starts N; failures r/M (current queue)` | report rows |
| V7 | Controlled deployment boundary (§3.6.1): writer inventory, launcher checks, old-binary exclusion evidence | S7 precondition |

S7–S9 stay blocked until W-1..W-5 and V7 are merged and the V1–V7 evidence is recorded against exact heads.

## 5. Evidence already on the stack vs still owed

| Vn | Owed | Have |
| --- | --- | --- |
| V2 | two-OS-process A/B late failure/ACK, fields unchanged | unit-level matrix (F3-f): B unreturned / accepted / failed, reverse order with exhaustion |
| V3 | SIGKILL after O3, SQLite rollback, replay once | F3-e child process, SIGKILL wait status, replay once, attempts unchanged |
| V4 | three D6 reasons, kill before T2', hook rollback, second call no reset | F4-a reasons; F3-e kill before T2'; F4-b replay once; hook runs in T2' tx |
| V5 | priority matrix (corrupt → duplicate → conflict → orphan → pending → normal) | partial: D14, queue_missing, pending/conflict holds; **no ordered matrix test yet** |
| V6 | strict raw mutations on the two captured fixtures | F2a/F2b suites; F2d-2a scan rejects; **no mutation sweep over nested result/witness nulls yet** |
| V7 | deployment exclusion | **none** |

Still owed beyond tests: uncertain-commit paths are exercised only through `confirmFanout*Commit` unit tests, not through an injected directory-fsync failure; two-OS-process V2 evidence; the `relays` `[]` vs `null` runtime regression once DM writers move to raw patches.

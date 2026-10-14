# Baton Linux reliability proof

- **task_id**: cloud-m1-linux-reliability-proof-20261014
- **attempt_id**: 2d5e2330-ee07-435a-8c02-1e59f92c5f1e
- **lane**: Grok Bot Linux (cloud VM only; no Darwin/Mac Mini)
- **pinned base_sha**: `09ac32c048b790d2f62f6bb9b0b51bd87dfbf1ae`
- **base_branch**: `jhfnetboy/night-m1-runtime-accepted-base`
- **proof branch**: `baton/grokbot-20261014-m1-linux-reliability-proof`
- **OS/arch**: Linux 6.12.94+ x86_64 (PREEMPT_DYNAMIC)
- **Go**: go1.24.4 linux/amd64
- **production changes**: none
- **test-only files added**: none (pre-existing regressions covered all requested paths)

## Baseline (exact commands)

| # | command | exit_code | duration |
|---|---------|-----------|----------|
| 1 | `go test ./... -race -count=1` | 0 | 54s |
| 2 | `go vet ./...` | 0 | 1s |
| 3 | `go test -tags integration ./tests -count=1` | 0 | 8s |

Race packages: all `ok` (20 packages with tests); 3 packages without test files (`cmd/hyphae-relay`, `internal/notify`, `scripts`). No FAIL lines. No race detector reports.

## Focused verification (matched names; each run once)

List step used `go test <pkg> -list '<regex>'` — every regex below matched exactly 1 test (nonzero). Then executed with `-count=1 -v`.

### D6-before-T2prime replay once
- `TestCrashAfterD6BeforeT2PrimeReplaysExactlyOnce` — PASS

### old-epoch snapshot replay then Stale / no R
- `TestFanoutAttemptHandlerOldEpochSnapshotReplaysDurableOnceThenStale` — PASS

### O3/T4 replay once
- `TestCrashAfterO3BeforeT4ReplaysExactlyOnce` — PASS
- `TestFanoutAttemptHandlerReplaysCrashedO3T4Once` — PASS

### forged recipient/ID/event no-mutation
- `TestFanoutAttemptHandlerForgedSnapshotNoMutation` (subtests: forged_ID, forged_recipient, forged_snapshot_event) — PASS
- `TestFanoutAttemptHandlerRejectsStaleAndForgedSnapshotsWithoutMutation` (stale_queue_epoch, same_ID_forged_content_never_publishes) — PASS

### live valid-but-different raw event vs frozen → Conflict no-mutation
- `TestFanoutAttemptHandlerLivePersistedEventMismatchIsConflictNoMutation` — PASS
  - Coverage gap: **none** for this path; committed regression present and green.

### D14 mismatched epoch without witness
- `TestFanoutAttemptHandlerQueuedWrongEpochWithoutWitnessD14ZeroMutation` — PASS

### escaped id/pubkey canonical signed publish
- `TestFanoutAttemptHandlerReservesEscapedEventMemberNames` — PASS
- `TestAttemptSendGroupAttemptCapturesCanonicalEscapedEvent` — PASS

### actual OS flock contention
- `TestDaemonHomeLockIsExclusivePerHome` — PASS (exclusive per-home flock)
- `TestReadRawOutboxEvidenceSnapshotBlocksOnSiblingLock` — PASS (sibling outbox lock blocks)
- `TestFanoutAttemptReservationRacesLockedQueueRotation` — PASS (waits on real `syscall.Flock` in outbox lock path)

### OpenDB owned pools / global sentinel / cross-connection WAL / busy_timeout
- `TestOpenDBDoesNotChangeGlobalDBAndOwnsItsHandle` — PASS
- `TestOpenDBPoolsCanUseSameFileIndependently` — PASS
- `TestOpenDBFailurePreservesGlobalDB` — PASS
- `TestInitDBPragmasApplyToEveryConnection` — PASS (WAL + busy_timeout on every connection)
- `TestConnectionLocalExecPragmasOnlyAffectOneHeldConnection` — PASS

### Focused command exit codes / durations

| label | package | exit_code | duration |
|-------|---------|-----------|----------|
| groupchat-focused (10 tests) | `./internal/groupchat` | 0 | 1s |
| messaging-focused (2 tests) | `./internal/messaging` | 0 | 2s |
| daemon-flock (1 test) | `./internal/daemon` | 0 | 1s |
| storage-opendb (5 tests) | `./internal/storage` | 0 | <1s |

## Verdict

- **PASS** on frozen baseline Linux full race + vet + integration, plus all named focused D6/O3/forgery/escaped/flock/OpenDB predicates.
- No production patch. No signature-validation weakening. No SQL/schema change.
- Sibling lane `cloud226-and-three-agy-lanes` not touched.
- Next: stop and hand back Linux evidence; coordinator combines with Darwin proof. Do not take next task.

## Identity correlation

- result_sha for this report commit is recorded in `baton-result.json` after push (excluding the final receipt-only commit).

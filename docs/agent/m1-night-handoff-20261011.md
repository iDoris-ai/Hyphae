# M1 night stack handoff (2026-10-11)

Review guide for the 23 stacked branches produced on the night of 2026-10-11.
All branches are pushed to `origin`; no pull requests were opened from the CLI
(no valid GitHub token on the build machine). Open them bottom-up with the
compare order below, or with `gh pr create --base <previous> --head <branch>`.

Hard gates honoured: nothing merged, nothing approved, heads of #176–#181
untouched, no tags or releases, PR-Daemon not managed.

## Stack base

`m1-f2-stack-base` = `main` (8b0b4d3) + merges of `origin/m1-f2b-3-group-entry-metadata`,
`origin/m1-f2c-locked-raw-reader`, `origin/m1-f2d1-raw-write-primitives` (PRs #178–#180).
It contains zero new code; open it as a draft and close it once those PRs land.

## Review order (each branch is based on the previous one)

| # | Branch | Slice | What it adds | Wired into production? |
|---|--------|-------|--------------|------------------------|
| 1 | m1-f2-stack-base | — | merge base only | — |
| 2 | m1-f2d2a-raw-outbox-scan | F2d-2a | strict span-preserving outbox scan, locked raw commit (`fsync→rename→dir fsync`) | no (library) |
| 3 | m1-f2d2b-o3-commit | F2d-2b | exact-condition O3 `failure_result` raw commit | no |
| 4 | m1-f2d2c-d6-commit | F2d-2c | exact-condition D6 `recovery_witness` raw commit | no |
| 5 | m1-f2e1-typed-writer-guard | F2e-1 | typed writers refuse round trips that would drop protocol records; writer inventory W1–W13 | yes (refusal path) |
| 6 | m1-f3a-group-failure-commit | F3-a | `CommitGroupFailureLocked` + groupchat O3→T4 boundary | no |
| 7 | m1-f3b-failure-replay | F3-b | recovery barrier replays an unconsumed O3 into T4 | no |
| 8 | m1-f3c-attempt-barrier | F3-c | R/S gated behind the barrier; uncertain O3 commits resolved by re-reading the file | no |
| 9 | m1-f3d-accepted-cleanup | F3-d | T3 relay-accepted commit + precise O2 cleanup | no |
| 10 | m1-f4a-recovery-authorize | F4-a | explicit D6 authorization with T2' in one transaction | no |
| 11 | m1-f4b-recovery-replay | F4-b | T2' replay for a committed D6 whose projection never landed | no |
| 12 | m1-f3e-crash-evidence | F3-e | SIGKILL subprocess evidence for O3→T4 and D6→T2' | tests |
| 13 | m1-f3f-ab-interleaving | F3-f | A/B interleaving matrix; barrier replay commits even when the reservation is refused | tests + fix |
| 14 | m1-f3g-wiring-plan | F3-g | wiring plan W-1…W-5 from inert primitives to `AttemptSendRouted` | docs |
| 15 | m1-f2f-v5-v6-evidence | F2f | V5 classification order and V6 nested mutation sweep | tests |
| 16 | m1-w1-group-attempt-handler | W-1 | `GroupAttemptHandler` route in messaging, `FanoutAttemptHandler` adapter in groupchat; opt-in only | opt-in, no caller yet |
| 17 | m1-f3h-uncertain-injection | F3-h | directory-fsync failure seam; uncertain-commit path exercised end to end | tests + seam |
| 18 | m1-f2e2a-dm-raw-primitives | F2e-2a | raw DM append/patch/remove that leave group records untouched | no |
| 19 | m1-f2e2b-typed-diff-commit | F2e-2b | `UpdateOutbox` falls back to DM-only raw patches when protocol records exist | yes |
| 20 | m1-w3a-group-o1-enqueue | W-3a | raw O1 first-enqueue writing `fanout_protocol=1` | no |
| 21 | m1-w3b-queue-targets-raw | W-3b | `enqueuePreparedRaw`: O1+T2 under the rev7 lock order | no (inert) |
| 22 | m1-w5a-bookkeeping-status | W-5a | read-only bookkeeping classification for `groupchat status` | no (inert) |
| 23 | m1-f2e2c-cleanup-retention | F2e-2c | generic `CleanupOutbox` never ages out group entries | yes (behaviour change) |

Behaviour changes visible to users today are only #5, #19 and #23. Everything
else is inert until the W-2/W-3c switches below are made.

## Review hotspots

- `requireConsumedFragment` (F2d-2b, amended): an absent consumed member is
  accepted regardless of the digest passed, because a failure receipt may
  outlive its record after a D6. Confirm this matches rev7 §3.5.
- `fanoutBarrierTx` (F3-f): when the barrier replays a record and the following
  reservation is refused, the replay is committed before the refusal is
  returned. Without this the replay rolled back on every refusal.
- `UpdateOutbox` (F2e-2b): the DM-only diff is computed by identity
  (`sameOutboxIdentity`); any group entry touched by a typed transaction is a
  refusal (`ErrOutboxTypedWriteHeld`), never a silent drop.
- `CleanupOutbox` (F2e-2c): group entries are retained forever by the generic
  cleaner; only the precise O2 removes them. Tests renamed accordingly.
- D6 for `reason=missing` passes no consumed digests; `reason=exhausted` passes
  the receipt digest and the recovery digest (F4-a).

## Verification run on every branch

```bash
gofmt -l . | grep -v '^third_party/'      # empty
go vet ./...
go test ./... -race -count=1
go test -tags integration ./tests -count=1
```

All four passed on every branch head before it was committed. The crash
evidence (#12, #17) re-executes the test binary as a child process and kills
it with SIGKILL at an injected gate; it is part of the normal `go test` run.

## Not done (needs a design decision or a review first)

- **W-2 store wiring.** No production code opens a groupchat store today
  (`groupchat.NewStore` is only used by tests), so the daemon/TUI cannot be
  given a `FanoutAttemptHandler` without deciding where the store lives (S7).
- **W-3c.** `EnqueuePrepared` and `RequeueFailed` still use the typed
  `queueTargets` path. Their existing tests use synthetic events and
  non-npub recipients that the strict scan rejects by design; switching them
  means rewriting those fixtures with real NIP-44 events first.
- **V7 deployment evidence**, the two-OS-process V2 run, S7–S9 and the M1
  acceptance pass remain open.

## Local artefacts (build machine only)

Git bundles of the whole stack and a wrap-up kit with one PR body per branch
plus `open-prs.sh` / `compare-urls.md` are in the operator's home directory.

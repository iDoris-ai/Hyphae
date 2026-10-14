# Baton M1 schema Linux proof-gap followup (test-only)

- Task `cloud-m1-schema-linux-proof-gap-20261014-112307`, attempt `d249bfba-97e1-4fa4-8664-5b89a6b9dbe3`
- Base `aa69dcdc61034e1a177c54bb10adcea4804893d3` (prior Linux proof). **Production changes: 0** (only `_test.go`, this dir, receipts).
- Resolved SHAs (`git cat-file -t` = commit): baseline **127** = `127f460c0eacc2e2d23067fdba485c636cb7dd0f`, candidate **47** = `47d8daf824a5dcee2bfb6488da3b62ce75594aa2` (127 is ancestor of 47; only production delta is `invite_id` in `requiredGroupchatShapes`).
- Host: `grok-bot-vm-307756233`, `Linux 6.12.94+ x86_64`, `go1.26.0 linux/amd64`. Local httptest relay on 127.0.0.1 only; temp HOME per test; no public relay, no production HOME, no credentials.

## Gap closed

1. Actual paired Linux red/green: identical test files overlaid on `git archive` of 127 and 47 (`repro.sh`).
2. Exact outbox checks: `bytes.Equal(before, after)` + sha256 equality (new tests, and aa69 `startup_proof_test.go` daemon tests upgraded from size/mtime-only).
3. Same missing-`invite_id` fixture as 47d8daf `setupMembersTableMissingInviteIDDB` / `TestNewStoreRejectsMembersTableMissingInviteID` (no different invalid DDL, no vacuous PK case).

## Paired results (`go test -race -count=1 -json -run TestProofGap ./internal/{groupchat,daemon,tui}/`, ×3)

| Tree | vet | build | race1 | race2 | race3 | JSON counts each run |
|---|---:|---:|---:|---:|---:|---|
| 127f460 baseline | 0 | 0 | 1 | 1 | 1 | pass 2 / fail 3 |
| 47d8daf candidate | 0 | 0 | 0 | 0 | 0 | pass 5 / fail 0 |

| Test | 127f460 observed (RED) | 47d8daf observed (GREEN) |
|---|---|---|
| `TestProofGapNewStoreMembersMissingInviteID` | `newStoreErr=<nil>` (falsely healthy); `CreateGroup` fails `no column named invite_id` | NewStore refuses `missing required column(s) invite_id`; CreateGroup semantic negative same |
| `TestProofGapNewChatModelMembersMissingInviteID` | `err=<nil> modelNil=false` (TUI constructs), dials=0 at construct | `group storage init failed …` `modelNil=true`, dials=0 |
| `TestProofGapDaemonMembersMissingInviteIDExit4ZeroDial` (compiled CLI) | not self-exited; **LOCAL relay dial at ~107 ms** (bound 8 s), statusSnapshots=4, killed by test | **literal exit 4**, dials=0, statusSnapshots=0, outbox `bytes.Equal=true`, sha256 `a6102941…1c7d` before = after; JSON envelope `other_error` / `group storage init failed` |
| `TestProofGapNewStoreAcceptsNullableInviteIDControl` (positive) | PASS | PASS |
| `TestProofGapDaemonNullableInviteIDHealthyDialsControl` (positive, proves dial counter non-vacuous) | PASS (dial ~113 ms) | PASS (dial ~116 ms) |

DM positive control: on 47, after the daemon refusal, `storage.OpenDB` + DM `messages` INSERT succeeds on the same broken file (inside the daemon test). Existing DM CLI isolation (`TestCloud…` in `internal/messaging`) re-run on the branch, see below.

Logs (sanitized; temp paths -> `<TMP>`): `logs/{b127,c47}.race{1,2,3}.json`, `logs/exit_codes.txt`, vet/build logs.

## Honest gaps / UNPROVEN

- **WorkStarted counter: UNPROVEN as a named counter.** No `WorkStarted` symbol/seam exists in the repo; no production hook added. Evidence instead: (a) TUI: nil model + pre-existing `inboxStarted/outboxStarted` = false; (b) CLI: zero relay dial + zero daemon `--json` status snapshots (stdout lines with `event_sequence`; baseline/healthy show 4) + exit 4; (c) source ordering in 47d8daf `internal/daemon/daemon.go`: group-store refusal returns at L152 / L160 (NewStore at L159) before `ResolveRelays` (L163), `NewStatusStream` and `runDaemonRuntime` (work loop).
- On 127 the outbox bytes were also unchanged in the ~400 ms before the test killed the daemon, so the byte/sha256 check discriminates nothing on baseline; the red is driven by dial / no self-exit / NewStore / TUI.
- The 47d8daf fixture `messages` table lacks `event_id`, so a healthy-control daemon logs a `preload seen … no such column: event_id` warning; it still dials (control holds). Not a schema-gate claim.
- PTY uncaptured still UNPROVEN; macOS not run; delivery is not acceptance; no M1/V7 release closure.

## Branch run (aa69 test tree + this followup, on 47d8daf production)

| Command | Exit | JSON |
|---|---:|---|
| `go vet ./...` | 0 | |
| `go build -o <TMP>/hyphae ./cmd/hyphae` | 0 | |
| `go test -race -count=1 -json -timeout 10m -run 'TestProofGap\|TestProof\|TestCloud' ./internal/{groupchat,daemon,tui,messaging}/` ×3 | 0,0,0 | pass 66 (23 top-level), fail 0, skip 0 each |
| `go test -race -count=1 -json -timeout 20m ./internal/{groupchat,daemon,tui}/` (full, once) | 0 | pass 448 (251 top-level), fail 0, skip 0 |

Full 774 suite not re-run (out of scope for this followup). See `logs/branch_exit_codes.txt`.

## Repro

```
bash baton-proofgap/repro.sh <out_dir>   # from repo root; paired 127/47 vet, build, race x3
```

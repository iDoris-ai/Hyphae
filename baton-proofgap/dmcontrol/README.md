# Standalone DM positive control (test-only, paired 127f460 / 47d8daf)

- Task `cloud-m1-paired-dm-control-20261014-120037`, attempt `121cdc0c-3f1c-4f5e-8e6e-1261ff0d2567`, base code `79ce1431a9893de273ecf196a3082ab037f2a482` (branch tip `0b027f3`).
- **Production changes: 0.** New file only: `internal/messaging/proofgap_dm_control_linux_test.go` (sha256 `61c89360fec010e168317e53bf5ad8b9fde4fe24468892260945ea135a861247`). Candidate/refusal tests and the old 6 raw Linux logs in `../logs/` are unmodified.
- Host: `Linux 6.12.94+ x86_64`, `go1.26.0 linux/amd64`. Temp HOME per test, no relay of any kind (history CLI is local-only), no credentials.

## Why

In 79ce143 the DM raw-SQL INSERT sat *after* `require.*` refusal assertions inside
`TestProofGapDaemonMembersMissingInviteIDExit4ZeroDial`, so on 127f460 (where those
assertions fail) the DM control never executed. `TestProofGapDMPositiveControl` is a
separate top-level test with no candidate-only assertion before it.

## What the control does

1. Healthy DM schema via real `storage.InitDB` + `MessageStore.StoreMessage` (2 DMs alice and bob).
2. Plants the EXACT 47d8daf `setupMembersTableMissingInviteIDDB` `groupchat_members` DDL (no `invite_id`); precondition asserts the table is present and lacks `invite_id`.
3. Production singleton `messaging.GetStore()`: GetMessage (read), DeleteMessage (delete), StoreMessage (create after malformed table), GetConversation, expects `[pg-dm-after pg-dm-keep]`.
4. Compiled CLI built from the SAME tree by the package's existing `TestMain` (`go build ./cmd/hyphae`): `hyphae history conversation --with bob --json` must exit 0, `ok=true`, ids `[pg-dm-after pg-dm-keep]`.
5. Post-condition: `groupchat_members` still lacks `invite_id` (control not vacuous; the DM path did not repair/remove the malformed group table).

## Paired results

Command per tree (after overlay on `git archive <sha>`):
`go vet ./internal/messaging/` then 3x
`go test -race -count=1 -json -timeout 5m -run '^TestProofGapDMPositiveControl$' ./internal/messaging/`

| Tree | vet | race1 | race2 | race3 | JSON each run |
|---|---:|---:|---:|---:|---|
| 127f460 baseline | 0 | 0 | 0 | 0 | `TestProofGapDMPositiveControl` run then pass; 0 fail, 0 skip |
| 47d8daf candidate | 0 | 0 | 0 | 0 | `TestProofGapDMPositiveControl` run then pass; 0 fail, 0 skip |

Every run logged: `PROOFGAP dm-control inProcIDs=[pg-dm-after pg-dm-keep] cliExit=0 cliOK=true cliIDs=[pg-dm-after pg-dm-keep] jsonErr=<nil> membersStillMissingInviteID=true stderr=""`.

Logs: `logs/{b127,c47}.dm.race{1,2,3}.json`, `logs/*.dm.vet.log` (empty = clean), `logs/exit_codes.txt`, `logs/source_hash.txt` (overlay sha256 identical on both trees). Repro: `bash baton-proofgap/dmcontrol/repro.sh <out_dir>` from repo root.

## Honest gaps / UNPROVEN

- The DM control uses the real InitDB `messages` schema plus the exact malformed `groupchat_members` DDL, not the 47d8daf fixture's minimal `messages` table (which lacks `event_id`). The group-table defect is identical.
- The CLI path is `history conversation` (local read). DM send/daemon DM delivery over a relay is NOT exercised here.
- Named WorkStarted counter: still UNPROVEN (no seam; no fake hook added).
- PTY and macOS not run. Delivery is not acceptance.

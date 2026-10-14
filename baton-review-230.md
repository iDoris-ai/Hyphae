# Baton review: #230 rows.Err fix (independent Linux verification)

- task_id: cloud-m1-230-20261014-023744
- attempt_id: b157afa2-feb0-4c65-a30a-0e7c5a12b2da
- Reviewed source SHA (frozen, fetched independently): `44eee6cb94bab8e95d4e7fd8569fff7f84e12a39`
- Parent: `9c0b0510e4f50b673e60b3194ecc0687fe30d4e8` (= current head of PR #230, `m1-w5b-bookkeeping-key-inspect`)
- Intended full #230 base: `m1-f5-night-handoff` @ `e7520d1821a3bcaea8f3985221282dea8e2494b5`; it is an ancestor of the reviewed SHA (5 commits ahead), and `git merge-tree` is clean.
- Author/committer of the reviewed commit: jhfnetboy <jhfnetboy@gmail.com>
- Scope: review only. No production edits in this branch; any fixes go back to AGY.

## Acceptance: PASS

## Diff under review (9c0b..44ee)

- `internal/groupchat/fanout_status.go`: +4 / -1. Production budget delta is a net +3 lines in one function, `InspectFanoutBookkeeping`.
  - `defer rows.Close()` replaces the manual close on the Scan error path.
  - `if err := rows.Err(); err != nil { return err }` comes after the `rows.Next()` loop and before the explicit `rows.Close()` and the per-recipient classification.
  - The function returns `nil, err` on any closure error, so no partial report can be returned.
- `internal/groupchat/fanout_status_test.go`: +218 lines (test-only driver seam plus two subtests).
- No other files changed.

## Findings (with executable evidence)

1. **The fix is correct.** An iterator error that `rows.Next` returns after at least one real row now surfaces as the sentinel error (`errors.Is`), and the function returns `nil` reports.
2. **The baseline bug is real.** On 9c0b, with 3 recipients and a fault after 1 streamed row, `InspectFanoutBookkeeping` returned `err=<nil>` and **1 of 3** reports. That is a partial report that looks complete. (I checked this with a temporary probe on a scratch baseline worktree; it was not committed.)
3. **The AGY driver seam exercises the real `database/sql.Rows.Next` semantics.** It wraps the real `modernc.org/sqlite` connector, and the fault comes from `driver.Rows.Next`, not from Close or Query. It does not bypass the production code: `Store{db: faultDB}` is the production `Store` type, and the production SQL is matched by substring. I found no mocks of `database/sql` itself.
4. **Gaps in the AGY test (non-blocking; my probe covers them):**
   - It never asserts that a real row streamed before the fault (`afterRows=1` is set but not checked).
   - Its `snapshot` covers only the outbox bytes and mtime plus the first recipient's row. It does not cover the other fanout rows or the DB/WAL bytes.
   - `faultStmt` and the Exec/Prepare wrappers are unused for this query, because database/sql takes the `QueryerContext` path. That is dead seam weight, not a bypass.
5. **Independent probe** (`fanout_status_review230_probe_test.go`, test-only, committed on this branch). For 2 and 3 recipients it checks five things:
   - exactly 1 real sqlite row streamed;
   - exactly 1 fault was delivered via `Next`;
   - the driver rows were closed exactly once, and only after the fault (automatic close by database/sql);
   - the sentinel error surfaced and the result was `nil`;
   - nothing changed: outbox bytes, outbox mtime, the full `groupchat_fanout` row dump, DB file bytes, and WAL bytes.
6. The `defer rows.Close()` plus the explicit `rows.Close()` is a harmless double close, since `sql.Rows.Close` is idempotent. It keeps the close-error check.

## Commands and results (Go 1.26.0, linux/amd64)

| Command | Tree | Exit |
|---|---|---|
| `go test ./internal/groupchat -run TestInspectFanoutBookkeepingRowsIteratorDriverSeam -count=1` | scratch baseline 9c0b + new test | 1 (expected FAIL: "An error is expected but got nil") |
| `go test ./internal/groupchat -run TestReview230Probe -count=1` | scratch baseline 9c0b + probe | 1 (expected FAIL: sentinel not surfaced) |
| `go test ./internal/groupchat -run 'TestReview230Probe\|TestInspectFanoutBookkeeping' -count=1` | 44ee + probe | 0 |
| `go test ./internal/groupchat -race -count=20 -run 'TestInspectFanoutBookkeeping\|TestReview230Probe'` | 44ee + probe | 0 |
| `go test ./internal/groupchat ./internal/messaging -race -count=1` | 44ee + probe | 0 |
| `go vet ./...` | 44ee + probe | 0 |
| `git diff --check 9c0b..44ee` | n/a | 0 |
| `gofmt -l internal/groupchat` | 44ee + probe | 0 (no output) |

## Not claimed

- This review does not repair task/report attribution, and it does not claim the cloud service was terminated.
- No PR, merge, force-push, tag, release, or deploy was done, and no other lane was touched. This review does not complete any milestone.
- The coordinator still needs to do independent acceptance.

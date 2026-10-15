# Baton v2 Wave2 — group reinvite negative-coverage review

- task_id: `v2-wave2-reinvite-test-20261015`
- attempt_id: `90acf67b-4ca5-410f-ac79-002cf30d4f27`
- base_sha: `01294a52ec597eec1c213b97a2ca8de7fc5b9795` (git HEAD verified after checkout; equal to `origin/main` at clone time)
- branch: `baton/grokbot-v2-20261015-reinvite-test`
- status: candidate, pending independent review

## Scope
Source write: `internal/groupchat/store_reinvite_negative_test.go` only (new file). No production store, schema or migration edits.
Note: `internal/groupchat/schema_health.go` from the read scope does not exist at this base_sha; the schema was read from `Store.migrate` in `store.go`.

## Current contract (read from store.go at base_sha)
`ReceiveInvite` requires sender == creator and recipient == invitee, rejects a group whose name/creator/roster/hash differ (`ErrProtocolMismatch`) and a locally left/cancelled group (`ErrInvalidTransition`). For a pending or active group it then looks up the invite by `(local_npub, invite_id)`: a known id is an idempotent no-op; an unknown id goes to `insertInvite`. `groupchat_invites` has `UNIQUE (local_npub, group_id, invitee_npub)`, so a fresh invite_id for an invitee that already has an invitation row fails at insert and the immediate transaction is rolled back.
There is no sentinel error for this case, so the test does not demand `ErrInvalidTransition`; it asserts refusal (non-nil error, `created=false`) and no state change.

## Test
`TestReceiveInviteFreshInviteIDForExistingInviteeRefusedWithoutStateChange`, three subtests built with the existing real fixtures (`newThreeMemberGroup`, `AcceptInvite`, `activate`, `buildIncoming` → production `VerifyIncoming` with NIP-44 encryption and creator signature):
1. pending group / pending invite
2. pending group / accepted invite
3. active group / active invite

Each subtest checks the fixture state, takes ordered snapshots of the invitee's local `groupchat_groups`, `groupchat_members` and `groupchat_invites` rows for that group, sends a creator-signed copy of the original invitation with only `invite_id` replaced by a fresh opaque ID, and asserts: error returned, `created=false`, all three snapshots identical, and the fresh invite_id present in zero invite or member rows. It then replays the original invitation and asserts it is still an idempotent no-op with unchanged rows.

Observed runtime refusal (identical in all three subtests): `store group invitation: constraint failed: UNIQUE constraint failed: groupchat_invites.local_npub, groupchat_invites.group_id, groupchat_invites.invitee_npub (2067)`. This is a runtime observation from the test, not an inference from the schema.

## Result
Expected-baseline PASS: this is coverage of current behaviour, not a reproduced bug. No contradiction with the current contract was found, and no failing hypothesis was demonstrated, so no separate temporary-DB demonstration was needed.

Observation for reviewers (not a fix, not in scope): the refusal comes from the SQLite constraint text rather than a typed error, so callers cannot distinguish it with `errors.Is`. If a typed refusal is wanted, that is a production change for a separate task.

## Commands (Go 1.24.4, linux/amd64)
| command | exit |
|---|---|
| `go test -count=1 -v ./internal/groupchat -run TestReceiveInviteFreshInviteID` | 0 (1 test, 3 subtests PASS) |
| `go test -v -race -count=20 ./internal/groupchat -run TestReceiveInviteFreshInviteID` | 0 (20 top-level PASS, 60 subtest PASS, 0 FAIL, 0 DATA RACE) |
| `go vet ./internal/groupchat` | 0 |
| `go build ./...` | 0 |
| `go test -race -count=1 ./internal/groupchat` | 0 |

No dependency fallbacks, replacements, cache copies, relay/PTY/OS probes or M1 acceptance runs were used.

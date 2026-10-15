# Baton v2 Wave1: encrypted-group negative coverage review

- task_id: `v2-wave1-group-negative-20261015`
- attempt_id: `97167ace-9d02-40e9-b0a9-9e7fbc21821d`
- branch: `baton/grokbot-v2-20261015-group-negative`
- base_sha: `56e81b18c48650a4f425bc599e465f7bd2a353a4` (verified: `git rev-parse HEAD` after checkout; equals remote head of the base branch at clone time)
- mode: source read only. No tests, harness, repository scripts, or hooks were run. Zero source diff; only the three metadata files are written.

## Provenance (reported separately, no assumption about main)

- Base `56e81b1` is on `jhfnetboy/m1-three-process-relay-proof-accepted`; `git merge-base --is-ancestor 56e81b1 origin/main` is false, so main does not contain this proof.
- Read-only reference `7df2029` (fanout_queue_test.go shared `created_at` fixture) is NOT an ancestor of the base. It is only on `origin/jhfnetboy/m1-group-createdat-fixture-accepted`. Inspected with `git show` only; not cherry-picked or recreated.

## Coverage map at base (evidence = test that exists in source)

| Rejection class | Unit coverage | Three-process e2e coverage | No-state-change asserted? |
|---|---|---|---|
| Bad signature | `verified_test.go:33-39` (`CreatedAt++`, ID recomputed, signature invalid) | `copied-pubkey` (`three_process_group_relay_test.go:1395-1403`, relay-refused, verify-refuse) | e2e yes (`recordNegative` scoped snapshot, :1623-1635) |
| Ciphertext tamper / wrong key | `verified_test.go:27-32` (wrong recipient key, mutated content) | `copied-claimed-sender` (:1376-1392, Mallory re-signs copied ciphertext) and `wrong-recipient-*` (:1364-1371) | e2e yes |
| Signed p-tag routing, classifier tag, plaintext | `verified_test.go:41-67` | not separately | n/a (verify layer, no DB) |
| Unverified / zero-value / malformed opaque value | `store_test.go:246-252`, `:317-338` | n/a | error only |
| Invitee / authority (wrong signer per control) | `store_test.go:254-314` (invite, accept, activation, decline, cancel signed by non-authority) | not covered | yes for accept/activation/decline/cancel (group state re-read); invite: error only |
| Roster / hash binding | `protocol_test.go:78-123`; activation spoof `store_test.go:209-228` | not covered | second spoof asserts only `require.Error` (:228) |
| Unknown sender | `messages_test.go:23-28` (fresh stranger key) | `foreign-sender-*` (:1347-1358, Mallory) | e2e yes; unit error only |
| Unknown version / unknown field / duplicate / case-variant keys | `protocol_test.go:13-75` | not covered | n/a (decode happens in `envelopeFrom` before any tx, `store.go:1130-1142`) |
| Wrong / unknown group | n/a | `wrong-group-*` (:1330-1346) uses a random never-created group ID | e2e yes |
| Replay of the same event | invite `store_test.go:535-539`, activation `:147-152`, message idempotency `store_test.go:474`, logical-ID conflict `messages_test.go:9-21` | duplicate delivery (`sawDup`) | yes (idempotent returns false) |

## Finding (REAL gap, source evidence)

**A creator-signed invite carrying a fresh `invite_id` for an existing (group, invitee) has no explicit state guard and no test. Its refusal depends only on an SQLite UNIQUE constraint that schema validation does not check.**

Evidence (actual source):

1. `ReceiveInvite` (`internal/groupchat/store.go:378-455`). For an existing group it compares name/creator/roster/hash (`:420-422`) and refuses only `left` and `cancelled` (`:423-425`). An `active`, `activating` or `pending` group falls through.
2. The `invite_id` lookup (`:428-440`) misses for a fresh ID. The code then calls `insertInvite` (`:444`) and rewrites `groupchat_members.invite_id` for the invitee (`:447-450`).
3. The only thing that stops this is `UNIQUE (local_npub, group_id, invitee_npub)` on `groupchat_invites` (`store.go:179`). That yields a raw `store group invitation: ... UNIQUE constraint failed` error from `insertInvite` (`:1057-1062`), not `ErrInvalidTransition` or `ErrProtocolMismatch`. The deferred `tx.Rollback()` (`:392`) means no committed change today (inference from code; not executed).
4. `validateGroupchatSchema` (`schema_health.go:23-45`, introspection via `PRAGMA table_info` only, `:80`) checks columns and primary keys, never UNIQUE constraints. `CREATE TABLE IF NOT EXISTS` no-ops on a pre-existing table. A store whose `groupchat_invites` lacks that UNIQUE would pass startup and would then **commit** a second pending invite and repoint the member's `invite_id` on an already active group (inference from code).
5. No test exercises this path. `rg ReceiveInvite internal/groupchat/*_test.go` shows only the original invite, its exact replay (`store_test.go:537`), a wrong signer (`:257`), and a second device (`:607`).

Threat bound (inference): only the group creator's key can produce this envelope, because sender==creator and recipient==invitee are enforced at `:383-385`. A cross-recipient replay is impossible: the event is NIP-44 to one recipient, and the signed p-tag is verified (`verified_test.go:47-56`). So this is a creator-authority or duplicate-creator-device robustness and no-state-change gap, not a third-party forgery.

## Smallest constrained test proposal (test-only, not implemented here)

New `internal/groupchat/store_reinvite_negative_test.go`, reusing the existing `newThreeMemberGroup`, `activate`, `buildIncoming` and `mustOpaque` helpers:

```go
func TestReceiveInviteFreshInviteIDForExistingInviteeRefusedWithoutStateChange(t *testing.T) {
	for _, active := range []bool{false, true} {
		g := newThreeMemberGroup(t)
		if active {
			g.activate()
		}
		before := dumpRows(t, g.bob.db) // groupchat_groups, groupchat_invites, groupchat_members for bob, ordered
		reinvite := g.invites[g.bob.npub]
		reinvite.InviteID = mustOpaque(t) // same group, creator, roster, hash; fresh ID
		created, err := g.bob.store.ReceiveInvite(buildIncoming(t, g.alice, g.bob, reinvite))
		require.Error(t, err)
		require.False(t, created)
		require.Equal(t, before, dumpRows(t, g.bob.db), "refused re-invite must not change group, invite, or member rows")
	}
}
```

- Expected at base: PASS (inference: the UNIQUE constraint plus rollback). This pins the current fail-closed and no-state-change behavior so that a regression in the schema or the guard is caught.
- Optional stricter assertion `require.ErrorIs(t, err, ErrInvalidTransition)`: expected FAIL at base (inference: raw SQL error). Making it pass needs a production guard in `ReceiveInvite` and/or a UNIQUE check in `schema_health.go`. That is implementation and requires new scoped authority.
- `dumpRows` is a small test-local helper (`SELECT * ... ORDER BY` on the three tables, scoped to `local_npub`).

## Secondary observation (lower value, no change recommended now)

The cross-group replay e2e (`wrong-group-*`) uses a never-created random group ID, so it exercises `ErrGroupNotFound`. It does not exercise "member of group G1 sends into a real G2 they are not in". The code path is keyed by `group_id` (`messages.go:168-173`, `activeRosterTx` `:232-245`) and is the same path as the stranger test (`messages_test.go:23-28`). An extra test would likely pass at base and adds little. Mentioned only for completeness.

## Not done / next

- No tests were run (out of scope by instruction). All "expected PASS/FAIL" statements are inference from source.
- Any implementation, whether the test file, a typed-error guard, or a schema UNIQUE check, needs new scoped authority. The coordinator verifies delivery independently.

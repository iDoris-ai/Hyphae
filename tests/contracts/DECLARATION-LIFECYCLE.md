# Declaration lifecycle reference check

`declaration_lifecycle_test.go` is a test-only selector for normalized declaration records. Its structural, action, and identity inputs assume successful signature and payload-schema validation. The selector independently checks timestamp and lifecycle semantics; negative and unsafe timestamp fixtures are defensive checks for corrupt inputs and do not claim those values pass schema validation. Fixture authors such as `alice` are synthetic labels, not wire public keys. Fixture IDs are test data, not signature evidence.

The shared JSON fixture captures these rules:

- Timestamps are nonnegative safe integers. A declaration is eligible only when `0 < expires_at - issued_at <= 86400` and `issued_at <= now + 300`.
- A record more than 300 seconds in the future is ignored for that selection pass and reported as rejected. Exactly 300 seconds is eligible. The paired future-clock fixtures reconsider the same event at a later `now`; callers must provide that record again. This reducer stores no future records.
- For each author, choose the eligible record with greatest `issued_at`; a same-second tie chooses the lexicographically smallest lowercase event ID. The result is independent of record order.
- Only after selection, `upsert` is active when `now < expires_at`; `withdraw` and expired upserts are inactive. Selection does not fall back to an older active declaration, including when the latest-known record is in `prior_known`.
- Callers pass explicit `prior_known` records together with newly observed records. This reducer never deletes expired or withdrawn records; a later upsert can restore active status. It only describes the supplied records and cannot establish global completeness from either partial input or relay EOSE.
- Identical records with the same event ID are deduplicated. A conflicting record with the same event ID returns `conflict` without a partial selection result; callers must retain their previous state.

Run with `go test ./tests/contracts -count=1`. Each fixture has a unique ID and is run as a named subtest; no fixture is skipped.

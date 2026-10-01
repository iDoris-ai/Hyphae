# Execution recovery candidate fixtures

This is a test-only reference contract for the T01-C candidate. It does not define wire fields, implement an execution entry point, persist a request ledger, or run work. Fixture observations are assumed to have passed the applicable structural, signature, and identity checks unless a case explicitly tests safe-integer validation. `register_run` and `start_existing_run` are permitted-action labels in the oracle; they are not observed run or side-effect counts.

The JSON fixture is language-neutral and carries independent expected actions, state outcomes, and reasons per case ID. Its manifest records the fixed source revision and SHA-256 of the two source documents. The source hashes cover each document's raw bytes at base commit `a4aa606eb81d5c040d94c51cdf94553e646d8674`:

- `docs/agent/t01-authorization-recovery-candidate.md`: `9ac0b52db70ad5e0e156ac8276e8aa7e13025e34cf3349ba6af49f21b9aa47b3`
- `docs/agent/t01-contract-gates.md`: `f739451f4821d321f465f565b69488c31d5468bb6c6dce3c28f8a28643de227a`
- `testdata/execution-recovery-fixtures.json` raw-byte SHA-256: `cb62fa0a84b64e7dc30b696415bc7645050794f664e77b2f5d53dda0a0714b55`

There are 79 unique cases, each executed as a named Go subtest with no skips. Coverage groups are:

- Clock/schema: `clock_*` (positive TTL, exact 86400/86401 seconds, future 300/301 seconds, strict expiry comparison, safe-integer gate).
- Run preflight: `preflight_*` (persisted approval, matching scope/digest, budget, local model availability, privacy, module permission, expiry).
- Duplicate delivery: `replay_*` (same-content serial/concurrent reuse, changed immutable content conflict, distinct request key).
- Candidate state graph: `transition_legal_*` covers all 20 edges in the source diagram. `transition_*` negative cases cover missing approval, scope mismatch, budget failure, uncertain start, timeout without explicit failure, unknown run-not-found, illegal terminal edges, and unknown status strings. The test extracts the Mermaid transition diagram from the pinned candidate document and checks that every legal edge has exactly one `transition_legal_*` case. The full state set remains `received`, `approval_pending`, `ready`, `run_registered`, `running`, `succeeded`, `failed`, `rejected`, `expired`, and `unknown`.
- Recovery: `recovery_*` covers known pre-call recovery, expiry before start, uncertain calls, same-run reconciliation, not-found remaining unknown, failed result transaction with no completion receipt, and receipt retry after request expiry.

The fixed oracle accepts only lexical JSON integers representable as safe integers for the clock fields; TTL must be positive and at most 86400 seconds, `issued_at` may be at most 300 seconds ahead of `now`, and starting requires `now < expires_at`. A persisted approval and matching scope/digest plus the remaining start preconditions are required before registering. A known-not-sent start may expire only once the request has expired. Recovery can start only through the existing run ID after repeating the approval, scope/digest, expiry, budget, privacy, and module checks; an uncertain call requires checking the same run ID. Executor timeouts do not establish failure. Not finding a run does not establish that no effect occurred. A same-run terminal observation becomes a state transition only after its result or error is reliably saved locally. A failed local result transaction leaves the prior state in place, requires another same-run query, and leaves the completion receipt unsent. A saved completed result may be delivered after request expiry.

The fixtures intentionally avoid freezing B/D wire field names and full receipt schemas. Candidate statements that require a future executor implementation or external evidence remain assertions about allowed recovery actions, not claims about actual side effects. This test does not establish cross-language agreement, relay behavior, or a four-repository acceptance result.

Run the focused check with:

```sh
go test ./tests/contracts -run 'ExecutionRecovery' -count=1
go test -race ./tests/contracts -run 'ExecutionRecovery' -count=1
```

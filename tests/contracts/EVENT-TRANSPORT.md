# T01-A event transport candidate fixtures

This test-only candidate follows [T01-A at PR #88, commit 901a840](https://github.com/iDoris-ai/Hyphae/blob/901a840/docs/agent/t01-transport-candidate.md) and the [non-execution envelope fields at PR #92, commit 94ad0e0](https://github.com/iDoris-ai/Hyphae/blob/94ad0e0/docs/agent/t01-envelope-candidate.md). It does not copy or enable production event handling. The candidate remains unfrozen; T07 and all execution paths remain closed.

## Fixture and validation contract

`event-transport-fixtures.json` uses profile `hyphae-event-transport-fixtures/1` and has 64 unique cases. Each case carries `local_pubkey`, one of `event_json`, `event_json_base64`, or `event_json_recipe`, and one expected stage. Recipes concatenate `prefix`, `fill` repeated `repeat` times, and `suffix` exactly as UTF-8 bytes; they do not trim or normalize. Optional `expected_bytes` counts the expanded event JSON only. The Base64 form exists to preserve a raw invalid-UTF-8 event that cannot be represented as a JSON string.

The reference validator follows this order:

1. Reject event JSON above 65,536 UTF-8 bytes.
2. Reject invalid raw UTF-8, malformed/trailing JSON, and duplicate object keys. The duplicate-key scan compares decoded keys and catches duplicates before Go's map decoder can overwrite them.
3. Require exactly the non-null event fields `id`, `pubkey`, `created_at`, `kind`, `tags`, `content`, and `sig`; reject unknown fields. `id` and `pubkey` must be 64 lowercase hex characters, `sig` 128 lowercase hex characters, and `created_at` a nonnegative safe integer. Tags are at most 8 arrays, each with 2 or 3 actual JSON strings no longer than 256 UTF-8 bytes; `null` is not treated as an empty string. The multibyte fixtures exercise 256 and 257 byte tag values.
4. Classify old/unknown kind, namespace, behavior, or encoding as `legacy_or_unsupported`. This never auto-converts an event into an execution request.
5. Apply the closed route candidate: kind `8787`, `c=hyphae-behavior/1`, and only `b=register|publish|inquire`, `x=json|nip44`, and optional `p`/`e`. Critical route keys must be unique even when their values repeat. This fixture suite narrows each accepted `c`, `b`, `x`, `p`, or `e` tag to exactly two strings; although the generic tag shape allows up to three, the third route-tag value has no defined meaning here and is rejected. This is a candidate rule for this suite, not a claim that PR #88 froze it.
6. Public `register`/`publish` requires `x=json` and forbids `p` and `e`; its content is limited to 32,768 UTF-8 bytes. Fixed, pre-signed multibyte-body fixtures exercise exactly 32,768 and 32,769 bytes. Private `inquire` requires `x=nip44` and exactly one `p`, permits at most one `e`, and requires `p` to equal the fixture's local public key. Its content is limited to 45,056 ASCII bytes, must be canonical padded standard Base64, decode to at least 99 bytes, and start with version byte `2`.
7. Check the declared event ID and signature with the pinned `fiatjaf.com/nostr` SDK (`v0.0.0-20260928115942-58e4c715304e`). The ID check is explicit because the SDK signature verifier recomputes the ID. Size and route failures occur before signature work.

The private Base64 boundary checks only encoding, decoded length, and version prefix. Fixtures use a structurally sized version-2 byte string with no valid MAC; passing it as `outer_valid` demonstrates that this layer does not decrypt or authenticate NIP-44 content. Public `json` content is checked for UTF-8 and size only. Body JSON/schema validity, encrypted body semantics, `e`/body consistency, response type, and request correlation belong to a later layer.

## Test identities and interpretation

The fixed signed public events were created once with disclosed test-only Nostr secret scalars 1 and 3; scalar 2 derives the fixture recipient identity. These are public test identities, never real user keys. Fixtures store their signed event JSON and public local identity, not a private key. Tests verify fixed IDs and signatures with the pinned SDK; they do not generate signatures while running tests. A valid event from the alternate public test identity is `outer_valid`: there is no trusted expected-sender field at this layer. Changing the signed `pubkey` produces `signature_invalid`. Sender authorization and response-author correlation require trusted request context and are not claimed here.

Expected stages are `outer_valid`, `raw_limit`, `json_invalid`, `event_invalid`, `legacy_or_unsupported`, `route_invalid`, `content_limit`, `content_invalid`, and `signature_invalid`. The test checks every unique fixture, including recipe byte counts, without skipping cases.

## Boundaries

These fixtures verify only a reference candidate. They do not prove that a production receive path enforces it, nor do they validate a body schema, relay persistence, author authorization, response association, decryption/MAC, current time, model/module policy, permission grant, or run registration. The strict JSON lexical policy from #91 remains a separate consumer/production prerequisite; this minimal duplicate-key and raw UTF-8 guard is not a full replacement. Only non-execution `register`, `publish`, and `inquire` candidates are described. The candidate does not enable `execute`, `receipt`, a model, a module, a permission, or a run.

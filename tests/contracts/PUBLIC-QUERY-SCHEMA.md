# Public query schema candidate

This is an executable schema and fixture candidate for the four public body types in T01-B: declaration, notification, query, and response. Its fixed source is [the T01 public query candidate at bdeff57ab0a3e6612f268498633656239701b02b](https://github.com/iDoris-ai/Hyphae/blob/bdeff57ab0a3e6612f268498633656239701b02b/docs/agent/t01-public-query-candidate.md), with the [envelope shape at 94ad0e053bdc79e691d2408768d371bc19ec59f7](https://github.com/iDoris-ai/Hyphae/blob/94ad0e053bdc79e691d2408768d371bc19ec59f7/docs/agent/t01-envelope-candidate.md). Response profile data is the profile object itself; only declaration upsert uses `{ "action": "upsert", "profile": ... }`.

The schema uses Draft 2020-12 and local `$defs` references. Its `$id` uses the reserved `.invalid` host. The test compiler denies external resource loading, keeping schema resolution offline.

The Go reference test uses `github.com/santhosh-tekuri/jsonschema/v6` v6.0.3 as a test-only dependency. It registers and asserts four custom formats: `hyphae-utf8-1-128`, `hyphae-utf8-1-1024`, `hyphae-utf8-1-4096`, and `hyphae-utf8-1-256`. They count UTF-8 bytes, not Unicode characters. Every consumer must implement these formats with the same byte semantics; `maxLength` alone is not equivalent. Regexes use a Go/ECMA-compatible subset and explicitly exclude characters outside each field's allowed alphabet, including CR/LF.

The body limit is 32,768 raw bytes and is checked before JSON/schema parsing. Query context uses the same size-first check before parsing and schema validation. The reference helper then validates Draft 2020-12 and checks lifecycle intervals, duplicate `(id, version)` capability pairs, response scope and exact capability id/version, and matching `request_id`. Response fixtures supply the original query as trusted test context except for cases that explicitly verify missing, oversized, or invalid context rejection. Context is test metadata, not authorization evidence.

The shared fixture file is versioned as `hyphae-public-query-fixtures/1`. The 70 cases each have a unique id, one raw body or deterministic repeat recipe, and an expected validation stage. Recipe expansion concatenates UTF-8 strings exactly as written; `repeat` repeats the whole `fill` string. Optional `expected_bytes` checks expanded body byte counts for size boundaries.

## Scope and limits

These tests specify a candidate body schema. They do not show that a production input path enforces the size limit, validates signatures, binds an event to its request, checks current time, persists state, or implements authorization. The strict lexical precheck developed separately for #91 remains a consumer/production prerequisite; this reference test does not replace it. It also does not establish Go/TypeScript equivalence or a canonicalization/digest rule. The schema and fixtures are not frozen wire protocol and do not enable production acceptance or execution messages.

An Agent24 consumer should load this exact fixture version, reconstruct recipes byte-for-byte, run the same schema branches and custom UTF-8 byte formats, apply the same body/context-limit-first and payload-semantic checks, and assert every fixture id's expected stage. The consumer should report unsupported cases explicitly rather than silently skipping them.

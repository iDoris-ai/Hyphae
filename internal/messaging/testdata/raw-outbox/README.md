# F2a raw outbox source fixtures

These are captured source-path bytes, not a new `Outbox` marshal. They support the read-only **legacy-schema core** of F2a; they do not establish full F2/V6 acceptance.

Source integration commit: `b4c425c9aaef7f5b10fc757748e4279847bf5c70`, tree `27633bb2d7979dd171f8fcc02c94c7dc8903f58d`. Its exact parents are approved #165 `c0a54111f78f357c6ae2772f06f9a3cc4fe7c63b` and #166 `7861d029b5ccc2959b80a184a390ec132ddd025f`; their merge-base is `699b36f74c6b3802b93768d670229d7b2d3b6cff`.

`capture_test.go.txt` was copied to `internal/groupchat/f2a_capture_test.go` in a temporary `git archive` extraction of that integration commit. The archive contained no F2a production code. Its existing `newMember`, `testAgentEvent`, `insertTestFanout`, and `mustOpaque` test helpers created temporary SQLite stores, random ephemeral signing identities, genuine NIP-44 ciphertext, source-compressed agent events, and valid Schnorr signatures. No private keys, keystores, production stores, or relay connections were used or retained. The prepared row is seeded using the exact #165 helper because that tree has no S4 prepare API; this is an O1/T2 source fixture, not proof of S4.

- `legacy-165-group-nil-relays.json`: `Store.EnqueuePrepared` → `queueTargets` → `EnqueueGroupOutboxEntry(eventJSON, recipient, nil, maxRetries)` → `UpdateOutbox` → `writeOutbox`. Direct `os.ReadFile` capture after T2; native QueueID and `route:group`, `relays:null`, `group_pending`, zero retry/last_attempt preserved. The accompanying `.row.json` is a query snapshot of the actual queued SQLite row, and `.event.json` contains the originally signed bytes supplied to enqueue.
- `legacy-dm-nil-relays.json`: `AddToOutbox` → `enqueueOutboxEntry(..., nil)` → `UpdateOutbox` → `writeOutbox`. Direct file capture preserves omitted route, native QueueID, null relays, pending state, and the original event. No group row is created for this DM event.

`SOURCE_HASHES.json` records the exact #165 files used and confirms they are identical in the integration tree. For `outbox.go`, which #166 changes elsewhere, the `writeOutbox` source segment through the following `AddToOutbox` declaration and `enqueueOutboxEntry` segment through the following `newOutboxQueueID` declaration were compared byte-for-byte. Both segments match #165. `SHA256SUMS` freezes the five captured artifact files. The generator is intentionally not a normal test: regenerating changes random identities, QueueIDs, encryption nonces and timestamps, so existing fixture bytes must not be silently replaced.

Capture command in that archive:

```sh
# HOME is a fresh temporary directory; every capture subtest also uses t.TempDir.
# F2A_FIXTURE_DIR is the absolute destination for these files.
GOPATH=/Users/jason/go GOCACHE=/Users/jason/Library/Caches/go-build \
  go test ./internal/groupchat -run '^TestCaptureLegacyNilRelays$' -count=1 -v
```

The command ran under the shared `/tmp/hyphae-em1-go-tests.lock` flock and passed both group and DM subtests. The group/DM capture uses source cryptography and serialization; the fixture consumer only parses captured raw bytes. Derived malformed inputs are explicitly test mutations, never represented as source captures.

## Implemented boundary and proposed follow-up

`ParseRawOutboxEvidence` is an inert byte-buffer API. It validates the legacy root, entry and signed-event schemas, required/null/type/range distinctions, route/status pairs, below-budget group failures, recipient binding, event ID/signature, and whole-file identity collisions. It preserves the complete document, raw entry, every field value including surrounding whitespace, and decoded event string bytes. It does not read files, acquire locks, normalize, assign identities, publish, or write. Any error invalidates the entire snapshot; partial entries remain review evidence only.

F2a is **partial relative to the complete rev7 raw schema**: `fanout_protocol`, `failure_result` and `recovery_witness` are unsupported fields, including otherwise valid forms. They fail closed in this isolated API and are not connected to production reads or writes. F2b must add accepted versioned nested schemas, conditional witness members, token/key/revision/generation ranges and cross-field constraints, while retaining exact nested fragments. This explicit split keeps F2a at 292 added production Go lines.

Remaining F2: F2b schema support; stable-sibling-lock snapshot reader; raw-preserving conditional writer covering all callers; protocol adoption; default `[]` for newly created entries; O3 atomic failure receipt and replay evidence; pending selection integration; protection of held entries and unconsumed/consumed result/witness fragments through clear/cleanup. Remaining V6 includes prepared O1-before-T2 capture, adoption and budget projection, DM default-relay execution, O3/T4 and D6/T2' recovery, and exact-fragment assertions across authorized writes.

Remaining F3: cross-store lock order and barriers; Reserve/Start/CommitFailure/CommitAccepted and exact cleanup coordination; late-result fencing, uncertain commit handling, recovery overlay; full writer inventory and managed-deployment/launcher/version gate with real process/crash evidence. F4 durable recovery witnesses, D6/T2' replay, S7 reconciliation and all new send/retry wiring remain outside this slice. Approved #165/#166 typed writers are not credited with raw-validation or lossless-writing capability.

# Relay worker: follow-ups from the 3-round adversarial review (2026-10-28)

Scope: the PR stack `relay-w1…w8` for `deploy/relay-worker`. The review found 1 High, 32 Medium and 73 Low items.
The High item and the Medium items about limits, tag sizes, deletion bookkeeping, live/stored matching and NIP-11 were fixed in this stack.
This list holds what was **not** fixed: every remaining Medium (also summarised in the README "Known limits") and the Low items that need more than a one-line change.

## Medium, deferred

| slice | file:line | severity | note |
| --- | --- | --- | --- |
| relay-w2-limits → relay-w3-store | `src/store.ts:43` | Medium | Per-event row-write amplification up to about 200 rows (64 tag inserts, each with 2 index rows) while the abuse budgets count 1 + tags/16 |
| relay-w2-limits → relay-w3-store | `src/db.ts:58` | Medium | storageFull depends on databaseSize, which never shrinks after deletes, so the 'full' state can become sticky |
| relay-w2-limits → relay-w3-store | `src/store.ts:22` | Medium | storeEvent can throw (SQL error, DO storage exception) and the only handling is a rethrow, with the per-event cost left to callers |
| relay-w8-entry-e2e-readme | `src/index.ts:58` | Medium | Every Upgrade request is forwarded to the single Durable Object before any rate limit, and refusals in the DO (503 at connection cap, 429 for per-source concurrency) consume no token, so the... |
| relay-w8-entry-e2e-readme | `src/relay.ts:79` | Medium | Connection-count and per-source concurrency are derived by scanning `ctx.getWebSockets()` (including sockets mid-close or with a null attachment) rather than from tracked state; a socket who... |
| relay-w2-limits | `src/limits.ts:164` | Medium | The global outbound budget (64MB) can be drained by 4 sources, so fan-out stops for everyone. The ordering also lets a source that is throttled burn nothing but still end the fan-out for oth... |
| relay-w2-limits | `src/limits.ts:111` | Medium | The per-source LRU can be flushed by anyone with many source addresses. Eviction resets a throttled abuser's buckets to full. |
| relay-w2-limits | `src/limits.ts:102` | Medium | Budgets is process-memory only and the Durable Object hibernates, so every limit tied to a source or to global state resets on eviction. The over-quota episode tracking (sockOut) is lost wit... |
| w4-deletion | `src/store.ts:30` | Medium | A deletion request truncated by the budget is stored as an event, so resending the same event returns 'duplicate' and the remaining targets are never processed. |
| relay-w4-deletion → relay-w5-query-subs | `src/subs.ts:134` | Medium | normalizeFilter silently drops unknown fields, which widens the filter instead of rejecting or narrowing it. |
| relay-w4-deletion → relay-w5-query-subs | `src/query.ts:64` | Medium | Multi-value kinds/authors/ids filters combined with ORDER BY created_at DESC can force a scan and sort of every matching row, so the row-read cost is not bounded by LIMIT. |
| relay-w4-deletion → relay-w5-query-subs | `src/subs.ts:188` | Medium | When a connection's subs do not fit in the 4MB cache, every broadcast re-reads and JSON.parses that connection's rows from SQL, once per event per socket. |
| relay-w6-handlers → relay-w7-durable-object | `src/relay.ts:109` | Medium | The global connection cap can be filled by a small number of sources, and idle connections are never reclaimed. This locks out every other client. |
| relay-w6-handlers → relay-w7-durable-object | `src/relay.ts:113` | Medium | Every upgrade deserializes the attachment of every open socket (up to 2000) to count connections per source. There is no per-source limit on frames. |
| relay-w5-query-subs → relay-w6-handlers | `src/fanout.ts:19` | Medium | Fan-out CPU is O(all subscriptions x filters) per event, and not bounded by maxDeliveriesPerEvent, which only counts successful deliveries. maxSubRows is 20k subs x up to 10 filters each, so... |
| relay-w5-query-subs → relay-w6-handlers | `src/fanout.ts:19` | Medium | SubStore.get does not cache a connection when the 4 MB cache is full, so every broadcast re-runs a SQL SELECT and JSON.parse for each uncached socket (rows-read billing amplification). |
| relay-w5-query-subs → relay-w6-handlers | `src/fanout.ts:23` | Medium | The global outbound budget is shared with no per-recipient fairness, and ephemeral events cost the sender no stored-bytes budget (charge 0). A few colluding sources can drain it. |

## Low, not fixed

| slice | file:line | severity | note |
| --- | --- | --- | --- |
| relay-w2-limits → relay-w3-store | `test/db.test.ts:62` | Low | Replacement tests do not pin the post-state of the replace-when-full path |
| relay-w2-limits → relay-w3-store | `src/db.ts:39` | Low | Indexed tag values are unbounded in length, apart from the event size cap |
| relay-w2-limits → relay-w3-store | `src/store.ts:28` | Low | Replacement path charges only the net footprint, so a 'full' relay can be churned for free by one author |
| relay-w2-limits → relay-w3-store | `src/store.ts:32` | Low | Stale check and the old-row lookup rely on the events_coord index covering (pubkey, kind, d), but the DDL runs every start and the index was added with no migration or backfill of the `d` co... |
| relay-w2-limits → relay-w3-store | `test/helpers/sqlite.ts:10` | Low | Test helper diverges from the real SqlStorage in ways that can hide bugs |
| relay-w1-scaffold-validation | `src/filter.ts:17` | Low | Unknown filter keys (e.g. 'search', 'foo') and non-single-letter '#' keys are accepted without any size or type check and are ignored. |
| relay-w1-scaffold-validation | `src/filter.ts:21` | Low | since/until/limit and kinds only check Number.isInteger: negative or 1e300 values and kinds outside 0..65535 are accepted. |
| relay-w1-scaffold-validation | `src/nostr.ts:41` | Low | validateEvent does not strip or reject unknown top-level fields; the event passes id/sig verification and later slices store/broadcast JSON.stringify(e) including the attacker's extra fields... |
| relay-w1-scaffold-validation | `src/nostr.ts:46` | Low | The future-timestamp check runs before id/sig verification (good) but the cost-ordered checks leave a full JSON.stringify+sha256 for any well-formed event before the signature is rejected; n... |
| relay-w1-scaffold-validation | `src/filter.ts:27` | Low | maxFilterBindings = 90 leaves only 10 spare bindings for since/until/limit/#tag-names and relies on the per-key accounting; filters with several single-letter tag keys each add +1 and are co... |
| relay-w1-scaffold-validation | `src/nostr.ts:58` | Low | isReplaceable() treats kinds 0 and 3 plus [10000,20000) as replaceable but kind 41 (channel metadata) and 2 (deprecated) are not; and validateEvent allows kind values up to 65535 though the ... |
| relay-w1-scaffold-validation | `src/nostr.ts:60` | Low | dTag returns '' for a missing d value (['d']) and for empty-string ['d',''], so addressable events with no value and with value '' share one coordinate; also first 'd' tag with a single elem... |
| relay-w1-scaffold-validation | `src/nostr.ts:32` | Low | validateEvent default `now` is computed per call with Date.now(); callers in Workers see a frozen clock within a request but across hibernation wake-ups each call is fine. However an event a... |
| relay-w8-entry-e2e-readme | `test/e2e/run.mjs:309` | Low | Deletion-limit test does not pin the stated behaviour: the comment says 'first 100 are tombstoned, the last 5 are not' but no assertion checks it. |
| relay-w8-entry-e2e-readme | `test/e2e/lib.mjs:16` | Low | freshIp() starts from a random counter on each run against a possibly long-lived relay; per-source buckets are per-minute state, so back-to-back runs can collide on 10.x.y.1 addresses. |
| relay-w8-entry-e2e-readme | `test/e2e/run.mjs:16` | Low | The cap/fanout/seed modes require manual relay restarts and are not part of `pnpm test`; `e2e` script (package.json:11) runs only the default mode, so most limit behaviour in the README is n... |
| relay-w8-entry-e2e-readme | `src/index.ts:54` | Low | Client address trust depends on an env override and a fallback: `SRC_HEADER` (if ever set in production) lets the client choose its own bucket key, and a missing CF-Connecting-IP collapses a... |
| relay-w8-entry-e2e-readme | `wrangler.toml:4` | Low | `workers_dev = true` publishes the relay on the *.workers.dev hostname by default, and no routes/observability/limits config is defined, so the deploy exposes an unprotected public endpoint ... |
| relay-w8-entry-e2e-readme | `README.md:24` | Low | Fill-time estimate in the caveats is internally unsound: it counts only stored event bytes against the 5 GB cap, while the per-source 8 MB/min quota is per /64 or IPv4 and IPv6 sources are f... |
| relay-w8-entry-e2e-readme | `src/index.ts:60` | Low | Non-WebSocket paths: all other requests (any method, any path) return 200 text and NIP-11 responses lack `Vary: Accept`. |
| relay-w8-entry-e2e-readme | `wrangler.toml:1` | Low | Deployment config has no schema/rollback story and relies on a compatibility_date that the README admits the installed wrangler falls back from. |
| relay-w8-entry-e2e-readme | `test/e2e/lib.mjs:59` | Low | E2E client treats any open failure as 'refused' and `wait` returns null on timeout, so limit tests cannot distinguish a 429/503 refusal from a network error or a hung relay. |
| relay-w2-limits | `src/limits.ts:16` | Low | After a backwards clock jump, an accepted take sets last = now, which is earlier than the previous last. The next call then refills for the whole interval a second time. |
| relay-w2-limits | `src/limits.ts:78` | Low | countViolation uses a fixed window anchored at the first violation, not a sliding window, and tests `!st.badAt` as a flag. The comment on ConnState says "rejected frames in the current windo... |
| relay-w2-limits | `src/limits.ts:27` | Low | sourceKey returns the raw, client-influenced header text (up to 64 chars) for anything unparsable. A missing header gives the shared key "unknown". |
| relay-w2-limits | `src/limits.ts:171` | Low | sockOut gets an entry for every connection that reaches chargeDelivery's socket step, and only forget() removes it. Entries for connections that disappear through a path that skips relay.ts:... |
| relay-w2-limits | `test/limits.test.ts:147` | Low | Several behaviors are not pinned by tests. |
| w4-deletion | `src/deletion.ts:104` | Low | The 'a' path caps victims at LIMIT 10 and sums per-victim write estimates, but a replaceable or addressable coordinate has at most one stored version. |
| w4-deletion | `src/deletion.ts:107` | Low | A cutoff that is updated in place (ON CONFLICT DO UPDATE) keeps its old rowid, so eviction is by creation order, not recency. |
| w4-deletion | `src/deletion.ts:76` | Low | Tombstones are written for e-tags whose target is absent, even when it is another author's still-unseen event. |
| w4-deletion | `src/store.ts:47` | Low | For a full database, the size check for kind 5 happens only after applyDeletion has done all its writes. |
| w4-deletion | `src/deletion.ts:65` | Low | In applyDeletion, a mutation sequence can fail midway (a SQL error between deleteEvent and the tombstone insert), and the only protection is the caller's transaction. |
| relay-w4-deletion → relay-w5-query-subs | `src/query.ts:91` | Low | The truncation hint can fire for a row that is only a duplicate of an event already returned. |
| relay-w4-deletion → relay-w5-query-subs | `src/query.ts:81` | Low | With several filters, results come back grouped per filter and not merged by created_at. An earlier filter can use the whole byte or count budget, so a later filter returns nothing. |
| relay-w4-deletion → relay-w5-query-subs | `src/subs.ts:245` | Low | SELECT count(*) FROM subs runs for every new subscription and reads up to maxSubRows (20,000) rows. Other relay-wide counters use the meta table to avoid this. |
| relay-w4-deletion → relay-w5-query-subs | `src/config.ts:41` | Low | maxResultBytes (4MB) is charged only on the stored event JSON; the response to a REQ is not counted against any outbound budget in this slice, so one REQ can emit 4MB synchronously. |
| relay-w4-deletion → relay-w5-query-subs | `src/subs.ts:195` | Low | A corrupt or non-array filters row is skipped on a parse error, but JSON.parse results that are valid and wrongly shaped (for example an object) are cached and later throw in matchAny. |
| relay-w4-deletion → relay-w5-query-subs | `src/subs.ts:182` | Low | Orphaned subscription rows count against the relay-wide maxSubRows until the next constructor run (cleanup exists only in the Relay constructor). |
| relay-w4-deletion → relay-w5-query-subs | `src/query.ts:98` | Low | utf8Length(json) allocates a full encoded copy of every row's JSON just to count bytes, up to 501 rows per filter. |
| relay-w6-handlers → relay-w7-durable-object | `src/relay.ts:148` | Low | Binary frames, oversized frames, invalid JSON, non-array frames and unknown message types are never counted as violations. Only frame-bucket exhaustion and handler rejections are. |
| relay-w6-handlers → relay-w7-durable-object | `test/config.test.ts:1` | Low | The Relay class (frame dispatch, size gate, upgrade limits, close-code mapping, stale-subscription cleanup, closed-flag handling) has no test in this slice. Only loadLimits is tested. |
| relay-w6-handlers → relay-w7-durable-object | `src/index.ts:19` | Low | (w8 usage) The NIP-11 document advertises the static LIMITS, but the Durable Object enforces loadLimits(env.LIMITS_JSON). |
| relay-w6-handlers → relay-w7-durable-object | `src/relay.ts:152` | Low | The message size is only checked after the runtime has delivered the whole frame (up to the platform maximum), and oversized frames charge no EVENT or violation budget. |
| relay-w6-handlers → relay-w7-durable-object | `src/relay.ts:87` | Low | The cleanup on every constructor run is O(rows) and issues one DELETE per stale connection. |
| relay-w6-handlers → relay-w7-durable-object | `src/relay.ts:122` | Low | If serializeAttachment throws after acceptWebSocket, the server socket stays accepted but has no attachment. The fetch catch returns 503. |
| relay-w6-handlers → relay-w7-durable-object | `src/relay.ts:176` | Low | A SQL failure in dropConn propagates out of webSocketClose and webSocketError (try/finally without a catch) as an unhandled exception. |
| relay-w6-handlers → relay-w7-durable-object | `src/relay.ts:50` | Low | Budgets (per-source upgrade, write and outbound buckets, the global buckets and the per-socket outbound state) are in-memory and reset to full whenever the object is evicted or redeployed. O... |
| relay-w6-handlers → relay-w7-durable-object | `src/relay.ts:131` | Low | The webSocketMessage catch sends a NOTICE, but a failure after partial work in a handler has no connection-level safeguard. |
| relay-w5-query-subs → relay-w6-handlers | `src/req.ts:31` | Low | A REQ that is invalid or rate-limited deletes the client's existing subscription with that id. When 'rq' tokens run out, a live subscription is torn down because of a later, unrelated REQ. |
| relay-w5-query-subs → relay-w6-handlers | `src/hub.ts:29` | Low | peekEventId returns an id from a regex over the first 4.2 KB or last 400 bytes of an unparsed, unvalidated oversized frame. The Hub interface file also holds an unrelated helper used only by... |
| relay-w5-query-subs → relay-w6-handlers | `src/events.ts:33` | Low | Invalid events (bad id, bad sig, bad tags) never call hub.violation, so a connection can send 120 bad-signature events a minute (240/min per source) indefinitely and make the DO do schnorr v... |
| relay-w5-query-subs → relay-w6-handlers | `src/limits.ts:116` | Low | Per-source state is a bounded LRU (maxSourceEntries 10k). Rotating many /64 or IPv4 sources evicts the state of other sources and resets their buckets, including an attacker's own. |
| relay-w5-query-subs → relay-w6-handlers | `src/events.ts:57` | Low | Partial state on the error path. broadcast() runs after the event is committed, and the outer catch replies 'error: internal error' (OK false) if anything in the post-store path throws. The ... |
| relay-w5-query-subs → relay-w6-handlers | `src/fanout.ts:16` | Low | Fan-out iterates ctx.getWebSockets() in a fixed order and deserializes every attachment for every event. When maxDeliveriesPerEvent or the global budget stops delivery, the same trailing soc... |
| relay-w5-query-subs → relay-w6-handlers | `src/hub.ts:10` | Low | Hub.take is documented as persisting the attachment. Every successful take, up to three per frame (fr, ev or rq, plus another), triggers a serializeAttachment, so each EVENT frame writes the... |
| relay-w5-query-subs → relay-w6-handlers | `src/req.ts:45` | Low | EOSE hint format ['more'] (array in third position) is asserted by the test as the NIP-67 shape, but the test only pins the shape this code chose, with no source cited. If clients expect a s... |

Low items that were fixed in passing: orphan or wrong comments (db.ts, config.ts, relay.ts), a dead `FullError` in the storage slice,
inherited keys in `loadLimits`, the stale README pointers, the `indexedTags` global limit, and several missing tests (cache invalidation, `<=` cutoff boundary, indexed-tag cap, duplicate replay, partial deletion reply).

## Round 4 review (Codex, on the fixes above)

Fixed in the stack: counters reconciled once under a version marker and never allowed to abort startup;
REQ history charged to the outbound budgets (truncated with `more`); frame validation charged to the
source's own weight before hashing; `loadLimits` accepts only bounded safe integers; `indexedTags` with a
cap of 0; `a` tags may carry a long `d`; eviction capped per request; the `e2e` script is back in package.json.

Documented, not fixed:

| slice | note | severity |
| --- | --- | --- |
| w2 limits | a few source addresses can drain the global event-weight and outbound budgets; agents behind one NAT share one source allowance | Medium |
| w2 limits | source and global budgets restart from full when the Durable Object is rebuilt | Medium |
| w6 fanout | fan-out matching is linear in the number of subscriptions and not indexed by predicate; not load tested | Medium |
| w8 index | the per-address limits are coarse abuse controls, not a fairness guarantee | Low |
| w8 README | redeploying over an existing Durable Object needs a state reset or a new migration tag (steps written, not run) | Low |

## Round 5 review (Codex, on the round-4 fixes)

Fixed in the stack: the tag part of the event weight is charged to the source's own share (so tag-dense
events are not cheap for it); REQ history has its own socket / source / global budgets and no longer
starves live delivery; the table-cap logic does not run on counters that failed to reconcile (a deletion
request retries the reconciliation, then answers `error: temporarily unavailable, retry`).

Not fixed:

| slice | note | severity |
| --- | --- | --- |
| w2 limits | live delivery still has no reserved bandwidth: a source that sends a lot of live events to its own sockets can use its live budgets up; a fuller design would reserve a minimum live share per socket | Medium |
| w2 limits | per-source, global and per-socket state is in memory only (reset on rebuild); persisting it would need a write per charge or periodic snapshots | Medium |
| w6 req | history and live use separate budgets, so one socket can in total receive history + live traffic up to both allowances | Low |
| w4 deletion | a failing reconciliation is retried by each deletion request, i.e. one extra pair of COUNT queries per request until it works | Low |

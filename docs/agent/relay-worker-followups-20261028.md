# Relay worker: follow-ups from the 3-round adversarial review (2026-10-28)

Scope: the PR stack `relay-w1…w8` for `deploy/relay-worker`. The review found 1 High, 32 Medium and 73 Low items.
The High item and the Medium items about limits, tag sizes, deletion bookkeeping, live/stored matching and NIP-11 were fixed in this stack.
This list holds what was **not** fixed: every remaining Medium (also summarised in the README "Known limits") and the Low items that need more than a one-line change.

## Production-readiness register for relay.aastar.io (read this first)

Status: **not production-ready.** The stack is code-complete and unit/e2e tested, but nothing below the
"Verified" line has been run against a real Durable Object at production scale. Items are labelled
`[read]` (checked in code on this branch), `[review]` (taken from a PR review, not re-run by the author
of this doc) or `[unverified]`.

### A. PR #301 (w2 limits) review: 7 Low/Nit items in `src/limits.ts`

These are being fixed in a separate PR to main on branch `jhfnetboy/relay-limits-hardening`
(not part of this stack; at the time of writing that branch was not yet visible on origin [read: `git ls-remote`]).
Until it merges, all seven are open on this stack. All were reported by the reviewer as non-blocking.

| # | sev | what | status |
| --- | --- | --- | --- |
| 1 | Low | `takeToken` accepts a negative cost (acts as a refund) and a clock that moved backwards (a successful take sets `last = now`, so the interval is refilled twice after a rewind). `eventWeight` uses `>>` (int32): `eventWeight(0, 2**31)` is negative, `2**32` gives 1. Unreachable today (frames are far below 2 GiB, Workers' `Date.now()` is near monotonic). Fix: reject `cost<=0` or non-finite, use `Math.floor(bytes/4096)`. | tracked in relay-limits-hardening [review] |
| 2 | Low | Per-source LRU eviction resets an exhausted source to a full bucket (reviewer reproduced with maxSourceEntries=100; production value 10000, so an attacker needs about 10k distinct sources / IPv6 /64s). Rejected operations also insert entries. | tracked in relay-limits-hardening [review] |
| 3 | Low | `overQuota` never closes a new socket that was refused only by the *source* quota (`if (!k) return false`; `chargeDelivery` has not yet called `sock(conn)`). The socket silently loses deliveries and is never closed with 1013. The reviewer did not reproduce this independently (Codex repro, code reading). | tracked in relay-limits-hardening [review] |
| 4 | Low | `overQuota` continuity test is too lenient: a drop every <2 windows counts as "continuous" (one drop per 100 s triggers on the 3rd). | tracked in relay-limits-hardening [review] |
| 5 | Nit | `countViolation` is a fixed window anchored at the first violation (98 violations in 2 s around a boundary do not trigger max=50) and `now - badAt > 60_000` is strict, so exactly 60000 ms still counts as the old window. The 300/min frame bucket is the backstop. | tracked in relay-limits-hardening [review] |
| 6 | Nit | `sockOut` entries are created by refused `chargeDelivery` calls and only `Budgets.forget` removes them. Wiring check: `Relay.dropConn` calls `this.budgets.forget(st.id)` and every close path funnels through `dropConn` (`src/relay.ts:145,159,194`) [read]. A socket whose attachment is null skips `forget` (`if (!st) return`) [read], so a leak remains possible for attachment-less sockets (see the Low item on `serializeAttachment` below). | partly mitigated by wiring; rest tracked in relay-limits-hardening |
| 7 | Nit | `sourceKey` returns unparseable input verbatim (`""`, `"001.2.3.4"`, `"1.2.3.4:80"` are each their own key). Safe only if the input is `CF-Connecting-IP`; `SRC_HEADER` (env override) or a missing header changes that (see `src/index.ts:54` in the Low table). An empty string makes all header-less requests share one bucket (fail-closed). | tracked in relay-limits-hardening; header trust is an open deployment question |

Wiring gates the reviewers set for the handler/DO slices, checked in code [read]: `Relay` runs
`EVENTS_DDL + DELETION_DDL + SUBS_DDL` and `reconcileCounters` at start (`src/relay.ts:53-54`);
`events.ts` handles `blocked`, `unavailable` and `ok_partial` separately from `ok` (`src/events.ts:60-66`).
Not re-run against a real Durable Object.

### B. PR #303 (w4 deletion) review notes

- **H303-1 (Medium)**: fixed in 0aac7805. `a` branch accounts `Math.max(estDel, delWrites)` like the `e` branch (`src/deletion.ts:143`).
- **H303-2 (Low), open**: the request's own stored rows are not counted in the deletion write budget
  (`store.ts`, `storageFull(db) && added + res.added - res.freed > 0` path). Reviewer's example: estimate
  5,022 vs actual 5,026, a deviation of about 4 rows against a 5,000 budget. Not changed on purpose: fixing
  it would change budget semantics. The reviewer did not re-run Codex's number.
- **Batch-wide `Math.max` (Low, open)**: the `Math.max(estDel, delWrites)` is taken over the whole batch of
  victims (at most 10), not per victim. A mixed batch where one victim is below its estimate and another
  above it under-counts by a few rows; negligible against the 5,000 cap and the same granularity as the check.
- H303-3 (test gap on `e.kind !== 5 && isBlocked`) was closed by test `deletion requests are never blocked themselves`.

### C. Known limits already published in `deploy/relay-worker/README.md` ("Known limits")

1. Global budgets (event weight, outbound bytes) are shared; a few source addresses can drain them. Agents behind one NAT share one source allowance. Per-address limits are coarse abuse controls.
2. Per-source state is a bounded in-memory LRU; many addresses flush it (resets a throttled source).
3. All in-memory budgets are lost when the Durable Object sleeps, is evicted or rebuilt; only per-connection buckets survive in the socket attachment.
4. Tombstones and coordinate cutoffs are evicted FIFO relay-wide; one key writing many can push out other authors' records.
5. A deletion request cut short by the 100-target / write budget is stored, so resending returns `duplicate`; clients must split into requests of at most 100 targets.
6. `sql.databaseSize` does not shrink after deletes, so "storage full" can be sticky until data is cleaned by hand.
7. Upgrades scan all open sockets (at most 2000) and every Upgrade is forwarded to the Durable Object before it can be refused.
8. Fan-out is O(subscriptions) per event, no predicate index, not load tested; 2000 connections not tested either.
9. `storeEvent` throws on SQL errors; the caller turns that into `OK false "error: ..."`.

### D. Still unverified or missing for production

None of these is implemented or tested in this stack unless stated.

| area | gap | note |
| --- | --- | --- |
| Load | No load test at 2000 concurrent connections or near the 5 GB storage cap | Config values (2000 connections, 5 GB) are limits written in `config.ts`/README, not measured capacity. Fan-out CPU, rows-read billing and DO CPU limits at that scale are unknown. [unverified] |
| Topology | Single Durable Object: every connection, every budget and all storage live in one object | One object is the throughput ceiling and the single point of failure; global budgets are per-object, not per-region. No sharding plan. |
| Rebuild | DO eviction/redeploy/rebuild resets all in-memory budgets to full | An attacker who can trigger or wait for eviction gets fresh allowances. Redeploying over an existing DO needs a state reset or a new migration tag; steps are written in the README but were not run. |
| Live bandwidth | No reserved bandwidth for live delivery | REQ history has separate budgets, but a source sending much live traffic to its own sockets can exhaust live budgets; a minimum live share per socket is not designed. |
| Monitoring | No monitoring or alerting plan | No metrics export, no alerts on 429/503 rates, storage-full, DO errors or `console.error` lines (for example `dropConn failed`). `wrangler.toml` defines no observability config. |
| Backup/restore | No backup or restore story for the SQLite-backed DO | A lost or corrupted object means lost events, tombstones and subscriptions; no export job, no restore drill. [unverified] |
| DNS cutover | No rollback procedure for moving relay.aastar.io to the Worker | The existing relay's DNS record, TTL, and how to switch back are not documented here; DNS was never touched by this work. |
| Abuse tooling | No ban/blocklist tooling, no operator view of top sources | Only automatic per-source throttling exists; an operator cannot ban a pubkey or address, or list the heaviest sources, short of redeploying. |
| Exposure | `workers_dev = true` publishes the relay on a workers.dev hostname; no routes defined | Decide whether that host should stay public once the custom domain is live. |
| Client address trust | `SRC_HEADER` override or a missing `CF-Connecting-IP` changes who shares a bucket | Production must run without the override and with Cloudflare's header. |
| E2E coverage | cap/fanout/seed e2e modes need manual relay restarts and are not in `pnpm test` | Most limit behaviour in the README was exercised only by hand or by unit tests. |

### E. Codex full-stack review and own read (added late; none fixed in this stack)

Labels: `[review]` = reported by the Codex full-stack review, not re-run here; `[read]` = our own code reading, not run.

| sev | what | label |
| --- | --- | --- |
| **High** | A tag-filtered REQ has unbounded SQL work: the `tags_cover` index has no `created_at`, `query.ts` `buildSql` uses `id IN (subquery) ORDER BY created_at`, and the history quota is charged after the fact. A cheap REQ can cost a large scan. | review |
| Medium | Multi-filter pagination: `runQuery` returns `more` at the first capped filter, so later filters are never read, and `until`-paging skips their events. | review |
| Medium | Upgrade quota of 60/min may be too small for a CLI history walk of up to 100 pages (one connection per page). Unverified. | review, unverified |
| Low | A kind-5 event's own stored rows are not in the deletion write budget (same as H303-2). | review |
| - | Per-EVENT fan-out is O(connections): `ctx.getWebSockets()` plus `deserializeAttachment` per socket; a subs-cache miss means one SQL read per connection. | read |
| - | One DO instance (`main`) is single-threaded; all traffic serialises on it. | read |
| - | No `setWebSocketAutoResponse` or heartbeat, so dead peers can hold slots until the 2000 cap fills. | read |
| - | Constructor runs `SELECT DISTINCT conn` on every cold start. | read |
| - | CORS headers exist only on the `nostr+json` and OPTIONS responses; plain 200/429/503 responses have none. | read |
| - | Double `hasEvent` check per EVENT. | read |
| - | `chargeEvent` runs before `storeEvent` and is not refunded on full/stale/blocked/unavailable results. | read |
| - | Per-source budgets are in memory and rebuilt after DO hibernation or rebuild (same as section D). | read |

### F. Acceptance status

- 7/7 real-client checks passed on `hyphae-relay-v2`. That is a **staging** address, not production relay.aastar.io.
- Hibernation idle-wake was verified with a Node WebSocket, **not** the Hyphae CLI: the daemon polls and no CLI command holds a live subscription.
- Group chat needs a fresh build of `bin/hyphae`; the checked-in `bin/hyphae` is stale.
- `hyphae event` exits 0 when the relay rejects the event, so scripts cannot rely on its exit code.
- Not tested: real Nostr client libraries against the `more` EOSE extension; load at 2000 connections; Durable Object billing; the 1013/1008 close paths.

(These acceptance facts come from the coordinator's report; the author of this doc did not re-run them.)

Verified in this stack (author-level, on the branches): typecheck and the vitest unit suites, plus the default
e2e mode. See each PR body for the numbers.

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

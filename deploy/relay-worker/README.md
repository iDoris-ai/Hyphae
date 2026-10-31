# Hyphae relay worker

A Nostr relay as a Cloudflare Worker plus one SQLite-backed Durable Object (`Relay`).
`src/index.ts` answers NIP-11 and CORS and forwards WebSocket upgrades to the single DO instance
(`idFromName("main")`). `src/relay.ts` is only the Durable Object shell (connections, frame
dispatch); the work is in small modules that take their dependencies explicitly:

| Module | Role |
| --- | --- |
| `config.ts` | every limit constant, `loadLimits` (test overrides) |
| `nostr.ts`, `filter.ts` | event validation, kind classes; filter validation and matching |
| `limits.ts` | token buckets, per-source state, global and outbound budgets, `sourceKey` |
| `db.ts`, `store.ts` | schema, size estimates, event storage, replacement, net-growth check |
| `deletion.ts` | NIP-09 (tombstones, coordinate cutoffs, eviction, write budget) |
| `query.ts`, `subs.ts` | REQ queries; subscription persistence, cache, size caps |
| `events.ts`, `req.ts`, `fanout.ts`, `hub.ts` | EVENT admission, REQ/CLOSE handling, broadcast, the interface they share |

## Status and honest caveats

- **Not load tested.** The 2000-connection cap is a configured limit, not a measured capacity.
  Nothing here has been run with anywhere near 2000 sockets, nor against the real Cloudflare
  edge. Only `wrangler dev --local` was used.
- **Single Durable Object.** Everything (all events, all sockets, all fan-out) goes through one
  object, so throughput is bounded by one single-threaded isolate. Sharding is not implemented.
- **Storage ceiling 5 GiB (5 368 709 120 bytes)** (`maxStorageBytes`, checked against `sql.databaseSize`; the Durable
  Object SQLite limit is the hard wall). When over the cap, writes that grow the database are
  refused (see below). There is no pruning or retention policy yet. **Fill-time estimate** (worst
  case, not measured): the global EVENT budget lets ~238 events of 500 KB per minute through (about
  120 MB/min), so an attacker with at least 15 source addresses could fill 5 GB in roughly 43
  minutes; each single address is limited to 8 MB/min of stored data, so one address needs about
  640 minutes (10.7 h). Storage is the first thing to watch (`wrangler tail`, dashboard).
- Per-source and global abuse controls are **in memory** (lost when the object is evicted or
  restarts); the per-connection buckets survive hibernation because they live in the socket
  attachment. A client can reset its own buckets by reconnecting; that is bounded by the
  per-address connection and upgrade limits and by the global caps. IPv6 sources are grouped by
  /64 (an attacker owning a /48 or larger still gets many /64 groups; there is no /48 grouping).
- A drained global budget (EVENT weight or outbound bytes) affects every client for the rest of the
  minute; the per-source quotas exist to make that expensive, not impossible.

## Protocol support

Implemented: NIP-01 (EVENT/REQ/CLOSE/OK/EOSE/CLOSED/NOTICE), NIP-09 (deletion by `e` and `a`
tags, persistent tombstones), NIP-11, NIP-16-style ephemeral/replaceable kinds and NIP-33-style
addressable kinds (as folded into NIP-01), NIP-67 (`["EOSE", subId, ["more"]]` hint).

**Not implemented:** NIP-40 (expiration), NIP-42 (auth), NIP-45 (COUNT), NIP-50 (search; the
`search` field in a filter is silently ignored), NIP-70 (protected events), NIP-13 (PoW),
paid/whitelisted access. Every event is public; there is no access control.

## Behaviour worth knowing

- Frames are limited to **512000 UTF-8 bytes**. An oversized `EVENT` whose id can be read from the
  first or last bytes gets `["OK", id, false, "invalid: event too large"]`; anything else gets a NOTICE.
- Events may have up to **256 tags**, each with at most **16 strings** of at most **1024 UTF-8 bytes**
  (the coordinate in an `a` tag may be 130 bytes longer, so an addressable event with a long `d` can
  still be deleted).
  Of those tags, at most **64 distinct single-letter (name, value) pairs are indexed** for `#x`
  filters; further tags are stored in the event JSON but cannot be queried by `#x`. Live matching
  uses the same indexed subset, so a subscription sees the same events live as it would in a REQ.
- History sent for a REQ is charged to its own budgets (4 MB/min per socket, 32 MB/min per source
  address, 128 MB/min globally), separate from live delivery, and cut short with the NIP-67 `more`
  hint where one runs out; a big pull therefore cannot make a socket miss live events; an ordinary inbox pull of a
  few hundred small events is far below the 4 MB/min socket allowance. A REQ returns at most
  **500 events and 4 MB** in total, newest first within each filter; filters
  are read in order, so the merged list is not globally sorted. If matching history was left out
  because of those caps, `EOSE` carries the NIP-67 hint `["more"]` (never a plain finish). A
  client-chosen `limit` below the cap never produces the hint.
- Filters are normalized: only `ids, authors, kinds, since, until, limit` and single-letter `#x`
  survive (unknown fields such as `search` are ignored and dropped, as NIP-01 says, not an error;
  a multi-character `#xx` key is rejected with `invalid: unsupported tag filter` because ignoring it
  would widen the filter); duplicate list values and duplicate filters are removed. An empty `ids/authors/kinds/#x` list matches nothing. `ids`/`authors` are hex
  prefixes (range scan on the primary index). A filter may bind at most 90 values (a prefix costs
  2); a stored subscription is at most 4 KB, 16 KB per connection, 20 000 rows overall.
- Deletion (NIP-09): `e` tags (own events, not other kind 5) and `a` tags (replaceable/addressable
  coordinates, versions up to the request time). Tombstones are persisted, so a deleted event
  cannot be re-published (`blocked: event was deleted`), including ephemeral events and deletions
  that arrive before the event. Deletion requests cannot be deleted. At most 100 `e`/`a` targets
  and 5000 row writes are processed per request; the rest is ignored and the OK says so.
  The estimated row writes (table, index rows and counter upserts) of one request are kept under
  5000: the budget is checked before every operation (an estimate, not an exact count), and the OK
  says `ignored: ...` when part of a request was skipped. A whole request evicts at most 256 old
  records; if a table is still full after that, the rest of the request is skipped. The counters
  behind the table caps are reconciled with the real row counts once (a version marker in `meta`),
  never on every start. If that reconciliation fails, startup still works and the next deletion
  request retries it; if the retry fails too the request is answered
  `error: temporarily unavailable, retry` and nothing is written.
  Tombstone and coordinate-cutoff tables are capped at 1 000 000 rows each; at the cap the **oldest
  record is evicted** (inside the same transaction) instead of refusing deletions, so an attacker
  cannot disable deletion for everyone, but the oldest deleted events can be published again.
- When the database is over `maxStorageBytes` the decision is by **net growth** (estimated bytes:
  JSON + row/index overhead + indexed tag rows): new regular events, brand-new
  replaceable/addressable coordinates, new subscriptions, replacements that make the event bigger
  and deletion requests that add more (their own rows plus tombstones) than they free are refused
  with `rate-limited: relay storage is full`. Same-size or smaller replacements, deletions that free
  more than they add, duplicates (idempotent OK) and ephemeral events still work. Note that this
  also stops new REQs.
- Byte quotas (subscription sizes, cache, outbound frames) count UTF-8 bytes, not characters.
- After the relay closes a socket (1008, 1011, 1013) it ignores any frame still in flight, so a
  closing connection cannot recreate subscriptions.
- Outbound delivery: a delivery over the per-source or per-socket byte quota is **dropped**
  (the subscriber misses that live event; REQ can recover it) and only a socket that stays over
  quota for about two further windows is closed with 1013. Someone who can make events match a
  victim's subscription (for example by tagging their `#p`) can burn the victim's per-socket quota
  for the minute; the victim loses live deliveries, not the connection.
- SQL errors never escape: they become `OK false "error: ..."` or `CLOSED "error: ..."`. If a
  subscription cannot be removed, the connection is closed instead of sending CLOSED.

## Limits (constants in `src/config.ts`, also advertised in NIP-11 `limitation`)

| Limit | Value |
| --- | --- |
| frame size | 512000 bytes |
| tags per event / indexed tags | 256 / 64 |
| subscriptions per connection / filters per REQ | 20 / 10 |
| max `limit`, bytes per REQ result | 500, 4 MB |
| EVENT per connection | 120 / min |
| REQ per connection | 60 / min (repeated REQs for one subId count; invalid ones too) |
| any frame per connection (incl. CLOSE, malformed) | 300 / min; 50 rejected frames in a minute closes the socket with 1008 |
| EVENT / REQ per source address | 240 / min, 120 / min (sum over its connections) |
| stored bytes per source address | 8 MB / min (ephemeral events excluded) |
| WebSocket upgrades per source address | 60 / min, at most 32 concurrent (a refusal for the concurrency limit does not use a token) |
| EVENT weight per source address | 7 500 / min (a quarter of the global budget). The size part is charged to the source for every frame before it is hashed or its signature checked, the tag part (the rest of the weight the global budget will be charged) once the event validated; replays, invalid events and ephemeral events pay too |
| global EVENT budget | 30 000 weight / min (weight = 1 + tag strings/32 + bytes/4096), charged only after the event validated and not for a replay of a stored event; running out is not counted as the connection's violation |
| ephemeral events (kinds 20000-29999) | never stored. A replay of the same id within 60 s (at most 10 000 ids remembered, oldest evicted first) is answered `OK true` with `duplicate:` and not broadcast again. The publisher's source is also charged 1 event-weight per 32 deliveries its event caused (after the fact: the cost is taken even if the bucket cannot cover it, up to one minute of debt, so the source's next events are refused until it is repaid). The id set is in memory: when the Durable Object is evicted or restarted it is empty, so a replay right after a wake-up is accepted once more; it still pays validation and rate limits |
| connections | 2000 (503 beyond) |
| fan-out | 5000 deliveries per event; 64 MB/min global; 16 MB/min per source address (a quarter of the global budget); 4 MB/min per socket (dropped, closed with 1013 only when sustained) |
| history per socket / per source / global | 4 MB / 32 MB / 128 MB per minute (REQ results, apart from the live-delivery budgets) |
| deletion tables | 1 000 000 tombstones and 1 000 000 coordinate cutoffs, oldest evicted at the cap |
| created_at in the future | 15 min |

The source address is `CF-Connecting-IP`, copied by `index.ts` into `x-hyphae-src` (a
client-supplied `x-hyphae-src` is overwritten). IPv4 is used as is, **IPv6 is grouped by /64**
(IPv4-mapped IPv6 counts as the IPv4 address); NIP-11 `limitation` says so and lists the
per-address numbers. Behind NAT many agents share one address and one budget; raise the per-source
constants if that hurts real users.

Why 60 upgrades per minute per address: the Hyphae Go client opens a **new WebSocket for every
query** (`relayquery.FetchWithTimeout`), and `Walk` pages through history one query per page, up to
100 pages, with no retry on HTTP 429. A full walk by one agent, or a few agents behind one NAT
polling their inbox every 30 s, must fit; ten per minute did not. The limit is a token bucket, so
the bucket holds 60 tokens and refills one per second, so a burst above 60 is refused (HTTP 429) until tokens come back; a client has to pace itself (a 100-page walk from a full bucket needs about 40 s of refill). If the client
later reuses one connection, this can be lowered again.

**Production must not set `SRC_HEADER`.** The default (`CF-Connecting-IP`) is overwritten by
Cloudflare's edge on every direct client request, so a client cannot choose its own address through it
(requests that reach this Worker as a subrequest from another Worker do not carry the end client's
address: cross-zone ones get a shared Cloudflare address, so they would all share one bucket). Any other
header named in `SRC_HEADER` is client-controlled and would let one client pick a fresh source
bucket per connection, defeating every per-address limit; it exists only for `wrangler dev --local`,
where the dev server does not set `CF-Connecting-IP`. `wrangler.toml` therefore has no `[vars]`
section. A WebSocket upgrade without the header is refused with 400 and logged (it used to fall into one
shared `unknown` bucket that one client could exhaust for all the others); this does not happen behind
Cloudflare. A request that reaches the Worker directly on
`*.workers.dev` goes through the same edge, so the same rule applies there.

Tests can override numeric limits with a `LIMITS_JSON` variable (for example
`wrangler dev --var 'LIMITS_JSON:{"maxStorageBytes":1}'`). Do not set it in production.

## Known limits

- The global budgets (event weight, outbound bytes) are shared by everyone; a handful of source
  addresses can still use them up. The per-source shares only make that more expensive.
- Per-source state is a bounded LRU in memory; many addresses can flush it, which resets a throttled
  source to a full bucket.
- All in-memory budgets (per source, global, per socket outbound) are lost when the Durable Object
  sleeps or is evicted; only the per-connection buckets survive in the socket attachment.
- Tombstones and coordinate cutoffs are evicted first-in-first-out for the whole relay, so one key
  can push other authors' deletion records out by writing many.
- A deletion request cut short by the 100-target / write budget is stored as an event, so sending
  the same event again returns `duplicate`; clients should split deletions into requests of at most
  100 targets.
- `sql.databaseSize` does not shrink after deletes, so "storage full" can persist until the object's
  data is cleaned up by hand.
- An upgrade counts the open sockets of its source by scanning all sockets (at most 2000), and every
  upgrade is first forwarded to the Durable Object before it can be refused.
- Fan-out costs O(subscriptions) per event, with no index by predicate, and has not been load
  tested; neither has 2000 connections.
- A few source addresses can still drain the global budgets, and agents behind one NAT share one
  source allowance. The per-address limits are coarse abuse controls, not a fairness guarantee.
- Source and global budgets restart from full whenever the Durable Object is rebuilt.
- `storeEvent` throws on SQL errors; the caller turns that into `OK false "error: ..."`.

## Develop and test

```bash
pnpm install
pnpm typecheck
pnpm test                         # vitest unit tests of every module (storage tests run over node:sqlite, Node 24)
pnpm exec wrangler dev --local --port 8787
pnpm e2e                          # in another terminal: end-to-end checks against ws://127.0.0.1:8787 (node test/e2e/run.mjs)
```

The e2e script gives every connection its own fake source address through the
`CF-Connecting-IP` header (if your wrangler overwrites it, start with `--var SRC_HEADER:x-e2e-src`
and pass `--src-header=x-e2e-src`). The cap checks need relays with tiny limits; see the header of
`test/e2e/run.mjs` (`--seed`, `--capped`, `--fanout`). Start every e2e run on fresh state (`--persist-to <new dir>`): the suite is not idempotent, a second run
on the same data fails the `#t` indexing check because the earlier run's events still match.

## Deploying (not done by this repo's tooling; run it yourself)

1. `pnpm install` and make sure `pnpm test`, `pnpm typecheck` and the e2e run pass.
2. `pnpm exec wrangler login` (or set `CLOUDFLARE_API_TOKEN` and `CLOUDFLARE_ACCOUNT_ID`).
3. `pnpm exec wrangler deploy`. The first deploy applies migration `v1` which creates the
   SQLite-backed `Relay` class (`new_sqlite_classes`); that needs a Workers plan with Durable Objects.
4. Optional custom domain: see "Custom domain relay.aastar.io" below, then check
   `curl -H 'Accept: application/nostr+json' https://<host>/`.
5. Point clients at `wss://<host>`. Watch `pnpm exec wrangler tail` during the first hours; start
   with the default limits and loosen them only based on real traffic.

`wrangler.toml` keeps `compatibility_date = "2026-01-01"`; the installed wrangler 4.40 warns that
its bundled workerd is older and falls back to `2025-09-24`, so local runs (unit, e2e) use different
compatibility behaviour than the configured date on the real edge; upgrade wrangler to remove the gap.
The platform also has its own Durable Object storage quota per account (the Free plan's is about
5 000 000 000 bytes, shared by all objects), which can stop writes before `maxStorageBytes`; use a paid plan.

## Custom domain relay.aastar.io

Not active in `wrangler.toml` (a commented example only), so a plain `wrangler deploy` does not
touch the domain. To serve `relay.aastar.io` from this Worker, uncomment and add to `wrangler.toml`:

```toml
routes = [
  { pattern = "relay.aastar.io", custom_domain = true, zone_name = "aastar.io" },
]
```

- With `custom_domain = true`, `wrangler deploy` creates the proxied DNS record and the certificate
  for `relay.aastar.io` itself. **A non-interactive deploy (CI, no TTY) makes wrangler override an
  existing DNS record / origin for that name without asking** (wrangler 4.40 behaviour as reported
  in review; not tested here). If another relay already serves the name, that takes it over
  immediately for every client. Check what the name points to **before** deploying, and run the
  first deploy with the stanza interactively.
- The API token needs: Account / Workers Scripts: Edit and, for the custom domain, Zone / Workers
  Routes: Edit on the `aastar.io` zone (plus Zone: Read), and the Durable Objects plan from step 3
  above. Cloudflare manages the domain's DNS record itself; Zone / DNS: Edit is only needed if you
  restore or edit the old record by hand during a rollback (do that with a separate, short-lived
  token). Not verified against the live account.
- Before switching traffic, deploy once without the route and smoke test `<name>.workers.dev`:
  the NIP-11 curl, then one EVENT and a REQ with any Nostr client. Do not run `test/e2e/run.mjs`
  against the edge: it fakes client addresses through a header the edge overwrites, so all its
  connections would share one source bucket. Read the Known limits above; none of it has been load
  tested against the real edge.
- Rollback: delete the custom domain under Workers & Pages > hyphae-relay > Settings > Domains &
  Routes (or `wrangler` equivalent; removing the `routes` entry and redeploying is not guaranteed to
  remove an existing binding, so confirm in the dashboard), then remove or comment out the `routes` stanza (a later deploy would otherwise claim the name
  again, silently in CI), restore the previous DNS record /
  origin for `relay.aastar.io` (write it down before the first deploy) and verify with the NIP-11 curl.
  Events stored in the Durable Object are not migrated back; data written while the Worker served
  the name stays in the Worker.

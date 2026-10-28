# Hyphae relay worker

A Nostr relay as a Cloudflare Worker plus one SQLite-backed Durable Object (`Relay`).
`src/index.ts` answers NIP-11 and CORS and forwards WebSocket upgrades to the single DO instance
(`idFromName("main")`), `src/relay.ts` is the relay, `src/nostr.ts` holds validation, filter
matching and the limit constants.

## Status and honest caveats

- **Not load tested.** The 2000-connection cap is a configured limit, not a measured capacity.
  Nothing here has been run with anywhere near 2000 sockets, nor against the real Cloudflare
  edge. Only `wrangler dev --local` was used.
- **Single Durable Object.** Everything (all events, all sockets, all fan-out) goes through one
  object, so throughput is bounded by one single-threaded isolate. Sharding is not implemented.
- **Storage ceiling 5 GB** (`maxStorageBytes`, checked against `sql.databaseSize`; the Durable
  Object SQLite limit is the hard wall). When over the cap, writes that grow the database are
  refused (see below). There is no pruning or retention policy yet.
- Per-source and global abuse controls are **in memory** (lost when the object is evicted or
  restarts); the per-connection buckets survive hibernation because they live in the socket
  attachment. A client can reset its own buckets by reconnecting; that is bounded by the
  per-address connection and upgrade limits and by the global caps.

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
- Events may have up to **256 tags**. Of those, at most **64 distinct single-letter (name, value)
  pairs are indexed** for `#x` filters; further tags are stored in the event JSON but cannot be
  queried by `#x` (live broadcast still matches them).
- A REQ returns at most **500 events and 4 MB** in total, newest first within each filter; filters
  are read in order, so the merged list is not globally sorted. If matching history was left out
  because of those caps, `EOSE` carries the NIP-67 hint `["more"]` (never a plain finish). A
  client-chosen `limit` below the cap never produces the hint.
- Filters are normalized: only `ids, authors, kinds, since, until, limit` and single-letter `#x`
  survive (unknown fields are dropped, not an error); duplicate list values and duplicate filters
  are removed. An empty `ids/authors/kinds/#x` list matches nothing. `ids`/`authors` are hex
  prefixes (range scan on the primary index). A filter may bind at most 90 values (a prefix costs
  2); a stored subscription is at most 4 KB, 16 KB per connection, 20 000 rows overall.
- Deletion (NIP-09): `e` tags (own events, not other kind 5) and `a` tags (replaceable/addressable
  coordinates, versions up to the request time). Tombstones are persisted, so a deleted event
  cannot be re-published (`blocked: event was deleted`), including ephemeral events and deletions
  that arrive before the event. Deletion requests cannot be deleted. At most 100 `e`/`a` targets
  and 5000 row writes are processed per request; the rest is ignored and the OK says so.
  Tombstone and coordinate-cutoff tables are capped at 1 000 000 rows each (new kind 5 refused).
- When the database is over `maxStorageBytes`: new regular events, brand-new replaceable/addressable
  coordinates, new subscriptions and deletion requests that free nothing are refused with
  `rate-limited: relay storage is full`. Real replacements, duplicates (idempotent OK) and
  ephemeral events still work. Note that this also stops new REQs.
- SQL errors never escape: they become `OK false "error: ..."` or `CLOSED "error: ..."`. If a
  subscription cannot be removed, the connection is closed instead of sending CLOSED.

## Limits (constants in `src/nostr.ts`, also advertised in NIP-11 `limitation`)

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
| WebSocket upgrades per source address | 10 / min, at most 8 concurrent |
| global EVENT budget | 30 000 weight / min (weight = 1 + tags/16 + bytes/4096) |
| connections | 2000 (503 beyond) |
| fan-out | 5000 deliveries per event; 64 MB/min global; 16 MB/min per socket (slow consumers closed with 1013) |
| created_at in the future | 15 min |

The source address is `CF-Connecting-IP`, copied by `index.ts` into `x-hyphae-src` (a
client-supplied `x-hyphae-src` is overwritten). Behind NAT many agents share one address and one
budget; raise the per-source constants if that hurts real users.

Tests can override numeric limits with a `LIMITS_JSON` variable (for example
`wrangler dev --var 'LIMITS_JSON:{"maxStorageBytes":1}'`). Do not set it in production.

## Develop and test

```bash
pnpm install
pnpm typecheck
pnpm test                         # vitest unit tests (src/nostr.ts)
pnpm exec wrangler dev --local --port 8787
node scripts/e2e.mjs              # in another terminal: end-to-end checks against ws://127.0.0.1:8787
```

The e2e script gives every connection its own fake source address through the
`CF-Connecting-IP` header (if your wrangler overwrites it, start with `--var SRC_HEADER:x-e2e-src`
and pass `--src-header=x-e2e-src`). The cap checks need relays with tiny limits; see the header of
`scripts/e2e.mjs` (`--seed`, `--capped`, `--fanout`). Use fresh `--persist-to` state for them.

## Deploying (not done by this repo's tooling; run it yourself)

1. `pnpm install` and make sure `pnpm test`, `pnpm typecheck` and the e2e run pass.
2. `pnpm exec wrangler login` (or set `CLOUDFLARE_API_TOKEN` and `CLOUDFLARE_ACCOUNT_ID`).
3. `pnpm exec wrangler deploy`. The first deploy applies migration `v1` which creates the
   SQLite-backed `Relay` class (`new_sqlite_classes`); that needs a Workers plan with Durable Objects.
4. Optional custom domain: add a route or custom domain for the worker in the dashboard or in
   `wrangler.toml`, then check `curl -H 'Accept: application/nostr+json' https://<host>/`.
5. Point clients at `wss://<host>`. Watch `pnpm exec wrangler tail` during the first hours; start
   with the default limits and loosen them only based on real traffic.

`wrangler.toml` keeps `compatibility_date = "2026-01-01"`; the installed wrangler 4.40 warns that
its bundled workerd is older and falls back, which does not affect local runs.

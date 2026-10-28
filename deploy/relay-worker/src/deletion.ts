import { type Db, W_EVENT_BASE, W_TAG_ROW, bump, counter, deleteEvent, setCounter, tagRows } from "./db";
import { HEX64, type NostrEvent, isAddressable, isReplaceable } from "./nostr";

export const DELETION_DDL = `
  -- NIP-09 tombstones: ids deleted by their author (also recorded before the event arrives).
  CREATE TABLE IF NOT EXISTS tombstones (
    event_id TEXT NOT NULL,
    pubkey TEXT NOT NULL,
    PRIMARY KEY (event_id, pubkey)
  );
  -- NIP-09 "a" deletions: versions of a replaceable/addressable coordinate up to cutoff are gone.
  CREATE TABLE IF NOT EXISTS coord_cutoffs (
    coord TEXT PRIMARY KEY,
    cutoff INTEGER NOT NULL
  );
`;

const TOMB_ROW = 160;
const CUTOFF_ROW = 150;
// Estimated row writes: the row, its primary-key index entry and the meta counter upsert.
const W_TOMB = 3;
const W_CUTOFF = 3;
const MAX_EVICT = 256; // records evicted by one deletion request, however far the tables are above their caps
const COUNTERS_VERSION = 1; // bump to force one more reconciliation at the next start

/** Parses an NIP-33 coordinate "kind:pubkey:d". */
export function parseCoord(v: string | undefined): { kind: number; pubkey: string; d: string } | null {
  if (typeof v !== "string") return null;
  const parts = v.split(":");
  if (parts.length < 3) return null;
  const kind = Number(parts[0]);
  if (!/^\d+$/.test(parts[0]) || !Number.isInteger(kind) || kind > 65535) return null;
  if (!HEX64.test(parts[1])) return null;
  return { kind, pubkey: parts[1], d: parts.slice(2).join(":") };
}

/** True when the author deleted this event id, or deleted its coordinate at or after its created_at. */
export function isBlocked(db: Db, e: NostrEvent, d: string): boolean {
  const dead = db.sql.exec("SELECT 1 FROM tombstones WHERE event_id = ? AND pubkey = ?", e.id, e.pubkey).toArray();
  if (dead.length > 0) return true;
  if (!isReplaceable(e.kind) && !isAddressable(e.kind)) return false;
  const c = db.sql.exec("SELECT cutoff FROM coord_cutoffs WHERE coord = ?", `${e.kind}:${e.pubkey}:${d}`).toArray();
  return c.length > 0 && e.created_at <= (c[0].cutoff as number);
}

type Table = "tombstones" | "coord_cutoffs";

/**
 * The counters are the only guard on the table caps, so the first start of a database (or of a new
 * COUNTERS_VERSION) makes them match the real row counts, in one transaction together with the
 * version marker. Later starts only read the marker. A failure is logged and startup goes on; the
 * next deletion request retries it (ensureCounters) before it touches the tables.
 */
export function reconcileCounters(db: Db) {
  ensureCounters(db);
}

/**
 * True when the counters are trustworthy (reconciled, marker written). Otherwise tries the
 * reconciliation once more and reports whether it worked; never throws. Code that enforces the
 * table caps must not run on counters that are not trusted.
 */
export function ensureCounters(db: Db): boolean {
  try {
    if (counter(db, "counters_v") >= COUNTERS_VERSION) return true;
    db.tx(() => {
      for (const table of ["tombstones", "coord_cutoffs"] as Table[]) {
        setCounter(db, table, db.sql.exec(`SELECT count(*) AS n FROM ${table}`).one().n as number);
      }
      setCounter(db, "counters_v", COUNTERS_VERSION);
    });
    return true;
  } catch (err) {
    console.error("counter reconciliation failed", err);
    return false;
  }
}

/**
 * How many of the oldest records one more insert must evict to stay under `cap`, limited by what
 * is left of the request's eviction allowance. `blocked` means it needs room but the allowance is gone.
 */
function evictPlan(db: Db, table: Table, cap: number, left: number): { n: number; blocked: boolean } {
  const have = counter(db, table);
  const need = Math.max(0, Math.min(have, have - cap + 1));
  const n = Math.min(need, left);
  return { n, blocked: need > 0 && n === 0 };
}

/** Estimated row writes of evicting n records: row and index entries, plus one counter upsert. */
const evictWrites = (n: number) => (n > 0 ? 2 * n + 1 : 0);

/** Evicts the n oldest records of a deletion table. */
function evict(db: Db, table: Table, n: number) {
  if (n <= 0) return;
  db.sql.exec(`DELETE FROM ${table} WHERE rowid IN (SELECT rowid FROM ${table} ORDER BY rowid LIMIT ?)`, n);
  bump(db, table, -n);
}

export interface DeletionResult {
  deleted: number;
  partial: boolean;
  added: number; // estimated bytes added (tombstones, cutoffs)
  freed: number; // estimated bytes freed (deleted events, evicted records)
}

/**
 * NIP-09: "e" tags delete by id, "a" tags delete versions of a coordinate up to the request time.
 * At most maxDeletionTargets targets are processed, and the estimated row writes (including
 * index rows and counter upserts) are kept under maxDeletionRowWrites: the budget is checked
 * before every operation. It is an estimate, not an exact count.
 * Tombstone and cutoff tables hold at most maxTombstones / maxCoordCutoffs rows; at the cap the
 * oldest records are evicted (their events could be re-published) instead of refusing deletions,
 * at most MAX_EVICT per request: when that allowance is gone and a table is still full the rest of
 * the request is skipped (reported as partial).
 */
export function applyDeletion(db: Db, req: NostrEvent): DeletionResult {
  const { sql, lim } = db;
  let targets = 0;
  let writes = 0;
  let deleted = 0;
  let added = 0;
  let freed = 0;
  let evicted = 0;
  const done = (partial: boolean) => ({ deleted, partial, added, freed });
  for (const t of req.tags) {
    const isE = t[0] === "e" && !!t[1] && HEX64.test(t[1]);
    if (!isE && t[0] !== "a") continue;
    if (targets >= lim.maxDeletionTargets) return done(true);
    targets++;
    if (isE) {
      const target = sql.exec("SELECT pubkey, kind FROM events WHERE id = ?", t[1]).toArray();
      const own = target.length > 0 && target[0].pubkey === req.pubkey && target[0].kind !== 5;
      if (target.length > 0 && !own) continue; // someone else's event, or another deletion request
      const needTomb = sql.exec("SELECT 1 FROM tombstones WHERE event_id = ? AND pubkey = ?", t[1], req.pubkey).toArray().length === 0;
      const estDel = own ? W_EVENT_BASE + W_TAG_ROW * tagRows(db, t[1]) : 0;
      const plan = needTomb ? evictPlan(db, "tombstones", lim.maxTombstones, MAX_EVICT - evicted) : { n: 0, blocked: false };
      if (plan.blocked) return done(true);
      const estTomb = needTomb ? W_TOMB + evictWrites(plan.n) : 0;
      if (writes + estDel + estTomb > lim.maxDeletionRowWrites) return done(true);
      if (own) {
        const del = deleteEvent(db, t[1]);
        writes += Math.max(estDel, del.writes);
        freed += del.freed;
        deleted++;
      }
      // Remember it (also when it has not arrived yet) so it cannot be re-published.
      if (needTomb) {
        evict(db, "tombstones", plan.n);
        evicted += plan.n;
        freed += plan.n * TOMB_ROW;
        sql.exec("INSERT INTO tombstones (event_id, pubkey) VALUES (?, ?)", t[1], req.pubkey);
        bump(db, "tombstones");
        writes += estTomb;
        added += TOMB_ROW;
      }
    } else {
      const c = parseCoord(t[1]);
      if (!c || c.pubkey !== req.pubkey || c.kind === 5) continue;
      if (!isReplaceable(c.kind) && !isAddressable(c.kind)) continue;
      const d = isAddressable(c.kind) ? c.d : "";
      const coord = `${c.kind}:${c.pubkey}:${d}`;
      const isNew = sql.exec("SELECT 1 FROM coord_cutoffs WHERE coord = ?", coord).toArray().length === 0;
      const victims = sql
        .exec("SELECT id FROM events WHERE pubkey = ? AND kind = ? AND d = ? AND created_at <= ? LIMIT 10", c.pubkey, c.kind, d, req.created_at)
        .toArray();
      let estDel = 0;
      for (const v of victims) estDel += W_EVENT_BASE + W_TAG_ROW * tagRows(db, v.id as string);
      const plan = isNew ? evictPlan(db, "coord_cutoffs", lim.maxCoordCutoffs, MAX_EVICT - evicted) : { n: 0, blocked: false };
      if (plan.blocked) return done(true);
      const estCut = W_CUTOFF + evictWrites(plan.n);
      if (writes + estDel + estCut > lim.maxDeletionRowWrites) return done(true);
      if (isNew) {
        evict(db, "coord_cutoffs", plan.n);
        evicted += plan.n;
        freed += plan.n * CUTOFF_ROW;
        bump(db, "coord_cutoffs");
        added += CUTOFF_ROW;
      }
      sql.exec(
        "INSERT INTO coord_cutoffs (coord, cutoff) VALUES (?, ?) ON CONFLICT(coord) DO UPDATE SET cutoff = max(cutoff, excluded.cutoff)",
        coord, req.created_at,
      );
      writes += estCut;
      for (const v of victims) {
        const del = deleteEvent(db, v.id as string);
        writes += del.writes;
        freed += del.freed;
        deleted++;
      }
    }
  }
  return done(false);
}

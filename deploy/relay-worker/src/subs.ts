import { type Db, storageFull } from "./db";
import { type Filter, TAG_KEY } from "./filter";
import { utf8Length } from "./nostr";

export const SUBS_DDL = `
  CREATE TABLE IF NOT EXISTS subs (
    conn TEXT NOT NULL,
    sub_id TEXT NOT NULL,
    filters TEXT NOT NULL,
    PRIMARY KEY (conn, sub_id)
  );
`;

const uniq = <T,>(a: T[]): T[] => [...new Set(a)];

/**
 * Keeps only what the relay understands (ids, authors, kinds, since, until, limit and
 * single-letter #x tags) and removes duplicate list values. Unknown fields (e.g. NIP-50
 * "search") are dropped without error.
 */
export function normalizeFilter(f: Filter): Filter {
  const out: Filter = {};
  if (f.ids) out.ids = uniq(f.ids);
  if (f.authors) out.authors = uniq(f.authors);
  if (f.kinds) out.kinds = uniq(f.kinds);
  if (f.since !== undefined) out.since = f.since;
  if (f.until !== undefined) out.until = f.until;
  if (f.limit !== undefined) out.limit = f.limit;
  for (const [k, v] of Object.entries(f)) {
    if (TAG_KEY.test(k) && Array.isArray(v)) out[k as `#${string}`] = uniq(v as string[]);
  }
  return out;
}

/** Canonical string for a normalized filter (key order and list order do not matter). */
function filterKey(f: Filter): string {
  return JSON.stringify(
    Object.keys(f)
      .sort()
      .map((k) => {
        const v = (f as Record<string, unknown>)[k];
        return [k, Array.isArray(v) ? [...v].sort() : v];
      }),
  );
}

/** Normalizes every filter and drops duplicates. */
export function normalizeFilters(filters: Filter[]): Filter[] {
  const seen = new Set<string>();
  const out: Filter[] = [];
  for (const f of filters) {
    const n = normalizeFilter(f);
    const key = filterKey(n);
    if (seen.has(key)) continue;
    seen.add(key);
    out.push(n);
  }
  return out;
}

export type Subscriptions = Map<string, Filter[]>;
export type Admission = { ok: true } | { ok: false; reason: string; drop: boolean };

/**
 * Subscriptions are persisted so a hibernated connection keeps receiving events after the object
 * wakes up. Whole connections are cached in memory up to subCacheBytes; the rest is read from SQL.
 */
export class SubStore {
  private cache = new Map<string, { subs: Subscriptions; bytes: number }>();
  private cacheBytes = 0;

  constructor(private db: Db) {}

  /** Subscriptions of one connection: from the cache when it fits, otherwise read from SQL. */
  get(conn: string): Subscriptions {
    const hit = this.cache.get(conn);
    if (hit) return hit.subs;
    const subs: Subscriptions = new Map();
    let bytes = 0;
    for (const row of this.db.sql.exec("SELECT sub_id, filters FROM subs WHERE conn = ?", conn).toArray()) {
      try {
        const json = row.filters as string;
        subs.set(row.sub_id as string, JSON.parse(json) as Filter[]);
        bytes += utf8Length(json);
      } catch {
        // corrupt row: skip
      }
    }
    if (this.cacheBytes + bytes <= this.db.lim.subCacheBytes) {
      this.cache.set(conn, { subs, bytes });
      this.cacheBytes += bytes;
    }
    return subs;
  }

  private invalidate(conn: string) {
    const hit = this.cache.get(conn);
    if (!hit) return;
    this.cacheBytes -= hit.bytes;
    this.cache.delete(conn);
  }

  /** Removes a subscription. A failing SQL delete is thrown to the caller. */
  remove(conn: string, subId: string) {
    this.invalidate(conn);
    this.db.sql.exec("DELETE FROM subs WHERE conn = ? AND sub_id = ?", conn, subId);
  }

  /** Forgets a connection. A failing SQL delete is thrown to the caller. */
  dropConn(conn: string) {
    this.invalidate(conn);
    this.db.sql.exec("DELETE FROM subs WHERE conn = ?", conn);
  }

  /**
   * Checks the per-subscription, per-connection and relay-wide size caps and stores the filters.
   * `drop` tells the caller whether an existing subscription of that id must be removed.
   */
  save(conn: string, subId: string, fjson: string): Admission {
    const { sql, lim } = this.db;
    const bytes = utf8Length(fjson);
    if (bytes > lim.maxSubBytes) return { ok: false, reason: "invalid: filter too large", drop: true };
    const mine = sql.exec("SELECT sub_id = ? AS same, length(CAST(filters AS BLOB)) AS len FROM subs WHERE conn = ?", subId, conn).toArray();
    const exists = mine.some((r) => r.same === 1);
    const others = mine.filter((r) => r.same !== 1).reduce((n, r) => n + (r.len as number), 0);
    if (others + bytes > lim.maxSubBytesPerConn) {
      return { ok: false, reason: "rate-limited: too many subscription bytes on this connection", drop: true };
    }
    if (!exists) {
      // A new subscription row grows the database.
      if (mine.length >= lim.maxSubsPerConn) return { ok: false, reason: "rate-limited: too many subscriptions", drop: false };
      const total = sql.exec("SELECT count(*) AS n FROM subs").one().n as number;
      if (total >= lim.maxSubRows) return { ok: false, reason: "rate-limited: relay has too many subscriptions", drop: false };
      if (storageFull(this.db)) return { ok: false, reason: "rate-limited: relay storage is full", drop: false };
    }
    this.invalidate(conn);
    sql.exec("INSERT OR REPLACE INTO subs (conn, sub_id, filters) VALUES (?, ?, ?)", conn, subId, fjson);
    return { ok: true };
  }
}

import type { Limits } from "./config";
import { utf8Length } from "./nostr";

/** What the storage modules need from the Durable Object: SQL, a transaction wrapper and the limits. */
export interface Db {
  sql: SqlStorage;
  tx<T>(fn: () => T): T;
  lim: Limits;
}

export const EVENTS_DDL = `
  CREATE TABLE IF NOT EXISTS events (
    id TEXT PRIMARY KEY,
    pubkey TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    kind INTEGER NOT NULL,
    d TEXT NOT NULL,
    json TEXT NOT NULL
  );
  CREATE INDEX IF NOT EXISTS events_created ON events (created_at DESC);
  CREATE INDEX IF NOT EXISTS events_kind ON events (kind, created_at DESC);
  CREATE INDEX IF NOT EXISTS events_author ON events (pubkey, kind, created_at DESC);
  CREATE INDEX IF NOT EXISTS events_coord ON events (pubkey, kind, d, created_at);
  CREATE TABLE IF NOT EXISTS event_tags (
    event_id TEXT NOT NULL,
    name TEXT NOT NULL,
    value TEXT NOT NULL
  );
  DROP INDEX IF EXISTS tags_lookup;
  CREATE INDEX IF NOT EXISTS tags_cover ON event_tags (name, value, event_id);
  CREATE INDEX IF NOT EXISTS tags_event ON event_tags (event_id);
`;

// Rough on-disk cost model used for the "does this write grow a full database" decision.
const ROW_OVERHEAD = 150; // events row + its indexes, besides the JSON
const TAG_ROW = 64; // one event_tags row + indexes, besides the value

/** The (name, value) pairs of single-letter tags that get indexed: distinct, at most `max`. */
export function indexedTags(tags: string[][], max: number): [string, string][] {
  const seen = new Set<string>();
  const out: [string, string][] = [];
  for (const t of tags) {
    if (out.length >= max) break;
    if (t[0].length !== 1 || t[1] === undefined) continue;
    const key = t[0] + "\0" + t[1];
    if (seen.has(key)) continue;
    seen.add(key);
    out.push([t[0], t[1]]);
  }
  return out;
}

export const hasEvent = (db: Db, id: string): boolean => db.sql.exec("SELECT 1 FROM events WHERE id = ?", id).toArray().length > 0;

export const storageFull = (db: Db): boolean => db.sql.databaseSize > db.lim.maxStorageBytes;

/** Estimated bytes of a stored event (JSON + row/index overhead + indexed tag rows). */
export function footprint(jsonBytes: number, tags: string[][], maxIndexed: number): number {
  let n = jsonBytes + ROW_OVERHEAD;
  for (const [, value] of indexedTags(tags, maxIndexed)) n += utf8Length(value) + TAG_ROW;
  return n;
}

export function storedFootprint(db: Db, id: string): number {
  const r = db.sql.exec("SELECT length(CAST(json AS BLOB)) AS n FROM events WHERE id = ?", id).toArray();
  if (r.length === 0) return 0;
  const t = db.sql
    .exec("SELECT coalesce(sum(length(CAST(value AS BLOB)) + ?), 0) AS n FROM event_tags WHERE event_id = ?", TAG_ROW, id)
    .one().n as number;
  return (r[0].n as number) + ROW_OVERHEAD + t;
}

/** Deletes an event and its tag rows; returns the rows written and the bytes freed (estimate). */
export function deleteEvent(db: Db, id: string): { writes: number; freed: number } {
  const freed = storedFootprint(db, id);
  const a = db.sql.exec("DELETE FROM event_tags WHERE event_id = ?", id);
  a.toArray();
  const b = db.sql.exec("DELETE FROM events WHERE id = ?", id);
  b.toArray();
  return { writes: Math.max(1, a.rowsWritten + b.rowsWritten), freed };
}

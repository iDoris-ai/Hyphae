import { type Db, deleteEvent, footprint, hasEvent, indexedTags, storageFull, storedFootprint } from "./db";
import { applyDeletion, ensureCounters, isBlocked } from "./deletion";
import { type NostrEvent, dTag, isAddressable, isReplaceable, utf8Length } from "./nostr";

export type StoreResult =
  | "ok"
  | "duplicate"
  | "stale"
  | "full"
  | "ok_partial"
  | "blocked"
  | "unavailable"
;

/** Thrown inside a transaction to roll it back when it would grow a full database. */
class FullError extends Error {}

/**
 * Stores one event in a single transaction. Replaceable and addressable kinds keep only the newest
 * version. When the database is over its size cap the decision is by net growth (estimated bytes):
 * a write that makes a full database bigger is refused, one that does not (a same-size or smaller
 * replacement) is allowed. SQL errors are thrown to the caller, which answers the client.
 */
export function storeEvent(db: Db, e: NostrEvent): StoreResult {
  // A deletion request enforces the table caps through the counters: do not run it on untrusted ones.
  if (e.kind === 5 && !ensureCounters(db)) return "unavailable";
  try {
    return db.tx(() => storeInTx(db, e));
  } catch (err) {
    if (err instanceof FullError) return "full";
    throw err;
  }
}

function storeInTx(db: Db, e: NostrEvent): StoreResult {
  const { sql } = db;
  if (hasEvent(db, e.id)) return "duplicate";
  const versioned = isReplaceable(e.kind) || isAddressable(e.kind);
  const d = isAddressable(e.kind) ? dTag(e) : "";
  if (e.kind !== 5 && isBlocked(db, e, d)) return "blocked"; // deletion requests themselves are never blocked
  const json = JSON.stringify(e);
  const added = footprint(utf8Length(json), e.tags, db.lim.maxIndexedTags);
  if (versioned) {
    const old = sql
      .exec("SELECT id, created_at FROM events WHERE pubkey = ? AND kind = ? AND d = ?", e.pubkey, e.kind, d)
      .toArray();
    for (const o of old) {
      const oCreated = o.created_at as number;
      const oId = o.id as string;
      if (oCreated > e.created_at || (oCreated === e.created_at && oId < e.id)) return "stale";
    }
    let freed = 0;
    for (const o of old) freed += storedFootprint(db, o.id as string);
    if (storageFull(db) && added - freed > 0) return "full";
    for (const o of old) deleteEvent(db, o.id as string);
  } else if (e.kind !== 5 && storageFull(db)) {
    return "full";
  }
  let partial = false;
  if (e.kind === 5) {
    const res = applyDeletion(db, e);
    partial = res.partial;
    // Net growth of a deletion request = its own rows + new tombstones/cutoffs - what it freed.
    if (storageFull(db) && added + res.added - res.freed > 0) throw new FullError();
  }
  sql.exec(
    "INSERT INTO events (id, pubkey, created_at, kind, d, json) VALUES (?, ?, ?, ?, ?, ?)",
    e.id, e.pubkey, e.created_at, e.kind, d, json,
  );
  for (const [name, value] of indexedTags(e.tags, db.lim.maxIndexedTags)) {
    sql.exec("INSERT INTO event_tags (event_id, name, value) VALUES (?, ?, ?)", e.id, name, value);
  }
  return partial ? "ok_partial" : "ok";
}

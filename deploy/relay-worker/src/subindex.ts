import { type Filter, filterIsEmpty } from "./filter";
import type { NostrEvent } from "./nostr";

const WILDCARD = "*";
/** A subscription needing more bucket keys than this is filed once under the wildcard, which bounds index memory (20 000 rows x 16 refs at most). */
const MAX_KEYS_PER_SUB = 16;

/**
 * The bucket keys a filter is filed under: ONE constraining dimension (one key per value), or the
 * wildcard. Any single constraint is enough for correctness because candidates are re-checked
 * with the full matcher; the choice only decides how selective the lookup is. Authors count only
 * when every entry is a full 64-hex key (a shorter prefix cannot be looked up by equality).
 */
function keysOfFilter(f: Filter): string[] {
  if (filterIsEmpty(f)) return [];
  if (f.authors && f.authors.every((a) => a.length === 64)) return f.authors.map((a) => "a:" + a);
  for (const t of ["p", "e"] as const) {
    const v = f[`#${t}`];
    if (v) return v.map((x) => `${t}:${x}`);
  }
  if (f.kinds) return f.kinds.map((k) => "k:" + k);
  return [WILDCARD];
}

/**
 * In-memory candidate index over the persisted subscriptions: bucket key -> "conn\0subId" refs.
 * It only narrows which subscriptions an event is compared with; matching semantics stay with
 * matchAny. Filters that constrain none of authors / #p / #e / kinds go to the wildcard bucket.
 */
export class SubIndex {
  private buckets = new Map<string, Set<string>>();
  private keys = new Map<string, string[]>();
  private byConn = new Map<string, Set<string>>();

  add(conn: string, subId: string, filters: Filter[]) {
    this.remove(conn, subId);
    const ref = conn + "\0" + subId;
    let keys = [...new Set(filters.flatMap(keysOfFilter))];
    if (keys.length > MAX_KEYS_PER_SUB) keys = [WILDCARD]; // still found as a candidate; matchAny decides
    for (const k of keys) {
      let b = this.buckets.get(k);
      if (!b) this.buckets.set(k, (b = new Set()));
      b.add(ref);
    }
    this.keys.set(ref, keys);
    let c = this.byConn.get(conn);
    if (!c) this.byConn.set(conn, (c = new Set()));
    c.add(subId);
  }

  remove(conn: string, subId: string) {
    const ref = conn + "\0" + subId;
    for (const k of this.keys.get(ref) ?? []) {
      const b = this.buckets.get(k);
      b?.delete(ref);
      if (b?.size === 0) this.buckets.delete(k);
    }
    this.keys.delete(ref);
    const c = this.byConn.get(conn);
    c?.delete(subId);
    if (c?.size === 0) this.byConn.delete(conn);
  }

  removeConn(conn: string) {
    for (const subId of [...(this.byConn.get(conn) ?? [])]) this.remove(conn, subId);
  }

  /** Refs that could match the event (duplicates possible when it hits several buckets). Lazy, so a work cap can stop early. */
  *candidates(e: NostrEvent): Generator<string> {
    const lookups = ["a:" + e.pubkey, "k:" + e.kind];
    for (const t of e.tags) if ((t[0] === "p" || t[0] === "e") && t[1] !== undefined) lookups.push(`${t[0]}:${t[1]}`);
    lookups.push(WILDCARD);
    for (const k of lookups) yield* this.buckets.get(k) ?? [];
  }
}

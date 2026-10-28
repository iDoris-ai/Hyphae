import type { Db } from "./db";
import { type Filter, filterIsEmpty } from "./filter";
import { utf8Length } from "./nostr";

export interface QueryResult {
  events: string[]; // stored event JSON, ready to send
  truncated: boolean; // matching history was left out because of the count/byte caps
}

/** `col BETWEEN lo AND hi` for hex prefixes: a range scan on the primary index instead of LIKE. */
function prefixRange(col: string, list: string[], where: string[], params: (string | number)[]) {
  where.push("(" + list.map(() => `${col} BETWEEN ? AND ?`).join(" OR ") + ")");
  for (const p of list) params.push(p.padEnd(64, "0"), p.padEnd(64, "f"));
}

function buildSql(f: Filter, limit: number): { sql: string; params: (string | number)[] } {
  const where: string[] = [];
  const params: (string | number)[] = [];
  if (f.ids) prefixRange("id", f.ids, where, params);
  if (f.authors) prefixRange("pubkey", f.authors, where, params);
  if (f.kinds) {
    where.push(`kind IN (${f.kinds.map(() => "?").join(",")})`);
    params.push(...f.kinds);
  }
  if (f.since !== undefined) {
    where.push("created_at >= ?");
    params.push(f.since);
  }
  if (f.until !== undefined) {
    where.push("created_at <= ?");
    params.push(f.until);
  }
  for (const [k, v] of Object.entries(f)) {
    if (!k.startsWith("#") || k.length !== 2 || !Array.isArray(v)) continue;
    where.push(`id IN (SELECT event_id FROM event_tags WHERE name = ? AND value IN (${v.map(() => "?").join(",")}))`);
    params.push(k[1], ...(v as string[]));
  }
  const sql =
    "SELECT id, json FROM events" +
    (where.length ? " WHERE " + where.join(" AND ") : "") +
    " ORDER BY created_at DESC, id ASC LIMIT ?";
  return { sql, params: [...params, limit] };
}

/**
 * Stored events matching the filters. Filters are read in order through a cursor, newest first
 * within each filter, and reading stops as soon as the total reaches maxLimit events or
 * maxResultBytes. `truncated` is true when matching history was left out because of those caps
 * (not because of the client's own `limit`).
 */
export function runQuery(db: Db, filters: Filter[]): QueryResult {
  const seen = new Set<string>();
  const events: string[] = [];
  let bytes = 0;
  for (const f of filters) {
    if (filterIsEmpty(f)) continue; // an empty list can never match
    const maxLimit = db.lim.maxLimit;
    const eff = Math.max(0, Math.min(f.limit ?? maxLimit, maxLimit));
    if (eff === 0) continue;
    // The client asked for more than we serve (or gave no limit): more history may exist.
    const capped = f.limit === undefined || f.limit > maxLimit;
    const { sql, params } = buildSql(f, eff + 1);
    let n = 0;
    for (const row of db.sql.exec(sql, ...params)) {
      if (n >= eff) {
        if (capped) return { events, truncated: true };
        break;
      }
      n++;
      const id = row.id as string;
      if (seen.has(id)) continue;
      const json = row.json as string;
      const size = utf8Length(json);
      if (events.length >= maxLimit || bytes + size > db.lim.maxResultBytes) return { events, truncated: true };
      seen.add(id);
      events.push(json);
      bytes += size;
    }
  }
  return { events, truncated: false };
}

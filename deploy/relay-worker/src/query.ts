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

interface Row {
  id: string;
  created_at: number;
  json: string;
  size: number;
}
const newestFirst = (a: Row, b: Row) => b.created_at - a.created_at || (a.id < b.id ? -1 : a.id > b.id ? 1 : 0);

/** The first "#x" key of a filter: it drives the scan through the (name, value, created_at, event_id) index. */
const driverKey = (f: Filter): string | undefined => Object.keys(f).find((k) => k.length === 2 && k[0] === "#" && Array.isArray((f as Record<string, unknown>)[k]));

/**
 * SQL for a filter whose driver tag (if any) has at most one value, so one index walk returns rows
 * already newest first and LIMIT stops it early (no sort of every row for that tag).
 */
export function buildSql(f: Filter, limit: number): { sql: string; params: (string | number)[] } {
  const where: string[] = [];
  const params: (string | number)[] = [];
  const dk = driverKey(f);
  const tagged = dk !== undefined;
  const time = tagged ? "t.created_at" : "created_at";
  const ev = tagged ? "e." : "";
  if (tagged) {
    where.push("t.name = ? AND t.value = ?");
    params.push(dk[1], (f as Record<string, string[]>)[dk][0]);
  }
  if (f.ids) prefixRange(ev + "id", f.ids, where, params);
  if (f.authors) prefixRange(ev + "pubkey", f.authors, where, params);
  if (f.kinds) {
    where.push(`${ev}kind IN (${f.kinds.map(() => "?").join(",")})`);
    params.push(...f.kinds);
  }
  if (f.since !== undefined) {
    where.push(time + " >= ?");
    params.push(f.since);
  }
  if (f.until !== undefined) {
    where.push(time + " <= ?");
    params.push(f.until);
  }
  for (const [k, v] of Object.entries(f)) {
    if (!k.startsWith("#") || k.length !== 2 || !Array.isArray(v) || k === dk) continue;
    where.push(`EXISTS (SELECT 1 FROM event_tags x WHERE x.event_id = ${ev}id AND x.name = ? AND x.value IN (${v.map(() => "?").join(",")}))`);
    params.push(k[1], ...(v as string[]));
  }
  const w = where.length ? " WHERE " + where.join(" AND ") : "";
  const sql = tagged
    ? "SELECT e.id AS id, e.created_at AS created_at, e.json AS json FROM event_tags t INDEXED BY tags_time CROSS JOIN events e ON e.id = t.event_id" +
      w + " ORDER BY t.created_at DESC, t.event_id ASC LIMIT ?"
    : "SELECT id, created_at, json FROM events" + w + " ORDER BY created_at DESC, id ASC LIMIT ?";
  return { sql, params: [...params, limit] };
}

/** A filter whose driver tag lists several values becomes one single-value filter per value. */
function expand(f: Filter): Filter[] {
  const dk = driverKey(f);
  const vals = dk ? (f as Record<string, string[]>)[dk] : [];
  if (!dk || vals.length <= 1) return [f];
  return [...new Set(vals)].map((v) => ({ ...f, [dk]: [v] }) as Filter);
}

/**
 * Stored events matching the filters, newest first, de-duplicated. Each filter is read on its own
 * with LIMIT (its limit + 1), so the rows read are bounded by the sum of the per-filter limits (a
 * multi-value driver tag costs one such read per value). A read also stops at the byte cap, and a
 * filter keeps only its best rows (at most maxResultBytes) while its units merge.
 * The filters' rows are then merged by (created_at DESC, id ASC) and cut at maxLimit events or
 * maxResultBytes. `truncated` is true when matching history was left out because of those caps (not
 * because of the client's own `limit`).
 */
export function runQuery(db: Db, filters: Filter[]): QueryResult {
  const maxLimit = db.lim.maxLimit;
  const all = new Map<string, Row>();
  let truncated = false;
  let boundary: Row | undefined; // newest row any filter could not fit: the merged result stops before it
  for (const f of filters) {
    if (filterIsEmpty(f)) continue; // an empty list can never match
    const eff = Math.max(0, Math.min(f.limit ?? maxLimit, maxLimit));
    if (eff === 0) continue;
    // The client asked for more than we serve (or gave no limit): more history may exist.
    const capped = f.limit === undefined || f.limit > maxLimit;
    // The filter's best rows so far (at most eff, at most maxResultBytes), merged unit by unit so
    // memory never holds more than that plus one unit's read.
    let group: Row[] = [];
    let dropped: Row | undefined; // newest row that did not fit the byte cap: nothing older may be used
    let over = false; // rows beyond eff exist (monotone: later units only push them further down)
    const noteDrop = (r: Row) => {
      if (!dropped || newestFirst(r, dropped) < 0) dropped = r;
    };
    for (const unit of expand(f)) {
      const { sql, params } = buildSql(unit, eff + 1);
      const got = new Map<string, Row>(group.map((r) => [r.id, r]));
      let bytes = 0;
      let n = 0;
      for (const r of db.sql.exec(sql, ...params)) {
        const json = r.json as string;
        const row = { id: r.id as string, created_at: r.created_at as number, json, size: utf8Length(json) };
        // The (eff+1)th row is only a probe for "more exists": it is never sent, so it has no byte check.
        if (n++ < eff && bytes + row.size > db.lim.maxResultBytes) {
          noteDrop(row); // nothing at or past this row can fit in the response
          break;
        }
        bytes += row.size;
        got.set(row.id, row);
      }
      let merged = [...got.values()].sort(newestFirst);
      if (dropped) merged = merged.filter((r) => newestFirst(r, dropped as Row) < 0);
      if (merged.length > eff) over = true;
      group = [];
      let total = 0;
      for (const r of merged.slice(0, eff)) {
        if (total + r.size > db.lim.maxResultBytes) {
          noteDrop(r);
          break;
        }
        total += r.size;
        group.push(r);
      }
    }
    // The dropped row only matters if it would have been among the filter's newest `eff`.
    if (dropped && (group.length < eff || newestFirst(dropped, group[group.length - 1]) < 0)) {
      truncated = true;
      if (!boundary || newestFirst(dropped, boundary) < 0) boundary = dropped;
    } else if (dropped && capped) {
      truncated = true; // a matching row beyond the count cap that was also too big to read
    }
    if (capped && over) truncated = true;
    for (const r of group) all.set(r.id, r);
  }
  const events: string[] = [];
  let bytes = 0;
  for (const r of [...all.values()].sort(newestFirst)) {
    if (boundary && newestFirst(r, boundary) >= 0) return { events, truncated: true };
    if (events.length >= maxLimit || bytes + r.size > db.lim.maxResultBytes) return { events, truncated: true };
    events.push(r.json);
    bytes += r.size;
  }
  return { events, truncated };
}

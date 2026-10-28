import { LIMITS, type Limits } from "./config";
import type { NostrEvent } from "./nostr";

export interface Filter {
  ids?: string[];
  authors?: string[];
  kinds?: number[];
  since?: number;
  until?: number;
  limit?: number;
  [tag: `#${string}`]: string[] | undefined;
}

/** The only tag filters the relay understands: a single ASCII letter after the #. */
export const TAG_KEY = /^#[A-Za-z]$/;

/**
 * Unknown keys (for example NIP-50 "search") are ignored, as NIP-01 says, and dropped by
 * normalizeFilter. A "#xx" key is rejected: ignoring it would silently widen the filter.
 */
export function validateFilter(f: unknown, lim: Pick<Limits, "maxFilterBindings"> = LIMITS): string | null {
  if (typeof f !== "object" || f === null || Array.isArray(f)) return "invalid: filter must be an object";
  for (const [k, v] of Object.entries(f as Record<string, unknown>)) {
    if (k === "ids" || k === "authors") {
      if (!Array.isArray(v) || v.some((x) => typeof x !== "string" || !/^[0-9a-f]{1,64}$/.test(x))) return `invalid: bad ${k}`;
    } else if (k === "kinds") {
      if (!Array.isArray(v) || v.some((x) => !Number.isInteger(x))) return "invalid: bad kinds";
    } else if (k === "since" || k === "until" || k === "limit") {
      if (!Number.isInteger(v)) return `invalid: bad ${k}`;
    } else if (k.startsWith("#")) {
      if (!TAG_KEY.test(k)) return "invalid: unsupported tag filter";
      if (!Array.isArray(v) || v.some((x) => typeof x !== "string")) return `invalid: bad ${k}`;
    }
  }
  if (countBindings(f as Filter) > lim.maxFilterBindings) return "invalid: filter too large";
  return null;
}

/** Number of SQL bind values a filter needs (SQLite in Durable Objects allows 100). */
function countBindings(f: Filter): number {
  let n = 0;
  for (const [k, v] of Object.entries(f)) {
    if (!Array.isArray(v)) continue;
    if (k === "ids" || k === "authors") n += v.length * 2; // BETWEEN lo AND hi
    else if (k === "kinds") n += v.length;
    else if (k.startsWith("#") && k.length === 2) n += v.length + 1; // values + tag name
  }
  return n;
}

/** True when the filter has an empty ids/authors/kinds/#x list, i.e. it can never match anything. */
export function filterIsEmpty(f: Filter): boolean {
  for (const [k, v] of Object.entries(f)) {
    const listKey = k === "ids" || k === "authors" || k === "kinds" || (k.startsWith("#") && k.length === 2);
    if (listKey && Array.isArray(v) && v.length === 0) return true;
  }
  return false;
}

const matchesPrefix = (list: string[], value: string) => list.some((p) => value.startsWith(p));

export function matchFilter(f: Filter, e: NostrEvent): boolean {
  if (f.ids && !matchesPrefix(f.ids, e.id)) return false;
  if (f.authors && !matchesPrefix(f.authors, e.pubkey)) return false;
  if (f.kinds && !f.kinds.includes(e.kind)) return false;
  if (f.since !== undefined && e.created_at < f.since) return false;
  if (f.until !== undefined && e.created_at > f.until) return false;
  for (const [k, v] of Object.entries(f)) {
    if (!k.startsWith("#") || k.length !== 2 || !Array.isArray(v)) continue;
    const name = k[1];
    if (!e.tags.some((t) => t[0] === name && v.includes(t[1]))) return false;
  }
  return true;
}

export function matchAny(filters: Filter[], e: NostrEvent): boolean {
  return filters.some((f) => matchFilter(f, e));
}

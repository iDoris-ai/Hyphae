import { schnorr } from "@noble/curves/secp256k1";
import { sha256 } from "@noble/hashes/sha2";
import { bytesToHex, hexToBytes, utf8ToBytes } from "@noble/hashes/utils";

export interface NostrEvent {
  id: string;
  pubkey: string;
  created_at: number;
  kind: number;
  tags: string[][];
  content: string;
  sig: string;
}

export interface Filter {
  ids?: string[];
  authors?: string[];
  kinds?: number[];
  since?: number;
  until?: number;
  limit?: number;
  [tag: `#${string}`]: string[] | undefined;
}

const MB = 1024 * 1024;

export const LIMITS = {
  // --- per message / event
  maxMessageBytes: 512_000, // UTF-8 bytes per frame (same as the old khatru relay)
  maxEventTags: 256,
  maxIndexedTags: 64, // distinct single-letter tags that are indexed per event
  maxFutureSeconds: 15 * 60,
  // --- subscriptions / queries
  maxSubsPerConn: 20,
  maxFiltersPerReq: 10,
  maxLimit: 500,
  maxSubIdLength: 64,
  maxFilterBindings: 90, // SQLite in Durable Objects allows 100 bound values
  maxSubBytes: 4096, // serialized filters of one subscription
  maxSubBytesPerConn: 16 * 1024,
  maxSubRows: 20_000, // subscription rows across the whole relay
  subCacheBytes: 4 * MB, // in-memory cache of subscriptions; the rest is read from SQL
  maxResultBytes: 4 * MB, // total size of the stored events one REQ returns
  // --- rate limits (token buckets, capacity = one minute)
  eventsPerMinute: 120, // per connection
  reqsPerMinute: 60, // per connection
  framesPerMinute: 300, // per connection, every frame (also malformed / unknown / CLOSE)
  maxViolations: 50, // rejected frames within a minute before the connection is closed (1008)
  sourceEventsPerMinute: 240, // per source address, summed over its connections
  sourceReqsPerMinute: 120,
  upgradesPerMinutePerSource: 10,
  maxConnsPerSource: 8,
  maxSourceEntries: 10_000, // LRU size of the per-source table
  globalEventWeightPerMinute: 30_000, // weighted: 1 + tags/16 + bytes/4096 per event
  // --- capacity
  maxConnections: 2000,
  maxStorageBytes: 5 * 1024 * MB,
  maxTombstones: 1_000_000,
  maxCoordCutoffs: 1_000_000,
  // --- deletion requests (NIP-09)
  maxDeletionTargets: 100, // e/a targets processed per kind 5
  maxDeletionRowWrites: 5000,
  // --- fan-out
  maxDeliveriesPerEvent: 5000,
  globalOutboundBytesPerMinute: 64 * MB,
  socketOutboundBytesPerMinute: 16 * MB,
};

export type Limits = { [K in keyof typeof LIMITS]: number };

/** Cost of one EVENT for the global bucket: grows with tag count and size. */
export const eventWeight = (tagCount: number, bytes: number) => 1 + (tagCount >> 4) + (bytes >> 12);

const HEX64 = /^[0-9a-f]{64}$/;
const HEX128 = /^[0-9a-f]{128}$/;

export function serializeEvent(e: NostrEvent): string {
  return JSON.stringify([0, e.pubkey, e.created_at, e.kind, e.tags, e.content]);
}

export function eventId(e: NostrEvent): string {
  return bytesToHex(sha256(utf8ToBytes(serializeEvent(e))));
}

/** Returns an error string, or null when the event is well-formed and correctly signed. */
export function validateEvent(e: unknown, now = Math.floor(Date.now() / 1000)): string | null {
  if (typeof e !== "object" || e === null) return "invalid: event must be an object";
  const ev = e as Record<string, unknown>;
  if (typeof ev.id !== "string" || !HEX64.test(ev.id)) return "invalid: bad id";
  if (typeof ev.pubkey !== "string" || !HEX64.test(ev.pubkey)) return "invalid: bad pubkey";
  if (typeof ev.sig !== "string" || !HEX128.test(ev.sig)) return "invalid: bad sig";
  if (!Number.isInteger(ev.created_at) || (ev.created_at as number) < 0) return "invalid: bad created_at";
  if (!Number.isInteger(ev.kind) || (ev.kind as number) < 0 || (ev.kind as number) > 65535) return "invalid: bad kind";
  if (typeof ev.content !== "string") return "invalid: bad content";
  if (!Array.isArray(ev.tags) || ev.tags.length > LIMITS.maxEventTags) return "invalid: bad tags";
  for (const t of ev.tags) {
    if (!Array.isArray(t) || t.length === 0 || t.some((x) => typeof x !== "string")) return "invalid: bad tags";
  }
  const event = e as NostrEvent;
  if ((event.created_at as number) > now + LIMITS.maxFutureSeconds) return "invalid: created_at too far in the future";
  if (eventId(event) !== event.id) return "invalid: id does not match event content";
  let ok = false;
  try {
    ok = schnorr.verify(hexToBytes(event.sig), hexToBytes(event.id), hexToBytes(event.pubkey));
  } catch {
    ok = false;
  }
  return ok ? null : "invalid: bad signature";
}

export function validateFilter(f: unknown): string | null {
  if (typeof f !== "object" || f === null || Array.isArray(f)) return "invalid: filter must be an object";
  for (const [k, v] of Object.entries(f as Record<string, unknown>)) {
    if (k === "ids" || k === "authors") {
      if (!Array.isArray(v) || v.some((x) => typeof x !== "string" || !/^[0-9a-f]{1,64}$/.test(x))) return `invalid: bad ${k}`;
    } else if (k === "kinds") {
      if (!Array.isArray(v) || v.some((x) => !Number.isInteger(x))) return "invalid: bad kinds";
    } else if (k === "since" || k === "until" || k === "limit") {
      if (!Number.isInteger(v)) return `invalid: bad ${k}`;
    } else if (k.startsWith("#")) {
      if (!Array.isArray(v) || v.some((x) => typeof x !== "string")) return `invalid: bad ${k}`;
    }
  }
  if (countBindings(f as Filter) > LIMITS.maxFilterBindings) return "invalid: filter too large";
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

const TAG_KEY = /^#[A-Za-z]$/;

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
export function filterKey(f: Filter): string {
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

/** Event id of an oversized `["EVENT",{...}]` frame, read from its first or last bytes only (no full parse). */
export function peekEventId(message: string): string | null {
  const m = /^\s*\[\s*"EVENT"\s*,\s*\{[^]{0,4000}?"id"\s*:\s*"([0-9a-f]{64})"/.exec(message.slice(0, 4200));
  if (m) return m[1];
  // Some clients put "id" and "sig" last, after a large content field.
  const t = /"id"\s*:\s*"([0-9a-f]{64})"\s*,\s*"sig"\s*:\s*"[0-9a-f]{128}"\s*\}\s*\]\s*$/.exec(message.slice(-400));
  return t ? t[1] : null;
}

/** The (name, value) pairs of single-letter tags that get indexed: distinct, at most maxIndexedTags. */
export function indexedTags(tags: string[][]): [string, string][] {
  const seen = new Set<string>();
  const out: [string, string][] = [];
  for (const t of tags) {
    if (t[0].length !== 1 || t[1] === undefined) continue;
    const key = t[0] + "\0" + t[1];
    if (seen.has(key)) continue;
    seen.add(key);
    out.push([t[0], t[1]]);
    if (out.length >= LIMITS.maxIndexedTags) break;
  }
  return out;
}

export interface Bucket {
  tokens: number;
  last: number;
}

/**
 * Constant-space token bucket (capacity = perMinute, refilled continuously).
 * Pure: a rejected attempt returns the bucket unchanged, so rejects add no state.
 */
export function takeToken(b: Bucket | undefined, now: number, perMinute: number, cost = 1): { ok: boolean; bucket: Bucket } {
  const cur = b ?? { tokens: perMinute, last: now };
  const elapsed = Math.max(0, now - cur.last);
  const tokens = Math.min(perMinute, cur.tokens + (elapsed / 60_000) * perMinute);
  if (tokens >= cost) return { ok: true, bucket: { tokens: tokens - cost, last: now } };
  return { ok: false, bucket: cur };
}

const encoder = new TextEncoder();
/** UTF-8 byte length of a string. */
export function utf8Length(s: string): number {
  return encoder.encode(s).length;
}

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

function matchesPrefix(list: string[], value: string): boolean {
  return list.some((p) => value.startsWith(p));
}

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

export const isEphemeral = (kind: number) => kind >= 20000 && kind < 30000;
export const isReplaceable = (kind: number) => kind === 0 || kind === 3 || (kind >= 10000 && kind < 20000);
export const isAddressable = (kind: number) => kind >= 30000 && kind < 40000;
export const dTag = (e: NostrEvent) => e.tags.find((t) => t[0] === "d")?.[1] ?? "";

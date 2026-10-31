import type { Limits } from "./config";

export interface Bucket {
  tokens: number;
  last: number;
}

/**
 * Constant-space token bucket (capacity = perMinute, refilled continuously).
 * Pure: a rejected attempt returns the bucket unchanged, so rejects add no state.
 */
export function takeToken(b: Bucket | undefined, now: number, perMinute: number, cost = 1): { ok: boolean; bucket: Bucket } {
  const cur = b ?? { tokens: perMinute, last: Number.isFinite(now) ? now : 0 };
  // A zero, negative or non-finite cost would refund or poison the bucket; a non-finite clock likewise.
  if (!(cost > 0) || !Number.isFinite(cost) || !Number.isFinite(now)) return { ok: false, bucket: cur };
  const elapsed = Math.max(0, now - cur.last);
  const tokens = Math.min(perMinute, cur.tokens + (elapsed / 60_000) * perMinute);
  // `last` never moves backwards, so a clock that steps back cannot make the same interval refill twice.
  if (tokens >= cost) return { ok: true, bucket: { tokens: tokens - cost, last: Math.max(cur.last, now) } };
  return { ok: false, bucket: cur };
}

/** Cost of one EVENT for the event-weight buckets: grows with the number of tag strings and with size. */
export const eventWeight = (tagStrings: number, bytes: number) => 1 + Math.floor(tagStrings / 32) + Math.floor(bytes / 4096);

/**
 * Key used for per-source limits: IPv4 as is, IPv6 grouped by /64 (one host owns a whole /64),
 * IPv4-mapped IPv6 as the IPv4 address. Anything unparsable is used unchanged (truncated).
 */
export function sourceKey(ip: string): string {
  return parseSourceKey(String(ip).slice(0, 256)).slice(0, 64) || "unknown"; // empty maps to one fixed key
}

function parseSourceKey(ip: string): string {
  const s = ip.trim().toLowerCase().split("%")[0];
  if (!s.includes(":")) return s.slice(0, 64);
  let head = s;
  const dotted = /^(.*:)(\d+\.\d+\.\d+\.\d+)$/.exec(s);
  if (dotted) {
    const p = dotted[2].split(".").map(Number);
    if (p.some((n) => !(n >= 0 && n <= 255))) return s.slice(0, 64);
    head = dotted[1] + ((p[0] << 8) | p[1]).toString(16) + ":" + ((p[2] << 8) | p[3]).toString(16);
  }
  const halves = head.split("::");
  if (halves.length > 2) return s.slice(0, 64);
  const left = halves[0] ? halves[0].split(":") : [];
  let groups = left;
  if (halves.length === 2) {
    const right = halves[1] ? halves[1].split(":") : [];
    const fill = 8 - left.length - right.length;
    if (fill < 1) return s.slice(0, 64);
    groups = [...left, ...Array<string>(fill).fill("0"), ...right];
  }
  if (groups.length !== 8 || groups.some((g) => !/^[0-9a-f]{1,4}$/.test(g))) return s.slice(0, 64);
  const n = groups.map((g) => parseInt(g, 16));
  if (n.slice(0, 5).every((x) => x === 0) && n[5] === 0xffff) return `${n[6] >> 8}.${n[6] & 255}.${n[7] >> 8}.${n[7] & 255}`;
  return n.slice(0, 4).map((x) => x.toString(16)).join(":") + "::/64";
}

/**
 * Per-connection state kept in the WebSocket attachment (survives hibernation, well under 2KB).
 * The rate-limit buckets live here so waking from hibernation does not reset them.
 * A client can still reset its buckets by reconnecting; that is bounded by the per-source
 * connection/upgrade limits, the global connection cap and the global storage cap.
 */
export interface ConnState {
  id: string;
  src: string; // source address, used to count connections per source
  ev?: Bucket; // EVENT frames
  rq?: Bucket; // REQ frames
  fr?: Bucket; // every frame
  bad?: number; // rejected frames in the current window
  badPrev?: number; // rejected frames in the window before it (weighted down as the current one ages)
  badAt?: number; // current window start
  lastSeen?: number; // ms timestamp of the last frame the client sent (set at upgrade, refreshed per frame)
  closed?: boolean; // we initiated a close: ignore everything the client still sends
}

/** Takes a token from one of the connection's own buckets (the caller persists `st`). Rejects change nothing. */
export function takeConn(st: ConnState, which: "ev" | "rq" | "fr", perMinute: number, now = Date.now()): boolean {
  const r = takeToken(st[which], now, perMinute);
  if (r.ok) st[which] = r.bucket;
  return r.ok;
}

/** Counts a rejected frame; returns true when the connection keeps misbehaving and should be closed (1008). */
export function countViolation(st: ConnState, max: number, now = Date.now()): boolean {
  // Two-bucket sliding approximation: windows are half-open [start, start+60s), so exactly 60s later
  // is already the next window, and the previous window still counts in proportion to its overlap.
  if (st.badAt === undefined) {
    st.badAt = now;
    st.bad = 0;
    st.badPrev = 0;
  }
  const elapsed = Math.max(0, now - st.badAt);
  if (elapsed >= 60_000) {
    const steps = Math.floor(elapsed / 60_000);
    st.badPrev = steps === 1 ? (st.bad ?? 0) : 0;
    st.bad = 0;
    st.badAt += steps * 60_000;
  }
  st.bad = (st.bad ?? 0) + 1;
  const weight = 1 - Math.max(0, now - st.badAt) / 60_000;
  return st.bad + (st.badPrev ?? 0) * weight >= max;
}

interface SourceState {
  up?: Bucket; // upgrades
  ev?: Bucket; // EVENT frames
  rq?: Bucket; // REQ frames
  wr?: Bucket; // stored-event bytes
  ew?: Bucket; // event weight
  out?: Bucket; // outbound bytes delivered to this source's sockets
  hist?: Bucket; // history bytes sent to this source's sockets
}

interface SockOut {
  b?: Bucket;
  h?: Bucket; // history bytes
  since: number; // start of the current over-quota episode
  last: number; // last dropped delivery
}

/** In-memory abuse controls shared by all connections (lost when the object is evicted). */
export class Budgets {
  private sources = new Map<string, SourceState>(); // LRU, bounded by maxSourceEntries
  private globalEv?: Bucket;
  private globalOut?: Bucket;
  private globalHist?: Bucket;
  private sockOut = new Map<string, SockOut>();

  constructor(private lim: Limits) {}

  /** Per-source state in a bounded LRU (most recently used last). */
  private source(src: string): SourceState {
    let s = this.sources.get(src);
    if (s) this.sources.delete(src);
    else s = {};
    this.sources.set(src, s);
    if (this.sources.size > this.lim.maxSourceEntries) {
      const oldest = this.sources.keys().next().value;
      if (oldest !== undefined) this.sources.delete(oldest);
    }
    return s;
  }

  /** EVENT / REQ frames summed over all connections of one source. */
  takeSource(src: string, which: "ev" | "rq"): boolean {
    const perMinute = which === "ev" ? this.lim.sourceEventsPerMinute : this.lim.sourceReqsPerMinute;
    const s = this.source(src);
    const r = takeToken(s[which], Date.now(), perMinute);
    if (r.ok) s[which] = r.bucket;
    return r.ok;
  }

  /** A WebSocket upgrade; the caller checks the concurrent-connection limit first so refusals cost nothing. */
  takeUpgrade(src: string): boolean {
    const s = this.source(src);
    const r = takeToken(s.up, Date.now(), this.lim.upgradesPerMinutePerSource);
    if (r.ok) s.up = r.bucket;
    return r.ok;
  }

  /**
   * Charges the cost of looking at a frame (hashing and signature verification) to its source's
   * share of the event weight, before anything is parsed or looked up. Replays and bad signatures
   * pay it too. Only the source's own budget is touched, never the global one.
   */
  chargeValidation(src: string, weight: number): boolean {
    const s = this.source(src);
    const r = takeToken(s.ew, Date.now(), this.lim.sourceEventWeightPerMinute, weight);
    if (r.ok) s.ew = r.bucket;
    return r.ok;
  }

  /**
   * Charges one validated event: the source's stored bytes (0 for ephemeral events) and the global
   * weighted budget. Both are checked before either is charged. "source" is the sender's fault,
   * "global" is not.
   */
  chargeEvent(src: string, storedBytes: number, weight: number): "ok" | "source" | "global" {
    const now = Date.now();
    const s = this.source(src);
    let wr;
    if (storedBytes > 0) {
      wr = takeToken(s.wr, now, this.lim.sourceWriteBytesPerMinute, storedBytes);
      if (!wr.ok) return "source";
    }
    const g = takeToken(this.globalEv, now, this.lim.globalEventWeightPerMinute, weight);
    if (!g.ok) return "global";
    this.globalEv = g.bucket;
    if (wr) s.wr = wr.bucket;
    return "ok";
  }

  /**
   * Charges one outbound delivery to the receiving source, the global budget and the socket,
   * all or nothing. "source" and "socket" drop just this delivery, "global" ends the event's fan-out.
   */
  chargeDelivery(src: string, conn: string, cost: number): "ok" | "source" | "global" | "socket" {
    const now = Date.now();
    const s = this.source(src);
    const k = this.sock(conn); // created before any refusal so overQuota can see a socket only the source quota refused
    const so = takeToken(s.out, now, this.lim.sourceOutboundBytesPerMinute, cost);
    if (!so.ok) return "source";
    const g = takeToken(this.globalOut, now, this.lim.globalOutboundBytesPerMinute, cost);
    if (!g.ok) return "global";
    const sk = takeToken(k.b, now, this.lim.socketOutboundBytesPerMinute, cost);
    if (!sk.ok) return "socket";
    s.out = so.bucket;
    this.globalOut = g.bucket;
    k.b = sk.bucket;
    return "ok";
  }

  private sock(conn: string): SockOut {
    let k = this.sockOut.get(conn);
    if (k) this.sockOut.delete(conn);
    else k = { since: 0, last: 0 };
    this.sockOut.set(conn, k); // LRU: most recently used last
    // Refused deliveries create entries too and a close path may be missed, so the table is bounded.
    if (this.sockOut.size > this.lim.maxSourceEntries) {
      const oldest = this.sockOut.keys().next().value;
      if (oldest !== undefined) this.sockOut.delete(oldest);
    }
    return k;
  }

  /**
   * Charges history sent for a REQ to the history budgets of the receiving source, the global
   * history budget and the socket, all or nothing. These are separate from the live-delivery
   * budgets above, so a big pull cannot make the socket miss live events.
   */
  chargeHistory(src: string, conn: string, cost: number): boolean {
    const now = Date.now();
    const s = this.source(src);
    const so = takeToken(s.hist, now, this.lim.sourceHistoryBytesPerMinute, cost);
    if (!so.ok) return false;
    const g = takeToken(this.globalHist, now, this.lim.globalHistoryBytesPerMinute, cost);
    if (!g.ok) return false;
    const k = this.sock(conn);
    const sk = takeToken(k.h, now, this.lim.socketHistoryBytesPerMinute, cost);
    if (!sk.ok) return false;
    s.hist = so.bucket;
    this.globalHist = g.bucket;
    k.h = sk.bucket;
    return true;
  }

  /**
   * Records a dropped delivery; true when the socket has stayed over quota long enough to be closed (1013).
   * "Continuously" means no gap of a full quota window (quotaWindowMs) or more between two drops.
   */
  overQuota(conn: string, now = Date.now()): boolean {
    const k = this.sockOut.get(conn);
    if (!k) return false;
    const w = this.lim.quotaWindowMs;
    if (k.last === 0 || now - k.last >= w) k.since = now; // a new episode
    k.last = now;
    return now - k.since >= (this.lim.maxOverQuotaWindows - 1) * w;
  }

  forget(conn: string) {
    this.sockOut.delete(conn);
  }
}

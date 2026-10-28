import { DurableObject } from "cloudflare:workers";
import {
  Bucket,
  Filter,
  LIMITS,
  Limits,
  NostrEvent,
  dTag,
  eventWeight,
  filterIsEmpty,
  indexedTags,
  isAddressable,
  isEphemeral,
  isReplaceable,
  matchAny,
  normalizeFilters,
  parseCoord,
  peekEventId,
  takeToken,
  utf8Length,
  validateEvent,
  validateFilter,
} from "./nostr";

/**
 * Per-connection state kept in the WebSocket attachment (survives hibernation, well under 2KB).
 * The rate-limit buckets live here so waking from hibernation does not reset them.
 * A client can still reset its buckets by reconnecting; that is bounded by the per-source
 * connection/upgrade limits, the global connection cap and the global storage cap.
 */
interface ConnState {
  id: string;
  src: string; // source address (from index.ts), used to count connections per source
  ev?: Bucket; // EVENT frames
  rq?: Bucket; // REQ frames
  fr?: Bucket; // every frame
  bad?: number; // rejected frames in the current window
  badAt?: number; // window start
}

interface Env {
  RELAY: DurableObjectNamespace;
  // Optional JSON object overriding numeric LIMITS (used by the e2e test to exercise the caps).
  LIMITS_JSON?: string;
}

type SubMap = Map<string, Filter[]>;
type Which = "ev" | "rq" | "fr";

const HEX64 = /^[0-9a-f]{64}$/;

type StoreResult = "ok" | "ok_partial" | "duplicate" | "stale" | "blocked" | "full" | "capped";

/** Thrown inside a transaction to roll it back when it would grow a full database. */
class FullError extends Error {}

interface SourceState {
  up?: Bucket;
  ev?: Bucket;
  rq?: Bucket;
}

/**
 * One Durable Object holds the whole relay: events and subscriptions live in
 * its SQLite store, and WebSocket Hibernation lets it sleep between messages.
 * Subscriptions are persisted (not kept only in memory) so that a hibernated
 * connection keeps receiving events after the object wakes up.
 */
export class Relay extends DurableObject<Env> {
  private sql: SqlStorage;
  private lim: Limits;
  // Subscription cache: whole connections only, bounded by lim.subCacheBytes. Uncached
  // connections are read from SQL one at a time while broadcasting.
  private subCache = new Map<string, { subs: SubMap; bytes: number }>();
  private subCacheBytes = 0;
  // In-memory (lost on eviction) abuse controls.
  private sources = new Map<string, SourceState>(); // LRU, bounded by maxSourceEntries
  private globalEv?: Bucket;
  private globalOut?: Bucket;
  private sockOut = new Map<string, Bucket>();

  constructor(ctx: DurableObjectState, env: Env) {
    super(ctx, env);
    this.sql = ctx.storage.sql;
    this.lim = { ...LIMITS };
    try {
      const o = env.LIMITS_JSON ? (JSON.parse(env.LIMITS_JSON) as Record<string, unknown>) : {};
      for (const [k, v] of Object.entries(o)) {
        if (k in LIMITS && typeof v === "number" && Number.isFinite(v)) (this.lim as Record<string, number>)[k] = v;
      }
    } catch (err) {
      console.error("bad LIMITS_JSON", err);
    }
    ctx.blockConcurrencyWhile(async () => {
      this.sql.exec(`
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
        CREATE TABLE IF NOT EXISTS subs (
          conn TEXT NOT NULL,
          sub_id TEXT NOT NULL,
          filters TEXT NOT NULL,
          PRIMARY KEY (conn, sub_id)
        );
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
        -- Row counters (tombstones / coord_cutoffs only grow, so counting rows each time would be O(n)).
        CREATE TABLE IF NOT EXISTS meta (k TEXT PRIMARY KEY, n INTEGER NOT NULL);
      `);
      // Drop subscriptions of connections that no longer exist. Best effort: stale rows are
      // harmless (never matched by a live connection) and are retried on the next start.
      try {
        const live = new Set<string>();
        for (const ws of ctx.getWebSockets()) {
          const st = ws.deserializeAttachment() as ConnState | null;
          if (st) live.add(st.id);
        }
        for (const row of this.sql.exec("SELECT DISTINCT conn FROM subs").toArray()) {
          if (!live.has(row.conn as string)) this.sql.exec("DELETE FROM subs WHERE conn = ?", row.conn);
        }
      } catch (err) {
        console.error("stale subscription cleanup failed", err);
      }
    });
  }

  // ---------------------------------------------------------------- connections

  async fetch(request: Request): Promise<Response> {
    try {
      const src = (request.headers.get("x-hyphae-src") ?? "unknown").slice(0, 64);
      const socks = this.ctx.getWebSockets();
      if (socks.length >= this.lim.maxConnections) {
        return new Response("relay is at its connection limit", { status: 503, headers: { "retry-after": "30" } });
      }
      const s = this.source(src);
      const up = takeToken(s.up, Date.now(), this.lim.upgradesPerMinutePerSource);
      if (!up.ok) {
        return new Response("too many connection attempts from this address", { status: 429, headers: { "retry-after": "60" } });
      }
      s.up = up.bucket;
      let n = 0;
      for (const w of socks) {
        if ((w.deserializeAttachment() as ConnState | null)?.src === src) n++;
      }
      if (n >= this.lim.maxConnsPerSource) {
        return new Response("too many connections from this address", { status: 429, headers: { "retry-after": "30" } });
      }
      const pair = new WebSocketPair();
      const [client, server] = Object.values(pair);
      this.ctx.acceptWebSocket(server);
      server.serializeAttachment({ id: crypto.randomUUID(), src } satisfies ConnState);
      return new Response(null, { status: 101, webSocket: client });
    } catch (err) {
      console.error("fetch failed", err);
      return new Response("relay unavailable", { status: 503, headers: { "retry-after": "30" } });
    }
  }

  async webSocketMessage(ws: WebSocket, message: string | ArrayBuffer): Promise<void> {
    try {
      await this.handleMessage(ws, message);
    } catch (err) {
      console.error("webSocketMessage failed", err);
      this.send(ws, ["NOTICE", "error: internal error"]);
    }
  }

  private async handleMessage(ws: WebSocket, message: string | ArrayBuffer) {
    const st = ws.deserializeAttachment() as ConnState | null;
    if (!st) return;
    // Every frame costs a token, whatever it contains.
    if (!this.take(ws, st, "fr", this.lim.framesPerMinute)) {
      this.violation(ws, st);
      this.send(ws, ["NOTICE", "rate-limited: too many messages"]);
      return;
    }
    if (typeof message !== "string") {
      this.send(ws, ["NOTICE", "error: message must be text"]);
      return;
    }
    // Size is measured in UTF-8 bytes. A string with more UTF-16 units than the limit is
    // already too big (each unit is >= 1 byte), so only encode when it could still fit.
    const max = this.lim.maxMessageBytes;
    const size = message.length > max ? Infinity : utf8Length(message);
    if (size > max) {
      const id = peekEventId(message);
      if (id) this.send(ws, ["OK", id, false, "invalid: event too large"]);
      else this.send(ws, ["NOTICE", "error: message too large"]);
      return;
    }
    let msg: unknown;
    try {
      msg = JSON.parse(message);
    } catch {
      this.send(ws, ["NOTICE", "error: invalid JSON"]);
      return;
    }
    if (!Array.isArray(msg) || typeof msg[0] !== "string") {
      this.send(ws, ["NOTICE", "error: message must be an array"]);
      return;
    }
    switch (msg[0]) {
      case "EVENT":
        return this.onEvent(ws, st, msg[1], size);
      case "REQ":
        return this.onReq(ws, st, msg.slice(1));
      case "CLOSE":
        return this.onClose(ws, st.id, msg[1]);
      default:
        this.send(ws, ["NOTICE", `error: unknown message type ${msg[0].slice(0, 32)}`]);
    }
  }

  async webSocketClose(ws: WebSocket, code: number, reason: string): Promise<void> {
    try {
      this.dropConn(ws);
    } finally {
      // Complete the close handshake explicitly. 1005/1006/1015 are reserved and cannot be sent.
      const safe = code === 1005 || code === 1006 || code === 1015 || code < 1000 || code > 4999 ? 1000 : code;
      try {
        ws.close(safe, reason?.slice(0, 120));
      } catch {
        // already closed
      }
    }
  }

  async webSocketError(ws: WebSocket): Promise<void> {
    try {
      this.dropConn(ws);
    } finally {
      try {
        ws.close(1011, "websocket error");
      } catch {
        // already closed
      }
    }
  }

  /** Forgets a connection. A failing SQL delete is thrown to the caller (who closes the socket). */
  private dropConn(ws: WebSocket) {
    const st = ws.deserializeAttachment() as ConnState | null;
    if (!st) return;
    this.invalidateSubs(st.id);
    this.sockOut.delete(st.id);
    this.sql.exec("DELETE FROM subs WHERE conn = ?", st.id);
  }

  private forceClose(ws: WebSocket, code: number, reason: string) {
    try {
      ws.close(code, reason);
    } catch {
      // already closed
    }
    try {
      this.dropConn(ws);
    } catch (err) {
      console.error("dropConn failed", err); // stale rows are removed on the next start
    }
  }

  private send(ws: WebSocket, payload: unknown[]) {
    this.sendRaw(ws, JSON.stringify(payload));
  }

  private sendRaw(ws: WebSocket, text: string) {
    try {
      ws.send(text);
    } catch {
      // Socket already gone; close handler will clean up.
    }
  }

  // ---------------------------------------------------------------- rate limiting

  /** Takes tokens from a per-connection bucket and persists it in the attachment. Rejects change nothing. */
  private take(ws: WebSocket, st: ConnState, which: Which, perMinute: number): boolean {
    const r = takeToken(st[which], Date.now(), perMinute);
    if (r.ok) {
      st[which] = r.bucket;
      ws.serializeAttachment(st);
    }
    return r.ok;
  }

  /** Counts a rejected frame; a connection that keeps misbehaving is closed with 1008. */
  private violation(ws: WebSocket, st: ConnState) {
    const now = Date.now();
    if (!st.badAt || now - st.badAt > 60_000) {
      st.badAt = now;
      st.bad = 0;
    }
    st.bad = (st.bad ?? 0) + 1;
    ws.serializeAttachment(st);
    if (st.bad >= this.lim.maxViolations) this.forceClose(ws, 1008, "policy violation: rate limit exceeded");
  }

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

  private takeSource(src: string, which: "ev" | "rq", perMinute: number): boolean {
    const s = this.source(src);
    const r = takeToken(s[which], Date.now(), perMinute);
    if (r.ok) s[which] = r.bucket;
    return r.ok;
  }

  private storageFull(): boolean {
    return this.sql.databaseSize > this.lim.maxStorageBytes;
  }

  // ---------------------------------------------------------------- EVENT

  private async onEvent(ws: WebSocket, st: ConnState, raw: unknown, size: number) {
    const id = typeof (raw as NostrEvent | undefined)?.id === "string" ? (raw as NostrEvent).id.slice(0, 64) : "";
    try {
      const tagCount = Array.isArray((raw as NostrEvent | undefined)?.tags) ? (raw as NostrEvent).tags.length : 0;
      const r = takeToken(this.globalEv, Date.now(), this.lim.globalEventWeightPerMinute, eventWeight(tagCount, size));
      if (
        !this.take(ws, st, "ev", this.lim.eventsPerMinute) ||
        !this.takeSource(st.src, "ev", this.lim.sourceEventsPerMinute) ||
        !r.ok
      ) {
        this.violation(ws, st);
        this.send(ws, ["OK", id, false, "rate-limited: slow down"]);
        return;
      }
      this.globalEv = r.bucket;
      const err = validateEvent(raw);
      if (err) {
        this.send(ws, ["OK", id, false, err]);
        return;
      }
      const e = raw as NostrEvent;
      if (isEphemeral(e.kind)) {
        const dead = this.sql.exec("SELECT 1 FROM tombstones WHERE event_id = ? AND pubkey = ?", e.id, e.pubkey).toArray();
        if (dead.length > 0) {
          this.send(ws, ["OK", e.id, false, "blocked: event was deleted"]);
          return;
        }
        this.broadcast(e);
        this.send(ws, ["OK", e.id, true, ""]);
        return;
      }
      switch (this.store(e)) {
        case "duplicate":
          this.send(ws, ["OK", e.id, true, "duplicate: already have this event"]);
          return;
        case "stale":
          this.send(ws, ["OK", e.id, false, "invalid: a newer version of this event exists"]);
          return;
        case "blocked":
          this.send(ws, ["OK", e.id, false, "blocked: event was deleted"]);
          return;
        case "full":
          this.send(ws, ["OK", e.id, false, "rate-limited: relay storage is full"]);
          return;
        case "capped":
          this.send(ws, ["OK", e.id, false, "rate-limited: deletion records are full"]);
          return;
        case "ok_partial":
          this.broadcast(e);
          this.send(ws, ["OK", e.id, true, `ignored: only the first ${this.lim.maxDeletionTargets} e/a targets were processed`]);
          return;
      }
      this.broadcast(e);
      this.send(ws, ["OK", e.id, true, ""]);
    } catch (err) {
      console.error("onEvent failed", err);
      this.send(ws, ["OK", id, false, "error: internal error"]);
    }
  }

  private counter(k: string): number {
    const r = this.sql.exec("SELECT n FROM meta WHERE k = ?", k).toArray();
    return r.length > 0 ? (r[0].n as number) : 0;
  }

  private bump(k: string) {
    this.sql.exec("INSERT INTO meta (k, n) VALUES (?, 1) ON CONFLICT(k) DO UPDATE SET n = n + 1", k);
  }

  private store(e: NostrEvent): StoreResult {
    try {
      return this.ctx.storage.transactionSync((): StoreResult => {
        if (this.sql.exec("SELECT 1 FROM events WHERE id = ?", e.id).toArray().length > 0) return "duplicate";
        const versioned = isReplaceable(e.kind) || isAddressable(e.kind);
        const d = isAddressable(e.kind) ? dTag(e) : "";
        if (e.kind !== 5) {
          // Deletion requests themselves can never be deleted or blocked.
          const dead = this.sql
            .exec("SELECT 1 FROM tombstones WHERE event_id = ? AND pubkey = ?", e.id, e.pubkey)
            .toArray();
          if (dead.length > 0) return "blocked";
          if (versioned) {
            const c = this.sql.exec("SELECT cutoff FROM coord_cutoffs WHERE coord = ?", `${e.kind}:${e.pubkey}:${d}`).toArray();
            if (c.length > 0 && e.created_at <= (c[0].cutoff as number)) return "blocked";
          }
        } else if (
          this.counter("tombstones") >= this.lim.maxTombstones ||
          this.counter("coord_cutoffs") >= this.lim.maxCoordCutoffs
        ) {
          return "capped";
        }
        let old: SqlStorageValue[][] | Record<string, SqlStorageValue>[] = [];
        if (versioned) {
          old = this.sql
            .exec("SELECT id, created_at FROM events WHERE pubkey = ? AND kind = ? AND d = ?", e.pubkey, e.kind, d)
            .toArray();
          for (const o of old as Record<string, SqlStorageValue>[]) {
            const oCreated = o.created_at as number;
            const oId = o.id as string;
            if (oCreated > e.created_at || (oCreated === e.created_at && oId < e.id)) return "stale";
          }
          // Only a real replacement (an old version is removed) is exempt from the storage cap.
          if (old.length === 0 && this.storageFull()) return "full";
          for (const o of old as Record<string, SqlStorageValue>[]) this.deleteEvent(o.id as string);
        } else if (e.kind !== 5 && this.storageFull()) {
          return "full";
        }
        let partial = false;
        if (e.kind === 5) {
          const res = this.applyDeletion(e);
          partial = res.partial;
          // A deletion request that frees nothing grows a full database: refuse (rolls back).
          if (res.deleted === 0 && this.storageFull()) throw new FullError();
        }
        this.sql.exec(
          "INSERT INTO events (id, pubkey, created_at, kind, d, json) VALUES (?, ?, ?, ?, ?, ?)",
          e.id, e.pubkey, e.created_at, e.kind, d, JSON.stringify(e),
        );
        for (const [name, value] of indexedTags(e.tags)) {
          this.sql.exec("INSERT INTO event_tags (event_id, name, value) VALUES (?, ?, ?)", e.id, name, value);
        }
        return partial ? "ok_partial" : "ok";
      });
    } catch (err) {
      if (err instanceof FullError) return "full";
      throw err;
    }
  }

  /**
   * NIP-09: "e" tags delete by id, "a" tags delete versions of a coordinate up to the request time.
   * At most maxDeletionTargets targets and maxDeletionRowWrites row changes are processed per request.
   */
  private applyDeletion(req: NostrEvent): { deleted: number; partial: boolean } {
    let targets = 0;
    let writes = 0;
    let deleted = 0;
    for (const t of req.tags) {
      const isE = t[0] === "e" && !!t[1] && HEX64.test(t[1]);
      if (!isE && t[0] !== "a") continue;
      if (targets >= this.lim.maxDeletionTargets || writes >= this.lim.maxDeletionRowWrites) return { deleted, partial: true };
      targets++;
      if (isE) {
        const target = this.sql.exec("SELECT pubkey, kind FROM events WHERE id = ?", t[1]).toArray();
        if (target.length > 0) {
          // Someone else's event, or another deletion request: ignore.
          if (target[0].pubkey !== req.pubkey || target[0].kind === 5) continue;
          writes += this.deleteEvent(t[1]);
          deleted++;
        }
        // Remember it (also when it has not arrived yet) so it cannot be re-published.
        if (this.sql.exec("SELECT 1 FROM tombstones WHERE event_id = ? AND pubkey = ?", t[1], req.pubkey).toArray().length === 0) {
          this.sql.exec("INSERT INTO tombstones (event_id, pubkey) VALUES (?, ?)", t[1], req.pubkey);
          this.bump("tombstones");
          writes++;
        }
      } else {
        const c = parseCoord(t[1]);
        if (!c || c.pubkey !== req.pubkey || c.kind === 5) continue;
        if (!isReplaceable(c.kind) && !isAddressable(c.kind)) continue;
        const d = isAddressable(c.kind) ? c.d : "";
        const coord = `${c.kind}:${c.pubkey}:${d}`;
        if (this.sql.exec("SELECT 1 FROM coord_cutoffs WHERE coord = ?", coord).toArray().length === 0) this.bump("coord_cutoffs");
        this.sql.exec(
          "INSERT INTO coord_cutoffs (coord, cutoff) VALUES (?, ?) ON CONFLICT(coord) DO UPDATE SET cutoff = max(cutoff, excluded.cutoff)",
          coord, req.created_at,
        );
        writes++;
        const victims = this.sql
          .exec("SELECT id FROM events WHERE pubkey = ? AND kind = ? AND d = ? AND created_at <= ? LIMIT 10", c.pubkey, c.kind, d, req.created_at)
          .toArray();
        for (const v of victims) {
          writes += this.deleteEvent(v.id as string);
          deleted++;
        }
      }
    }
    return { deleted, partial: false };
  }

  /** Returns the number of rows written (for the deletion budget). */
  private deleteEvent(id: string): number {
    const a = this.sql.exec("DELETE FROM event_tags WHERE event_id = ?", id);
    a.toArray();
    const b = this.sql.exec("DELETE FROM events WHERE id = ?", id);
    b.toArray();
    return Math.max(1, a.rowsWritten + b.rowsWritten);
  }

  // ---------------------------------------------------------------- subscriptions

  /** Subscriptions of one connection: from the cache when it fits, otherwise read from SQL. */
  private getSubs(conn: string): SubMap {
    const hit = this.subCache.get(conn);
    if (hit) return hit.subs;
    const subs: SubMap = new Map();
    let bytes = 0;
    for (const row of this.sql.exec("SELECT sub_id, filters FROM subs WHERE conn = ?", conn).toArray()) {
      try {
        const json = row.filters as string;
        subs.set(row.sub_id as string, JSON.parse(json) as Filter[]);
        bytes += json.length;
      } catch {
        // corrupt row: skip
      }
    }
    if (this.subCacheBytes + bytes <= this.lim.subCacheBytes) {
      this.subCache.set(conn, { subs, bytes });
      this.subCacheBytes += bytes;
    }
    return subs;
  }

  private invalidateSubs(conn: string) {
    const hit = this.subCache.get(conn);
    if (!hit) return;
    this.subCacheBytes -= hit.bytes;
    this.subCache.delete(conn);
  }

  /** Removes a subscription. A failing SQL delete is thrown to the caller. */
  private removeSub(conn: string, subId: string) {
    this.invalidateSubs(conn);
    this.sql.exec("DELETE FROM subs WHERE conn = ? AND sub_id = ?", conn, subId);
  }

  /** Every CLOSED we send means the subscription no longer exists, so drop it first (or close the socket). */
  private closeSub(ws: WebSocket, conn: string, subId: string, reason: string) {
    try {
      this.removeSub(conn, subId);
    } catch (err) {
      // Cannot guarantee the subscription is gone: do not claim CLOSED, drop the connection instead.
      console.error("removeSub failed", err);
      this.forceClose(ws, 1011, "internal error");
      return;
    }
    this.send(ws, ["CLOSED", subId, reason]);
  }

  private onReq(ws: WebSocket, st: ConnState, args: unknown[]) {
    const conn = st.id;
    const subId = args[0];
    if (typeof subId !== "string" || subId.length === 0 || subId.length > LIMITS.maxSubIdLength) {
      this.send(ws, ["NOTICE", "error: invalid subscription id"]);
      return;
    }
    // Every REQ costs a token, including repeated REQs for the same subId and invalid ones.
    if (!this.take(ws, st, "rq", this.lim.reqsPerMinute) || !this.takeSource(st.src, "rq", this.lim.sourceReqsPerMinute)) {
      this.violation(ws, st);
      this.closeSub(ws, conn, subId, "rate-limited: too many REQs, slow down");
      return;
    }
    const rawFilters = args.slice(1);
    if (rawFilters.length === 0 || rawFilters.length > LIMITS.maxFiltersPerReq) {
      this.closeSub(ws, conn, subId, "invalid: need 1 to " + LIMITS.maxFiltersPerReq + " filters");
      return;
    }
    for (const f of rawFilters) {
      const err = validateFilter(f);
      if (err) {
        this.closeSub(ws, conn, subId, err);
        return;
      }
    }
    const fs = normalizeFilters(rawFilters as Filter[]);
    const fjson = JSON.stringify(fs);
    if (utf8Length(fjson) > this.lim.maxSubBytes) {
      this.closeSub(ws, conn, subId, "invalid: filter too large");
      return;
    }
    try {
      const mine = this.sql
        .exec("SELECT sub_id = ? AS same, length(filters) AS len FROM subs WHERE conn = ?", subId, conn)
        .toArray();
      const exists = mine.some((r) => r.same === 1);
      const otherBytes = mine.filter((r) => r.same !== 1).reduce((n, r) => n + (r.len as number), 0);
      if (otherBytes + fjson.length > this.lim.maxSubBytesPerConn) {
        this.closeSub(ws, conn, subId, "rate-limited: too many subscription bytes on this connection");
        return;
      }
      if (!exists) {
        // A new subscription row grows the database.
        if (mine.length >= LIMITS.maxSubsPerConn) {
          this.send(ws, ["CLOSED", subId, "rate-limited: too many subscriptions"]);
          return;
        }
        const total = this.sql.exec("SELECT count(*) AS n FROM subs").one().n as number;
        if (total >= this.lim.maxSubRows) {
          this.send(ws, ["CLOSED", subId, "rate-limited: relay has too many subscriptions"]);
          return;
        }
        if (this.storageFull()) {
          this.send(ws, ["CLOSED", subId, "rate-limited: relay storage is full"]);
          return;
        }
      }
      this.invalidateSubs(conn);
      this.sql.exec("INSERT OR REPLACE INTO subs (conn, sub_id, filters) VALUES (?, ?, ?)", conn, subId, fjson);
      const idJson = JSON.stringify(subId);
      const { events, truncated } = this.query(fs);
      for (const json of events) this.sendRaw(ws, `["EVENT",${idJson},${json}]`);
      // NIP-67: tell the client that matching history was left out (never "finish" in that case).
      this.send(ws, truncated ? ["EOSE", subId, ["more"]] : ["EOSE", subId]);
    } catch (err) {
      console.error("onReq failed", err);
      this.closeSub(ws, conn, subId, "error: could not process request");
    }
  }

  private onClose(ws: WebSocket, conn: string, subId: unknown) {
    if (typeof subId !== "string") return;
    try {
      this.removeSub(conn, subId);
    } catch (err) {
      console.error("removeSub failed", err);
      this.forceClose(ws, 1011, "internal error");
    }
  }

  /**
   * Stored events (as JSON strings) matching the filters. Filters are read in order through a
   * cursor, newest first within each filter, and reading stops as soon as the total reaches
   * maxLimit events or maxResultBytes. `truncated` is true when matching history was left out
   * because of those caps (not because of the client's own `limit`).
   */
  private query(filters: Filter[]): { events: string[]; truncated: boolean } {
    const seen = new Set<string>();
    const out: string[] = [];
    let bytes = 0;
    const range = (col: string, list: string[], where: string[], params: (string | number)[]) => {
      where.push("(" + list.map(() => `${col} BETWEEN ? AND ?`).join(" OR ") + ")");
      for (const p of list) params.push(p.padEnd(64, "0"), p.padEnd(64, "f"));
    };
    for (const f of filters) {
      if (filterIsEmpty(f)) continue; // an empty list can never match
      const where: string[] = [];
      const params: (string | number)[] = [];
      if (f.ids) range("id", f.ids, where, params);
      if (f.authors) range("pubkey", f.authors, where, params);
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
        where.push(
          `id IN (SELECT event_id FROM event_tags WHERE name = ? AND value IN (${v.map(() => "?").join(",")}))`,
        );
        params.push(k[1], ...(v as string[]));
      }
      const eff = Math.max(0, Math.min(f.limit ?? LIMITS.maxLimit, LIMITS.maxLimit));
      if (eff === 0) continue;
      // The client asked for more than we serve (or gave no limit): more history may exist.
      const capped = f.limit === undefined || f.limit > LIMITS.maxLimit;
      const sql =
        "SELECT id, json FROM events" +
        (where.length ? " WHERE " + where.join(" AND ") : "") +
        " ORDER BY created_at DESC, id ASC LIMIT ?";
      let n = 0;
      for (const row of this.sql.exec(sql, ...params, eff + 1)) {
        if (n >= eff) {
          if (capped) return { events: out, truncated: true };
          break;
        }
        n++;
        const id = row.id as string;
        if (seen.has(id)) continue;
        const json = row.json as string;
        const size = utf8Length(json);
        if (out.length >= LIMITS.maxLimit || bytes + size > this.lim.maxResultBytes) return { events: out, truncated: true };
        seen.add(id);
        out.push(json);
        bytes += size;
      }
    }
    return { events: out, truncated: false };
  }

  // ---------------------------------------------------------------- fan-out

  /**
   * Delivers an event to matching subscriptions. The JSON is built once. Per-event, global and
   * per-socket budgets bound the work; a socket over its byte quota is a slow consumer and is closed.
   */
  private broadcast(e: NostrEvent) {
    const json = JSON.stringify(e);
    const evBytes = utf8Length(json);
    let delivered = 0;
    for (const ws of this.ctx.getWebSockets()) {
      try {
        const st = ws.deserializeAttachment() as ConnState | null;
        if (!st) continue;
        for (const [subId, filters] of this.getSubs(st.id)) {
          if (!matchAny(filters, e)) continue;
          if (delivered >= this.lim.maxDeliveriesPerEvent) return;
          const idJson = JSON.stringify(subId);
          const cost = evBytes + idJson.length + 12;
          const now = Date.now();
          const g = takeToken(this.globalOut, now, this.lim.globalOutboundBytesPerMinute, cost);
          if (!g.ok) return; // global outbound budget exhausted: drop the rest of this event's delivery
          const s = takeToken(this.sockOut.get(st.id), now, this.lim.socketOutboundBytesPerMinute, cost);
          if (!s.ok) {
            this.forceClose(ws, 1013, "outbound quota exceeded, try again later");
            break;
          }
          this.globalOut = g.bucket;
          this.sockOut.set(st.id, s.bucket);
          this.sendRaw(ws, `["EVENT",${idJson},${json}]`);
          delivered++;
        }
      } catch (err) {
        console.error("broadcast failed for a socket", err);
      }
    }
  }
}

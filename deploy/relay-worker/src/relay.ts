import { DurableObject } from "cloudflare:workers";
import { type Limits, loadLimits } from "./config";
import { DELETION_DDL, reconcileCounters } from "./deletion";
import { type Db, EVENTS_DDL } from "./db";
import { processEvent } from "./events";
import { type Hub, peekEventId } from "./hub";
import { idleSockets } from "./idle";
import { Budgets, type ConnState, countViolation, takeConn } from "./limits";
import { utf8Length } from "./nostr";
import { processClose, processReq } from "./req";
import { SUBS_DDL, SubStore } from "./subs";

interface Env {
  RELAY: DurableObjectNamespace;
  // Optional JSON object overriding numeric LIMITS (used by the e2e test to exercise the caps).
  LIMITS_JSON?: string;
}

/**
 * One Durable Object holds the whole relay: events and subscriptions live in its SQLite store,
 * and WebSocket Hibernation lets it sleep between messages. This class is only the shell:
 * connection lifecycle, frame parsing and dispatch. The work is in the modules it hands `hub` to.
 */
export class Relay extends DurableObject<Env> {
  private lim: Limits;
  private db: Db;
  private budgets: Budgets;
  private subs: SubStore;
  private hub: Hub;

  constructor(ctx: DurableObjectState, env: Env) {
    super(ctx, env);
    this.lim = loadLimits(env.LIMITS_JSON);
    this.db = { sql: ctx.storage.sql, tx: (fn) => ctx.storage.transactionSync(fn), lim: this.lim };
    this.budgets = new Budgets(this.lim);
    this.subs = new SubStore(this.db);
    this.hub = {
      db: this.db,
      lim: this.lim,
      budgets: this.budgets,
      subs: this.subs,
      send: (ws, payload) => this.sendRaw(ws, JSON.stringify(payload)),
      sendRaw: (ws, text) => this.sendRaw(ws, text),
      take: (ws, st, which, perMinute) => {
        const ok = takeConn(st, which, perMinute);
        if (ok) ws.serializeAttachment(st);
        return ok;
      },
      violation: (ws, st) => this.violation(ws, st),
      forceClose: (ws, code, reason, st) => this.forceClose(ws, code, reason, st),
      sockets: () => ctx.getWebSockets(),
    };
    // A text "ping" is answered with "pong" by the runtime without waking the object from hibernation.
    ctx.setWebSocketAutoResponse(new WebSocketRequestResponsePair("ping", "pong"));
    ctx.blockConcurrencyWhile(async () => {
      this.db.sql.exec(EVENTS_DDL + DELETION_DDL + SUBS_DDL);
      reconcileCounters(this.db);
      // Drop subscriptions of connections that no longer exist. Best effort: a stale row is never
      // matched by a live connection but counts toward maxSubRows until a later start removes it.
      try {
        const live = new Set<string>();
        for (const ws of ctx.getWebSockets()) {
          const st = ws.deserializeAttachment() as ConnState | null;
          if (st) live.add(st.id);
        }
        for (const row of this.db.sql.exec("SELECT DISTINCT conn FROM subs").toArray()) {
          if (!live.has(row.conn as string)) this.db.sql.exec("DELETE FROM subs WHERE conn = ?", row.conn);
        }
      } catch (err) {
        console.error("stale subscription cleanup failed", err);
      }
    });
  }

  async fetch(request: Request): Promise<Response> {
    // The Worker forwards only WebSocket upgrades and /healthz probes here, so any other request is a probe.
    if (request.headers.get("Upgrade")?.toLowerCase() !== "websocket") return this.health();
    try {
      const src = (request.headers.get("x-hyphae-src") ?? "unknown").slice(0, 64);
      // A socket we already started closing still lists until its close handshake ends (up to ~10 s under wrangler dev); it holds no slot.
      let socks = this.ctx.getWebSockets().filter((w) => !(w.deserializeAttachment() as ConnState | null)?.closed);
      // At a cap, first free the slots held by connections that went silent (half-dead sockets never send a close).
      const sameSrc = (w: WebSocket) => (w.deserializeAttachment() as ConnState | null)?.src === src;
      if (socks.length >= this.lim.maxConnections || socks.filter(sameSrc).length >= this.lim.maxConnsPerSource) {
        socks = this.reapIdle(socks);
      }
      if (socks.length >= this.lim.maxConnections) {
        return new Response("relay is at its connection limit", { status: 503, headers: { "retry-after": "30" } });
      }
      // Concurrency first: a refused attempt must not burn an upgrade token.
      const open = socks.filter(sameSrc).length;
      if (open >= this.lim.maxConnsPerSource) {
        return new Response("too many connections from this address", { status: 429, headers: { "retry-after": "30" } });
      }
      if (!this.budgets.takeUpgrade(src)) {
        return new Response("too many connection attempts from this address", { status: 429, headers: { "retry-after": "60" } });
      }
      const pair = new WebSocketPair();
      const [client, server] = Object.values(pair);
      this.ctx.acceptWebSocket(server);
      server.serializeAttachment({ id: crypto.randomUUID(), src, lastSeen: Date.now() } satisfies ConnState);
      return new Response(null, { status: 101, webSocket: client });
    } catch (err) {
      console.error("fetch failed", err);
      return new Response("relay unavailable", { status: 503, headers: { "retry-after": "30" } });
    }
  }

  async webSocketMessage(ws: WebSocket, message: string | ArrayBuffer): Promise<void> {
    try {
      this.handleMessage(ws, message);
    } catch (err) {
      console.error("webSocketMessage failed", err);
      this.hub.send(ws, ["NOTICE", "error: internal error"]);
    }
  }

  private handleMessage(ws: WebSocket, message: string | ArrayBuffer) {
    const st = ws.deserializeAttachment() as ConnState | null;
    if (!st || st.closed) return; // closing: frames still in flight must not create state
    st.lastSeen = Date.now(); // persisted by take() or violation() just below
    // Every frame costs a token, whatever it contains.
    if (!this.hub.take(ws, st, "fr", this.lim.framesPerMinute)) {
      this.violation(ws, st);
      return this.hub.send(ws, ["NOTICE", "rate-limited: too many messages"]);
    }
    if (typeof message !== "string") return this.hub.send(ws, ["NOTICE", "error: message must be text"]);
    // Size is measured in UTF-8 bytes. A string with more UTF-16 units than the limit is
    // already too big (each unit is >= 1 byte), so only encode when it could still fit.
    const max = this.lim.maxMessageBytes;
    const size = message.length > max ? Infinity : utf8Length(message);
    if (size > max) {
      const id = peekEventId(message);
      return this.hub.send(ws, id ? ["OK", id, false, "invalid: event too large"] : ["NOTICE", "error: message too large"]);
    }
    let msg: unknown;
    try {
      msg = JSON.parse(message);
    } catch {
      return this.hub.send(ws, ["NOTICE", "error: invalid JSON"]);
    }
    if (!Array.isArray(msg) || typeof msg[0] !== "string") return this.hub.send(ws, ["NOTICE", "error: message must be an array"]);
    switch (msg[0]) {
      case "EVENT":
        return processEvent(this.hub, ws, st, msg[1], size);
      case "REQ":
        return processReq(this.hub, ws, st, msg.slice(1));
      case "CLOSE":
        return processClose(this.hub, ws, st.id, msg[1]);
      default:
        this.hub.send(ws, ["NOTICE", `error: unknown message type ${msg[0].slice(0, 32)}`]);
    }
  }

  async webSocketClose(ws: WebSocket, code: number, reason: string): Promise<void> {
    try {
      this.dropConn(ws);
    } finally {
      // Complete the close handshake explicitly (reason cut to 120 characters). 1005/1006/1015 are reserved and cannot be sent.
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

  /** Closes (1001) the sockets idle past the timeout and returns the rest. Never throws. */
  private reapIdle(socks: WebSocket[]): WebSocket[] {
    try {
      const idle = new Set(
        idleSockets(
          socks,
          (w) => ({ st: w.deserializeAttachment() as ConnState | null, auto: this.ctx.getWebSocketAutoResponseTimestamp(w)?.getTime() }),
          Date.now(),
          this.lim.idleTimeoutSeconds * 1000,
        ),
      );
      for (const w of idle) this.forceClose(w, 1001, "idle timeout");
      return idle.size ? socks.filter((w) => !idle.has(w)) : socks;
    } catch (err) {
      console.error("idle reaping failed", err);
      return socks;
    }
  }

  /** Liveness probe: succeeds only if the object runs and its SQLite store answers. */
  private health(): Response {
    try {
      this.db.sql.exec("SELECT count(*) AS n FROM sqlite_master").toArray();
      return new Response("ok\n", { headers: { "content-type": "text/plain; charset=utf-8", "cache-control": "no-store" } });
    } catch (err) {
      console.error("health check failed", err);
      return new Response("relay storage unavailable\n", { status: 503, headers: { "cache-control": "no-store" } });
    }
  }

  /** Forgets a connection. A failing SQL delete is thrown to the caller (who closes the socket). */
  private dropConn(ws: WebSocket) {
    const st = ws.deserializeAttachment() as ConnState | null;
    if (!st) return;
    this.budgets.forget(st.id);
    this.subs.dropConn(st.id);
  }

  private forceClose(ws: WebSocket, code: number, reason: string, st?: ConnState) {
    // Mark first: frames the client sent before it sees our close are ignored by handleMessage.
    const cur = st ?? (ws.deserializeAttachment() as ConnState | null);
    if (cur) {
      cur.closed = true;
      try {
        ws.serializeAttachment(cur);
      } catch {
        // ignore
      }
    }
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

  /** Counts a rejected frame; a connection that keeps misbehaving is closed with 1008. */
  private violation(ws: WebSocket, st: ConnState) {
    const close = countViolation(st, this.lim.maxViolations);
    ws.serializeAttachment(st);
    if (close) this.forceClose(ws, 1008, "policy violation: rate limit exceeded", st);
  }

  private sendRaw(ws: WebSocket, text: string) {
    try {
      ws.send(text);
    } catch {
      // Socket already gone; close handler will clean up.
    }
  }
}

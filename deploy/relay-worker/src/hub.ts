import type { Limits } from "./config";
import type { Db } from "./db";
import type { Budgets, ConnState } from "./limits";
import { utf8Length } from "./nostr";
import type { SubStore } from "./subs";

/** What the message handlers need from the Durable Object (passed in explicitly, no shared `this`). */
export interface Hub {
  db: Db;
  lim: Limits;
  budgets: Budgets;
  subs: SubStore;
  send(ws: WebSocket, payload: unknown[]): void;
  sendRaw(ws: WebSocket, text: string): void;
  /** Takes a token from one of the connection's own buckets and persists the attachment. */
  take(ws: WebSocket, st: ConnState, which: "ev" | "rq" | "fr", perMinute: number): boolean;
  /** Counts a rejected frame; a connection that keeps misbehaving is closed with 1008. */
  violation(ws: WebSocket, st: ConnState): void;
  forceClose(ws: WebSocket, code: number, reason: string, st?: ConnState): void;
  sockets(): WebSocket[];
}

/** Bytes an `["EVENT",subId,<json>]` frame costs against the outbound or history budgets. */
export const deliveryCost = (eventBytes: number, subIdJson: string): number => eventBytes + utf8Length(subIdJson) + 12;

const TOKEN = /\s*("(?:[^"\\]|\\.)*"|[[\]{}:,]|[^\s"[\]{}:,]+)/y;

/** Top-level event id of an oversized `["EVENT",{...}]` frame, read from its first or last bytes only (no full parse). */
export function peekEventId(message: string): string | null {
  if (!/^\s*\[\s*"EVENT"\s*,\s*\{/.test(message.slice(0, 64))) return null;
  // Walk the leading tokens, tracking depth so only a key of the event object itself (depth 2) counts.
  const head = message.slice(0, 4200);
  TOKEN.lastIndex = 0;
  let depth = 0;
  let prev = "";
  let idValue = false;
  for (let m = TOKEN.exec(head); m; m = TOKEN.exec(head)) {
    const t = m[1];
    if (idValue) return /^"[0-9a-f]{64}"$/.test(t) ? t.slice(1, -1) : null;
    if (t === "[" || t === "{") depth++;
    else if (t === "]" || t === "}") depth--;
    else idValue = t === ":" && depth === 2 && prev === '"id"';
    prev = t;
  }
  // Some clients put "id" and "sig" last, after a large content field.
  const t = /"id"\s*:\s*"([0-9a-f]{64})"\s*,\s*"sig"\s*:\s*"[0-9a-f]{128}"\s*\}\s*\]\s*$/.exec(message.slice(-400));
  return t ? t[1] : null;
}

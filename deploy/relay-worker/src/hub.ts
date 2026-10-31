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

/** Event id of an oversized `["EVENT",{...}]` frame, read from its first or last bytes only (no full parse). */
export function peekEventId(message: string): string | null {
  const m = /^\s*\[\s*"EVENT"\s*,\s*\{[^]{0,4000}?"id"\s*:\s*"([0-9a-f]{64})"/.exec(message.slice(0, 4200));
  if (m) return m[1];
  // Some clients put "id" and "sig" last, after a large content field.
  const t = /"id"\s*:\s*"([0-9a-f]{64})"\s*,\s*"sig"\s*:\s*"[0-9a-f]{128}"\s*\}\s*\]\s*$/.exec(message.slice(-400));
  return t ? t[1] : null;
}

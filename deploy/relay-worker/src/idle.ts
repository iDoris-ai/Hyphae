import type { ConnState } from "./limits";

/** What the runtime knows about one socket: its attachment and when the runtime last auto-answered a "ping" (ms). */
export interface SocketActivity {
  st: ConnState | null;
  auto?: number;
}

/**
 * The sockets that have shown no sign of life for `timeoutMs`. Life = a frame that woke the object
 * (`st.lastSeen`) or a "ping" the runtime answered on its own (`auto`). Server-to-client traffic does
 * not count: a half-dead socket still "receives". A socket without `lastSeen` (opened before this
 * field existed), without an attachment, or already closing is never returned. `timeoutMs` <= 0 disables.
 */
export function idleSockets<T>(socks: T[], read: (w: T) => SocketActivity, now: number, timeoutMs: number): T[] {
  if (!(timeoutMs > 0)) return [];
  return socks.filter((w) => {
    const { st, auto } = read(w);
    if (!st || st.closed || st.lastSeen === undefined) return false;
    return now - Math.max(st.lastSeen, auto ?? 0) > timeoutMs;
  });
}

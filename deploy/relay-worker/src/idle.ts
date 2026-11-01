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
  const last = (w: T) => {
    const { st, auto } = read(w);
    return st && !st.closed && st.lastSeen !== undefined ? Math.max(st.lastSeen, auto ?? 0) : undefined;
  };
  // Oldest first, so a caller that needs only a few takes the longest-silent ones.
  return socks
    .filter((w) => {
      const t = last(w);
      return t !== undefined && now - t > timeoutMs;
    })
    .sort((a, b) => last(a)! - last(b)!);
}

export interface ReapCaps {
  maxConnections: number;
  maxConnsPerSource: number;
  idleTimeoutSeconds: number;
}

/**
 * Which sockets to close so that a new connection from `src` fits, taking no more than needed.
 * A full source frees only its OWN idle sockets (otherwise a client sitting at its cap could close
 * other sources' subscribers just by retrying). Only a full relay looks at everyone's.
 * `socks` must not contain sockets that are already closing.
 */
export function planReap<T>(socks: T[], read: (w: T) => SocketActivity, src: string, caps: ReapCaps, now: number): T[] {
  const ms = caps.idleTimeoutSeconds * 1000;
  const out: T[] = [];
  const mine = socks.filter((w) => read(w).st?.src === src);
  if (mine.length >= caps.maxConnsPerSource) {
    out.push(...idleSockets(mine, read, now, ms).slice(0, mine.length - caps.maxConnsPerSource + 1));
  }
  const taken = new Set(out);
  const rest = socks.filter((w) => !taken.has(w));
  if (rest.length >= caps.maxConnections) {
    out.push(...idleSockets(rest, read, now, ms).slice(0, rest.length - caps.maxConnections + 1));
  }
  return out;
}

/** CORS headers for browser clients. Wildcard origin: the relay is public and sends no credentials, so no Vary is needed. */
export const CORS: Record<string, string> = {
  "access-control-allow-origin": "*",
  "access-control-allow-methods": "GET, OPTIONS",
  "access-control-allow-headers": "*",
  // Without this a browser hides Retry-After from the page, so it could not tell a 429/503 from a dead relay.
  "access-control-expose-headers": "retry-after",
};

/**
 * Adds the CORS headers to every response except a WebSocket upgrade. The 101 is returned as the same
 * object: re-wrapping it would drop its `webSocket` and break the connection.
 */
export function withCors(res: Response): Response {
  if (res.status === 101 || res.webSocket) return res;
  const out = new Response(res.body, res); // a copy with mutable headers (responses from a stub are immutable)
  for (const [k, v] of Object.entries(CORS)) out.headers.set(k, v);
  return out;
}

/** How long a probe result is reused inside one Worker isolate: a healthy answer, and (shorter) a failed one. */
export const HEALTH_OK_TTL_MS = 5000;
export const HEALTH_FAIL_TTL_MS = 2000;

/**
 * Wraps the Durable Object probe so that public /healthz traffic reaches the object at most about once per TTL
 * per isolate. Concurrent callers share one in-flight probe; a probe that throws counts as a failure.
 * (Each isolate has its own cache, so the bound is per isolate, not global.)
 */
export function createHealthCache(now: () => number = Date.now) {
  let at = -Infinity;
  let ok = false;
  let inflight: Promise<boolean> | undefined;
  return (probe: () => Promise<boolean>): Promise<boolean> => {
    if (inflight) return inflight;
    if (now() - at < (ok ? HEALTH_OK_TTL_MS : HEALTH_FAIL_TTL_MS)) return Promise.resolve(ok);
    inflight = probe()
      .catch(() => false)
      .then((r) => {
        ok = r;
        at = now();
        inflight = undefined;
        return r;
      });
    return inflight;
  };
}

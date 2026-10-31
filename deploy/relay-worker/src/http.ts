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

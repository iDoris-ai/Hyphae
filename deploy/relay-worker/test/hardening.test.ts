import { describe, expect, it } from "vitest";
import { CORS, withCors } from "../src/http";
import { idleSockets } from "../src/idle";
import type { ConnState } from "../src/limits";
import { LIMITS, loadLimits } from "../src/config";

describe("withCors", () => {
  it("adds the CORS headers to error and plain responses and keeps status, body and other headers", async () => {
    for (const status of [200, 400, 429, 503]) {
      const res = withCors(new Response("x", { status, headers: { "retry-after": "30" } }));
      expect(res.status).toBe(status);
      expect(await res.text()).toBe("x");
      expect(res.headers.get("retry-after")).toBe("30");
      for (const [k, v] of Object.entries(CORS)) expect(res.headers.get(k)).toBe(v);
    }
  });
  it("works on a response with immutable headers (what a Durable Object stub returns)", () => {
    const res = Response.redirect("https://example.com/", 302); // immutable headers
    expect(() => res.headers.set("x", "y")).toThrow();
    expect(withCors(res).headers.get("access-control-allow-origin")).toBe("*");
  });
  it("returns a 101 response as the very same object", () => {
    const sock = {} as unknown;
    const res = { status: 101, webSocket: sock, headers: new Headers() } as unknown as Response;
    expect(withCors(res)).toBe(res);
    expect(res.headers.has("access-control-allow-origin")).toBe(false);
  });
  it("lets a browser read Retry-After", () => {
    expect(CORS["access-control-expose-headers"]).toBe("retry-after");
  });
});

describe("idleTimeoutSeconds limit", () => {
  it("defaults to 900 and can be overridden or disabled, but not set to a bad value", () => {
    expect(LIMITS.idleTimeoutSeconds).toBe(900);
    expect(loadLimits('{"idleTimeoutSeconds":5}').idleTimeoutSeconds).toBe(5);
    expect(loadLimits('{"idleTimeoutSeconds":0}').idleTimeoutSeconds).toBe(0);
    for (const bad of ["-1", "1.5", '"9"', "null"]) {
      const lim = loadLimits(`{"idleTimeoutSeconds":${bad}}`);
      expect(lim.idleTimeoutSeconds).toBe(900);
    }
  });
});



const NOW = 10_000_000;
const T = 900_000;
const mk = (st: Partial<ConnState> | null, auto?: number) => ({ st: st && ({ id: "a", src: "s", ...st } as ConnState), auto });
const run = (items: ReturnType<typeof mk>[], timeout = T) => idleSockets(items, (w) => w, NOW, timeout);

describe("idleSockets", () => {
  it("returns sockets silent for longer than the timeout, not those at or inside it", () => {
    const old = mk({ lastSeen: NOW - T - 1 });
    const edge = mk({ lastSeen: NOW - T });
    const fresh = mk({ lastSeen: NOW - 1 });
    expect(run([old, edge, fresh])).toEqual([old]);
  });
  it("counts a runtime-answered ping as life", () => {
    const pinged = mk({ lastSeen: NOW - 5 * T }, NOW - 1000);
    const stalePing = mk({ lastSeen: NOW - 5 * T }, NOW - 2 * T);
    expect(run([pinged, stalePing])).toEqual([stalePing]);
  });
  it("never returns unknown-age, closing or attachment-less sockets", () => {
    expect(run([mk({}), mk({ lastSeen: 0, closed: true }), mk(null)])).toEqual([]);
  });
  it("is disabled by a zero timeout", () => {
    expect(run([mk({ lastSeen: 0 })], 0)).toEqual([]);
  });
});

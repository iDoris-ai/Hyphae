import { describe, expect, it, vi } from "vitest";
import { CORS, createHealthCache, HEALTH_FAIL_TTL_MS, HEALTH_OK_TTL_MS, withCors } from "../src/http";
import { type ConnState, idleSockets, planReap } from "../src/limits";
import { LIMITS, loadLimits } from "../src/config";

vi.mock("cloudflare:workers", () => ({ DurableObject: class {} }));

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

describe("planReap", () => {
  const NOW = 10_000_000;
  type S = { st: ConnState; auto?: number };
  const sock = (src: string, ageMs: number, id = `${src}${ageMs}`): S => ({ st: { id, src, lastSeen: NOW - ageMs } });
  const read = (w: S) => w;
  const caps = { maxConnections: 100, maxConnsPerSource: 3, idleTimeoutSeconds: 10 };
  const plan = (socks: S[], src: string, c = caps) => planReap(socks, read, src, c, NOW);

  it("a full source frees only its own idle sockets, never another source's", () => {
    const a = [sock("A", 20_000, "a1"), sock("A", 1000, "a2"), sock("A", 500, "a3")];
    const b = [sock("B", 99_000, "b1"), sock("B", 50_000, "b2")];
    expect(plan([...a, ...b], "A").map((w) => w.st.id)).toEqual(["a1"]);
  });
  it("a full source with no idle sockets of its own closes nobody, even if others are idle", () => {
    const a = [sock("A", 1000), sock("A", 2000), sock("A", 3000)];
    expect(plan([...a, sock("B", 99_000)], "A")).toEqual([]);
  });
  it("a source below its cap closes nobody while the relay has room", () => {
    expect(plan([sock("A", 99_000), sock("B", 99_000)], "A")).toEqual([]);
  });
  it("a full relay frees others' idle sockets, only as many as needed and the longest-silent first", () => {
    const c = { ...caps, maxConnections: 4 };
    const socks = [sock("B", 30_000, "b1"), sock("C", 90_000, "c1"), sock("D", 20_000, "d1"), sock("E", 1000, "e1")];
    expect(plan(socks, "A", c).map((w) => w.st.id)).toEqual(["c1"]);
  });
  it("counts what the per-source step already frees toward the global need", () => {
    const c = { ...caps, maxConnections: 4, maxConnsPerSource: 2 };
    const socks = [sock("A", 30_000, "a1"), sock("A", 100, "a2"), sock("B", 90_000, "b1"), sock("C", 100, "c1")];
    expect(plan(socks, "A", c).map((w) => w.st.id)).toEqual(["a1"]);
  });
  it("a source still at its cap after freeing its own sockets does not close others' on a full relay (it is refused anyway)", () => {
    const c = { maxConnections: 3, maxConnsPerSource: 1, idleTimeoutSeconds: 1 };
    const socks = [sock("A", 100, "a1"), sock("B", 5000, "b1"), sock("C", 100, "c1")];
    expect(plan(socks, "A", c)).toEqual([]);
  });
  it("a source at its cap frees only as many of its own idle sockets as the new one needs", () => {
    const c = { ...caps, maxConnsPerSource: 3 };
    const a = [sock("A", 30_000, "a1"), sock("A", 20_000, "a2"), sock("A", 15_000, "a3")];
    expect(plan(a, "A", c).map((w) => w.st.id)).toEqual(["a1"]);
    const c2 = { ...caps, maxConnsPerSource: 2 };
    expect(plan(a, "A", c2).map((w) => w.st.id)).toEqual(["a1", "a2"]);
  });
  it("closes nothing when idle reaping is disabled", () => {
    expect(plan([sock("A", 99_000), sock("A", 99_000), sock("A", 99_000)], "A", { ...caps, idleTimeoutSeconds: 0 })).toEqual([]);
  });
});

describe("createHealthCache", () => {
  it("probes once per TTL, caches failures for a shorter time, and shares an in-flight probe", async () => {
    let t = 1000;
    const check = createHealthCache(() => t);
    let calls = 0;
    let result = true;
    const probe = async () => {
      calls++;
      return result;
    };
    expect(await check(probe)).toBe(true);
    expect(await Promise.all([check(probe), check(probe)])).toEqual([true, true]);
    expect(calls).toBe(1);
    t += HEALTH_OK_TTL_MS - 1;
    await check(probe);
    expect(calls).toBe(1);
    t += 1;
    result = false;
    expect(await check(probe)).toBe(false);
    expect(calls).toBe(2);
    t += HEALTH_FAIL_TTL_MS - 1;
    result = true;
    expect(await check(probe)).toBe(false); // failure still cached
    expect(calls).toBe(2);
    t += 1;
    expect(await check(probe)).toBe(true);
    expect(calls).toBe(3);
  });
  it("treats a throwing probe as a failure and recovers on the next probe", async () => {
    let t = 0;
    const check = createHealthCache(() => t);
    expect(await check(async () => Promise.reject(new Error("do down")))).toBe(false);
    t += HEALTH_FAIL_TTL_MS;
    expect(await check(async () => true)).toBe(true);
  });
  it("concurrent callers share one in-flight probe", async () => {
    const check = createHealthCache(() => 0);
    let calls = 0;
    let release!: () => void;
    const probe = () => new Promise<boolean>((r) => { calls++; release = () => r(true); });
    const both = Promise.all([check(probe), check(probe)]);
    release();
    expect(await both).toEqual([true, true]);
    expect(calls).toBe(1);
  });
});

describe("Worker routing", () => {
  it("/healthz reaches the Durable Object even when the client sends Accept: application/nostr+json", async () => {
    const { default: worker } = await import("../src/index");
    const calls: string[] = [];
    const env = {
      RELAY: {
        idFromName: () => "id",
        get: () => ({ fetch: async (r: Request) => (calls.push(new URL(r.url).pathname), new Response("ok\n")) }),
      },
    };
    const res = await worker.fetch(new Request("https://relay.test/healthz", { headers: { Accept: "application/nostr+json" } }), env as never);
    expect(res.status).toBe(200);
    expect(await res.text()).toBe("ok\n");
    expect(res.headers.get("content-type")).not.toContain("nostr+json");
    expect(res.headers.get("access-control-allow-origin")).toBe("*");
    expect(calls).toEqual(["/healthz"]);
    // Only GET probes the object.
    const before = calls.length;
    const post = await worker.fetch(new Request("https://relay.test/healthz", { method: "POST" }), env as never);
    expect(post.status).toBe(200);
    expect(await post.text()).toContain("Hyphae relay");
    expect(calls).toHaveLength(before);
    // NIP-11 on other paths still does not touch the object.
    const n = await worker.fetch(new Request("https://relay.test/", { headers: { Accept: "application/nostr+json" } }), env as never);
    expect(n.headers.get("content-type")).toBe("application/nostr+json");
    expect(calls).toHaveLength(before);
  });
});

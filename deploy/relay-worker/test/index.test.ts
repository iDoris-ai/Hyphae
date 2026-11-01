import { describe, expect, it } from "vitest";
import worker from "../src/index";
import { sourceKey } from "../src/limits";

type Seen = { headers: Headers } | null;

/** A Durable Object namespace that records the request it was given and answers with `reply`. */
function env(extra: Record<string, unknown> = {}, reply: () => Response | Promise<Response> = () => new Response("ok", { status: 200 })) {
  const state: { seen: Seen; name: string | null } = { seen: null, name: null };
  const RELAY = {
    idFromName: (n: string) => ((state.name = n), n),
    get: () => ({
      fetch: async (req: Request) => {
        state.seen = { headers: req.headers };
        return reply();
      },
    }),
  };
  return { e: { RELAY, ...extra } as never, state };
}

const upgrade = (headers: Record<string, string> = {}) =>
  new Request("https://relay.test/", { headers: { upgrade: "websocket", connection: "Upgrade", ...headers } });

describe("Worker entry: WebSocket upgrade", () => {
  it("refuses an upgrade that carries no source address, without calling the Durable Object", async () => {
    const { e, state } = env();
    const res = await worker.fetch(upgrade(), e);
    expect(res.status).toBe(400);
    expect(state.seen).toBeNull();
  });

  it("forwards the client address as x-hyphae-src, replacing one the client sent", async () => {
    const { e, state } = env();
    const res = await worker.fetch(upgrade({ "cf-connecting-ip": "203.0.113.9", "x-hyphae-src": "forged" }), e);
    expect(res.status).toBe(200);
    expect(state.name).toBe("main");
    expect(state.seen?.headers.get("x-hyphae-src")).toBe(sourceKey("203.0.113.9"));
    expect(state.seen?.headers.get("x-hyphae-src")).not.toBe("forged");
  });

  it("uses the header named by SRC_HEADER and ignores CF-Connecting-IP then", async () => {
    const { e, state } = env({ SRC_HEADER: "x-test-src" });
    await worker.fetch(upgrade({ "x-test-src": "198.51.100.7", "cf-connecting-ip": "203.0.113.9" }), e);
    expect(state.seen?.headers.get("x-hyphae-src")).toBe(sourceKey("198.51.100.7"));
    const none = await worker.fetch(upgrade({ "cf-connecting-ip": "203.0.113.9" }), e);
    expect(none.status).toBe(400); // the named header is missing, the default one does not count
  });

  it("answers 503 with a retry hint when the Durable Object call throws", async () => {
    const { e } = env({}, () => {
      throw new Error("overloaded");
    });
    const res = await worker.fetch(upgrade({ "cf-connecting-ip": "203.0.113.9" }), e);
    expect(res.status).toBe(503);
    expect(res.headers.get("retry-after")).toBe("30");
  });
});

describe("Worker entry: plain HTTP", () => {
  it("serves NIP-11 for any Accept casing and never touches the Durable Object", async () => {
    const { e, state } = env();
    const res = await worker.fetch(new Request("https://relay.test/", { headers: { accept: "Application/Nostr+Json" } }), e);
    expect(res.headers.get("content-type")).toBe("application/nostr+json");
    expect(res.headers.get("access-control-allow-origin")).toBe("*");
    const doc = (await res.json()) as { supported_nips: number[]; limitation: { max_limit: number } };
    expect(doc.supported_nips).toEqual([1, 9, 11, 67]);
    expect(doc.limitation.max_limit).toBeGreaterThan(0);
    expect(state.seen).toBeNull();
  });

  it("builds NIP-11 from LIMITS_JSON, the same variable the Durable Object reads", async () => {
    const { e } = env({ LIMITS_JSON: '{"maxLimit":123}' });
    const res = await worker.fetch(new Request("https://relay.test/", { headers: { accept: "application/nostr+json" } }), e);
    expect(((await res.json()) as { limitation: { max_limit: number } }).limitation.max_limit).toBe(123);
  });

  it("answers OPTIONS with 204 and CORS, and other GETs with plain text", async () => {
    const { e } = env();
    const opt = await worker.fetch(new Request("https://relay.test/", { method: "OPTIONS" }), e);
    expect(opt.status).toBe(204);
    expect(opt.headers.get("access-control-allow-origin")).toBe("*");
    const txt = await worker.fetch(new Request("https://relay.test/"), e);
    expect(txt.status).toBe(200);
    expect(txt.headers.get("content-type")).toContain("text/plain");
  });
});

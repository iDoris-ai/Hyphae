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

describe("Worker entry: edge admission of upgrades", () => {
  // upgradesPerMinutePerSource=1 -> the edge allows 2 per source (twice the Durable Object's budget).
  const tight = { LIMITS_JSON: '{"upgradesPerMinutePerSource":1}' };
  /** A namespace that counts how many requests reach the Durable Object. */
  function counted(extra: Record<string, unknown> = {}) {
    const calls = { n: 0 };
    const RELAY = {
      idFromName: () => "main",
      get: () => ({
        fetch: async () => {
          calls.n++;
          return new Response("ok", { status: 200 });
        },
      }),
    };
    return { e: { RELAY, ...extra } as never, calls };
  }
  const from = (ip: string) => upgrade({ "cf-connecting-ip": ip });

  it("answers an upgrade flood from one source with 429 and Retry-After, without calling the Durable Object", async () => {
    const { e, calls } = counted(tight);
    expect((await worker.fetch(from("198.51.100.50"), e)).status).toBe(200);
    expect((await worker.fetch(from("198.51.100.50"), e)).status).toBe(200);
    const res = await worker.fetch(from("198.51.100.50"), e);
    expect(res.status).toBe(429);
    expect(Number(res.headers.get("retry-after"))).toBeGreaterThanOrEqual(1);
    expect(calls.n).toBe(2); // the refused attempt never reached the Durable Object
    // a browser page can read why it was refused
    expect(res.headers.get("access-control-allow-origin")).toBe("*");
    expect(res.headers.get("access-control-expose-headers")).toContain("retry-after");
  });

  it("does not let one flooding source use up another source's allowance", async () => {
    const { e, calls } = counted(tight);
    for (let i = 0; i < 5; i++) await worker.fetch(from("198.51.100.51"), e);
    expect((await worker.fetch(from("198.51.100.52"), e)).status).toBe(200);
    expect(calls.n).toBe(3); // 2 admitted from the flooder, 1 from the other source
  });

  it("never spends upgrade tokens on /healthz, NIP-11 or plain GETs", async () => {
    const { e } = counted(tight);
    const ip = { "cf-connecting-ip": "198.51.100.53" };
    for (let i = 0; i < 6; i++) {
      await worker.fetch(new Request("https://relay.test/healthz", { headers: ip }), e);
      await worker.fetch(new Request("https://relay.test/", { headers: { ...ip, accept: "application/nostr+json" } }), e);
      await worker.fetch(new Request("https://relay.test/", { headers: ip }), e);
    }
    // the full allowance of 2 is still there, and only the third upgrade is refused
    expect((await worker.fetch(from("198.51.100.53"), e)).status).toBe(200);
    expect((await worker.fetch(from("198.51.100.53"), e)).status).toBe(200);
    expect((await worker.fetch(from("198.51.100.53"), e)).status).toBe(429);
  });

  it("honours the Cloudflare rate-limit binding, and admits when the binding throws", async () => {
    const refusing = counted({ ...tight, UPGRADE_LIMITER: { limit: async () => ({ success: false }) } });
    const res = await worker.fetch(from("198.51.100.54"), refusing.e);
    expect(res.status).toBe(429);
    expect(res.headers.get("retry-after")).toBe("10");
    expect(refusing.calls.n).toBe(0);

    const broken = counted({
      ...tight,
      UPGRADE_LIMITER: {
        limit: async () => {
          throw new Error("binding down");
        },
      },
    });
    expect((await worker.fetch(from("198.51.100.55"), broken.e)).status).toBe(200); // fail open, the Durable Object still limits
  });
});

describe("Worker entry: landing page", () => {
  const get = (path: string, accept?: string) =>
    worker.fetch(new Request(`https://relay.test${path}`, accept ? { headers: { accept } } : {}), env().e);

  it("serves HTML with a strict CSP to a browser at / and never calls the Durable Object", async () => {
    const { e, state } = env();
    const res = await worker.fetch(new Request("https://relay.test/", { headers: { accept: "text/html,application/xhtml+xml" } }), e);
    expect(res.status).toBe(200);
    expect(res.headers.get("content-type")).toContain("text/html");
    expect(res.headers.get("content-security-policy")).toContain("default-src 'none'");
    expect(await res.text()).toContain("wss://relay.aastar.io");
    expect(state.seen).toBeNull();
  });

  it("keeps the plain answer for clients that do not ask for HTML, and NIP-11 for those that ask for it", async () => {
    expect(await (await get("/")).text()).toContain("connect with a Nostr client");
    expect(await (await get("/", "*/*")).text()).toContain("connect with a Nostr client");
    const nip = await get("/", "text/html, application/nostr+json");
    expect(nip.headers.get("content-type")).toBe("application/nostr+json");
  });

  it("serves the logo as SVG and nothing else under other paths", async () => {
    const logo = await get("/logo.svg");
    expect(logo.headers.get("content-type")).toBe("image/svg+xml");
    expect(await logo.text()).toContain("<svg");
    expect(await (await get("/other", "text/html")).text()).toContain("connect with a Nostr client");
  });
});

describe("Worker entry: landing page languages", () => {
  const page = async (path: string, al?: string) => {
    const headers: Record<string, string> = { accept: "text/html" };
    if (al) headers["accept-language"] = al;
    const res = await worker.fetch(new Request(`https://relay.test${path}`, { headers }), env().e);
    return { res, text: await res.text() };
  };

  it("picks the language from ?lang= first, then Accept-Language, else English", async () => {
    expect((await page("/?lang=th", "zh-CN")).text).toContain('<html lang="th"');
    expect((await page("/", "zh-CN,zh;q=0.9,en;q=0.8")).text).toContain('<html lang="zh"');
    expect((await page("/", "th-TH")).text).toContain('<html lang="th"');
    expect((await page("/", "fr-FR")).text).toContain('<html lang="en"');
    expect((await page("/?lang=xx")).text).toContain('<html lang="en"');
    expect((await page("/")).res.headers.get("vary")).toContain("Accept-Language");
  });

  it("states what the relay can and cannot do in every language", async () => {
    for (const l of ["en", "zh", "th"]) {
      const { text } = await page(`/?lang=${l}`);
      expect(text).toContain("NIP-44");
      expect(text).toContain("hyphae relay set");
      expect(text).toContain("SHA256SUMS");
      expect(text).toContain("hyphae identity create");
      expect(text.match(/<li>/g)?.length).toBe(5);
      expect(text.match(/<details>/g)?.length).toBe(7);
    }
  });
});

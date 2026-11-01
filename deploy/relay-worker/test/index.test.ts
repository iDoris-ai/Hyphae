import { describe, expect, it, vi } from "vitest";

vi.mock("cloudflare:workers", () => ({ DurableObject: class {} }));

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
    // NIP-11 on other paths still does not touch the object.
    const n = await worker.fetch(new Request("https://relay.test/", { headers: { Accept: "application/nostr+json" } }), env as never);
    expect(n.headers.get("content-type")).toBe("application/nostr+json");
    expect(calls).toHaveLength(1);
  });
});

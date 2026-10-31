import { describe, expect, it } from "vitest";
import { createAdmission, type RateLimiter } from "../src/admission";

const clock = () => {
  let t = 1_000_000;
  return { now: () => t, advance: (ms: number) => (t += ms) };
};
const limiter = (success: boolean | Error) => {
  const calls: string[] = [];
  const l: RateLimiter = {
    async limit({ key }) {
      calls.push(key);
      if (success instanceof Error) throw success;
      return { success };
    },
  };
  return { l, calls };
};

describe("edge admission", () => {
  it("admits up to the per-minute budget then refuses with a Retry-After, per source", async () => {
    const c = clock();
    const a = createAdmission({ perMinute: 6, now: c.now });
    for (let i = 0; i < 6; i++) expect((await a.check("1.1.1.1")).ok).toBe(true);
    const r = await a.check("1.1.1.1");
    expect(r).toMatchObject({ ok: false, by: "isolate" });
    expect(r.ok === false && r.retryAfter).toBe(10); // 6/min = one token per 10 s
    expect((await a.check("2.2.2.2")).ok).toBe(true); // other sources are unaffected
    c.advance(10_000);
    expect((await a.check("1.1.1.1")).ok).toBe(true); // refilled
  });

  it("Retry-After stays a finite integer when the budget is zero", async () => {
    const r = await createAdmission({ perMinute: 0, now: clock().now }).check("k");
    expect(r).toEqual({ ok: false, retryAfter: 60, by: "isolate" });
  });

  it("a refused attempt does not reach the Cloudflare binding", async () => {
    const { l, calls } = limiter(true);
    const a = createAdmission({ perMinute: 1, now: clock().now });
    await a.check("k", l);
    await a.check("k", l);
    expect(calls).toEqual(["k"]);
  });

  it("refuses with 10 s Retry-After when the binding says no", async () => {
    const { l } = limiter(false);
    const r = await createAdmission({ perMinute: 100 }).check("k", l);
    expect(r).toEqual({ ok: false, retryAfter: 10, by: "binding" });
  });

  it("fails open when the binding throws, and logs only once", async () => {
    const { l } = limiter(new Error("boom"));
    const logs: string[] = [];
    const a = createAdmission({ perMinute: 100, log: (m) => logs.push(m) });
    for (let i = 0; i < 5; i++) expect((await a.check("k", l)).ok).toBe(true);
    expect(logs).toHaveLength(1);
  });

  it("works without a binding", async () => {
    expect((await createAdmission({ perMinute: 5 }).check("k", undefined)).ok).toBe(true);
  });

  it("keeps memory bounded under a many-source flood and evicts the least recently used", async () => {
    const a = createAdmission({ perMinute: 1, maxKeys: 3, now: clock().now });
    await a.check("a");
    await a.check("b");
    await a.check("c");
    await a.check("a"); // refused, but a becomes most recent
    await a.check("d"); // evicts b
    expect((await a.check("b")).ok).toBe(true); // b was evicted: fresh bucket
    expect((await a.check("a")).ok).toBe(false); // a was kept
  });

  it("a normal client within the Durable Object budget (60 burst, then 1/s) is never refused", async () => {
    const c = clock();
    const a = createAdmission({ perMinute: 120, now: c.now });
    for (let i = 0; i < 60; i++) expect((await a.check("x")).ok).toBe(true);
    for (let i = 0; i < 120; i++) {
      c.advance(1000);
      expect((await a.check("x")).ok).toBe(true);
    }
  });
});

import { describe, expect, it } from "vitest";
import { LIMITS, type Limits } from "../src/config";
import { Budgets, type ConnState, countViolation, eventWeight, sourceKey, takeConn, takeToken } from "../src/limits";

const lim = (over: Partial<Limits> = {}): Limits => ({ ...LIMITS, ...over });

describe("takeToken with cost", () => {
  it("charges the cost and rejects when it exceeds what is left", () => {
    const r1 = takeToken(undefined, 0, 100, 60);
    expect(r1.ok).toBe(true);
    const r2 = takeToken(r1.bucket, 0, 100, 60);
    expect(r2.ok).toBe(false);
    expect(r2.bucket).toBe(r1.bucket);
    expect(takeToken(undefined, 0, 100, 101).ok).toBe(false);
  });
});

describe("takeToken (#5)", () => {
  it("starts full, drains, and rejects once empty", () => {
    let b = undefined as ReturnType<typeof takeToken>["bucket"] | undefined;
    let accepted = 0;
    for (let i = 0; i < 70; i++) {
      const r = takeToken(b, 1000, 60);
      b = r.bucket;
      if (r.ok) accepted++;
    }
    expect(accepted).toBe(60);
  });
  it("a rejected attempt leaves the bucket untouched", () => {
    const drained = { tokens: 0.5, last: 1000 };
    const r = takeToken(drained, 1500, 60); // +0.5s refills 0.5 token -> 1.0, allowed
    expect(r.ok).toBe(true);
    const r2 = takeToken(drained, 1100, 60); // +0.1 -> 0.6 < 1
    expect(r2.ok).toBe(false);
    expect(r2.bucket).toBe(drained);
  });
  it("refills continuously and caps at capacity", () => {
    const empty = { tokens: 0, last: 0 };
    expect(takeToken(empty, 1000, 60).ok).toBe(true); // 1 token/s
    expect(takeToken(empty, 500, 60).ok).toBe(false);
    const r = takeToken({ tokens: 0, last: 0 }, 10 * 60_000, 60);
    expect(r.bucket.tokens).toBeCloseTo(59);
  });
  it("ignores a clock that goes backwards", () => {
    const b = { tokens: 3, last: 5000 };
    const r = takeToken(b, 1000, 60);
    expect(r.ok).toBe(true);
    expect(r.bucket.tokens).toBe(2);
  });
});

describe("sourceKey (F2)", () => {
  it("keeps IPv4 as is", () => {
    expect(sourceKey("203.0.113.7")).toBe("203.0.113.7");
    expect(sourceKey("unknown")).toBe("unknown");
  });
  it("groups IPv6 by /64", () => {
    const a = sourceKey("2001:db8:1:2::1");
    expect(a).toBe("2001:db8:1:2::/64");
    expect(sourceKey("2001:0db8:0001:0002:ffff:ffff:ffff:ffff")).toBe(a);
    expect(sourceKey("2001:DB8:1:2:a:b:c:d")).toBe(a);
    expect(sourceKey("2001:db8:1:3::1")).not.toBe(a);
    expect(sourceKey("2001:db8::1")).toBe("2001:db8:0:0::/64");
    expect(sourceKey("::1")).toBe("0:0:0:0::/64");
  });
  it("handles zone ids and IPv4-mapped addresses", () => {
    expect(sourceKey("fe80::1%eth0")).toBe(sourceKey("fe80::2"));
    expect(sourceKey("::ffff:203.0.113.7")).toBe("203.0.113.7");
    expect(sourceKey("::ffff:cb00:7107")).toBe("203.0.113.7");
  });
  it("does not throw on garbage", () => {
    for (const g of ["", "::::", "1:2:3", "zz::1", "1::2::3", "1:2:3:4:5:6:7:8:9", "::ffff:999.1.1.1"]) {
      expect(() => sourceKey(g)).not.toThrow();
    }
    expect(sourceKey("1::2::3")).toBe("1::2::3");
  });
});

describe("eventWeight", () => {
  it("eventWeight grows with the number of tag strings and with size", () => {
    expect(eventWeight(0, 200)).toBe(1);
    expect(eventWeight(64, 200)).toBe(3);
    expect(eventWeight(0, 100_000)).toBe(1 + 24);
  });
});

describe("per-connection buckets and violations", () => {
  it("takeConn drains a connection's own bucket and rejects without changing it", () => {
    const st: ConnState = { id: "c", src: "s" };
    let ok = 0;
    for (let i = 0; i < 10; i++) if (takeConn(st, "ev", 5, 1000)) ok++;
    expect(ok).toBe(5);
    const before = st.ev;
    expect(takeConn(st, "ev", 5, 1000)).toBe(false);
    expect(st.ev).toBe(before);
    expect(st.rq).toBeUndefined();
  });
  it("countViolation asks for a close at the threshold and restarts its window after a minute", () => {
    const st: ConnState = { id: "c", src: "s" };
    expect(countViolation(st, 3, 1000)).toBe(false);
    expect(countViolation(st, 3, 2000)).toBe(false);
    expect(countViolation(st, 3, 3000)).toBe(true);
    const st2: ConnState = { id: "c", src: "s" };
    countViolation(st2, 3, 1000);
    countViolation(st2, 3, 2000);
    expect(countViolation(st2, 3, 70_000)).toBe(false); // a new window
    expect(st2.bad).toBe(1);
  });
});

describe("refundEvent", () => {
  it("gives back exactly what chargeEvent took, from the source and the global budget", () => {
    const b = new Budgets(lim({ sourceWriteBytesPerMinute: 1000, globalEventWeightPerMinute: 10 }));
    expect(b.chargeEvent("a", 900, 9)).toBe("ok");
    expect(b.chargeEvent("a", 900, 1)).toBe("source");
    b.refundEvent("a", 900, 9);
    expect(b.chargeEvent("a", 1000, 10)).toBe("ok"); // both are back to full
  });
  it("never lifts a bucket above its capacity, and ignores negative, zero and non-finite amounts", () => {
    const b = new Budgets(lim({ sourceWriteBytesPerMinute: 1000, globalEventWeightPerMinute: 10 }));
    expect(b.chargeEvent("a", 100, 1)).toBe("ok");
    b.refundEvent("a", 100_000, 1000); // far more than was taken
    expect(b.chargeEvent("a", 1001, 1)).toBe("source");
    expect(b.chargeEvent("a", 1000, 10)).toBe("ok");
    expect(b.chargeEvent("a", 1, 1)).toBe("source");
    b.refundEvent("a", -500, -5);
    b.refundEvent("a", NaN, NaN);
    b.refundEvent("a", Infinity, Infinity);
    b.refundEvent("a", 0, 0);
    expect(b.chargeEvent("a", 1, 1)).toBe("source");
    expect(b.chargeEvent("z", 0, 1)).toBe("global");
  });
  it("does not create state for a source that is not tracked", () => {
    const b = new Budgets(lim({ maxSourceEntries: 1 }));
    b.refundEvent("never-seen", 500, 1);
    expect((b as unknown as { sources: Map<string, unknown> }).sources.size).toBe(0);
  });
});

describe("Budgets", () => {
  it("chargeEvent: a source over its stored bytes is told so; running out of global weight is a different answer", () => {
    const b = new Budgets(lim({ sourceWriteBytesPerMinute: 1000, globalEventWeightPerMinute: 10 }));
    expect(b.chargeEvent("a", 600, 1)).toBe("ok");
    expect(b.chargeEvent("a", 600, 1)).toBe("source");
    expect(b.chargeEvent("b", 0, 9)).toBe("ok"); // ephemeral: no stored bytes; global is 1 + 9 = 10
    expect(b.chargeEvent("c", 10, 1)).toBe("global");
  });
  it("chargeEvent charges nothing when it refuses", () => {
    const b = new Budgets(lim({ sourceWriteBytesPerMinute: 1000, globalEventWeightPerMinute: 5 }));
    expect(b.chargeEvent("a", 900, 6)).toBe("global"); // the source budget must not have been spent
    expect(b.chargeEvent("a", 900, 1)).toBe("ok");
  });
  it("chargeValidation: a source pays for looking at frames out of its own share, never from the global budget", () => {
    const b = new Budgets(lim({ globalEventWeightPerMinute: 100, sourceEventWeightPerMinute: 25 }));
    let ok = 0;
    for (let i = 0; i < 40; i++) if (b.chargeValidation("a", 1)) ok++;
    expect(ok).toBe(25);
    expect(b.chargeValidation("a", 1)).toBe(false); // its own fault
    expect(b.chargeValidation("b", 25)).toBe(true); // other sources are unaffected
    expect(b.chargeEvent("c", 0, 100)).toBe("ok"); // and the global budget was never touched
  });
  it("chargeHistory has its own socket, source and global budgets, apart from live delivery", () => {
    const b = new Budgets(lim({ socketHistoryBytesPerMinute: 100, sourceHistoryBytesPerMinute: 150, globalHistoryBytesPerMinute: 1000, socketOutboundBytesPerMinute: 100 }));
    expect(b.chargeHistory("s", "c1", 100)).toBe(true);
    expect(b.chargeHistory("s", "c1", 10)).toBe(false); // the socket's history allowance is used up
    expect(b.chargeHistory("s", "c2", 60)).toBe(false); // 160 > 150 for the source
    expect(b.chargeHistory("s", "c2", 50)).toBe(true);
    expect(b.chargeHistory("t", "c3", 100)).toBe(true); // another source
    expect(b.chargeDelivery("s", "c1", 100)).toBe("ok"); // live delivery of the same socket is untouched
    const g = new Budgets(lim({ globalHistoryBytesPerMinute: 100 }));
    expect(g.chargeHistory("a", "x", 80)).toBe(true);
    expect(g.chargeHistory("b", "y", 80)).toBe(false);
  });
  it("takeUpgrade and takeSource are per source", () => {
    const b = new Budgets(lim({ upgradesPerMinutePerSource: 2, sourceEventsPerMinute: 1 }));
    expect([b.takeUpgrade("a"), b.takeUpgrade("a"), b.takeUpgrade("a"), b.takeUpgrade("b")]).toEqual([true, true, false, true]);
    expect([b.takeSource("a", "ev"), b.takeSource("a", "ev"), b.takeSource("b", "ev")]).toEqual([true, false, true]);
  });
  it("the per-source table is a bounded LRU", () => {
    const b = new Budgets(lim({ maxSourceEntries: 2, upgradesPerMinutePerSource: 1 }));
    b.takeUpgrade("a");
    b.takeUpgrade("b");
    b.takeUpgrade("c"); // evicts "a"
    expect(b.takeUpgrade("a")).toBe(true); // forgotten, so it starts with a full bucket again
    expect(b.takeUpgrade("c")).toBe(false);
  });
  it("chargeDelivery: a source gets its share, the socket its quota, the global budget ends the fan-out", () => {
    const b = new Budgets(lim({ sourceOutboundBytesPerMinute: 250, socketOutboundBytesPerMinute: 150, globalOutboundBytesPerMinute: 1000 }));
    expect(b.chargeDelivery("s", "c1", 100)).toBe("ok");
    expect(b.chargeDelivery("s", "c1", 100)).toBe("socket"); // 200 > 150 on this socket
    expect(b.chargeDelivery("s", "c2", 100)).toBe("ok");
    expect(b.chargeDelivery("s", "c3", 100)).toBe("source"); // 300 > 250 for the source
    const g = new Budgets(lim({ globalOutboundBytesPerMinute: 100 }));
    expect(g.chargeDelivery("s", "c", 80)).toBe("ok");
    expect(g.chargeDelivery("t", "d", 80)).toBe("global");
  });
  it("with the default limits a socket that keeps exceeding its own quota is dropped, then closed", () => {
    const b = new Budgets(lim()); // defaults: 4MB per socket, 16MB per source, 3 windows of 60s
    const MB = 1024 * 1024;
    let r = "ok";
    for (let i = 0; i < 5 && r === "ok"; i++) r = b.chargeDelivery("s", "c", MB);
    expect(r).toBe("socket"); // the socket quota is reachable before the source share
    expect(b.overQuota("c", 1_000_000)).toBe(false);
    expect(b.overQuota("c", 1_055_000)).toBe(false);
    expect(b.overQuota("c", 1_110_000)).toBe(false);
    expect(b.overQuota("c", 1_121_000)).toBe(true); // over quota for two windows, gaps below one window
  });
  it("overQuota closes a socket only after it stays over quota for (N-1) windows", () => {
    const b = new Budgets(lim({ quotaWindowMs: 1000, maxOverQuotaWindows: 3, socketOutboundBytesPerMinute: 1 }));
    b.chargeDelivery("s", "c", 1);
    b.chargeDelivery("s", "c", 1); // the socket entry exists now
    expect(b.overQuota("c", 10_000)).toBe(false); // episode starts
    expect(b.overQuota("c", 10_900)).toBe(false);
    expect(b.overQuota("c", 11_800)).toBe(false); // 1.8 windows
    expect(b.overQuota("c", 12_100)).toBe(true); // 2.1 windows, every gap below one window
    expect(b.overQuota("c", 20_000)).toBe(false); // gap >= 1 window: a new episode
    b.forget("c");
    expect(b.overQuota("c", 30_000)).toBe(false);
  });
  it("overQuota: a gap of exactly one window ends the episode, a gap just below it does not", () => {
    const mk = () => {
      const b = new Budgets(lim({ quotaWindowMs: 1000, maxOverQuotaWindows: 3, socketOutboundBytesPerMinute: 1 }));
      b.chargeDelivery("s", "c", 5);
      return b;
    };
    const a = mk();
    a.overQuota("c", 10_000);
    a.overQuota("c", 10_999);
    a.overQuota("c", 11_998);
    expect(a.overQuota("c", 12_997)).toBe(true); // every gap 999: continuous, ~3 windows
    const b = mk();
    b.overQuota("c", 10_000);
    b.overQuota("c", 11_000); // gap == window: new episode
    expect(b.overQuota("c", 12_000)).toBe(false); // gap == window again: still no continuous episode
  });
  it("a socket refused only by the source quota can still be closed with overQuota", () => {
    const b = new Budgets(lim({ quotaWindowMs: 1000, maxOverQuotaWindows: 3, sourceOutboundBytesPerMinute: 1 }));
    expect(b.chargeDelivery("s", "fresh", 5)).toBe("source"); // never reached the socket bucket
    expect(b.overQuota("fresh", 10_000)).toBe(false);
    expect(b.overQuota("fresh", 10_900)).toBe(false);
    expect(b.overQuota("fresh", 12_100)).toBe(false); // gap 1.2 windows: restarted
    expect(b.overQuota("fresh", 12_900)).toBe(false);
    expect(b.overQuota("fresh", 14_200)).toBe(false);
    expect(b.overQuota("fresh", 14_900)).toBe(false);
    const c = new Budgets(lim({ quotaWindowMs: 1000, maxOverQuotaWindows: 3, sourceOutboundBytesPerMinute: 1 }));
    c.chargeDelivery("s", "x", 5);
    c.overQuota("x", 10_000);
    c.overQuota("x", 10_900);
    c.overQuota("x", 11_800);
    expect(c.overQuota("x", 12_100)).toBe(true);
  });
  it("the per-socket table is bounded, oldest first, and forget still removes an entry", () => {
    const b = new Budgets(lim({ maxSourceEntries: 2, sourceOutboundBytesPerMinute: 1, quotaWindowMs: 1000, maxOverQuotaWindows: 2 }));
    b.chargeDelivery("s", "a", 5);
    b.chargeDelivery("s", "b", 5);
    b.chargeDelivery("s", "c", 5); // evicts "a"
    expect(b.overQuota("a", 1000)).toBe(false);
    b.overQuota("c", 1000);
    expect(b.overQuota("c", 1900)).toBe(false);
    expect(b.overQuota("c", 2000)).toBe(true); // "c" survived
    b.forget("c");
    expect(b.overQuota("c", 2100)).toBe(false);
  });
});

describe("hardening (review of #301)", () => {
  it("takeToken rejects zero, negative and non-finite costs and clocks, leaving the bucket untouched", () => {
    const b = { tokens: 1, last: 0 };
    for (const c of [0, -5, NaN, Infinity]) {
      const r = takeToken(b, 0, 10, c);
      expect(r.ok).toBe(false);
      expect(r.bucket).toBe(b);
    }
    expect(takeToken(b, NaN, 10, 1).ok).toBe(false);
    expect(takeToken(undefined, 0, 10, -1).bucket.tokens).toBe(10);
  });
  it("takeToken never moves last backwards", () => {
    const r = takeToken({ tokens: 3, last: 5000 }, 1000, 60);
    expect(r.bucket.last).toBe(5000);
    // the interval 1000..5000 is not refilled a second time when the clock recovers
    const r2 = takeToken(r.bucket, 5000, 60, 1);
    expect(r2.bucket.tokens).toBe(1);
  });
  it("eventWeight does not overflow int32 for huge sizes", () => {
    expect(eventWeight(0, 2 ** 31)).toBe(1 + 2 ** 19);
    expect(eventWeight(0, 2 ** 32)).toBe(1 + 2 ** 20);
    expect(eventWeight(2 ** 32, 0)).toBe(1 + 2 ** 27);
  });
  it("countViolation: an exact-60s boundary is the next window, and a burst across it is still caught", () => {
    const a: ConnState = { id: "c", src: "s" };
    countViolation(a, 3, 1000);
    countViolation(a, 3, 2000);
    expect(countViolation(a, 3, 61_000)).toBe(true); // exactly 60s after the window start: next window, previous one fully weighted
    const b: ConnState = { id: "c", src: "s" };
    countViolation(b, 3, 1000);
    countViolation(b, 3, 2000);
    expect(countViolation(b, 2, 61_001)).toBe(true); // a fixed window would have reset to 1 here
    const c: ConnState = { id: "c", src: "s" };
    countViolation(c, 3, 1000);
    countViolation(c, 3, 2000);
    expect(countViolation(c, 3, 100_000)).toBe(false); // the previous window has mostly aged out
    expect(countViolation({ id: "c", src: "s" }, 3, 0)).toBe(false); // now = 0 is a valid start
    const d: ConnState = { id: "c", src: "s" };
    countViolation(d, 3, 0);
    countViolation(d, 3, 1);
    expect(countViolation(d, 3, 2)).toBe(true);
  });
  it("sourceKey caps the key length and maps an empty input to a fixed key", () => {
    expect(sourceKey("")).toBe("unknown");
    expect(sourceKey("   ")).toBe("unknown");
    expect(sourceKey("%eth0")).toBe("unknown");
    expect(sourceKey("x".repeat(100_000)).length).toBeLessThanOrEqual(64);
    expect(sourceKey("1:2:3:4:5:6:7:8:9" + "a".repeat(500)).length).toBeLessThanOrEqual(64);
  });
});

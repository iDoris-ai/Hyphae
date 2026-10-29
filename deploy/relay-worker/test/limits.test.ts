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
    expect(b.overQuota("c", 1_070_000)).toBe(false);
    expect(b.overQuota("c", 1_130_000)).toBe(true); // over quota for about two windows
  });
  it("overQuota closes a socket only after it stays over quota for (N-1) windows", () => {
    const b = new Budgets(lim({ quotaWindowMs: 1000, maxOverQuotaWindows: 3, socketOutboundBytesPerMinute: 1 }));
    b.chargeDelivery("s", "c", 1);
    b.chargeDelivery("s", "c", 1); // the socket entry exists now
    expect(b.overQuota("c", 10_000)).toBe(false); // episode starts
    expect(b.overQuota("c", 11_500)).toBe(false); // 1.5 windows
    expect(b.overQuota("c", 12_100)).toBe(true); // 2.1 windows, gaps below 2 windows
    expect(b.overQuota("c", 20_000)).toBe(false); // gap >= 2 windows: a new episode
    b.forget("c");
    expect(b.overQuota("c", 30_000)).toBe(false);
  });
});

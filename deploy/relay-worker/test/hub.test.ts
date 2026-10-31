import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Limits } from "../src/config";
import { EVENTS_DDL } from "../src/db";
import { DELETION_DDL } from "../src/deletion";
import { broadcast } from "../src/fanout";
import { type Hub, peekEventId } from "../src/hub";
import { processEvent } from "../src/events";
import { Budgets, type ConnState, countViolation, takeConn } from "../src/limits";
import { processClose, processReq } from "../src/req";
import { storeEvent } from "../src/store";
import { SUBS_DDL, SubStore } from "../src/subs";
import { alice, bob, signed } from "./helpers/keys";
import { makeDb } from "./helpers/sqlite";

// Token buckets refill with the wall clock. A frozen clock keeps every budget assertion exact; the
// one test that needs time to pass advances it explicitly.
beforeEach(() => {
  vi.useFakeTimers({ toFake: ["Date"] });
  vi.setSystemTime(new Date("2026-10-28T12:00:00Z"));
});
afterEach(() => {
  vi.useRealTimers();
});

class FakeWs {
  sent: string[] = [];
  closed?: [number, string];
  att: unknown;
  send(t: string) {
    this.sent.push(t);
  }
  serializeAttachment(v: unknown) {
    this.att = structuredClone(v);
  }
  deserializeAttachment() {
    return this.att;
  }
  close(code: number, reason: string) {
    this.closed = [code, reason];
  }
  frames() {
    return this.sent.map((s) => JSON.parse(s) as unknown[]);
  }
}

function setup(over: Partial<Limits> = {}) {
  const db = makeDb(EVENTS_DDL + DELETION_DDL + SUBS_DDL, over);
  const lim = db.lim;
  const sockets: FakeWs[] = [];
  const hub: Hub = {
    db,
    lim,
    budgets: new Budgets(lim),
    subs: new SubStore(db),
    send: (ws, p) => ws.send(JSON.stringify(p)),
    sendRaw: (ws, t) => ws.send(t),
    take: (ws, st, which, perMinute) => {
      const ok = takeConn(st, which, perMinute);
      if (ok) ws.serializeAttachment(st);
      return ok;
    },
    violation: (ws, st) => {
      if (countViolation(st, lim.maxViolations)) hub.forceClose(ws, 1008, "policy violation", st);
      ws.serializeAttachment(st);
    },
    forceClose: (ws, code, reason, st) => {
      const cur = st ?? (ws.deserializeAttachment() as ConnState);
      cur.closed = true;
      ws.serializeAttachment(cur);
      (ws as unknown as FakeWs).close(code, reason);
    },
    sockets: () => sockets as unknown as WebSocket[],
  };
  const conn = (src = "1.1.1.1") => {
    const ws = new FakeWs();
    const st: ConnState = { id: "c" + sockets.length, src };
    ws.serializeAttachment(st);
    sockets.push(ws);
    return { ws: ws as unknown as WebSocket, fake: ws, st };
  };
  return { db, hub, conn };
}

describe("peekEventId", () => {
  it("finds the id of an oversized EVENT frame without parsing it", () => {
    const e = signed(alice, { content: "x".repeat(600_000) });
    expect(peekEventId(JSON.stringify(["EVENT", e]))).toBe(e.id);
    expect(peekEventId(JSON.stringify(["EVENT", { id: e.id, pubkey: e.pubkey, content: "y".repeat(600_000) }]))).toBe(e.id);
    expect(peekEventId(JSON.stringify(["REQ", "x", {}]))).toBeNull();
    expect(peekEventId("garbage")).toBeNull();
  });
});

describe("processEvent", () => {
  it("stores a valid event, answers OK and delivers it to a matching subscriber", () => {
    const { hub, conn, db } = setup();
    const sub = conn();
    processReq(hub, sub.ws, sub.st, ["live", { kinds: [1] }]);
    const pub = conn();
    const e = signed(alice, { created_at: Math.floor(Date.now() / 1000) });
    processEvent(hub, pub.ws, pub.st, e, 300);
    expect(pub.fake.frames()).toEqual([["OK", e.id, true, ""]]);
    expect(sub.fake.frames().at(-1)).toEqual(["EVENT", "live", e]);
    expect(db.sql.exec("SELECT count(*) AS n FROM events").one().n).toBe(1);
    processEvent(hub, pub.ws, pub.st, e, 300);
    expect(pub.fake.frames().at(-1)?.[3]).toMatch(/duplicate/);
  });
  it("rejects a bad signature and an empty tag without storing anything", () => {
    const { hub, conn, db } = setup();
    const c = conn();
    const bad = { ...signed(alice, { created_at: Math.floor(Date.now() / 1000) }), sig: "0".repeat(128) };
    processEvent(hub, c.ws, c.st, bad, 300);
    processEvent(hub, c.ws, c.st, signed(alice, { tags: [[]], created_at: Math.floor(Date.now() / 1000) }), 300);
    expect(c.fake.frames().map((f) => f[2])).toEqual([false, false]);
    expect(db.sql.exec("SELECT count(*) AS n FROM events").one().n).toBe(0);
  });
  it("charges the global budget only after validation: junk with many tags drains nothing", () => {
    const { hub, conn } = setup({ globalEventWeightPerMinute: 5 });
    const atk = conn("9.9.9.9");
    const junk = { ...signed(alice), tags: Array.from({ length: 50_000 }, () => []) };
    for (let i = 0; i < 20; i++) processEvent(hub, atk.ws, atk.st, junk, 500_000);
    expect(new Set(atk.fake.frames().map((f) => f[3]))).toEqual(new Set(["invalid: bad tags"]));
    const honest = conn("8.8.8.8");
    processEvent(hub, honest.ws, honest.st, signed(bob, { created_at: Math.floor(Date.now() / 1000) }), 300);
    expect(honest.fake.frames()[0][2]).toBe(true);
  });
  it("a drained global budget is not the connection's violation; a source over its stored bytes is", () => {
    const { hub, conn } = setup({ globalEventWeightPerMinute: 1, sourceWriteBytesPerMinute: 500 });
    const now = Math.floor(Date.now() / 1000);
    const a = conn("1.1.1.1");
    processEvent(hub, a.ws, a.st, signed(alice, { created_at: now }), 100);
    processEvent(hub, a.ws, a.st, signed(alice, { created_at: now, content: "second" }), 100);
    expect(a.fake.frames()[1][3]).toMatch(/relay is busy/);
    expect(a.st.bad).toBeUndefined();
    const b = conn("2.2.2.2");
    processEvent(hub, b.ws, b.st, signed(bob, { created_at: now }), 600);
    expect(b.fake.frames()[0][3]).toMatch(/too much load/);
    expect(b.st.bad).toBe(1);
  });
  it("a replay of a stored event is answered 'duplicate' without charging the shared budgets", () => {
    const { hub, conn } = setup({ globalEventWeightPerMinute: 2, sourceWriteBytesPerMinute: 700 });
    const now = Math.floor(Date.now() / 1000);
    const c = conn();
    const e = signed(alice, { created_at: now });
    processEvent(hub, c.ws, c.st, e, 500);
    for (let i = 0; i < 5; i++) processEvent(hub, c.ws, c.st, e, 500); // would exceed 700 bytes if charged
    expect(c.fake.frames().map((f) => f[2])).toEqual([true, true, true, true, true, true]);
    expect(String(c.fake.frames()[5][3])).toMatch(/duplicate/);
    processEvent(hub, c.ws, c.st, signed(bob, { created_at: now }), 100); // the budgets are still there
    expect(c.fake.frames()[6][2]).toBe(true);
  });
  it("answers OK with a note when only part of a deletion request was processed, and fans an ephemeral event out", () => {
    const { hub, conn } = setup({ maxDeletionTargets: 1 });
    const now = Math.floor(Date.now() / 1000);
    const c = conn();
    processEvent(hub, c.ws, c.st, signed(alice, { kind: 5, created_at: now, tags: [["e", "a".repeat(64)], ["e", "b".repeat(64)]] }), 300);
    expect(String(c.fake.frames()[0][3])).toMatch(/^ignored/);
    const sub = conn("5.5.5.5");
    processReq(hub, sub.ws, sub.st, ["s", { kinds: [20001] }]);
    const eph = signed(alice, { kind: 20001, created_at: now });
    processEvent(hub, c.ws, c.st, eph, 300);
    expect(sub.fake.frames().at(-1)).toEqual(["EVENT", "s", eph]);
    expect(c.fake.frames().at(-1)).toEqual(["OK", eph.id, true, ""]);
  });
  it("hashing and verifying is paid for by the source before it happens: bad signatures and replays run out of its share", () => {
    const { hub, conn } = setup({ sourceEventWeightPerMinute: 5 });
    const now = Math.floor(Date.now() / 1000);
    const bad = conn("6.6.6.6");
    const forged = { ...signed(alice, { created_at: now }), sig: "0".repeat(128) };
    for (let i = 0; i < 8; i++) processEvent(hub, bad.ws, bad.st, forged, 200);
    const msgs = bad.fake.frames().map((f) => String(f[3]));
    expect(msgs.slice(0, 5).every((m) => m.startsWith("invalid"))).toBe(true);
    expect(msgs.slice(5).every((m) => /too much load/.test(m))).toBe(true);
    expect(bad.st.bad).toBe(3);
    const replay = conn("7.7.7.7");
    const e = signed(bob, { created_at: now });
    for (let i = 0; i < 8; i++) processEvent(hub, replay.ws, replay.st, e, 200);
    const r = replay.fake.frames().map((f) => String(f[3]));
    expect(r[0]).toBe("");
    expect(r.slice(1, 5).every((m) => /duplicate/.test(m))).toBe(true);
    expect(r.slice(5).every((m) => /too much load/.test(m))).toBe(true);
    // the global budget was never involved: another source is untouched
    const other = conn("8.8.8.8");
    processEvent(hub, other.ws, other.st, signed(alice, { content: "fine", created_at: now }), 200);
    expect(other.fake.frames()[0][2]).toBe(true);
  });
  it("tag-dense events cost the source the tag part of the weight too, and never reach the global budget when it is out", () => {
    const { hub, conn } = setup({ sourceEventWeightPerMinute: 300, globalEventWeightPerMinute: 1000 });
    const now = Math.floor(Date.now() / 1000);
    const dense = (i: number) => signed(alice, { kind: 20001, content: String(i), created_at: now, tags: Array.from({ length: 256 }, () => Array(16).fill("")) });
    const a = conn("1.2.3.4");
    for (let i = 0; i < 4; i++) processEvent(hub, a.ws, a.st, dense(i), 13_000); // weight 132 each, 300 allowed
    expect(a.fake.frames().map((f) => f[2])).toEqual([true, true, false, false]);
    expect(String(a.fake.frames()[2][3])).toMatch(/too much load/);
    expect(a.st.bad).toBe(2);
    const b = conn("5.6.7.8"); // the global budget is only down by the two accepted events (264 of 1000)
    for (let i = 0; i < 5; i++) processEvent(hub, b.ws, b.st, signed(bob, { kind: 20001, content: "o" + i, created_at: now }), 300);
    expect(b.fake.frames().every((f) => f[2] === true)).toBe(true);
    expect(hub.budgets.chargeEvent("c", 0, 731)).toBe("ok");
    expect(hub.budgets.chargeEvent("c", 0, 1)).toBe("global");
  });
  it("answers a deletion request that cannot be made safe with the retryable error, writes nothing and tells nobody", () => {
    const log = vi.spyOn(console, "error").mockImplementation(() => {});
    const { hub, conn, db } = setup();
    const now = Math.floor(Date.now() / 1000);
    const watcher = conn("9.9.9.9");
    processReq(hub, watcher.ws, watcher.st, ["w", { kinds: [5] }]);
    const real = db.sql.exec.bind(db.sql);
    (db.sql as unknown as { exec: unknown }).exec = (q: string, ...p: unknown[]) => {
      if (/count\(\*\)/i.test(q) && /FROM (tombstones|coord_cutoffs)/.test(q)) throw new Error("count failed");
      return real(q, ...p);
    };
    const c = conn();
    const del = signed(alice, { kind: 5, tags: [["e", "f".repeat(64)]], created_at: now });
    processEvent(hub, c.ws, c.st, del, 300);
    expect(c.fake.frames()).toEqual([["OK", del.id, false, "error: temporarily unavailable, retry"]]);
    expect(watcher.fake.frames().filter((f) => f[0] === "EVENT")).toEqual([]); // not broadcast
    expect(real("SELECT count(*) AS n FROM tombstones").one().n).toBe(0);
    expect(real("SELECT count(*) AS n FROM events").one().n).toBe(0);
    log.mockRestore();
  });
  it("maps stale, blocked and full store results to rejecting OK frames and tells no subscriber", () => {
    const { hub, conn } = setup();
    const now = Math.floor(Date.now() / 1000);
    const sub = conn("9.9.9.9");
    processReq(hub, sub.ws, sub.st, ["s", { kinds: [0, 1] }]);
    const c = conn();
    const newer = signed(alice, { kind: 0, created_at: now, content: "{}" });
    const older = signed(alice, { kind: 0, created_at: now - 10, content: "{\"a\":1}" });
    processEvent(hub, c.ws, c.st, newer, 300);
    processEvent(hub, c.ws, c.st, older, 300);
    expect(c.fake.frames().at(-1)).toEqual(["OK", older.id, false, "invalid: a newer version of this event exists"]);
    const victim = signed(alice, { created_at: now - 5 });
    processEvent(hub, c.ws, c.st, signed(alice, { kind: 5, created_at: now, tags: [["e", victim.id]] }), 300);
    const before = sub.fake.sent.length;
    processEvent(hub, c.ws, c.st, victim, 300);
    expect(c.fake.frames().at(-1)).toEqual(["OK", victim.id, false, "blocked: event was deleted"]);
    expect(sub.fake.sent.length).toBe(before);
    const full = setup({ maxStorageBytes: 0 });
    const f = full.conn();
    const e = signed(alice, { created_at: now });
    processEvent(full.hub, f.ws, f.st, e, 300);
    expect(f.fake.frames().at(-1)).toEqual(["OK", e.id, false, "rate-limited: relay storage is full"]);
  });

  it("closes a connection with 1008 after maxViolations rejected frames", () => {
    const { hub, conn } = setup({ eventsPerMinute: 1, maxViolations: 3 });
    const c = conn();
    for (let i = 0; i < 5; i++) processEvent(hub, c.ws, c.st, signed(alice, { content: "e" + i }), 300);
    expect(c.fake.closed?.[0]).toBe(1008);
    expect(c.st.closed).toBe(true);
  });
  it("does not broadcast an ephemeral event whose id was deleted", () => {
    const { hub, conn, db } = setup();
    const now = Math.floor(Date.now() / 1000);
    const eph = signed(alice, { kind: 20001, created_at: now });
    storeEvent(db, signed(alice, { kind: 5, tags: [["e", eph.id]], created_at: now }));
    const c = conn();
    processEvent(hub, c.ws, c.st, eph, 300);
    expect(c.fake.frames()[0][3]).toMatch(/blocked/);
  });
});

describe("processReq and processClose", () => {
  it("replies with the stored events and EOSE, and CLOSE removes the subscription", () => {
    const { hub, conn, db } = setup();
    const e = signed(alice);
    storeEvent(db, e);
    const c = conn();
    processReq(hub, c.ws, c.st, ["s", { kinds: [1] }, { kinds: [1] }]);
    expect(c.fake.frames()).toEqual([["EVENT", "s", e], ["EOSE", "s"]]);
    expect(hub.subs.get(c.st.id).get("s")).toEqual([{ kinds: [1] }]); // duplicate filters collapsed
    processClose(hub, c.ws, c.st.id, "s");
    expect(hub.subs.get(c.st.id).size).toBe(0);
  });
  it("adds the NIP-67 'more' hint only when the caps left matches out", () => {
    const { hub, conn, db } = setup({ maxResultBytes: 1500 });
    for (let i = 0; i < 5; i++) storeEvent(db, signed(alice, { content: "x".repeat(700) + i, created_at: 100 + i }));
    const c = conn();
    processReq(hub, c.ws, c.st, ["s", { kinds: [1] }]);
    expect(c.fake.frames().at(-1)).toEqual(["EOSE", "s", ["more"]]);
    processReq(hub, c.ws, c.st, ["t", { kinds: [1], limit: 1 }]);
    expect(c.fake.frames().at(-1)).toEqual(["EOSE", "t"]);
  });
  it("history counts against the outbound budgets and is cut short with the 'more' hint", () => {
    const { hub, conn, db } = setup({ socketHistoryBytesPerMinute: 2500 });
    for (let i = 0; i < 5; i++) storeEvent(db, signed(alice, { content: "h".repeat(600) + i, created_at: 100 + i }));
    const c = conn();
    processReq(hub, c.ws, c.st, ["s", { kinds: [1] }]);
    const frames = c.fake.frames();
    const got = frames.filter((f) => f[0] === "EVENT").length;
    expect(got).toBeGreaterThan(0);
    expect(got).toBeLessThan(5);
    expect(frames.at(-1)).toEqual(["EOSE", "s", ["more"]]);
    processReq(hub, c.ws, c.st, ["t", { kinds: [1] }]); // the socket's minute is used up
    expect(c.fake.frames().slice(frames.length)).toEqual([["EOSE", "t", ["more"]]]);
    const other = conn("3.3.3.3"); // a different socket has its own allowance
    processReq(hub, other.ws, other.st, ["s", { kinds: [1] }]);
    expect(other.fake.frames().filter((f) => f[0] === "EVENT").length).toBeGreaterThan(0);
  });
  it("pulling a lot of history does not use up the budgets of live delivery", () => {
    const { hub, conn, db } = setup({ socketHistoryBytesPerMinute: 2000, sourceHistoryBytesPerMinute: 2000 });
    for (let i = 0; i < 6; i++) storeEvent(db, signed(alice, { kind: 1, tags: [["t", "x"]], content: "h".repeat(600) + i, created_at: 100 + i }));
    const c = conn();
    for (const id of ["a", "b", "c"]) processReq(hub, c.ws, c.st, [id, { "#t": ["x"] }]);
    expect(c.fake.frames().filter((f) => f[0] === "EOSE").every((f) => (f[2] as string[] | undefined)?.[0] === "more")).toBe(true); // all cut short
    const live = signed(bob, { kind: 20001, tags: [["t", "x"]] });
    broadcast(hub, live);
    expect(c.fake.frames().filter((f) => f[0] === "EVENT" && (f[2] as { id: string }).id === live.id).length).toBe(3); // one per subscription
  });
  it("a normal inbox pull is not truncated, and neither is a second one right after", () => {
    const { hub, conn, db } = setup(); // default budgets
    for (let i = 0; i < 300; i++) storeEvent(db, signed(alice, { kind: 30078, tags: [["d", "m" + i], ["p", bob.pk], ["c", "chat"]], content: "x".repeat(300) + i, created_at: 100 + i }));
    const c = conn();
    const filter = { kinds: [30078], "#p": [bob.pk], limit: 500 };
    for (const id of ["a", "b"]) {
      processReq(hub, c.ws, c.st, [id, filter]);
      const frames = c.fake.frames().filter((f) => f[1] === id);
      expect(frames.filter((f) => f[0] === "EVENT")).toHaveLength(300);
      expect(frames.at(-1)).toEqual(["EOSE", id]);
    }
  });
  it("an invalid or rate-limited REQ closes the existing subscription of that id", () => {
    const { hub, conn } = setup({ reqsPerMinute: 2 });
    const c = conn();
    processReq(hub, c.ws, c.st, ["s", { kinds: [1] }]);
    processReq(hub, c.ws, c.st, ["s", { ids: ["XYZ"] }]);
    expect(c.fake.frames().at(-1)).toEqual(["CLOSED", "s", "invalid: bad ids"]);
    expect(hub.subs.get(c.st.id).size).toBe(0);
    processReq(hub, c.ws, c.st, ["u", { kinds: [1] }]);
    processReq(hub, c.ws, c.st, ["u", { kinds: [2] }]);
    expect(String(c.fake.frames().at(-1)?.[2])).toMatch(/rate-limited/);
    expect(hub.subs.get(c.st.id).has("u")).toBe(false);
  });
  it("a REQ with an invalid subscription id still pays a token and counts as a violation", () => {
    const { hub, conn } = setup({ reqsPerMinute: 2, maxViolations: 3 });
    const c = conn();
    for (let i = 0; i < 4; i++) processReq(hub, c.ws, c.st, ["", {}]);
    const notices = c.fake.frames().map((f) => String(f[1]));
    expect(notices.slice(0, 2)).toEqual(["error: invalid subscription id", "error: invalid subscription id"]);
    expect(notices[2]).toMatch(/rate-limited/); // the bucket ran dry although no id was ever valid
    expect(c.fake.closed?.[0]).toBe(1008); // and the violations closed the socket
  });
  it("rejects bad subscription ids, filter counts and oversize filters", () => {
    const { hub, conn } = setup();
    const c = conn();
    processReq(hub, c.ws, c.st, ["", {}]);
    processReq(hub, c.ws, c.st, ["a"]);
    processReq(hub, c.ws, c.st, ["b", ...Array(11).fill({})]);
    processReq(hub, c.ws, c.st, ["c", { "#t": ["y".repeat(5000)] }]);
    expect(c.fake.frames().map((f) => String(f[2] ?? f[1]))).toEqual([
      "error: invalid subscription id",
      "invalid: need 1 to 10 filters",
      "invalid: need 1 to 10 filters",
      "invalid: filter too large",
    ]);
  });
});

describe("storage failures", () => {
  it("a REQ whose subscription row cannot be written ends with CLOSED and no row left behind", () => {
    const log = vi.spyOn(console, "error").mockImplementation(() => {});
    const { hub, conn, db } = setup();
    const c = conn();
    processReq(hub, c.ws, c.st, ["s", { kinds: [1] }]);
    const real = db.sql.exec.bind(db.sql);
    (db.sql as unknown as { exec: unknown }).exec = (q: string, ...p: unknown[]) => {
      if (q.startsWith("INSERT OR REPLACE INTO subs")) throw new Error("disk full");
      return real(q, ...p);
    };
    processReq(hub, c.ws, c.st, ["s", { kinds: [2] }]);
    expect(c.fake.frames().at(-1)).toEqual(["CLOSED", "s", "error: could not process request"]);
    expect(real("SELECT count(*) AS n FROM subs").one().n).toBe(0);
    log.mockRestore();
  });
});

describe("broadcast", () => {
  const live = (hub: Hub, c: ReturnType<ReturnType<typeof setup>["conn"]>, f: object = { kinds: [20001] }) => processReq(hub, c.ws, c.st, ["s", f]);
  it("serializes once and delivers only to matching subscriptions", () => {
    const { hub, conn } = setup();
    const a = conn("a");
    const b = conn("b");
    live(hub, a);
    live(hub, b, { kinds: [7] });
    const e = signed(alice, { kind: 20001 });
    broadcast(hub, e);
    expect(a.fake.frames().at(-1)).toEqual(["EVENT", "s", e]);
    expect(b.fake.frames()).toEqual([["EOSE", "s"]]);
  });
  it("stops at maxDeliveriesPerEvent", () => {
    const { hub, conn } = setup({ maxDeliveriesPerEvent: 2 });
    const cs = [conn("a"), conn("b"), conn("c")];
    cs.forEach((c) => live(hub, c));
    broadcast(hub, signed(alice, { kind: 20001 }));
    expect(cs.filter((c) => c.fake.frames().some((f) => f[0] === "EVENT")).length).toBe(2);
  });
  it("drops deliveries over the socket quota and closes the socket only when it stays over quota", async () => {
    const { hub, conn } = setup({ socketOutboundBytesPerMinute: 700, quotaWindowMs: 50, maxOverQuotaWindows: 2 });
    const c = conn();
    live(hub, c);
    const big = (i: number) => signed(alice, { kind: 20001, content: "q".repeat(300) + i });
    broadcast(hub, big(1));
    broadcast(hub, big(2));
    expect(c.fake.frames().filter((f) => f[0] === "EVENT")).toHaveLength(1);
    expect(c.fake.closed).toBeUndefined();
    vi.setSystemTime(Date.now() + 60); // still over quota one window later (the clock is frozen, so move it)
    broadcast(hub, big(3));
    expect(c.fake.closed?.[0]).toBe(1013);
    expect((c.fake.att as ConnState).closed).toBe(true);
  });
  it("live matching sees only the tags a stored query could see (the indexed subset)", () => {
    const { hub, conn } = setup({ maxIndexedTags: 2 });
    const c = conn();
    processReq(hub, c.ws, c.st, ["first", { "#t": ["v0"] }]);
    processReq(hub, c.ws, c.st, ["last", { "#t": ["v2"] }]);
    broadcast(hub, signed(alice, { kind: 20001, tags: [["t", "v0"], ["t", "v1"], ["t", "v2"]] }));
    const delivered = c.fake.frames().filter((f) => f[0] === "EVENT").map((f) => f[1]);
    expect(delivered).toEqual(["first"]);
  });
  it("with indexing turned off no tag filter matches live either", () => {
    const { hub, conn } = setup({ maxIndexedTags: 0 });
    const c = conn();
    processReq(hub, c.ws, c.st, ["s", { "#t": ["v"] }]);
    broadcast(hub, signed(alice, { kind: 20001, tags: [["t", "v"]] }));
    expect(c.fake.frames().filter((f) => f[0] === "EVENT")).toHaveLength(0);
  });
  it("skips sockets that are already closing", () => {
    const { hub, conn } = setup();
    const c = conn();
    live(hub, c);
    const st = c.fake.att as ConnState;
    st.closed = true;
    c.fake.att = st;
    broadcast(hub, signed(alice, { kind: 20001 }));
    expect(c.fake.frames().filter((f) => f[0] === "EVENT")).toHaveLength(0);
  });
});

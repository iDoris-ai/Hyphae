import { describe, expect, it, vi } from "vitest";
import { EVENTS_DDL } from "../src/db";
import { DELETION_DDL, parseCoord, reconcileCounters } from "../src/deletion";
import { storeEvent } from "../src/store";
import { validateEvent } from "../src/nostr";
import { alice, bob, newKey, signed } from "./helpers/keys";
import { makeDb } from "./helpers/sqlite";

describe("parseCoord (NIP-09 a tags)", () => {
  it("parses kind:pubkey:d, keeping colons in d", () => {
    expect(parseCoord(`30023:${alice.pk}:my:slug`)).toEqual({ kind: 30023, pubkey: alice.pk, d: "my:slug" });
    expect(parseCoord(`10002:${alice.pk}:`)).toEqual({ kind: 10002, pubkey: alice.pk, d: "" });
  });
  it("rejects malformed coordinates", () => {
    expect(parseCoord(undefined)).toBeNull();
    expect(parseCoord("1:abc:")).toBeNull();
    expect(parseCoord(`x:${alice.pk}:`)).toBeNull();
    expect(parseCoord(`70000:${alice.pk}:`)).toBeNull();
    expect(parseCoord(`-1:${alice.pk}:`)).toBeNull();
    expect(parseCoord(`30023:${alice.pk}`)).toBeNull();
  });
});

const open = (over = {}) => makeDb(EVENTS_DDL + DELETION_DDL, over);
const has = (db: ReturnType<typeof open>, id: string) => db.sql.exec("SELECT 1 FROM events WHERE id = ?", id).toArray().length === 1;

describe("deletion by id (e tags)", () => {
  it("deletes the owner's event, blocks re-publishing it and keeps the deletion request", () => {
    const db = open();
    const note = signed(alice, { content: "note" });
    storeEvent(db, note);
    const del = signed(alice, { kind: 5, tags: [["e", note.id]] });
    expect(storeEvent(db, del)).toBe("ok");
    expect(has(db, note.id)).toBe(false);
    expect(has(db, del.id)).toBe(true);
    expect(storeEvent(db, note)).toBe("blocked");
  });
  it("ignores another key's deletion, and cannot delete a deletion request", () => {
    const db = open();
    const note = signed(alice);
    storeEvent(db, note);
    storeEvent(db, signed(bob, { kind: 5, tags: [["e", note.id]] }));
    expect(has(db, note.id)).toBe(true);
    const del = signed(alice, { kind: 5, tags: [["e", note.id]] });
    storeEvent(db, del);
    storeEvent(db, signed(alice, { kind: 5, tags: [["e", del.id]], content: "again" }));
    expect(has(db, del.id)).toBe(true);
  });
  it("a deletion that arrives before the event blocks it later, but only for the same author", () => {
    const db = open();
    const early = signed(alice, { content: "early" });
    storeEvent(db, signed(alice, { kind: 5, tags: [["e", early.id]] }));
    expect(storeEvent(db, early)).toBe("blocked");
    const theirs = signed(alice, { content: "theirs" });
    storeEvent(db, signed(bob, { kind: 5, tags: [["e", theirs.id]] }));
    expect(storeEvent(db, theirs)).toBe("ok");
  });
});

describe("deletion by coordinate (a tags)", () => {
  it("removes versions up to the request time, blocks older ones and accepts newer ones", () => {
    const db = open();
    const coord = `30023:${alice.pk}:x`;
    const v1 = signed(alice, { kind: 30023, tags: [["d", "x"]], created_at: 100 });
    storeEvent(db, v1);
    storeEvent(db, signed(bob, { kind: 5, tags: [["a", coord]], created_at: 110 })); // not the owner
    expect(has(db, v1.id)).toBe(true);
    storeEvent(db, signed(alice, { kind: 5, tags: [["a", coord]], created_at: 110 }));
    expect(has(db, v1.id)).toBe(false);
    expect(storeEvent(db, signed(alice, { kind: 30023, tags: [["d", "x"]], created_at: 105, content: "mid" }))).toBe("blocked");
    expect(storeEvent(db, signed(alice, { kind: 30023, tags: [["d", "x"]], created_at: 120, content: "new" }))).toBe("ok");
  });
  it("works for replaceable kinds with an empty d", () => {
    const db = open();
    storeEvent(db, signed(alice, { kind: 10002, created_at: 100 }));
    storeEvent(db, signed(alice, { kind: 5, tags: [["a", `10002:${alice.pk}:`]], created_at: 110 }));
    expect(db.sql.exec("SELECT count(*) AS n FROM events WHERE kind = 10002").one().n).toBe(0);
  });
});

describe("eviction at the table caps", () => {
  it("evicts the oldest tombstones, never refuses a deletion, newest stay effective", () => {
    const db = open({ maxTombstones: 3 });
    const evs = Array.from({ length: 5 }, (_, i) => signed(alice, { content: "e" + i }));
    for (const e of evs) storeEvent(db, e);
    for (const e of evs) expect(storeEvent(db, signed(alice, { kind: 5, tags: [["e", e.id]] }))).toBe("ok");
    expect(db.sql.exec("SELECT count(*) AS n FROM tombstones").one().n).toBe(3);
    expect(storeEvent(db, evs[4])).toBe("blocked");
    expect(storeEvent(db, evs[2])).toBe("blocked");
    expect(storeEvent(db, evs[0])).toBe("ok");
    expect(storeEvent(db, evs[1])).toBe("ok");
  });
  it("does the same for coordinate cutoffs", () => {
    const db = open({ maxCoordCutoffs: 3 });
    for (let i = 1; i <= 4; i++) {
      storeEvent(db, signed(alice, { kind: 30023, tags: [["d", "c" + i]], created_at: 100 }));
      storeEvent(db, signed(alice, { kind: 5, tags: [["a", `30023:${alice.pk}:c${i}`]], created_at: 110 }));
    }
    expect(storeEvent(db, signed(alice, { kind: 30023, tags: [["d", "c1"]], created_at: 105, content: "x" }))).toBe("ok");
    expect(storeEvent(db, signed(alice, { kind: 30023, tags: [["d", "c4"]], created_at: 105, content: "x" }))).toBe("blocked");
  });
});

describe("counter reconciliation runs once", () => {
  /** A Db whose sql counts the COUNT(*) queries it is asked to run. */
  function counting(over = {}) {
    const db = open(over);
    const real = db.sql.exec.bind(db.sql);
    const seen = { counts: 0, failOn: "" };
    (db.sql as unknown as { exec: unknown }).exec = (q: string, ...p: unknown[]) => {
      if (/count\(\*\)/i.test(q) && /FROM (tombstones|coord_cutoffs)/.test(q)) seen.counts++;
      if (seen.failOn && q.includes(seen.failOn)) throw new Error("boom");
      return real(q, ...p);
    };
    return { db, seen };
  }
  it("counts the tables and writes the version marker the first time, and only reads the marker afterwards", () => {
    const { db, seen } = counting();
    storeEvent(db, signed(alice, { kind: 5, tags: [["e", "a".repeat(64)]] }));
    reconcileCounters(db);
    expect(seen.counts).toBe(2);
    expect(db.sql.exec("SELECT n FROM meta WHERE k = 'counters_v'").one().n).toBe(1);
    reconcileCounters(db);
    reconcileCounters(db);
    expect(seen.counts).toBe(2);
  });
  it("does not throw when the reconciliation fails, and leaves no marker behind", () => {
    const { db, seen } = counting();
    const log = vi.spyOn(console, "error").mockImplementation(() => {});
    seen.failOn = "count(*)";
    expect(() => reconcileCounters(db)).not.toThrow();
    expect(log).toHaveBeenCalled();
    seen.failOn = "";
    expect(db.sql.exec("SELECT count(*) AS n FROM meta WHERE k = 'counters_v'").one().n).toBe(0);
    log.mockRestore();
  });
});

describe("untrusted counters", () => {
  /** A Db where the COUNT queries of the reconciliation can be made to fail. */
  function flaky(over = {}) {
    const db = open(over);
    const real = db.sql.exec.bind(db.sql);
    const st = { fail: false };
    (db.sql as unknown as { exec: unknown }).exec = (q: string, ...p: unknown[]) => {
      if (st.fail && /count\(\*\)/i.test(q) && /FROM (tombstones|coord_cutoffs)/.test(q)) throw new Error("count failed");
      return real(q, ...p);
    };
    return { db, st };
  }
  it("a deletion request retries the reconciliation first, and works once it succeeds", () => {
    const log = vi.spyOn(console, "error").mockImplementation(() => {});
    const { db, st } = flaky({ maxTombstones: 3 });
    for (let i = 0; i < 3; i++) db.sql.exec("INSERT INTO tombstones (event_id, pubkey) VALUES (?, ?)", String(i).padStart(64, "0"), alice.pk);
    st.fail = true;
    expect(() => reconcileCounters(db)).not.toThrow(); // startup survives
    expect(db.sql.exec("SELECT count(*) AS n FROM meta WHERE k = 'counters_v'").one().n).toBe(0);
    st.fail = false; // the store is healthy again
    expect(storeEvent(db, signed(alice, { kind: 5, tags: [["e", "f".repeat(64)]] }))).toBe("ok");
    expect(db.sql.exec("SELECT n FROM meta WHERE k = 'counters_v'").one().n).toBe(1);
    expect(db.sql.exec("SELECT count(*) AS n FROM tombstones").one().n).toBe(3); // cap held: the oldest was evicted
    log.mockRestore();
  });
  it("if the retry fails too the request gets a retryable answer and nothing is written", () => {
    const log = vi.spyOn(console, "error").mockImplementation(() => {});
    const { db, st } = flaky({ maxTombstones: 3 });
    for (let i = 0; i < 3; i++) db.sql.exec("INSERT INTO tombstones (event_id, pubkey) VALUES (?, ?)", String(i).padStart(64, "0"), alice.pk);
    st.fail = true;
    const del = signed(alice, { kind: 5, tags: [["e", "f".repeat(64)]] });
    expect(storeEvent(db, del)).toBe("unavailable");
    st.fail = false; // (the checks below use COUNT queries themselves)
    expect(db.sql.exec("SELECT count(*) AS n FROM tombstones").one().n).toBe(3);
    expect(db.sql.exec("SELECT count(*) AS n FROM events").one().n).toBe(0);
    expect(storeEvent(db, del)).toBe("ok"); // the same request succeeds when it is retried
    log.mockRestore();
  });
  it("other events do not depend on the counters", () => {
    const log = vi.spyOn(console, "error").mockImplementation(() => {});
    const { db, st } = flaky();
    st.fail = true;
    expect(storeEvent(db, signed(alice))).toBe("ok");
    log.mockRestore();
  });
});

describe("long d values", () => {
  it("an addressable event with a 954-byte d is accepted and can be deleted by its coordinate", () => {
    const db = open();
    const d = "d".repeat(954);
    const ev = signed(alice, { kind: 30078, tags: [["d", d]], created_at: 100 });
    expect(validateEvent(ev, 200)).toBeNull();
    storeEvent(db, ev);
    const del = signed(alice, { kind: 5, tags: [["a", `30078:${alice.pk}:${d}`]], created_at: 110 });
    expect(validateEvent(del, 200)).toBeNull();
    expect(storeEvent(db, del)).toBe("ok");
    expect(has(db, ev.id)).toBe(false);
  });
});

describe("counters and bounded eviction", () => {
  const count = (db: ReturnType<typeof open>, t: string) => db.sql.exec(`SELECT count(*) AS n FROM ${t}`).one().n as number;
  it("reconcileCounters makes the meta counters match the real rows", () => {
    const db = open();
    storeEvent(db, signed(alice, { kind: 5, tags: [["e", "a".repeat(64)], ["e", "b".repeat(64)]] }));
    db.sql.exec("UPDATE meta SET n = 99999 WHERE k = 'tombstones'");
    db.sql.exec("DELETE FROM meta WHERE k = 'counters_v'"); // reconciliation is once per version marker
    reconcileCounters(db);
    expect(db.sql.exec("SELECT n FROM meta WHERE k = 'tombstones'").one().n).toBe(2);
    expect(db.sql.exec("SELECT n FROM meta WHERE k = 'coord_cutoffs'").one().n).toBe(0);
  });
  it("one insert evicts at most 256 records even when the table is far above its cap", () => {
    const db = open({ maxTombstones: 3 });
    for (let i = 0; i < 600; i++) db.sql.exec("INSERT INTO tombstones (event_id, pubkey) VALUES (?, ?)", String(i).padStart(64, "0"), alice.pk);
    reconcileCounters(db);
    storeEvent(db, signed(alice, { kind: 5, tags: [["e", "f".repeat(64)]] }));
    expect(count(db, "tombstones")).toBe(600 - 256 + 1);
  });
  it("a whole request may evict at most 256 records; the rest of it is skipped and reported", () => {
    const db = open({ maxTombstones: 3 });
    for (let i = 0; i < 600; i++) db.sql.exec("INSERT INTO tombstones (event_id, pubkey) VALUES (?, ?)", String(i).padStart(64, "0"), alice.pk);
    db.sql.exec("DELETE FROM meta");
    reconcileCounters(db);
    const req = signed(alice, { kind: 5, tags: ["f", "e", "d"].map((c) => ["e", c.repeat(64)]) });
    expect(storeEvent(db, req)).toBe("ok_partial");
    expect(count(db, "tombstones")).toBe(600 - 256 + 1); // only the first target got its tombstone
  });
  it("a coordinate cutoff blocks created_at <= cutoff and no later version", () => {
    const db = open();
    storeEvent(db, signed(alice, { kind: 5, tags: [["a", `10002:${alice.pk}:`]], created_at: 100 }));
    expect(storeEvent(db, signed(alice, { kind: 10002, created_at: 100 }))).toBe("blocked");
    expect(storeEvent(db, signed(alice, { kind: 10002, created_at: 101 }))).toBe("ok");
  });
});

describe("deletion budgets", () => {
  it("processes at most maxDeletionTargets targets and says so", () => {
    const db = open({ maxDeletionTargets: 4 });
    const ids = Array.from({ length: 6 }, (_, i) => signed(alice, { content: "t" + i }));
    expect(storeEvent(db, signed(alice, { kind: 5, tags: ids.map((e) => ["e", e.id]) }))).toBe("ok_partial");
    expect(storeEvent(db, ids[3])).toBe("blocked");
    expect(storeEvent(db, ids[4])).toBe("ok");
  });
  it("never exceeds the estimated row-write budget (3 per tombstone, 6 once the cap forces an eviction)", () => {
    const db = open({ maxDeletionRowWrites: 30, maxTombstones: 3 });
    const us = Array.from({ length: 20 }, (_, i) => signed(alice, { content: "u" + i }));
    expect(storeEvent(db, signed(alice, { kind: 5, tags: us.map((u) => ["e", u.id]) }))).toBe("ok_partial");
    // targets 0..4 were processed (3 + 3 + 3 + 6 + 6 = 21, the next one would need 6 more > 30 - 21... 27), the cap keeps the newest 3
    const blocked = us.map((u, i) => [i, db.sql.exec("SELECT 1 FROM tombstones WHERE event_id = ?", u.id).toArray().length]).filter(([, n]) => n);
    expect(db.sql.exec("SELECT count(*) AS n FROM tombstones").one().n).toBe(3);
    expect(blocked.length).toBe(3);
    expect(Math.max(...blocked.map(([i]) => i))).toBeLessThan(10);
  });
});

describe("deletion budget for coordinates", () => {
  it("charges a coordinate target by the same estimate as an id target, so a request cannot delete more than the budget allows", () => {
    const db = open({ maxDeletionRowWrites: 100 });
    const tags = (i: number) => [["d", "c" + i], ...Array.from({ length: 7 }, (_, j) => ["t", `v${i}-${j}`])];
    const evs = Array.from({ length: 10 }, (_, i) => signed(alice, { kind: 30023, tags: tags(i), created_at: 100 }));
    for (const e of evs) storeEvent(db, e);
    const req = signed(alice, { kind: 5, created_at: 110, tags: evs.map((_, i) => ["a", `30023:${alice.pk}:c${i}`]) });
    expect(storeEvent(db, req)).toBe("ok_partial");
    const left = evs.filter((e) => has(db, e.id)).length;
    // each target is charged 3 (cutoff) + 6 + 3 * 8 tag rows = 33, so 3 of the 10 fit in 100
    expect(10 - left).toBeLessThanOrEqual(3);
  });
});

describe("deletion requests are never blocked themselves", () => {
  it("a kind 5 whose id was named by an earlier deletion is still stored", () => {
    const db = open();
    const second = signed(alice, { kind: 5, tags: [["e", "f".repeat(64)]], content: "second" });
    storeEvent(db, signed(alice, { kind: 5, tags: [["e", second.id]], content: "first" }));
    expect(storeEvent(db, second)).toBe("ok");
    expect(has(db, second.id)).toBe(true);
  });
});

describe("deletion when the database is over its size cap", () => {
  it("allows a deletion that frees more than it adds and refuses one that does not", () => {
    const db = open();
    const key = newKey();
    const big = signed(key, { content: "n".repeat(5000) });
    const small = signed(key, { content: "s" });
    storeEvent(db, big);
    storeEvent(db, small);
    db.lim.maxStorageBytes = 1;
    expect(storeEvent(db, signed(key, { kind: 5, tags: [["e", "a".repeat(64)]] }))).toBe("full");
    expect(storeEvent(db, signed(key, { kind: 5, tags: [["e", small.id]] }))).toBe("full");
    expect(storeEvent(db, signed(key, { kind: 5, tags: [["e", big.id]] }))).toBe("ok");
    expect(has(db, small.id)).toBe(true);
    expect(has(db, big.id)).toBe(false);
  });
});

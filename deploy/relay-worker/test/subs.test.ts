import { describe, expect, it } from "vitest";
import { EVENTS_DDL } from "../src/db";
import { DELETION_DDL } from "../src/deletion";
import { runQuery } from "../src/query";
import { storeEvent } from "../src/store";
import { SUBS_DDL, SubStore, normalizeFilter, normalizeFilters } from "../src/subs";
import { alice, bob, signed } from "./helpers/keys";
import { makeDb } from "./helpers/sqlite";

describe("filter normalization", () => {
  it("keeps only known fields and single-letter tags, and dedupes lists", () => {
    const n = normalizeFilter({
      ids: ["ab", "ab"],
      kinds: [1, 1, 2],
      search: "hello",
      "#t": ["x", "x"],
      "#abc": ["no"],
      limit: 5,
    } as never);
    expect(n).toEqual({ ids: ["ab"], kinds: [1, 2], limit: 5, "#t": ["x"] });
    expect("search" in n).toBe(false);
  });
  it("normalizeFilters drops duplicate filters regardless of key/list order", () => {
    const out = normalizeFilters([
      { kinds: [1, 2], authors: ["aa"] },
      { authors: ["aa"], kinds: [2, 1] },
      { kinds: [1], search: "x" } as never,
      { kinds: [1] },
    ]);
    expect(out).toHaveLength(2);
  });
});

const open = (over = {}) => makeDb(EVENTS_DDL + DELETION_DDL + SUBS_DDL, over);
const contents = (r: { events: string[] }) => r.events.map((j) => (JSON.parse(j) as { content: string }).content);

describe("runQuery", () => {
  it("returns newest first and matches full ids and authors as well as short prefixes", () => {
    const db = open();
    const a = signed(alice, { created_at: 100, content: "a" });
    const b = signed(bob, { created_at: 200, content: "b" });
    storeEvent(db, a);
    storeEvent(db, b);
    expect(contents(runQuery(db, [{}]))).toEqual(["b", "a"]);
    expect(contents(runQuery(db, [{ authors: [alice.pk] }]))).toEqual(["a"]);
    expect(contents(runQuery(db, [{ authors: [alice.pk.slice(0, 6)], ids: [a.id.slice(0, 10)] }]))).toEqual(["a"]);
    expect(contents(runQuery(db, [{ ids: [b.id] }]))).toEqual(["b"]);
    expect(runQuery(db, [{ ids: ["ffffffff"] }]).events).toEqual([]);
  });
  it("filters by kind, since/until and tags, and an empty list matches nothing", () => {
    const db = open();
    storeEvent(db, signed(alice, { kind: 1, created_at: 100, content: "k1", tags: [["t", "x"]] }));
    storeEvent(db, signed(alice, { kind: 2, created_at: 200, content: "k2", tags: [["t", "y"]] }));
    expect(contents(runQuery(db, [{ kinds: [2] }]))).toEqual(["k2"]);
    expect(contents(runQuery(db, [{ since: 150 }]))).toEqual(["k2"]);
    expect(contents(runQuery(db, [{ until: 150 }]))).toEqual(["k1"]);
    expect(contents(runQuery(db, [{ "#t": ["x"] }]))).toEqual(["k1"]);
    expect(runQuery(db, [{ "#t": ["z"] }]).events).toEqual([]);
    expect(runQuery(db, [{ authors: [] }, { kinds: [] }, { "#t": [] }]).events).toEqual([]);
  });
  it("the result cap is the maxLimit of the Db", () => {
    const db = open({ maxLimit: 2 });
    for (let i = 0; i < 3; i++) storeEvent(db, signed(alice, { content: "m" + i, created_at: 100 + i }));
    expect(runQuery(db, [{ kinds: [1] }])).toMatchObject({ truncated: true });
    expect(runQuery(db, [{ kinds: [1] }]).events).toHaveLength(2);
  });
  it("merges filters without duplicates", () => {
    const db = open();
    storeEvent(db, signed(alice, { content: "only" }));
    expect(runQuery(db, [{ kinds: [1] }, { authors: [alice.pk] }]).events).toHaveLength(1);
  });
  it("a client limit below the cap is not a truncation", () => {
    const db = open();
    for (let i = 0; i < 5; i++) storeEvent(db, signed(alice, { content: "n" + i, created_at: 100 + i }));
    const r = runQuery(db, [{ limit: 3 }]);
    expect(contents(r)).toEqual(["n4", "n3", "n2"]);
    expect(r.truncated).toBe(false);
  });
  it("reports truncation when the byte budget cuts matching history", () => {
    const db = open({ maxResultBytes: 2500 });
    for (let i = 0; i < 6; i++) storeEvent(db, signed(alice, { content: "x".repeat(900) + i, created_at: 100 + i }));
    const r = runQuery(db, [{ kinds: [1] }]);
    expect(r.events.length).toBeGreaterThan(0);
    expect(r.events.length).toBeLessThan(6);
    expect(r.truncated).toBe(true);
    expect(contents(r)[0].endsWith("5")).toBe(true);
  });
  it("reports truncation when the count cap hides more matches, but not when exactly the cap matches", () => {
    const db = open();
    for (let i = 0; i < 501; i++) storeEvent(db, signed(alice, { content: "c" + i, created_at: 1000 + i }));
    expect(runQuery(db, [{ kinds: [1] }])).toMatchObject({ truncated: true });
    expect(runQuery(db, [{ kinds: [1], until: 1499 }])).toMatchObject({ truncated: false });
    expect(runQuery(db, [{ kinds: [1], limit: 100000 }]).events).toHaveLength(500);
  });
});

describe("SubStore", () => {
  const f = (s: string): string => JSON.stringify([{ "#t": [s] }]);
  it("persists, reads back, replaces and removes subscriptions", () => {
    const db = open();
    const subs = new SubStore(db);
    expect(subs.save("c1", "s1", f("a"))).toEqual({ ok: true });
    expect(subs.save("c1", "s1", f("b"))).toEqual({ ok: true });
    expect(subs.save("c2", "s1", f("z"))).toEqual({ ok: true });
    expect([...subs.get("c1").keys()]).toEqual(["s1"]);
    expect(subs.get("c1").get("s1")).toEqual([{ "#t": ["b"] }]);
    subs.remove("c1", "s1");
    expect(subs.get("c1").size).toBe(0);
    subs.dropConn("c2");
    expect(db.sql.exec("SELECT count(*) AS n FROM subs").one().n).toBe(0);
  });
  it("caps one subscription at 4KB and one connection at 16KB, counting UTF-8 bytes", () => {
    const subs = new SubStore(open());
    expect(subs.save("c", "big", f("y".repeat(5000)))).toMatchObject({ ok: false, drop: true });
    const cjk = (i: number) => f("你".repeat(1190) + i); // ~3.6KB of UTF-8 but only ~1.2k characters
    const results = [1, 2, 3, 4, 5].map((i) => subs.save("c", "s" + i, cjk(i)).ok);
    expect(results).toEqual([true, true, true, true, false]);
  });
  it("caps subscriptions per connection and rows overall without dropping the existing subscription", () => {
    const subs = new SubStore(open({ maxSubsPerConn: 2, maxSubRows: 3 }));
    expect(subs.save("c1", "a", f("1")).ok && subs.save("c1", "b", f("2")).ok).toBe(true);
    expect(subs.save("c1", "c", f("3"))).toMatchObject({ ok: false, drop: false });
    expect(subs.save("c2", "a", f("4")).ok).toBe(true);
    expect(subs.save("c3", "a", f("5"))).toMatchObject({ ok: false, drop: false });
    expect(subs.save("c1", "a", f("6")).ok).toBe(true); // replacing an existing row is fine
  });
  it("refuses new subscription rows when the database is full, but lets an existing one be replaced", () => {
    const db = open();
    const subs = new SubStore(db);
    subs.save("c", "a", f("1"));
    db.lim.maxStorageBytes = 1;
    expect(subs.save("c", "b", f("2"))).toMatchObject({ ok: false, reason: expect.stringContaining("storage is full") });
    expect(subs.save("c", "a", f("3")).ok).toBe(true);
  });
  it("a saved or dropped connection is re-read, not served from a stale cache", () => {
    const subs = new SubStore(open());
    subs.save("c", "a", f("1"));
    expect(subs.get("c").size).toBe(1); // cached now
    subs.save("c", "b", f("2"));
    expect(subs.get("c").size).toBe(2);
    subs.dropConn("c");
    expect(subs.get("c").size).toBe(0);
  });
  it("only caches connections while the cache has room", () => {
    const db = open({ subCacheBytes: 100 });
    const subs = new SubStore(db);
    subs.save("c", "a", f("x".repeat(200)));
    expect(subs.get("c").size).toBe(1); // read from SQL every time: correct, just not cached
    db.sql.exec("DELETE FROM subs");
    expect(subs.get("c").size).toBe(0);
  });
});

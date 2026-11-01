import { describe, expect, it } from "vitest";
import { EVENTS_DDL } from "../src/db";
import { DELETION_DDL } from "../src/deletion";
import { type Filter, matchAny } from "../src/filter";
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

describe("SubStore candidate index", () => {
  // deterministic PRNG so a failure is reproducible
  const rng = (seed: number) => () => ((seed = (seed * 1664525 + 1013904223) >>> 0) / 2 ** 32);
  const hex = (i: number) => i.toString(16).padStart(64, "0");

  it("candidates + matchAny equal the brute-force match over randomized filters and events", () => {
    const r = rng(42);
    const pick = <T,>(a: T[]) => a[Math.floor(r() * a.length)];
    const subs = new SubStore(open());
    const all = new Map<string, Filter[]>();
    const maybe = (p: number) => r() < p;
    for (let i = 0; i < 300; i++) {
      const filters: Filter[] = Array.from({ length: 1 + Math.floor(r() * 3) }, () => {
        const f: Filter = {};
        if (maybe(0.3)) f.authors = [maybe(0.8) ? hex(Math.floor(r() * 6)) : hex(Math.floor(r() * 6)).slice(0, 3 + Math.floor(r() * 5))];
        if (maybe(0.4)) f.kinds = maybe(0.05) ? [] : maybe(0.1) ? Array.from({ length: 20 }, (_, k) => k) : [pick([1, 7, 20001, 30023]), pick([1, 7])];
        if (maybe(0.3)) f["#p"] = [hex(Math.floor(r() * 5)), "zz"];
        if (maybe(0.2)) f["#e"] = [hex(Math.floor(r() * 5))];
        if (maybe(0.2)) f["#t"] = [pick(["a", "b"])];
        if (maybe(0.1)) f.since = 50;
        if (maybe(0.1)) f.ids = [hex(Math.floor(r() * 4)).slice(0, 8)];
        return f;
      });
      const id = `c${i % 40}`;
      const sub = `s${Math.floor(i / 40)}`;
      expect(subs.save(id, sub, JSON.stringify(filters))).toEqual({ ok: true });
      all.set(`${id}\0${sub}`, filters);
    }
    // the index is built lazily on the first lookup; later saves/removes must keep it in step
    subs.candidates({ pubkey: "", kind: 0, tags: [] } as never);
    subs.remove("c1", "s0");
    all.delete("c1\0s0");
    subs.dropConn("c2");
    for (const k of [...all.keys()]) if (k.startsWith("c2\0")) all.delete(k);
    expect(subs.save("c3", "s0", JSON.stringify([{ kinds: [9] }]))).toEqual({ ok: true });
    all.set("c3\0s0", [{ kinds: [9] }]);
    let hits = 0;
    for (let i = 0; i < 400; i++) {
      const tags: string[][] = [];
      if (maybe(0.5)) tags.push(["p", hex(Math.floor(r() * 5))]);
      if (maybe(0.3)) tags.push(["e", hex(Math.floor(r() * 5))]);
      if (maybe(0.3)) tags.push(["t", pick(["a", "b"])]);
      const e = { id: hex(Math.floor(r() * 4)), pubkey: hex(Math.floor(r() * 6)), kind: pick([1, 7, 9, 20001, 30023]), created_at: Math.floor(r() * 100), tags, content: "", sig: "" };
      const brute = new Set([...all].filter(([, f]) => matchAny(f, e)).map(([k]) => k));
      const viaIndex = new Set([...subs.candidates(e)].filter((ref) => matchAny(all.get(ref) ?? [], e)));
      expect(viaIndex).toEqual(brute);
      hits += brute.size;
    }
    expect(hits).toBeGreaterThan(100); // the generator really produces matches
  });

  it("files a filter under one dimension, so an unrelated event does not even see it as a candidate", () => {
    const subs = new SubStore(open());
    subs.save("c", "k", JSON.stringify([{ kinds: [7] }]));
    subs.save("c", "a", JSON.stringify([{ authors: [hex(1)] }]));
    subs.save("c", "w", JSON.stringify([{ since: 1 }]));
    const e = { id: hex(9), pubkey: hex(2), kind: 1, created_at: 5, tags: [], content: "", sig: "" };
    expect([...subs.candidates(e)]).toEqual(["c\0w"]);
  });

  it("evicts the least recently used connection when the subscription cache is full", () => {
    const db = open({ subCacheBytes: 30 }); // each connection below is 13 bytes: room for two
    const subs = new SubStore(db);
    const f = JSON.stringify([{ kinds: [1] }]);
    for (const c of ["a", "b", "c"]) subs.save(c, "s", f);
    let reads = 0;
    const exec = db.sql.exec.bind(db.sql);
    (db.sql as unknown as { exec: unknown }).exec = (q: string, ...p: unknown[]) => {
      if (q.startsWith("SELECT sub_id, filters")) reads++;
      return exec(q, ...p);
    };
    subs.get("a");
    subs.get("b");
    subs.get("a"); // refreshes a: b is now the least recently used
    subs.get("c"); // does not fit: evicts b, not a
    expect(reads).toBe(3);
    subs.get("a");
    subs.get("c");
    expect(reads).toBe(3); // a and c are cached
    subs.get("b");
    expect(reads).toBe(4); // b was evicted
  });

  it("files a subscription that needs more than 16 bucket keys under the wildcard (bounded index memory)", () => {
    const subs = new SubStore(open());
    subs.save("c", "few", JSON.stringify([{ kinds: Array.from({ length: 16 }, (_, k) => k) }]));
    subs.save("c", "many", JSON.stringify([{ kinds: Array.from({ length: 17 }, (_, k) => k) }]));
    const e = { id: hex(9), pubkey: hex(2), kind: 99, created_at: 5, tags: [], content: "", sig: "" };
    expect([...subs.candidates(e)]).toEqual(["c\0many"]);
    expect([...subs.candidates({ ...e, kind: 3 })].sort()).toEqual(["c\0few", "c\0many"]);
  });

  describe("index clean-up", () => {
    const ev = { id: hex(9), pubkey: hex(2), kind: 7, created_at: 5, tags: [], content: "", sig: "" };
    const refs = (subs: SubStore, kind = 7) => [...subs.candidates({ ...ev, kind })];
    const fresh = () => {
      const subs = new SubStore(open());
      subs.candidates(ev); // build the index now, so later changes must be applied to it
      return subs;
    };
    it("a removed subscription (CLOSE) is no longer a candidate", () => {
      const subs = fresh();
      subs.save("c", "s", JSON.stringify([{ kinds: [7] }]));
      expect(refs(subs)).toEqual(["c\0s"]);
      subs.remove("c", "s");
      expect(refs(subs)).toEqual([]);
    });
    it("a dropped connection leaves no candidates", () => {
      const subs = fresh();
      subs.save("c", "a", JSON.stringify([{ kinds: [7] }]));
      subs.save("c", "b", JSON.stringify([{ since: 1 }]));
      subs.save("d", "a", JSON.stringify([{ kinds: [7] }]));
      subs.dropConn("c");
      expect(refs(subs)).toEqual(["d\0a"]);
    });
    it("replacing a subscription with a different filter drops its old buckets", () => {
      const subs = fresh();
      subs.save("c", "s", JSON.stringify([{ kinds: [7] }]));
      subs.save("c", "s", JSON.stringify([{ kinds: [8] }]));
      expect(refs(subs, 7)).toEqual([]);
      expect(refs(subs, 8)).toEqual(["c\0s"]);
    });
  });
});

import { describe, expect, it } from "vitest";
import { EVENTS_DDL, footprint, hasEvent, indexedTags } from "../src/db";
import { DELETION_DDL } from "../src/deletion";
import { storeEvent } from "../src/store";
import { alice, bob, signed } from "./helpers/keys";
import { makeDb } from "./helpers/sqlite";

describe("indexedTags", () => {
  it("indexedTags: single-letter only, distinct, at most 64", () => {
    const tags = [["t", "a"], ["t", "a"], ["title", "x"], ["p"], ["e", "1"]];
    expect(indexedTags(tags, 64)).toEqual([["t", "a"], ["e", "1"]]);
    const many = Array.from({ length: 200 }, (_, i) => ["t", "v" + i]);
    const idx = indexedTags(many, 64);
    expect(idx.length).toBe(64);
    expect(idx[0]).toEqual(["t", "v0"]);
  });
});

const DDL =
  EVENTS_DDL
  + DELETION_DDL
  ;

const ids = (db: ReturnType<typeof makeDb>) => db.sql.exec("SELECT id FROM events").toArray().map((r) => r.id);

describe("storeEvent", () => {
  it("stores an event once and reports a duplicate afterwards", () => {
    const db = makeDb(DDL);
    const e = signed(alice);
    expect(storeEvent(db, e)).toBe("ok");
    expect(storeEvent(db, e)).toBe("duplicate");
    expect(ids(db)).toEqual([e.id]);
  });
  it("indexes distinct single-letter tags only", () => {
    const db = makeDb(DDL);
    storeEvent(db, signed(alice, { tags: [["t", "x"], ["t", "x"], ["title", "y"], ["p", bob.pk]] }));
    expect(db.sql.exec("SELECT name, value FROM event_tags ORDER BY name").toArray()).toEqual([
      { name: "p", value: bob.pk },
      { name: "t", value: "x" },
    ]);
  });
  it("indexedTags with a cap of 0 indexes nothing, and nothing is stored in event_tags", () => {
    expect(indexedTags([["t", "a"]], 0)).toEqual([]);
    const db = makeDb(DDL, { maxIndexedTags: 0 });
    storeEvent(db, signed(alice, { tags: [["t", "1"]] }));
    expect(db.sql.exec("SELECT count(*) AS n FROM event_tags").one().n).toBe(0);
  });
  it("hasEvent and the indexed-tag cap follow the limits of the Db", () => {
    const db = makeDb(DDL, { maxIndexedTags: 2 });
    const e = signed(alice, { tags: [["t", "1"], ["t", "2"], ["t", "3"]] });
    expect(hasEvent(db, e.id)).toBe(false);
    storeEvent(db, e);
    expect(hasEvent(db, e.id)).toBe(true);
    expect(db.sql.exec("SELECT count(*) AS n FROM event_tags").one().n).toBe(2);
  });
  it("keeps only the newest replaceable event and refuses older ones", () => {
    const db = makeDb(DDL);
    const v1 = signed(alice, { kind: 10002, created_at: 100 });
    const v2 = signed(alice, { kind: 10002, created_at: 200, content: "v2" });
    expect(storeEvent(db, v1)).toBe("ok");
    expect(storeEvent(db, v2)).toBe("ok");
    expect(ids(db)).toEqual([v2.id]);
    expect(storeEvent(db, signed(alice, { kind: 10002, created_at: 150, content: "old" }))).toBe("stale");
    expect(ids(db)).toEqual([v2.id]);
  });
  it("replaces addressable events per d tag", () => {
    const db = makeDb(DDL);
    const x1 = signed(alice, { kind: 30023, tags: [["d", "x"]], created_at: 100 });
    const x2 = signed(alice, { kind: 30023, tags: [["d", "x"]], created_at: 200, content: "x2" });
    const y1 = signed(alice, { kind: 30023, tags: [["d", "y"]], created_at: 100 });
    for (const e of [x1, x2, y1]) storeEvent(db, e);
    expect(ids(db).sort()).toEqual([x2.id, y1.id].sort());
  });
  it("tie on created_at: the lower id wins", () => {
    const db = makeDb(DDL);
    const [a, b] = [signed(alice, { kind: 0, created_at: 100, content: "a" }), signed(alice, { kind: 0, created_at: 100, content: "b" })].sort((x, y) => (x.id < y.id ? -1 : 1));
    storeEvent(db, b);
    expect(storeEvent(db, a)).toBe("ok");
    expect(storeEvent(db, b)).toBe("stale");
    expect(ids(db)).toEqual([a.id]);
  });
});

describe("storeEvent when the database is over its size cap (net growth)", () => {
  const full = () => makeDb(DDL, { maxStorageBytes: 1 });
  it("refuses new events and brand-new replaceable or addressable coordinates", () => {
    const db = full();
    expect(storeEvent(db, signed(alice))).toBe("full");
    expect(storeEvent(db, signed(alice, { kind: 10002 }))).toBe("full");
    expect(storeEvent(db, signed(alice, { kind: 30023, tags: [["d", "x"]] }))).toBe("full");
  });
  it("allows an equal-size replacement, refuses small -> large, allows large -> small", () => {
    const db = makeDb(DDL);
    const small = signed(alice, { kind: 10002, created_at: 100, content: "v1" });
    const big = signed(bob, { kind: 10002, created_at: 100, content: "L".repeat(20_000) });
    storeEvent(db, small);
    storeEvent(db, big);
    db.lim.maxStorageBytes = 1; // now "full"
    expect(storeEvent(db, signed(alice, { kind: 10002, created_at: 200, content: "v2" }))).toBe("ok");
    expect(storeEvent(db, signed(alice, { kind: 10002, created_at: 300, content: "B".repeat(20_000) }))).toBe("full");
    expect(storeEvent(db, signed(bob, { kind: 10002, created_at: 200, content: "" }))).toBe("ok");
  });
  it("answers a duplicate with duplicate even when full", () => {
    const db = makeDb(DDL);
    const e = signed(alice);
    storeEvent(db, e);
    db.lim.maxStorageBytes = 1;
    expect(storeEvent(db, e)).toBe("duplicate");
  });
  it("footprint grows with content and indexed tags", () => {
    expect(footprint(100, [], 64)).toBeLessThan(footprint(200, [], 64));
    expect(footprint(100, [], 64)).toBeLessThan(footprint(100, [["t", "abc"]], 64));
    expect(footprint(100, [["t", "a"], ["t", "b"]], 1)).toBeLessThan(footprint(100, [["t", "a"], ["t", "b"]], 2));
  });
});

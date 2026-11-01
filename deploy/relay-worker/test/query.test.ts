import { DatabaseSync } from "node:sqlite";
import { describe, expect, it } from "vitest";
import { LIMITS, loadLimits } from "../src/config";
import { EVENTS_DDL, migrateEventTags } from "../src/db";
import { DELETION_DDL } from "../src/deletion";
import { validateFilter } from "../src/filter";
import { buildSql, queryUnits, runQuery } from "../src/query";
import { storeEvent } from "../src/store";
import { alice, bob, signed } from "./helpers/keys";
import { makeDb } from "./helpers/sqlite";

const open = (over = {}) => makeDb(EVENTS_DDL + DELETION_DDL, over);
const text = (r: { events: string[] }) => r.events.map((j) => (JSON.parse(j) as { content: string }).content);
const plan = (db: ReturnType<typeof open>, f: Parameters<typeof buildSql>[0]) => {
  const { sql, params } = buildSql(f, 11);
  return db.sql
    .exec("EXPLAIN QUERY PLAN " + sql, ...params)
    .toArray()
    .map((r) => r.detail as string);
};
/** Counts the rows the relay pulls out of SQLite (iterated rows), to prove a read bound. */
function counted(db: ReturnType<typeof open>) {
  const real = db.sql.exec.bind(db.sql);
  const c = { rows: 0 };
  (db.sql as { exec: unknown }).exec = (q: string, ...p: unknown[]) => {
    const cur = real(q, ...p) as unknown as { toArray(): Record<string, unknown>[] };
    if (!q.startsWith("SELECT id, created_at, json") && !q.startsWith("SELECT e.id")) return cur;
    const rows = cur.toArray();
    c.rows += rows.length;
    return { ...cur, toArray: () => rows, [Symbol.iterator]: () => rows[Symbol.iterator]() };
  };
  return c;
}

describe("tag-filter query plan", () => {
  it("walks tags_time newest first and needs no temp b-tree for ORDER BY", () => {
    const db = open();
    for (const f of [
      { "#p": ["x"], limit: 10 },
      { "#p": ["x"], kinds: [1], since: 5, until: 50, limit: 10 },
      { "#p": ["x"], "#e": ["y"], authors: [alice.pk] },
    ]) {
      const p = plan(db, f).join("\n");
      expect(p).toContain("tags_time");
      expect(p).not.toContain("TEMP B-TREE");
    }
  });
  it("returns the newest events of a popular tag and stops reading after the limit", () => {
    const db = open();
    for (let i = 0; i < 40; i++) storeEvent(db, signed(i % 2 ? alice : bob, { content: "p" + i, created_at: 100 + i, tags: [["p", "x"]] }));
    storeEvent(db, signed(alice, { content: "other", created_at: 500, tags: [["p", "z"]] }));
    const c = counted(db);
    const r = runQuery(db, [{ "#p": ["x"], limit: 3 }]);
    expect(text(r)).toEqual(["p39", "p38", "p37"]);
    expect(r.truncated).toBe(false);
    expect(c.rows).toBeLessThanOrEqual(4); // limit + 1 probe row
  });
  it("combines several tag values and a second tag, newest first, without duplicates", () => {
    const db = open();
    storeEvent(db, signed(alice, { content: "both", created_at: 300, tags: [["p", "a"], ["p", "b"], ["e", "q"]] }));
    storeEvent(db, signed(alice, { content: "a-only", created_at: 200, tags: [["p", "a"]] }));
    storeEvent(db, signed(alice, { content: "b-eq", created_at: 100, tags: [["p", "b"], ["e", "q"]] }));
    expect(text(runQuery(db, [{ "#p": ["a", "b"] }]))).toEqual(["both", "a-only", "b-eq"]);
    expect(text(runQuery(db, [{ "#p": ["a", "b"], "#e": ["q"] }]))).toEqual(["both", "b-eq"]);
    expect(text(runQuery(db, [{ "#p": ["a", "b"], limit: 2 }]))).toEqual(["both", "a-only"]);
  });
});

describe("tag index migration", () => {
  it("adds created_at to an old event_tags table, backfills it, swaps the index, and is idempotent", () => {
    const raw = new DatabaseSync(":memory:");
    raw.exec(`
      CREATE TABLE events (id TEXT PRIMARY KEY, pubkey TEXT NOT NULL, created_at INTEGER NOT NULL, kind INTEGER NOT NULL, d TEXT NOT NULL, json TEXT NOT NULL);
      CREATE TABLE event_tags (event_id TEXT NOT NULL, name TEXT NOT NULL, value TEXT NOT NULL);
      CREATE INDEX tags_cover ON event_tags (name, value, event_id);
      INSERT INTO events VALUES ('e1','pk',777,1,'','{}');
      INSERT INTO event_tags VALUES ('e1','p','x'), ('ghost','p','x');`);
    const exec = (q: string, ...p: unknown[]) => {
      const st = raw.prepare(q);
      const rows = st.columns().length > 0 ? (st.all(...(p as never[])) as Record<string, unknown>[]) : (st.run(...(p as never[])), []);
      return { toArray: () => rows, one: () => rows[0], [Symbol.iterator]: () => rows[Symbol.iterator]() };
    };
    const old = { sql: { exec } as unknown as SqlStorage, tx: <T,>(fn: () => T) => fn(), lim: LIMITS };
    migrateEventTags(old);
    raw.exec(EVENTS_DDL);
    migrateEventTags(old); // second start: nothing to do
    raw.exec(EVENTS_DDL);
    expect(raw.prepare("SELECT event_id, created_at FROM event_tags ORDER BY event_id").all().map((r) => ({ ...(r as object) }))).toEqual([
      { event_id: "e1", created_at: 777 },
      { event_id: "ghost", created_at: 0 },
    ]);
    const idx = raw.prepare("SELECT name FROM sqlite_master WHERE type = 'index' AND tbl_name = 'event_tags'").all().map((r) => (r as { name: string }).name);
    expect(idx).toContain("tags_time");
    expect(idx).not.toContain("tags_cover");
  });
});

describe("multi-filter REQ", () => {
  it("returns the union newest first, de-duplicated, and reads at most the sum of the limits", () => {
    const db = open();
    for (let i = 0; i < 30; i++) storeEvent(db, signed(alice, { kind: 1, content: "a" + i, created_at: 1000 + i * 3 }));
    for (let i = 0; i < 30; i++) storeEvent(db, signed(bob, { kind: 2, content: "b" + i, created_at: 1001 + i * 3 }));
    for (let i = 0; i < 30; i++) storeEvent(db, signed(alice, { kind: 7, content: "c" + i, created_at: 1002 + i * 3, tags: [["t", "k"]] }));
    const c = counted(db);
    const r = runQuery(db, [{ kinds: [1], limit: 4 }, { kinds: [2], limit: 4 }, { "#t": ["k"], limit: 4 }, { authors: [alice.pk], kinds: [1], limit: 2 }]);
    expect(text(r)).toEqual(["c29", "b29", "a29", "c28", "b28", "a28", "c27", "b27", "a27", "c26", "b26", "a26"]);
    expect(new Set(r.events).size).toBe(r.events.length);
    expect(r.truncated).toBe(false);
    expect(c.rows).toBeLessThanOrEqual(5 + 5 + 5 + 3); // sum of (limit + 1 probe row)
  });
  it("a later filter is not starved by an earlier one that uses the whole cap, and ordering is global", () => {
    const db = open({ maxLimit: 5 });
    for (let i = 0; i < 20; i++) storeEvent(db, signed(alice, { kind: 1, content: "old" + i, created_at: 100 + i }));
    storeEvent(db, signed(bob, { kind: 2, content: "new-a", created_at: 900 }));
    storeEvent(db, signed(bob, { kind: 2, content: "new-b", created_at: 901 }));
    const r = runQuery(db, [{ kinds: [1] }, { kinds: [2] }]);
    expect(text(r)).toEqual(["new-b", "new-a", "old19", "old18", "old17"]);
    expect(r.truncated).toBe(true);
  });
  it("byte pruning inside one multi-value tag filter never returns an event outside the client's newest `limit`", () => {
    const db = open();
    const mk = (content: string, at: number, v: string) => signed(alice, { content, created_at: at, tags: [["p", v]] });
    const A = mk("a".repeat(600), 300, "a");
    const B = mk("b".repeat(600), 200, "a");
    const C = mk("c".repeat(100), 100, "b");
    for (const e of [A, B, C]) storeEvent(db, e);
    db.lim.maxResultBytes = JSON.stringify(A).length + JSON.stringify(C).length + 10; // A+C fit, A+B do not
    const r = runQuery(db, [{ "#p": ["a", "b"], limit: 2 }]);
    expect(text(r)).toEqual(["a".repeat(600)]); // newest two are A,B; B does not fit; C is not in the newest two
    expect(r.truncated).toBe(true);
  });
  it("rows cut by the byte cap in an older branch do not make the result truncated", () => {
    const db = open();
    const mk = (content: string, at: number, v: string) => signed(alice, { content, created_at: at, tags: [["p", v]] });
    const A = mk("a".repeat(600), 300, "a");
    const B = mk("b".repeat(600), 200, "a");
    const C = mk("c".repeat(100), 500, "b");
    const D = mk("d".repeat(100), 400, "b");
    for (const e of [A, B, C, D]) storeEvent(db, e);
    db.lim.maxResultBytes = JSON.stringify(C).length + JSON.stringify(D).length + 10; // A+B overflow
    const r = runQuery(db, [{ "#p": ["a", "b"], limit: 2 }]);
    expect(text(r)).toEqual(["c".repeat(100), "d".repeat(100)]);
    expect(r.truncated).toBe(false);
  });
  it("the count cap is reported even when the extra row alone would not fit the byte budget", () => {
    const db = open({ maxLimit: 1 });
    const A = signed(alice, { content: "a".repeat(600), created_at: 200 });
    const B = signed(alice, { content: "b".repeat(600), created_at: 100 });
    storeEvent(db, A);
    storeEvent(db, B);
    db.lim.maxResultBytes = JSON.stringify(A).length + 10;
    const r = runQuery(db, [{}]);
    expect(r.events).toHaveLength(1);
    expect(r.truncated).toBe(true);
  });
  it("a row one filter could not fit stops the merged result: older rows of other filters are not admitted", () => {
    const db = open();
    const A = signed(alice, { kind: 1, content: "a".repeat(600), created_at: 300 });
    const B = signed(alice, { kind: 1, content: "b".repeat(600), created_at: 200 });
    const C = signed(alice, { kind: 2, content: "c".repeat(100), created_at: 100 });
    for (const e of [A, B, C]) storeEvent(db, e);
    db.lim.maxResultBytes = JSON.stringify(A).length + JSON.stringify(C).length + 10; // A+C fit, A+B do not
    const r = runQuery(db, [{ kinds: [1], limit: 2 }, { kinds: [2], limit: 1 }]);
    expect(text(r)).toEqual(["a".repeat(600)]);
    expect(r.truncated).toBe(true);
  });
  it("a too-big row beyond the count cap still reports truncation", () => {
    const db = open({ maxLimit: 1 });
    const A = signed(alice, { content: "a".repeat(900), created_at: 1, tags: [["p", "a"]] });
    const B = signed(alice, { content: "b", created_at: 2, tags: [["p", "b"]] });
    storeEvent(db, A);
    storeEvent(db, B);
    db.lim.maxResultBytes = JSON.stringify(A).length - 10;
    for (const vals of [["a", "b"], ["b", "a"]]) {
      const r = runQuery(db, [{ "#p": vals }]);
      expect(text(r)).toEqual(["b"]);
      expect(r.truncated).toBe(true);
    }
  });
  it("stops one filter at the byte cap", () => {
    const db = open({ maxResultBytes: 2500 });
    for (let i = 0; i < 8; i++) storeEvent(db, signed(alice, { content: "y".repeat(900) + i, created_at: 100 + i }));
    const r = runQuery(db, [{ kinds: [1] }]);
    expect(r.truncated).toBe(true);
    expect(r.events.length).toBeLessThan(8);
    expect(text(r)[0].endsWith("7")).toBe(true);
  });
});

describe("bound parameters", () => {
  it("the largest filter validateFilter accepts stays under SQLite's 100 bound values", () => {
    const authors = Array.from({ length: 45 }, (_, i) => i.toString(16).padStart(2, "0"));
    const shapes = [
      { authors, since: 1, until: 2, limit: 3 },
      { kinds: Array.from({ length: 90 }, (_, i) => i), since: 1, until: 2 },
      { "#p": ["a"], "#e": Array.from({ length: 87 }, (_, i) => "v" + i), since: 1, until: 2 },
      { "#p": Array.from({ length: 89 }, (_, i) => "v" + i), since: 1, until: 2 },
    ];
    for (const f of shapes) {
      expect(validateFilter(f, LIMITS)).toBeNull();
      const dk = Object.keys(f).find((k) => k.startsWith("#"));
      const unit = dk ? { ...f, [dk]: [((f as unknown as Record<string, string[]>)[dk])[0]] } : f;
      expect(buildSql(unit as never, 501).params.length).toBeLessThanOrEqual(100);
    }
  });
});

describe("query unit budget", () => {
  const vals = (n: number) => Array.from({ length: n }, (_, i) => "v" + i);
  it("counts one index read per distinct driver-tag value, one for other filters, none for empty ones", () => {
    expect(queryUnits([{ "#p": vals(89) }])).toBe(89);
    expect(queryUnits([{ "#p": ["a", "a", "b"] }, { kinds: [1] }, { "#p": [] }, { authors: [] }])).toBe(3);
    expect(queryUnits([{ "#p": vals(60), limit: 0 }, { "#t": vals(60), limit: -1 }, { kinds: [1] }])).toBe(1);
    expect(queryUnits(Array.from({ length: 10 }, () => ({ "#p": vals(89) })))).toBe(890);
  });
  it("is a limit the operator can override through LIMITS_JSON, with a default one maximal filter fits in", () => {
    expect(LIMITS.maxQueryUnits).toBeGreaterThanOrEqual(89);
    expect(loadLimits('{"maxQueryUnits":7}').maxQueryUnits).toBe(7);
    expect(loadLimits('{"maxQueryUnits":-1}').maxQueryUnits).toBe(LIMITS.maxQueryUnits);
  });
});

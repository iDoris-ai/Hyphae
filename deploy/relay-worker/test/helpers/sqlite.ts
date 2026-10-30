import { DatabaseSync } from "node:sqlite";
import { LIMITS, type Limits } from "../../src/config";
import type { Db } from "../../src/db";

/** A Db over node:sqlite, shaped like the Durable Object SqlStorage the relay uses. */
export function makeDb(ddl: string, over: Partial<Limits> = {}): Db {
  const d = new DatabaseSync(":memory:");
  d.exec(ddl);
  const sql = {
    exec(q: string, ...params: unknown[]) {
      const stmt = d.prepare(q);
      let rows: Record<string, unknown>[] = [];
      let written = 0;
      if (stmt.columns().length > 0) rows = stmt.all(...(params as never[])) as Record<string, unknown>[];
      else written = Number(stmt.run(...(params as never[])).changes);
      return {
        toArray: () => rows,
        one: () => {
          if (rows.length !== 1) throw new Error("expected one row");
          return rows[0];
        },
        [Symbol.iterator]: () => rows[Symbol.iterator](),
        rowsWritten: written,
      };
    },
    get databaseSize() {
      const r = d.prepare("SELECT (SELECT page_count FROM pragma_page_count) * (SELECT page_size FROM pragma_page_size) AS n").get() as { n: number };
      return r.n;
    },
  };
  return {
    sql: sql as unknown as SqlStorage,
    tx: (fn) => {
      d.exec("BEGIN");
      try {
        const r = fn();
        d.exec("COMMIT");
        return r;
      } catch (e) {
        d.exec("ROLLBACK");
        throw e;
      }
    },
    lim: { ...LIMITS, ...over },
  };
}

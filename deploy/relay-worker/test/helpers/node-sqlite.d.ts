// Minimal typing for the part of node:sqlite the test helper uses (the project does not depend on @types/node).
declare module "node:sqlite" {
  export class DatabaseSync {
    constructor(path: string);
    exec(sql: string): void;
    prepare(sql: string): {
      columns(): unknown[];
      all(...params: unknown[]): unknown[];
      get(...params: unknown[]): unknown;
      run(...params: unknown[]): { changes: number | bigint };
    };
  }
}

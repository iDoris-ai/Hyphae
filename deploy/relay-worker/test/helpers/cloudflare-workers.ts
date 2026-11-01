// Stand-in for the `cloudflare:workers` module, for vitest only (see vitest.config.ts).
export class DurableObject {
  constructor(
    public ctx: unknown,
    public env: unknown,
  ) {}
}

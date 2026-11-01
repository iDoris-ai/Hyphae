import { fileURLToPath } from "node:url";
import { defineConfig } from "vitest/config";

// `cloudflare:workers` only exists inside workerd. The tests that load the Worker entry (which re-exports the
// Durable Object class) get a minimal stand-in; the real runtime is covered by `pnpm e2e` against `wrangler dev`.
export default defineConfig({
  resolve: {
    alias: { "cloudflare:workers": fileURLToPath(new URL("./test/helpers/cloudflare-workers.ts", import.meta.url)) },
  },
});

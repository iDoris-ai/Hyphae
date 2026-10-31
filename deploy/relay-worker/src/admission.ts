import { type Bucket, takeToken } from "./limits";

/** Shape of Cloudflare's Workers Rate Limiting binding (`[[ratelimits]]`). */
export interface RateLimiter {
  limit(options: { key: string }): Promise<{ success: boolean }>;
}

export type Admission = { ok: true } | { ok: false; retryAfter: number; by: "isolate" | "binding" };

/** Retry-After for a refusal by the Cloudflare binding: its window is fixed at 10 s (see wrangler.toml). */
const BINDING_RETRY_AFTER = 10;

/**
 * Edge admission control for WebSocket upgrades, run in the Worker BEFORE the Durable Object is called,
 * so a handshake flood does not queue in the single DO's input. Two best-effort filters, in this order:
 *  1. a per-isolate token bucket per source key (exact within one isolate, but isolates are many and
 *     short-lived, so a flood spread over them sees a fraction of the limit: weak by design);
 *  2. optionally Cloudflare's Rate Limiting binding (per Cloudflare location, eventually consistent,
 *     permissive: it may admit more than the limit).
 * Both limits are deliberately above the Durable Object's own per-source limits: this layer only drops
 * floods, the DO stays the authority for what a normal client may do. If the binding throws, the
 * request is admitted (fail open; the DO limits remain) and the error is logged once per isolate.
 */
export function createAdmission(opts: {
  perMinute: number;
  maxKeys?: number;
  now?: () => number;
  log?: (msg: string, err: unknown) => void;
}) {
  const maxKeys = opts.maxKeys ?? 10_000;
  const now = opts.now ?? Date.now;
  const log = opts.log ?? ((m, e) => console.error(m, e));
  const buckets = new Map<string, Bucket>(); // Map order = least recently used first
  let bindingErrorLogged = false;

  return {
    async check(key: string, limiter?: RateLimiter): Promise<Admission> {
      const t = now();
      const r = takeToken(buckets.get(key), t, opts.perMinute);
      // Re-insert so the key becomes most recently used; evict from the front so memory stays bounded
      // under a many-source flood (an evicted source just gets a fresh full bucket: fails open).
      buckets.delete(key);
      buckets.set(key, r.bucket);
      if (buckets.size > maxKeys) buckets.delete(buckets.keys().next().value as string);
      if (!r.ok) {
        // A zero budget would divide by zero; cap at one refill window so the header is always a finite integer.
        const wait = Math.ceil(((1 - r.bucket.tokens) / opts.perMinute) * 60);
        const retryAfter = Number.isFinite(wait) ? Math.min(60, Math.max(1, wait)) : 60;
        return { ok: false, retryAfter, by: "isolate" };
      }
      if (limiter) {
        try {
          const { success } = await limiter.limit({ key });
          if (!success) return { ok: false, retryAfter: BINDING_RETRY_AFTER, by: "binding" };
        } catch (err) {
          if (!bindingErrorLogged) {
            bindingErrorLogged = true;
            log("edge rate limiter failed; admitting (Durable Object limits still apply)", err);
          }
        }
      }
      return { ok: true };
    },
  };
}

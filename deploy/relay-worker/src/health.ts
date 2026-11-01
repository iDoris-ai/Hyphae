/** How long a probe result is reused inside one Worker isolate: a healthy answer, and (shorter) a failed one. */
export const HEALTH_OK_TTL_MS = 5000;
export const HEALTH_FAIL_TTL_MS = 2000;

/**
 * Wraps the Durable Object probe so that public /healthz traffic reaches the object at most about once per TTL
 * per isolate. Concurrent callers share one in-flight probe; a probe that throws counts as a failure.
 * (Each isolate has its own cache, so the bound is per isolate, not global.)
 */
export function createHealthCache(now: () => number = Date.now) {
  let at = -Infinity;
  let ok = false;
  let inflight: Promise<boolean> | undefined;
  return (probe: () => Promise<boolean>): Promise<boolean> => {
    if (inflight) return inflight;
    if (now() - at < (ok ? HEALTH_OK_TTL_MS : HEALTH_FAIL_TTL_MS)) return Promise.resolve(ok);
    inflight = probe()
      .catch(() => false)
      .then((r) => {
        ok = r;
        at = now();
        inflight = undefined;
        return r;
      });
    return inflight;
  };
}

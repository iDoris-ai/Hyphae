const MB = 1024 * 1024;

/**
 * Every tunable number of the relay. Code that enforces a limit takes it as an argument (default:
 * these values), so a different set - for example the one `loadLimits` builds - really applies.
 */
export const LIMITS = {
  maxEventTags: 256,
  maxTagElements: 16, // strings in one tag
  maxTagElementBytes: 1024, // UTF-8 bytes of one tag string
  maxFutureSeconds: 15 * 60,
  maxFilterBindings: 90, // SQLite in Durable Objects allows 100 bound values
  // --- rate limits (token buckets, capacity = one minute)
  eventsPerMinute: 120, // per connection
  reqsPerMinute: 60, // per connection
  framesPerMinute: 300, // per connection, every frame (also malformed / unknown / CLOSE)
  maxViolations: 50, // rejected frames within a minute before the connection is closed (1008)
  sourceEventsPerMinute: 240, // per source address, summed over its connections
  sourceReqsPerMinute: 120,
  // The Go client dials a new socket per query (Walk: up to 100 pages), so upgrades are generous.
  upgradesPerMinutePerSource: 60,
  maxConnsPerSource: 32,
  sourceWriteBytesPerMinute: 8 * MB, // stored-event bytes per source address (ephemeral events excluded)
  maxSourceEntries: 10_000, // LRU size of the per-source table
  globalEventWeightPerMinute: 30_000, // weighted: 1 + tag strings/32 + bytes/4096 per event
  sourceEventWeightPerMinute: 7_500, // one source may use at most 25% of the global event weight
  globalOutboundBytesPerMinute: 64 * MB,
  sourceOutboundBytesPerMinute: 16 * MB, // 25% of the global budget: deliveries to one source's sockets
  socketOutboundBytesPerMinute: 4 * MB, // below the per-source share so one socket cannot use all of it; over quota: deliveries are dropped, the socket is closed only when sustained
  // History sent for REQs has its own budgets, so pulling history cannot starve live delivery.
  sourceHistoryBytesPerMinute: 32 * MB,
  socketHistoryBytesPerMinute: 4 * MB,
  globalHistoryBytesPerMinute: 128 * MB,
  quotaWindowMs: 60_000,
  maxOverQuotaWindows: 3, // close (1013) after dropping continuously for about (N-1) windows
  // --- storage
  maxIndexedTags: 64, // distinct single-letter tags that are indexed per event
  maxStorageBytes: 5 * 1024 * MB,
  // --- deletion requests (NIP-09)
  maxTombstones: 1_000_000, // at the cap the OLDEST record is evicted (new kind 5 is never refused for it)
  maxCoordCutoffs: 1_000_000,
  maxDeletionTargets: 100, // e/a targets processed per kind 5
  maxDeletionRowWrites: 5000, // estimated row writes (incl. index rows) per kind 5; never exceeded
  // --- subscriptions / queries
  maxLimit: 500,
  maxSubsPerConn: 20,
  maxSubBytes: 4096, // serialized filters of one subscription
  maxSubBytesPerConn: 16 * 1024,
  maxSubRows: 20_000, // subscription rows across the whole relay
  subCacheBytes: 4 * MB, // in-memory cache of subscriptions; the rest is read from SQL
  maxResultBytes: 4 * MB, // total size of the stored events one REQ returns
  // --- frames and fan-out
  maxFiltersPerReq: 10,
  maxSubIdLength: 64,
  maxDeliveriesPerEvent: 5000,
  // --- connections and frames
  maxMessageBytes: 512_000, // UTF-8 bytes per frame (same as the old khatru relay)
  maxConnections: 2000,
  // A connection that has sent nothing (not even a text "ping") for this long may be closed when a connection
  // slot is needed (checked only at the connection caps, never by a timer). 0 disables.
  idleTimeoutSeconds: 900,
};

export type Limits = { [K in keyof typeof LIMITS]: number };

const MINIMUM: Partial<Record<keyof Limits, number>> = { maxLimit: 1 };

/**
 * The defaults, with overrides from a JSON object (the e2e tests start the relay with tiny caps).
 * An override must be a safe integer between its minimum (0, or 1 for maxLimit) and 1e9 (or the
 * default when that is larger); anything else - unknown keys, fractions, huge numbers - is ignored
 * and reported once.
 */
export function loadLimits(json?: string): Limits {
  const lim: Limits = { ...LIMITS };
  try {
    const o = json ? (JSON.parse(json) as Record<string, unknown>) : {};
    const ignored: string[] = [];
    for (const [k, v] of Object.entries(o)) {
      const key = k as keyof Limits;
      const ok =
        Object.hasOwn(LIMITS, k) &&
        typeof v === "number" &&
        Number.isSafeInteger(v) &&
        v >= (MINIMUM[key] ?? 0) &&
        v <= Math.max(1e9, LIMITS[key]);
      if (ok) lim[key] = v;
      else ignored.push(k);
    }
    if (ignored.length > 0) console.error("ignored invalid LIMITS_JSON keys:", ignored.join(", "));
  } catch (err) {
    console.error("bad LIMITS_JSON", err);
  }
  return lim;
}

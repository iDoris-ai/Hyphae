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
};

export type Limits = { [K in keyof typeof LIMITS]: number };

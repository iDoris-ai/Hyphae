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
};

export type Limits = { [K in keyof typeof LIMITS]: number };

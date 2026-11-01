import { type Limits, loadLimits } from "./config";
import { type RateLimiter, createAdmission } from "./admission";
import { createHealthCache, withCors } from "./http";
import { LANDING_CSP, LANDING_HTML, LOGO_SVG } from "./landing";
import { sourceKey } from "./limits";
export { Relay } from "./relay";

interface Env {
  RELAY: DurableObjectNamespace;
  // Cloudflare Workers Rate Limiting binding; optional so the Worker still runs where it is not configured.
  UPGRADE_LIMITER?: RateLimiter;
  // Header that carries the client address. Cloudflare sets CF-Connecting-IP itself; the
  // override exists only so local tests (where the client can pick the header) can use another.
  SRC_HEADER?: string;
  // Optional JSON overrides of the numeric limits; must be the same value the Durable Object sees.
  LIMITS_JSON?: string;
}

/** The NIP-11 document, built from the limits the Durable Object actually enforces. */
const nip11 = (lim: Limits) => ({
  name: "Hyphae relay",
  description: "Public Nostr relay for Hyphae agents (Cloudflare Worker + Durable Object).",
  supported_nips: [1, 9, 11, 67],
  software: "https://github.com/iDoris-ai/Hyphae",
  version: "0.1.0",
  limitation: {
    max_message_length: lim.maxMessageBytes,
    max_subscriptions: lim.maxSubsPerConn,
    max_filters: lim.maxFiltersPerReq,
    max_limit: lim.maxLimit,
    max_subid_length: lim.maxSubIdLength,
    max_event_tags: lim.maxEventTags,
    created_at_upper_limit: lim.maxFutureSeconds,
    // Non-standard, informational:
    max_indexed_tags: lim.maxIndexedTags,
    max_result_bytes: lim.maxResultBytes,
    max_subscription_bytes: lim.maxSubBytes,
    max_connections: lim.maxConnections,
    // "ip" limits are per source address; IPv6 addresses are grouped by /64, IPv4-mapped IPv6 as IPv4.
    max_connections_per_ip: lim.maxConnsPerSource,
    upgrades_per_minute_per_ip: lim.upgradesPerMinutePerSource,
    write_bytes_per_minute_per_ip: lim.sourceWriteBytesPerMinute,
    outbound_bytes_per_minute_per_ip: lim.sourceOutboundBytesPerMinute,
    history_bytes_per_minute_per_ip: lim.sourceHistoryBytesPerMinute,
    history_bytes_per_minute_per_connection: lim.socketHistoryBytesPerMinute,
    history_bytes_per_minute_global: lim.globalHistoryBytesPerMinute,
    max_tombstones: lim.maxTombstones,
    events_per_minute_per_connection: lim.eventsPerMinute,
    events_per_minute_per_ip: lim.sourceEventsPerMinute,
    reqs_per_minute_per_connection: lim.reqsPerMinute,
    reqs_per_minute_per_ip: lim.sourceReqsPerMinute,
    messages_per_minute_per_connection: lim.framesPerMinute,
    max_deletion_targets: lim.maxDeletionTargets,
  },
});

// loadLimits reports bad overrides on every call; cache per LIMITS_JSON so one bad value logs once per isolate, not once per request.
let limitsCache: { key: string | undefined; value: Limits } | undefined;
const limitsFor = (json: string | undefined): Limits => {
  if (!limitsCache || limitsCache.key !== json) limitsCache = { key: json, value: loadLimits(json) };
  return limitsCache.value;
};

// Edge admission: twice the Durable Object's per-source upgrade budget, so only floods are dropped here.
// Only the WebSocket-upgrade branch of route() calls it. A /healthz, NIP-11 or plain GET without an Upgrade
// header never takes a token and is never refused by it (a GET that does send Upgrade: websocket is an upgrade,
// and does); /healthz has its own per-isolate cache. withCors in the default export wraps the edge 429 too,
// so a browser page can read its Retry-After.
let admission: { perMinute: number; value: ReturnType<typeof createAdmission> } | undefined;
const admissionFor = (lim: Limits) => {
  const perMinute = lim.upgradesPerMinutePerSource * 2;
  if (!admission || admission.perMinute !== perMinute) admission = { perMinute, value: createAdmission({ perMinute }) };
  return admission.value;
};

const checkHealth = createHealthCache();

async function route(request: Request, env: Env): Promise<Response> {
    if (request.method === "OPTIONS") {
      return new Response(null, { status: 204, headers: { "access-control-max-age": "86400" } });
    }
    if (request.headers.get("Upgrade")?.toLowerCase() === "websocket") {
      // No source address means a misconfigured path; sharing one "unknown" budget would let one client starve the rest.
      const src = request.headers.get(env.SRC_HEADER ?? "CF-Connecting-IP");
      if (!src) {
        console.error("upgrade without a source address header");
        return new Response("source address unavailable", { status: 400 });
      }
      const key = sourceKey(src);
      const adm = await admissionFor(limitsFor(env.LIMITS_JSON)).check(key, env.UPGRADE_LIMITER);
      if (!adm.ok) {
        return new Response("too many connection attempts", { status: 429, headers: { "retry-after": String(adm.retryAfter) } });
      }
      try {
        // Pass the client address to the Durable Object. `set` replaces any x-hyphae-src the client sent.
        const headers = new Headers(request.headers);
        headers.set("x-hyphae-src", key);
        return await env.RELAY.get(env.RELAY.idFromName("main")).fetch(new Request(request, { headers }));
      } catch (err) {
        console.error("relay forward failed", err);
        return new Response("relay unavailable", { status: 503, headers: { "retry-after": "30" } });
      }
    }
    // Before NIP-11 content negotiation, or a probe sending Accept: application/nostr+json would never reach the object.
    if (new URL(request.url).pathname === "/healthz" && request.method === "GET") {
      const ok = await checkHealth(async () => {
        // A real call into the Durable Object that runs a SQLite query; the other HTTP paths never reach it.
        const res = await env.RELAY.get(env.RELAY.idFromName("main")).fetch(new Request(request.url, { method: "GET" }));
        return res.status === 200;
      });
      const headers = { "content-type": "text/plain; charset=utf-8", "cache-control": "no-store" };
      return ok ? new Response("ok\n", { headers }) : new Response("relay unavailable\n", { status: 503, headers });
    }
    if (request.headers.get("Accept")?.toLowerCase().includes("application/nostr+json")) {
      return new Response(JSON.stringify(nip11(limitsFor(env.LIMITS_JSON))), {
        headers: { "content-type": "application/nostr+json" },
      });
    }
    // Browsers only: a client that does not ask for text/html keeps the one-line plain answer below.
    const path = new URL(request.url).pathname;
    if (request.method === "GET" && path === "/logo.svg") {
      return new Response(LOGO_SVG, { headers: { "content-type": "image/svg+xml", "cache-control": "public, max-age=3600" } });
    }
    if (request.method === "GET" && path === "/" && request.headers.get("Accept")?.toLowerCase().includes("text/html")) {
      return new Response(LANDING_HTML, {
        headers: { "content-type": "text/html; charset=utf-8", "content-security-policy": LANDING_CSP, "cache-control": "public, max-age=300" },
      });
    }
    return new Response("Hyphae relay: connect with a Nostr client over WebSocket.\n", {
      headers: { "content-type": "text/plain; charset=utf-8" },
    });
}

export default {
  // CORS goes on every answer a browser can read; withCors leaves the 101 untouched.
  fetch: async (request: Request, env: Env): Promise<Response> => withCors(await route(request, env)),
};

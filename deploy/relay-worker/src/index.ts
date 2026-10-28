import { LIMITS } from "./nostr";
export { Relay } from "./relay";

interface Env {
  RELAY: DurableObjectNamespace;
  // Header that carries the client address. Cloudflare sets CF-Connecting-IP itself; the
  // override exists only so local tests (where the client can pick the header) can use another.
  SRC_HEADER?: string;
}

const NIP11 = {
  name: "Hyphae relay",
  description: "Public Nostr relay for Hyphae agents (Cloudflare Worker + Durable Object).",
  supported_nips: [1, 9, 11, 67],
  software: "https://github.com/iDoris-ai/Hyphae",
  version: "0.1.0",
  limitation: {
    max_message_length: LIMITS.maxMessageBytes,
    max_subscriptions: LIMITS.maxSubsPerConn,
    max_filters: LIMITS.maxFiltersPerReq,
    max_limit: LIMITS.maxLimit,
    max_subid_length: LIMITS.maxSubIdLength,
    max_event_tags: LIMITS.maxEventTags,
    created_at_upper_limit: LIMITS.maxFutureSeconds,
    // Non-standard, informational:
    max_indexed_tags: LIMITS.maxIndexedTags,
    max_result_bytes: LIMITS.maxResultBytes,
    max_subscription_bytes: LIMITS.maxSubBytes,
    max_connections: LIMITS.maxConnections,
    max_connections_per_ip: LIMITS.maxConnsPerSource,
    upgrades_per_minute_per_ip: LIMITS.upgradesPerMinutePerSource,
    events_per_minute_per_connection: LIMITS.eventsPerMinute,
    events_per_minute_per_ip: LIMITS.sourceEventsPerMinute,
    reqs_per_minute_per_connection: LIMITS.reqsPerMinute,
    reqs_per_minute_per_ip: LIMITS.sourceReqsPerMinute,
    messages_per_minute_per_connection: LIMITS.framesPerMinute,
    max_deletion_targets: LIMITS.maxDeletionTargets,
  },
};

const CORS = {
  "access-control-allow-origin": "*",
  "access-control-allow-methods": "GET, OPTIONS",
  "access-control-allow-headers": "*",
};

export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    if (request.method === "OPTIONS") {
      return new Response(null, { status: 204, headers: { ...CORS, "access-control-max-age": "86400" } });
    }
    if (request.headers.get("Upgrade")?.toLowerCase() === "websocket") {
      try {
        // Pass the client address to the Durable Object. `set` replaces any x-hyphae-src the client sent.
        const headers = new Headers(request.headers);
        headers.set("x-hyphae-src", (request.headers.get(env.SRC_HEADER ?? "CF-Connecting-IP") ?? "unknown").slice(0, 64));
        return await env.RELAY.get(env.RELAY.idFromName("main")).fetch(new Request(request, { headers }));
      } catch (err) {
        console.error("relay forward failed", err);
        return new Response("relay unavailable", { status: 503, headers: { "retry-after": "30" } });
      }
    }
    if (request.headers.get("Accept")?.includes("application/nostr+json")) {
      return new Response(JSON.stringify(NIP11), {
        headers: { "content-type": "application/nostr+json", ...CORS },
      });
    }
    return new Response("Hyphae relay: connect with a Nostr client over WebSocket.\n", {
      headers: { "content-type": "text/plain; charset=utf-8" },
    });
  },
};

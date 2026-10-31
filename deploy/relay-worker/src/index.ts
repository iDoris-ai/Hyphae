import { type Limits, loadLimits } from "./config";
import { sourceKey } from "./limits";
export { Relay } from "./relay";

interface Env {
  RELAY: DurableObjectNamespace;
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
        headers.set("x-hyphae-src", sourceKey(request.headers.get(env.SRC_HEADER ?? "CF-Connecting-IP") ?? "unknown"));
        return await env.RELAY.get(env.RELAY.idFromName("main")).fetch(new Request(request, { headers }));
      } catch (err) {
        console.error("relay forward failed", err);
        return new Response("relay unavailable", { status: 503, headers: { "retry-after": "30" } });
      }
    }
    if (request.headers.get("Accept")?.toLowerCase().includes("application/nostr+json")) {
      return new Response(JSON.stringify(nip11(loadLimits(env.LIMITS_JSON))), {
        headers: { "content-type": "application/nostr+json", ...CORS },
      });
    }
    return new Response("Hyphae relay: connect with a Nostr client over WebSocket.\n", {
      headers: { "content-type": "text/plain; charset=utf-8" },
    });
  },
};

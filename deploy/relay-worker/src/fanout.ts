import { type Hub, deliveryCost } from "./hub";
import { matchAny } from "./filter";
import type { ConnState } from "./limits";
import { indexedTags } from "./db";
import { type NostrEvent, utf8Length } from "./nostr";

/**
 * Delivers an event to matching subscriptions. The JSON is built once. Budgets bound the work:
 * per event, global bytes/min, per-source bytes/min (a source gets at most a quarter of the
 * global budget) and per-socket bytes/min. A delivery over a per-source or per-socket quota is
 * dropped (and counted); only a socket that stays over quota for several windows is closed.
 * Returns how many deliveries were sent.
 */
export function broadcast(hub: Hub, e: NostrEvent): number {
  const json = JSON.stringify(e);
  const evBytes = utf8Length(json);
  // Live matching sees the same tags a stored query does: only the indexed subset.
  const seen = { ...e, tags: indexedTags(e.tags, hub.lim.maxIndexedTags) };
  let delivered = 0;
  for (const ws of hub.sockets()) {
    try {
      const st = ws.deserializeAttachment() as ConnState | null;
      if (!st || st.closed) continue;
      for (const [subId, filters] of hub.subs.get(st.id)) {
        if (!matchAny(filters, seen)) continue;
        if (delivered >= hub.lim.maxDeliveriesPerEvent) return delivered;
        const idJson = JSON.stringify(subId);
        const charge = hub.budgets.chargeDelivery(st.src, st.id, deliveryCost(evBytes, idJson));
        if (charge === "global") return delivered; // global outbound budget exhausted: drop the rest of this event's delivery
        if (charge === "source") continue; // this source already uses its share: drop this delivery
        if (charge === "socket") {
          if (hub.budgets.overQuota(st.id)) {
            hub.forceClose(ws, 1013, "outbound quota exceeded, try again later", st);
            break;
          }
          continue;
        }
        hub.sendRaw(ws, `["EVENT",${idJson},${json}]`);
        delivered++;
      }
    } catch (err) {
      console.error("broadcast failed for a socket", err);
    }
  }
  return delivered;
}

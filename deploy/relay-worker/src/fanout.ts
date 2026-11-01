import { type Hub, deliveryCost } from "./hub";
import { type Filter, matchAny } from "./filter";
import type { ConnState } from "./limits";
import { indexedTags } from "./db";
import { type NostrEvent, utf8Length } from "./nostr";

/**
 * Delivers an event to matching subscriptions. The JSON is built once. Only subscriptions the
 * candidate index returns are compared (full matchAny still decides), and at most
 * maxFanoutWorkPerEvent candidates are looked at per event (loadLimits keeps it >= maxSubRows, so
 * normally nobody is skipped); the rest are skipped and a warning is logged (no counter).
 * Budgets bound the delivery: per event, global bytes/min, per-source bytes/min (a source
 * gets at most a quarter of the global budget) and per-socket bytes/min. A delivery over a
 * per-source or per-socket quota is dropped (and counted); only a socket that stays over quota
 * for several windows is closed.
 */
export function broadcast(hub: Hub, e: NostrEvent) {
  const json = JSON.stringify(e);
  const evBytes = utf8Length(json);
  // Live matching sees the same tags a stored query does: only the indexed subset.
  const seen = { ...e, tags: indexedTags(e.tags, hub.lim.maxIndexedTags) };
  let delivered = 0;
  let looked = 0; // distinct candidate subscriptions compared: this is what the work cap limits
  let visits = 0; // index references walked, duplicates included (bound 16x the work cap: a subscription has at most 16 index references, so it never binds before the cap)
  const done = new Set<string>();
  const conns = new Map<string, { ws: WebSocket; st: ConnState } | null>(); // sockets only: filters are not held here, SubStore's LRU bounds them
  let last: { conn: string; subs: Map<string, Filter[]> } | undefined; // filters of the connection just handled (at most one, 16 KB)
  let byId: Map<string, WebSocket> | undefined; // fallback for sockets that carry no connection-id tag
  const open = (conn: string) => {
    let ws = hub.socketOf?.(conn);
    if (!ws) {
      byId ??= new Map(hub.sockets().map((w) => [(w.deserializeAttachment() as ConnState | null)?.id ?? "", w]));
      ws = byId.get(conn);
    }
    const st = ws?.deserializeAttachment() as ConnState | null | undefined;
    return ws && st && !st.closed ? { ws, st } : null;
  };
  try {
    for (const ref of hub.subs.candidates(seen)) {
      if (looked >= hub.lim.maxFanoutWorkPerEvent || ++visits > 16 * hub.lim.maxFanoutWorkPerEvent) {
        console.warn(`fan-out work cap (${looked} compared, ${visits} walked) reached for event ${e.id}; remaining candidates skipped`);
        return;
      }
      if (done.has(ref)) continue;
      done.add(ref);
      looked++;
      const cut = ref.indexOf("\0");
      const conn = ref.slice(0, cut);
      const subId = ref.slice(cut + 1);
      try {
        let c = conns.get(conn);
        if (c === undefined) conns.set(conn, (c = open(conn)));
        if (!c) continue;
        if (last?.conn !== conn) last = { conn, subs: hub.subs.get(conn) };
        const filters = last.subs.get(subId);
        if (!filters || !matchAny(filters, seen)) continue;
        if (delivered >= hub.lim.maxDeliveriesPerEvent) return;
        const idJson = JSON.stringify(subId);
        const charge = hub.budgets.chargeDelivery(c.st.src, c.st.id, deliveryCost(evBytes, idJson));
        if (charge === "global") return; // global outbound budget exhausted: drop the rest of this event's delivery
        if (charge === "source") continue; // this source already uses its share: drop this delivery
        if (charge === "socket") {
          if (hub.budgets.overQuota(c.st.id)) {
            hub.forceClose(c.ws, 1013, "outbound quota exceeded, try again later", c.st);
            conns.set(conn, null);
          }
          continue;
        }
        hub.sendRaw(c.ws, `["EVENT",${idJson},${json}]`);
        delivered++;
      } catch (err) {
        console.error("broadcast failed for a subscription", err);
      }
    }
  } catch (err) {
    console.error("broadcast candidate lookup failed", err);
  }
}

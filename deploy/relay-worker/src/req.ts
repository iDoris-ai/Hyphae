import { validateFilter } from "./filter";
import { type Hub, deliveryCost } from "./hub";
import { utf8Length } from "./nostr";
import type { ConnState } from "./limits";
import { runQuery } from "./query";
import { normalizeFilters } from "./subs";

/** Every CLOSED we send means the subscription no longer exists, so drop it first (or close the socket). */
function closeSub(hub: Hub, ws: WebSocket, conn: string, subId: string, reason: string) {
  try {
    hub.subs.remove(conn, subId);
  } catch (err) {
    // Cannot guarantee the subscription is gone: do not claim CLOSED, drop the connection instead.
    console.error("removeSub failed", err);
    hub.forceClose(ws, 1011, "internal error");
    return;
  }
  hub.send(ws, ["CLOSED", subId, reason]);
}

export function processReq(hub: Hub, ws: WebSocket, st: ConnState, args: unknown[]) {
  const conn = st.id;
  const subId = args[0] as string;
  const validId = typeof subId === "string" && subId.length > 0 && subId.length <= hub.lim.maxSubIdLength;
  // Every REQ costs a token, including repeated REQs for the same subId and ones with an invalid id.
  if (!hub.take(ws, st, "rq", hub.lim.reqsPerMinute) || !hub.budgets.takeSource(st.src, "rq")) {
    hub.violation(ws, st);
    if (!validId) return hub.send(ws, ["NOTICE", "rate-limited: too many REQs, slow down"]);
    return closeSub(hub, ws, conn, subId, "rate-limited: too many REQs, slow down");
  }
  if (!validId) {
    hub.violation(ws, st);
    hub.send(ws, ["NOTICE", "error: invalid subscription id"]);
    return;
  }
  const raw = args.slice(1);
  if (raw.length === 0 || raw.length > hub.lim.maxFiltersPerReq) {
    return closeSub(hub, ws, conn, subId, "invalid: need 1 to " + hub.lim.maxFiltersPerReq + " filters");
  }
  for (const f of raw) {
    const err = validateFilter(f, hub.lim);
    if (err) return closeSub(hub, ws, conn, subId, err);
  }
  const filters = normalizeFilters(raw as Parameters<typeof normalizeFilters>[0]);
  const fjson = JSON.stringify(filters);
  try {
    const saved = hub.subs.save(conn, subId, fjson);
    if (!saved.ok) {
      if (saved.drop) closeSub(hub, ws, conn, subId, saved.reason);
      else hub.send(ws, ["CLOSED", subId, saved.reason]);
      return;
    }
    const idJson = JSON.stringify(subId);
    // History is outbound traffic too: it is charged to the history budgets (source, global, socket),
    // separate from live delivery. The smallest possible frame is reserved BEFORE reading anything, so an
    // exhausted allowance costs no query at all; the reserve is part of the first event's cost.
    const reserve = deliveryCost(0, idJson);
    if (!hub.budgets.chargeHistory(st.src, conn, reserve)) return void hub.send(ws, ["EOSE", subId, ["more"]]);
    const { events, truncated } = runQuery(hub.db, filters);
    let sent = 0;
    for (const json of events) {
      if (!hub.budgets.chargeHistory(st.src, conn, deliveryCost(utf8Length(json), idJson) - (sent === 0 ? reserve : 0))) break;
      hub.sendRaw(ws, `["EVENT",${idJson},${json}]`);
      sent++;
    }
    // NIP-67: tell the client that matching history was left out (never "finish" in that case).
    hub.send(ws, truncated || sent < events.length ? ["EOSE", subId, ["more"]] : ["EOSE", subId]);
  } catch (err) {
    console.error("onReq failed", err);
    closeSub(hub, ws, conn, subId, "error: could not process request");
  }
}

export function processClose(hub: Hub, ws: WebSocket, conn: string, subId: unknown) {
  if (typeof subId !== "string") return;
  try {
    hub.subs.remove(conn, subId);
  } catch (err) {
    console.error("removeSub failed", err);
    hub.forceClose(ws, 1011, "internal error");
  }
}

import { broadcast } from "./fanout";
import type { Hub } from "./hub";
import { eventWeight, type ConnState } from "./limits";
import { type NostrEvent, isEphemeral, validateEvent } from "./nostr";
import { isBlocked } from "./deletion";
import { hasEvent } from "./db";
import { storeEvent } from "./store";

/**
 * EVENT admission: this connection's and this source's own budgets first (a reject counts as the
 * connection's violation), then cheap structural checks, then signature validation, and only then
 * the shared budgets, so unvalidated junk cannot drain what honest clients need.
 */
export function processEvent(hub: Hub, ws: WebSocket, st: ConnState, raw: unknown, size: number) {
  const { lim } = hub;
  const id = typeof (raw as NostrEvent | undefined)?.id === "string" ? (raw as NostrEvent).id.slice(0, 64) : "";
  const reply = (ok: boolean, msg: string, eid = id) => hub.send(ws, ["OK", eid, ok, msg]);
  try {
    if (!hub.take(ws, st, "ev", lim.eventsPerMinute) || !hub.budgets.takeSource(st.src, "ev")) {
      hub.violation(ws, st);
      return reply(false, "rate-limited: slow down");
    }
    // Hashing and verifying a signature is real work: the source pays for it before we do it,
    // whether the event turns out to be new, a replay or invalid (its own budget only).
    if (!hub.budgets.chargeValidation(st.src, eventWeight(0, size))) {
      hub.violation(ws, st);
      return reply(false, "rate-limited: too much load from this address, slow down");
    }
    const err = validateEvent(raw, undefined, lim); // refuses an oversized tag list before looking at the tags
    if (err) return reply(false, err);
    const e = raw as NostrEvent;
    const ephemeral = isEphemeral(e.kind);
    // Now the tags are known to be sane: the source also pays for the tag part of the event weight,
    // the same weight the global budget will be charged, so tag-heavy events cannot be cheap for it.
    const strings = e.tags.reduce((n, t) => n + t.length, 0);
    const weight = eventWeight(strings, size);
    if (weight > eventWeight(0, size) && !hub.budgets.chargeValidation(st.src, weight - eventWeight(0, size))) {
      hub.violation(ws, st);
      return reply(false, "rate-limited: too much load from this address, slow down", e.id);
    }
    // A replay of a stored event costs the shared budgets nothing.
    if (!ephemeral && hasEvent(hub.db, e.id)) return reply(true, "duplicate: already have this event", e.id);
    // The sender's stored bytes and event weight count against it; the global budget running out is not its fault.
    const charge = hub.budgets.chargeEvent(st.src, ephemeral ? 0 : size, weight);
    if (charge === "source") {
      hub.violation(ws, st);
      return reply(false, "rate-limited: too much load from this address, slow down", e.id);
    }
    if (charge === "global") return reply(false, "rate-limited: relay is busy, retry later", e.id);
    if (ephemeral) {
      if (isBlocked(hub.db, e, "")) return reply(false, "blocked: event was deleted", e.id);
      broadcast(hub, e);
      return reply(true, "", e.id);
    }
    switch (storeEvent(hub.db, e)) {
      case "duplicate":
        return reply(true, "duplicate: already have this event", e.id);
      case "stale":
        return reply(false, "invalid: a newer version of this event exists", e.id);
      case "blocked":
        return reply(false, "blocked: event was deleted", e.id);
      case "full":
        return reply(false, "rate-limited: relay storage is full", e.id);
      case "unavailable":
        return reply(false, "error: temporarily unavailable, retry", e.id);
      case "ok_partial":
        broadcast(hub, e);
        return reply(true, `ignored: only part of the e/a targets were processed (limits: ${lim.maxDeletionTargets} targets, ${lim.maxDeletionRowWrites} row writes)`, e.id);
    }
    broadcast(hub, e);
    reply(true, "", e.id);
  } catch (err) {
    console.error("onEvent failed", err);
    reply(false, "error: internal error");
  }
}

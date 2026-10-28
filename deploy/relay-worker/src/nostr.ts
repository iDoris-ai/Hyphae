import { schnorr } from "@noble/curves/secp256k1";
import { sha256 } from "@noble/hashes/sha2";
import { bytesToHex, hexToBytes, utf8ToBytes } from "@noble/hashes/utils";
import { LIMITS, type Limits } from "./config";

export interface NostrEvent {
  id: string;
  pubkey: string;
  created_at: number;
  kind: number;
  tags: string[][];
  content: string;
  sig: string;
}

export const HEX64 = /^[0-9a-f]{64}$/;
const HEX128 = /^[0-9a-f]{128}$/;
const encoder = new TextEncoder();
const A_TAG_PREFIX = 130; // room for "kind:pubkey:" in an "a" tag, with margin

/** UTF-8 byte length of a string. */
export const utf8Length = (s: string): number => encoder.encode(s).length;

export function serializeEvent(e: NostrEvent): string {
  return JSON.stringify([0, e.pubkey, e.created_at, e.kind, e.tags, e.content]);
}

export function eventId(e: NostrEvent): string {
  return bytesToHex(sha256(utf8ToBytes(serializeEvent(e))));
}

export type EventLimits = Pick<Limits, "maxEventTags" | "maxTagElements" | "maxTagElementBytes" | "maxFutureSeconds">;

/** Returns an error string, or null when the event is well-formed and correctly signed. */
export function validateEvent(e: unknown, now = Math.floor(Date.now() / 1000), lim: EventLimits = LIMITS): string | null {
  if (typeof e !== "object" || e === null) return "invalid: event must be an object";
  const ev = e as Record<string, unknown>;
  if (typeof ev.id !== "string" || !HEX64.test(ev.id)) return "invalid: bad id";
  if (typeof ev.pubkey !== "string" || !HEX64.test(ev.pubkey)) return "invalid: bad pubkey";
  if (typeof ev.sig !== "string" || !HEX128.test(ev.sig)) return "invalid: bad sig";
  if (!Number.isInteger(ev.created_at) || (ev.created_at as number) < 0) return "invalid: bad created_at";
  if (!Number.isInteger(ev.kind) || (ev.kind as number) < 0 || (ev.kind as number) > 65535) return "invalid: bad kind";
  if (typeof ev.content !== "string") return "invalid: bad content";
  if (!Array.isArray(ev.tags) || ev.tags.length > lim.maxEventTags) return "invalid: bad tags";
  for (const t of ev.tags) {
    if (!Array.isArray(t) || t.length === 0 || t.length > lim.maxTagElements) return "invalid: bad tags";
    for (let i = 0; i < t.length; i++) {
      const x = t[i];
      // An "a" coordinate is "kind:pubkey:" (at most 70 bytes) plus a "d" value of up to the element limit.
      const cap = t[0] === "a" && i === 1 ? lim.maxTagElementBytes + A_TAG_PREFIX : lim.maxTagElementBytes;
      if (typeof x !== "string" || x.length > cap || utf8Length(x) > cap) return "invalid: bad tags";
    }
  }
  const event = e as NostrEvent;
  if ((event.created_at as number) > now + lim.maxFutureSeconds) return "invalid: created_at too far in the future";
  if (eventId(event) !== event.id) return "invalid: id does not match event content";
  let ok = false;
  try {
    ok = schnorr.verify(hexToBytes(event.sig), hexToBytes(event.id), hexToBytes(event.pubkey));
  } catch {
    ok = false;
  }
  return ok ? null : "invalid: bad signature";
}

export const isEphemeral = (kind: number) => kind >= 20000 && kind < 30000;
export const isReplaceable = (kind: number) => kind === 0 || kind === 3 || (kind >= 10000 && kind < 20000);
export const isAddressable = (kind: number) => kind >= 30000 && kind < 40000;
export const dTag = (e: NostrEvent) => e.tags.find((t) => t[0] === "d")?.[1] ?? "";

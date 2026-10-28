import { schnorr } from "@noble/curves/secp256k1";
import { bytesToHex } from "@noble/hashes/utils";
import { type NostrEvent, eventId } from "../../src/nostr";

export const NOW = 1_800_000_000;

export function newKey() {
  const sk = schnorr.utils.randomPrivateKey();
  return { sk, pk: bytesToHex(schnorr.getPublicKey(sk)) };
}

export function signed(
  key: { sk: Uint8Array; pk: string },
  over: Partial<Pick<NostrEvent, "kind" | "created_at" | "tags" | "content">> = {},
): NostrEvent {
  const base = {
    pubkey: key.pk,
    created_at: over.created_at ?? NOW,
    kind: over.kind ?? 1,
    tags: over.tags ?? [],
    content: over.content ?? "hello",
  };
  const id = eventId({ ...base, id: "", sig: "" });
  const sig = bytesToHex(schnorr.sign(id, key.sk));
  return { ...base, id, sig };
}

export const alice = newKey();
export const bob = newKey();

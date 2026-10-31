// Shared helpers for the end-to-end checks: arguments, keys, signed events, a small WebSocket client.
import { schnorr } from "@noble/curves/secp256k1";
import { sha256 } from "@noble/hashes/sha2";
import { bytesToHex, hexToBytes, utf8ToBytes } from "@noble/hashes/utils";

export const ARGS = process.argv.slice(2);
export const flagVal = (name) => {
  const i = ARGS.indexOf(name);
  return i >= 0 ? ARGS[i + 1] : undefined;
};
export const SEED_FILE = flagVal("--seed");
export const CAPPED_FILE = flagVal("--capped");
export const SRC_HEADER = (ARGS.find((a) => a.startsWith("--src-header=")) ?? "--src-header=CF-Connecting-IP").split("=")[1];
export const WS_URL = ARGS.find((a, i) => !a.startsWith("--") && ARGS[i - 1] !== "--seed" && ARGS[i - 1] !== "--capped") ?? "ws://127.0.0.1:8787";
export const HTTP_URL = WS_URL.replace(/^ws/, "http");
export const MAX_LIMIT = 500;
let ipCounter = Math.floor(Math.random() * 60000);
export const nextCounter = () => ipCounter++;
export const freshIp = () => {
  const n = nextCounter();
  return `10.${(n >> 8) & 255}.${n & 255}.${(n >> 16) + 1}`;
};

let passed = 0;
let failed = 0;
/** Prints the totals and exits with the right status. */
export function finish() {
  console.log(`\n${passed} passed, ${failed} failed`);
  process.exit(failed ? 1 : 0);
}
export const check = (name, cond, detail = "") => {
  if (cond) {
    passed++;
    console.log(`  PASS  ${name}`);
  } else {
    failed++;
    console.log(`  FAIL  ${name}${detail ? "  -> " + detail : ""}`);
  }
};
export const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

export function newKey() {
  const sk = schnorr.utils.randomPrivateKey();
  return { sk, pk: bytesToHex(schnorr.getPublicKey(sk)) };
}

export function makeEvent(key, { kind = 1, content = "", tags = [], created_at = Math.floor(Date.now() / 1000) } = {}) {
  const id = bytesToHex(sha256(utf8ToBytes(JSON.stringify([0, key.pk, created_at, kind, tags, content]))));
  return { id, pubkey: key.pk, created_at, kind, tags, content, sig: bytesToHex(schnorr.sign(id, key.sk)) };
}

export class Client {
  constructor() {
    this.msgs = [];
    this.waiters = [];
    this.closeCode = null;
  }
  async open(ip = freshIp()) {
    this.ws = new WebSocket(WS_URL, { headers: { [SRC_HEADER]: ip } });
    this.ws.addEventListener("close", (ev) => {
      this.closeCode = ev.code;
    });
    this.ws.addEventListener("message", (ev) => {
      const m = JSON.parse(ev.data);
      this.msgs.push(m);
      for (const w of [...this.waiters]) w();
    });
    await new Promise((res, rej) => {
      this.ws.addEventListener("open", res, { once: true });
      this.ws.addEventListener("error", () => rej(new Error(`cannot connect to ${WS_URL}`)), { once: true });
    });
    return this;
  }
  send(m) {
    this.ws.send(JSON.stringify(m));
  }
  /** Wait for a message matching pred (searching already-received ones first, consuming it). */
  async wait(pred, timeout = 5000) {
    const take = () => {
      const i = this.msgs.findIndex(pred);
      return i >= 0 ? this.msgs.splice(i, 1)[0] : null;
    };
    let hit = take();
    if (hit) return hit;
    return new Promise((resolve) => {
      const t = setTimeout(() => {
        this.waiters = this.waiters.filter((w) => w !== check1);
        resolve(null);
      }, timeout);
      const check1 = () => {
        const h = take();
        if (h) {
          clearTimeout(t);
          this.waiters = this.waiters.filter((w) => w !== check1);
          resolve(h);
        }
      };
      this.waiters.push(check1);
    });
  }
  async publish(ev) {
    this.send(["EVENT", ev]);
    return (await this.wait((m) => m[0] === "OK" && m[1] === ev.id)) ?? ["OK", ev.id, null, "TIMEOUT"];
  }
  /** REQ and collect events until EOSE. */
  async req(subId, ...filters) {
    this.send(["REQ", subId, ...filters]);
    const events = [];
    for (;;) {
      const m = await this.wait((x) => (x[0] === "EVENT" || x[0] === "EOSE" || x[0] === "CLOSED") && x[1] === subId);
      if (!m) return { events, eose: false, closed: null, hint: null };
      if (m[0] === "EVENT") events.push(m[2]);
      else if (m[0] === "EOSE") return { events, eose: true, closed: null, hint: m[2] ?? null };
      else return { events, eose: false, closed: m[2], hint: null };
    }
  }
  close() {
    try { this.ws.close(); } catch {}
  }
}

export const open = async (ip) => new Client().open(ip);
export const uniq = () => bytesToHex(schnorr.utils.randomPrivateKey()).slice(0, 16);


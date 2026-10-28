#!/usr/bin/env node
// End-to-end checks for the relay worker.
//   node scripts/e2e.mjs [ws://127.0.0.1:8787] [--src-header=NAME] [--seed FILE | --capped FILE]
// Each connection pretends to come from its own address by sending it in the source header
// (CF-Connecting-IP by default; start wrangler with `--var SRC_HEADER:x-e2e-src` and pass
// --src-header=x-e2e-src if the dev server overwrites CF-Connecting-IP).
// --seed FILE   : (normal relay) store a replaceable event and a note, write their keys to FILE.
// --capped FILE : (fresh relay state seeded with --seed, restarted with tiny caps) run the cap checks:
//   --var 'LIMITS_JSON:{"maxStorageBytes":1,"maxConnections":6,"maxTombstones":1}'
// --fanout      : (any relay started with the fan-out caps) check the delivery budgets:
//   --var 'LIMITS_JSON:{"maxDeliveriesPerEvent":2,"socketOutboundBytesPerMinute":3000}'
import { readFileSync, writeFileSync } from "node:fs";
import { schnorr } from "@noble/curves/secp256k1";
import { sha256 } from "@noble/hashes/sha2";
import { bytesToHex, hexToBytes, utf8ToBytes } from "@noble/hashes/utils";

const ARGS = process.argv.slice(2);
const flagVal = (name) => {
  const i = ARGS.indexOf(name);
  return i >= 0 ? ARGS[i + 1] : undefined;
};
const SEED_FILE = flagVal("--seed");
const CAPPED_FILE = flagVal("--capped");
const SRC_HEADER = (ARGS.find((a) => a.startsWith("--src-header=")) ?? "--src-header=CF-Connecting-IP").split("=")[1];
const WS_URL = ARGS.find((a, i) => !a.startsWith("--") && ARGS[i - 1] !== "--seed" && ARGS[i - 1] !== "--capped") ?? "ws://127.0.0.1:8787";
const HTTP_URL = WS_URL.replace(/^ws/, "http");
const MAX_LIMIT = 500;
let ipCounter = Math.floor(Math.random() * 60000);
const freshIp = () => `10.${(ipCounter >> 8) & 255}.${ipCounter & 255}.${(ipCounter++ >> 16) + 1}`;

let passed = 0;
let failed = 0;
const check = (name, cond, detail = "") => {
  if (cond) {
    passed++;
    console.log(`  PASS  ${name}`);
  } else {
    failed++;
    console.log(`  FAIL  ${name}${detail ? "  -> " + detail : ""}`);
  }
};
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

function newKey() {
  const sk = schnorr.utils.randomPrivateKey();
  return { sk, pk: bytesToHex(schnorr.getPublicKey(sk)) };
}

function makeEvent(key, { kind = 1, content = "", tags = [], created_at = Math.floor(Date.now() / 1000) } = {}) {
  const id = bytesToHex(sha256(utf8ToBytes(JSON.stringify([0, key.pk, created_at, kind, tags, content]))));
  return { id, pubkey: key.pk, created_at, kind, tags, content, sig: bytesToHex(schnorr.sign(id, key.sk)) };
}

class Client {
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

const open = async (ip) => new Client().open(ip);
const uniq = () => bytesToHex(schnorr.utils.randomPrivateKey()).slice(0, 16);

async function main() {
  console.log(`relay: ${WS_URL}`);
  const alice = newKey();
  const bob = newKey();

  console.log("NIP-11");
  {
    const r = await fetch(HTTP_URL, { headers: { Accept: "application/nostr+json" } });
    const j = await r.json();
    check("status 200 + nostr+json content-type", r.status === 200 && (r.headers.get("content-type") ?? "").includes("application/nostr+json"));
    check("has name and supported_nips incl. 1", typeof j.name === "string" && j.supported_nips?.includes(1));
    check("CORS header present", r.headers.get("access-control-allow-origin") === "*");
    check("limitation.max_limit == 500", j.limitation?.max_limit === MAX_LIMIT);
    check("NIP-11 has Access-Control-Allow-Methods", /GET/.test(r.headers.get("access-control-allow-methods") ?? ""));
    const pre = await fetch(HTTP_URL, {
      method: "OPTIONS",
      headers: { Origin: "https://example.com", "Access-Control-Request-Method": "GET", "Access-Control-Request-Headers": "accept" },
    });
    check(
      "OPTIONS preflight -> 204 + CORS origin/methods/headers",
      pre.status === 204 &&
        pre.headers.get("access-control-allow-origin") === "*" &&
        /GET/.test(pre.headers.get("access-control-allow-methods") ?? "") &&
        !!pre.headers.get("access-control-allow-headers"),
      `status=${pre.status}`,
    );
  }

  if (SEED_FILE) return runSeed();
  if (CAPPED_FILE) return runCapped(alice);
  if (ARGS.includes("--fanout")) return runFanout(alice);

  const a = await open();

  console.log("publish");
  const note = makeEvent(alice, { content: "hello " + uniq() });
  {
    const ok = await a.publish(note);
    check("valid event -> OK true", ok[2] === true, JSON.stringify(ok));
    const dup = await a.publish(note);
    check("duplicate -> OK true with 'duplicate'", dup[2] === true && /duplicate/.test(dup[3]), JSON.stringify(dup));
    const bad = makeEvent(alice, { content: "badsig" });
    bad.sig = bytesToHex(schnorr.sign(bad.id, bob.sk));
    const r = await a.publish(bad);
    check("bad signature -> OK false", r[2] === false && /signature/.test(r[3]), JSON.stringify(r));
    const tampered = makeEvent(alice, { content: "x" });
    tampered.content = "y";
    const r2 = await a.publish(tampered);
    check("tampered content (id mismatch) -> OK false", r2[2] === false, JSON.stringify(r2));
    const fut = makeEvent(alice, { content: "future", created_at: Math.floor(Date.now() / 1000) + 3600 });
    const r3 = await a.publish(fut);
    check("far-future created_at -> OK false", r3[2] === false, JSON.stringify(r3));
    const stored = await a.req("bad", { ids: [bad.id] });
    check("rejected event not stored", stored.eose && stored.events.length === 0);
  }

  console.log("REQ / EOSE / live broadcast / CLOSE");
  {
    const r = await a.req("s1", { ids: [note.id] });
    check("REQ returns stored event then EOSE", r.eose && r.events.length === 1 && r.events[0].id === note.id);
    const byPrefix = await a.req("s-prefix", { authors: [alice.pk.slice(0, 10)], ids: [note.id.slice(0, 12)] });
    check("authors/ids hex-prefix match", byPrefix.events.length === 1 && byPrefix.events[0].id === note.id);
    const byFull = await a.req("s-full", { authors: [alice.pk], ids: [note.id] });
    check("full 64-char authors/ids match", byFull.events.length === 1);
    const empty = await a.req("s-empty", { authors: [bob.pk] });
    check("REQ with no matches -> just EOSE", empty.eose && empty.events.length === 0);

    const b = await open();
    const tag = uniq();
    const sub = await b.req("live", { kinds: [1], "#t": [tag] });
    check("second connection subscribes (EOSE)", sub.eose);
    const live1 = makeEvent(alice, { content: "live1", tags: [["t", tag]] });
    await a.publish(live1);
    const got = await b.wait((m) => m[0] === "EVENT" && m[1] === "live" && m[2].id === live1.id);
    check("live event delivered to other connection", !!got);
    const other = makeEvent(alice, { content: "nomatch", tags: [["t", "different-" + tag]] });
    await a.publish(other);
    const nope = await b.wait((m) => m[0] === "EVENT" && m[2]?.id === other.id, 600);
    check("non-matching event not delivered", !nope);

    b.send(["CLOSE", "live"]);
    await sleep(200);
    const live2 = makeEvent(alice, { content: "live2", tags: [["t", tag]] });
    await a.publish(live2);
    const after = await b.wait((m) => m[0] === "EVENT" && m[2]?.id === live2.id, 800);
    check("CLOSE stops delivery", !after);
    b.close();
  }

  console.log("replaceable events");
  for (const kind of [10002, 0]) {
    const k = newKey();
    const t0 = Math.floor(Date.now() / 1000) - 100;
    const older = makeEvent(k, { kind, content: "old", created_at: t0 });
    const newer = makeEvent(k, { kind, content: "new", created_at: t0 + 10 });
    await a.publish(older);
    const rn = await a.publish(newer);
    const q = await a.req("rep" + kind, { authors: [k.pk], kinds: [kind] });
    check(`kind ${kind}: newer replaces older (only newest kept)`, rn[2] === true && q.events.length === 1 && q.events[0].id === newer.id, `${q.events.length} events`);
    const stale = makeEvent(k, { kind, content: "stale", created_at: t0 - 50 });
    const rs = await a.publish(stale);
    const q2 = await a.req("rep2" + kind, { authors: [k.pk], kinds: [kind] });
    check(`kind ${kind}: late-arriving older version rejected`, rs[2] === false && q2.events.length === 1 && q2.events[0].id === newer.id, JSON.stringify(rs));
  }

  console.log("addressable events (kind 30023)");
  {
    const k = newKey();
    const t0 = Math.floor(Date.now() / 1000) - 100;
    const x1 = makeEvent(k, { kind: 30023, tags: [["d", "x"]], content: "x1", created_at: t0 });
    const x2 = makeEvent(k, { kind: 30023, tags: [["d", "x"]], content: "x2", created_at: t0 + 5 });
    const y1 = makeEvent(k, { kind: 30023, tags: [["d", "y"]], content: "y1", created_at: t0 });
    for (const e of [x1, x2, y1]) await a.publish(e);
    const q = await a.req("addr", { authors: [k.pk], kinds: [30023] });
    const ids = q.events.map((e) => e.id).sort();
    check("per-d-tag replacement (x2 + y1 kept)", ids.length === 2 && ids.includes(x2.id) && ids.includes(y1.id), JSON.stringify(q.events.map((e) => e.content)));
  }

  console.log("deletion (kind 5)");
  {
    const victim = makeEvent(alice, { content: "to delete " + uniq() });
    await a.publish(victim);
    const evil = makeEvent(bob, { kind: 5, tags: [["e", victim.id]], content: "not mine" });
    const re = await a.publish(evil);
    const still = await a.req("del1", { ids: [victim.id] });
    check("deletion by another key does NOT delete", re[2] === true && still.events.length === 1, `OK=${JSON.stringify(re)} found=${still.events.length}`);
    const good = makeEvent(alice, { kind: 5, tags: [["e", victim.id]], content: "mine" });
    const rg = await a.publish(good);
    const gone = await a.req("del2", { ids: [victim.id] });
    check("deletion by owner deletes", rg[2] === true && gone.events.length === 0, `found=${gone.events.length}`);
  }

  console.log("ephemeral (kind 20001)");
  {
    const b = await open();
    const sub = await b.req("eph", { kinds: [20001] });
    check("subscriber ready", sub.eose);
    const eph = makeEvent(alice, { kind: 20001, content: "ephemeral " + uniq() });
    const ok = await a.publish(eph);
    const got = await b.wait((m) => m[0] === "EVENT" && m[1] === "eph" && m[2].id === eph.id);
    check("ephemeral: OK true and delivered live", ok[2] === true && !!got, JSON.stringify(ok));
    const q = await a.req("eph2", { ids: [eph.id] });
    check("ephemeral: not stored", q.eose && q.events.length === 0);
    b.close();
  }

  console.log("tag filters");
  {
    const target = newKey();
    const other = newKey();
    const e1 = makeEvent(alice, { content: "to target", tags: [["p", target.pk]] });
    const e2 = makeEvent(alice, { content: "to other", tags: [["p", other.pk]] });
    const e3 = makeEvent(alice, { content: "no p" });
    for (const e of [e1, e2, e3]) await a.publish(e);
    const q = await a.req("ptag", { "#p": [target.pk] });
    check("#p filter returns only matching event", q.events.length === 1 && q.events[0].id === e1.id, `${q.events.length} events`);
    const q2 = await a.req("ptag2", { "#p": [target.pk, other.pk], authors: [alice.pk], since: Math.floor(Date.now() / 1000) - 60 });
    check("#p with multiple values (OR) + authors + since", q2.events.length === 2);
    const q3 = await a.req("ptag3", { "#p": ["f".repeat(64)] });
    check("#p with unknown value -> empty", q3.eose && q3.events.length === 0);
  }

  console.log("limit");
  {
    const tag = uniq();
    const small = await (async () => {
      const k = newKey();
      const base = Math.floor(Date.now() / 1000) - 1000;
      for (let i = 0; i < 5; i++) await a.publish(makeEvent(k, { content: "n" + i, tags: [["t", tag + "s"]], created_at: base + i }));
      return a.req("lim3", { "#t": [tag + "s"], limit: 3 });
    })();
    check("limit:3 returns exactly 3, newest first", small.events.length === 3 && small.events[0].created_at >= small.events[2].created_at && small.events[0].content === "n4", small.events.map((e) => e.content).join(","));
    check("client-chosen limit gives a plain EOSE (no 'more' hint)", small.eose && small.hint === null, JSON.stringify(small.hint));

    // 510 events over 5 connections (relay rate-limits ~120 events/min per connection).
    const conns = await Promise.all(Array.from({ length: 5 }, open));
    const k = newKey();
    const base = Math.floor(Date.now() / 1000) - 5000;
    let okCount = 0;
    await Promise.all(
      conns.map(async (c, ci) => {
        for (let i = 0; i < 102; i++) {
          const r = await c.publish(makeEvent(k, { content: `${ci}-${i}`, tags: [["t", tag]], created_at: base + ci * 102 + i }));
          if (r[2] === true) okCount++;
        }
      }),
    );
    check("510 events accepted", okCount === 510, `ok=${okCount}`);
    const big = await a.req("limbig", { "#t": [tag], limit: 100000 });
    check(`limit 100000 capped at ${MAX_LIMIT}`, big.events.length === MAX_LIMIT, `got ${big.events.length}`);
    check("cap hit -> EOSE carries the NIP-67 ['more'] hint", JSON.stringify(big.hint) === '["more"]', JSON.stringify(big.hint));
    const m2 = await open();
    const multi = await m2.req("limmulti", { authors: [k.pk], "#t": [tag], until: base + 300 }, { authors: [k.pk], "#t": [tag], since: base + 301 });
    const minTs = Math.min(...multi.events.map((e) => e.created_at));
    check(
      "two filters (301 + 209 events) are capped at 500 in total, with the 'more' hint",
      multi.events.length === MAX_LIMIT && JSON.stringify(multi.hint) === '["more"]',
      `got ${multi.events.length}, hint=${JSON.stringify(multi.hint)}, oldest=${minTs - base}`,
    );
    const exact = await m2.req("limexact", { authors: [k.pk], "#t": [tag], until: base + 499 });
    check("exactly 500 matches -> 500 events and a plain EOSE", exact.events.length === MAX_LIMIT && exact.hint === null, `got ${exact.events.length}, hint=${JSON.stringify(exact.hint)}`);
    m2.close();
    const noLimit = await a.req("limnone", { "#t": [tag] });
    check(`no limit -> default cap ${MAX_LIMIT} + 'more'`, noLimit.events.length === MAX_LIMIT && JSON.stringify(noLimit.hint) === '["more"]', `got ${noLimit.events.length}`);
    conns.forEach((c) => c.close());
  }


  console.log("deletion v2: tombstones, a tags, kind 5 immutability");
  {
    const m = await open();
    const v = makeEvent(alice, { content: "tomb " + uniq() });
    await m.publish(v);
    const del1 = makeEvent(alice, { kind: 5, tags: [["e", v.id]], content: "bye" });
    await m.publish(del1);
    const re = await m.publish(v);
    check("deleted event cannot be re-published (blocked)", re[2] === false && /blocked/.test(re[3]), JSON.stringify(re));

    const early = makeEvent(alice, { content: "early " + uniq() });
    await m.publish(makeEvent(alice, { kind: 5, tags: [["e", early.id]] }));
    const rb = await m.publish(early);
    check("deletion before arrival blocks the event later", rb[2] === false && /blocked/.test(rb[3]), JSON.stringify(rb));

    const theirs = makeEvent(alice, { content: "theirs " + uniq() });
    await m.publish(makeEvent(bob, { kind: 5, tags: [["e", theirs.id]] }));
    const rt = await m.publish(theirs);
    check("another key's pre-arrival deletion does not block", rt[2] === true, JSON.stringify(rt));

    await m.publish(makeEvent(alice, { kind: 5, tags: [["e", del1.id]] }));
    const qd = await m.req("k5", { ids: [del1.id] });
    check("a kind 5 deletion request cannot be deleted", qd.events.length === 1);

    for (const [kind, d] of [[30023, "x"], [10002, ""]]) {
      const k = newKey();
      const t0 = Math.floor(Date.now() / 1000) - 100;
      const tags = kind === 30023 ? [["d", d]] : [];
      const old = makeEvent(k, { kind, tags, content: "v1", created_at: t0 });
      await m.publish(old);
      const coord = `${kind}:${k.pk}:${d}`;
      const stranger = makeEvent(bob, { kind: 5, tags: [["a", coord]], created_at: t0 + 10 });
      await m.publish(stranger);
      const still = await m.req("a0" + kind, { authors: [k.pk], kinds: [kind] });
      check(`kind ${kind}: a-tag deletion by another key ignored`, still.events.length === 1);
      const delReq = makeEvent(k, { kind: 5, tags: [["a", coord]], created_at: t0 + 10 });
      const rd = await m.publish(delReq);
      const gone = await m.req("a1" + kind, { authors: [k.pk], kinds: [kind] });
      check(`kind ${kind}: a-tag deletion by owner removes versions <= request time`, rd[2] === true && gone.events.length === 0);
      const mid = makeEvent(k, { kind, tags, content: "v-mid", created_at: t0 + 5 });
      const rm = await m.publish(mid);
      check(`kind ${kind}: version older than the cutoff is blocked`, rm[2] === false && /blocked/.test(rm[3]), JSON.stringify(rm));
      const fresh = makeEvent(k, { kind, tags, content: "v-new", created_at: t0 + 20 });
      const rf = await m.publish(fresh);
      const q2 = await m.req("a2" + kind, { authors: [k.pk], kinds: [kind] });
      check(`kind ${kind}: version newer than the cutoff accepted`, rf[2] === true && q2.events.length === 1 && q2.events[0].id === fresh.id);
    }
    m.close();
  }

  console.log("validation: empty tag, empty filter lists, filter size");
  {
    const m = await open();
    const bad = await m.publish(makeEvent(alice, { content: "empty tag", tags: [[]] }));
    check("event with an empty tag [] -> OK false", bad[2] === false && /tags/.test(bad[3]), JSON.stringify(bad));
    const e1 = await m.req("empty1", { authors: [] });
    const e2 = await m.req("empty2", { kinds: [] });
    const e3 = await m.req("empty3", { ids: [] });
    const e4 = await m.req("empty4", { "#t": [], authors: [alice.pk] });
    check(
      "empty authors/kinds/ids/#t list matches nothing (EOSE, 0 events)",
      [e1, e2, e3, e4].every((r) => r.eose && r.events.length === 0),
      JSON.stringify([e1, e2, e3, e4].map((r) => r.events.length)),
    );
    const big = await m.req("big", { kinds: Array.from({ length: 100 }, (_, i) => i) });
    check("filter with 100 kinds -> CLOSED 'filter too large'", /filter too large/.test(big.closed ?? ""), String(big.closed));
    const ok90 = await m.req("big90", { kinds: Array.from({ length: 90 }, (_, i) => i + 70000) });
    check("filter with 90 values still works", ok90.eose);
    m.close();
  }

  console.log("invalid REQ drops an existing subscription (#9)");
  {
    const sub = await open();
    const pub = await open();
    const tag = uniq();
    await sub.req("dup", { kinds: [1], "#t": [tag] });
    const bad = await sub.req("dup", { ids: ["ZZ"] });
    check("invalid re-REQ of existing subId -> CLOSED", !!bad.closed);
    await pub.publish(makeEvent(alice, { content: "after close", tags: [["t", tag]] }));
    const leak = await sub.wait((m) => m[0] === "EVENT" && m[1] === "dup", 800);
    check("subscription is gone after the invalid REQ (no delivery)", !leak);
    sub.close();
    pub.close();
  }

  console.log("REQ rate limit (#3)");
  {
    const r = await open();
    const tag = uniq();
    await r.req("live-rl", { kinds: [1], "#t": [tag] });
    for (let i = 0; i < 70; i++) r.send(["REQ", "rl", { kinds: [99999], limit: 1 }]); // same subId every time
    await sleep(1500);
    const eose = r.msgs.filter((m) => m[0] === "EOSE" && m[1] === "rl").length;
    const limited = r.msgs.filter((m) => m[0] === "CLOSED" && m[1] === "rl" && /rate-limited/.test(m[2])).length;
    check("repeated REQs on one subId are rate limited (<= ~60 served)", eose >= 55 && eose <= 63 && limited >= 5, `eose=${eose} limited=${limited}`);
    r.close();
  }

  console.log("EVENT rate limit (#5)");
  {
    const r = await open();
    const evs = Array.from({ length: 140 }, (_, i) => makeEvent(alice, { kind: 20001, content: "rl" + i + uniq() }));
    for (const e of evs) r.send(["EVENT", e]);
    await sleep(2000);
    const oks = r.msgs.filter((m) => m[0] === "OK");
    const acc = oks.filter((m) => m[2] === true).length;
    const rej = oks.filter((m) => m[2] === false && /rate-limited/.test(m[3])).length;
    check("140 rapid events: ~120 accepted, rest rate-limited", oks.length === 140 && acc >= 118 && acc <= 126 && rej >= 14, `acc=${acc} rej=${rej} total=${oks.length}`);
    r.close();
  }

  console.log("result size caps (4MB) + truncation hint");
  {
    const p = await open();
    const tag = uniq();
    const content = "x".repeat(400_000);
    const now = Math.floor(Date.now() / 1000);
    for (let i = 0; i < 12; i++) await p.publish(makeEvent(alice, { content: content + i, tags: [["t", tag]], created_at: now - 1000 + i }));
    const q = await p.req("bytes", { "#t": [tag], limit: 100 });
    const total = q.events.reduce((n, e) => n + Buffer.byteLength(JSON.stringify(e)), 0);
    check(
      "12 x ~400KB events: response cut at the 4MB budget, newest first, EOSE says 'more'",
      q.eose && q.events.length >= 9 && q.events.length <= 10 && total <= 4 * 1024 * 1024 && q.events[0].content.endsWith("11") && JSON.stringify(q.hint) === '["more"]',
      `n=${q.events.length} bytes=${total} hint=${JSON.stringify(q.hint)}`,
    );
    const few = await p.req("bytes2", { "#t": [tag], limit: 5 });
    check("under the byte budget (limit 5) -> plain EOSE", few.events.length === 5 && few.hint === null);
    p.close();
  }

  console.log("event size / tag limits (A, E)");
  {
    const p = await open();
    const e = makeEvent(alice, { content: "你".repeat(180_000) }); // ~180k chars but ~540KB of UTF-8
    p.send(["EVENT", e]);
    const ok = await p.wait((m) => m[0] === "OK" && m[1] === e.id, 3000);
    check("frame > 512000 UTF-8 bytes -> OK false 'event too large' (not just a NOTICE)", !!ok && ok[2] === false && /too large/.test(ok[3]), JSON.stringify(ok));
    const mid = makeEvent(alice, { content: "你".repeat(100_000) }); // ~300KB: allowed
    const rm = await p.publish(mid);
    check("~300KB multi-byte event is accepted", rm[2] === true, JSON.stringify(rm));
    p.ws.send(JSON.stringify(["REQ", "junk", { "#t": ["z".repeat(600_000)] }]));
    const notice = await p.wait((m) => m[0] === "NOTICE" && /too large/.test(m[1]), 3000);
    check("oversized non-EVENT frame -> NOTICE", !!notice);
    const tags = Array.from({ length: 257 }, (_, i) => ["t", "v" + i]);
    const r257 = await p.publish(makeEvent(alice, { content: "257 tags", tags }));
    check("257 tags -> OK false", r257[2] === false && /tags/.test(r257[3]), JSON.stringify(r257));
    const t70 = Array.from({ length: 70 }, (_, i) => ["t", "w" + i]);
    t70.push(["t", "w0"]);
    const ev70 = makeEvent(alice, { content: "70 tags", tags: t70 });
    const r70 = await p.publish(ev70);
    const first = await p.req("idx1", { "#t": ["w0"] });
    const last = await p.req("idx2", { "#t": ["w69"] });
    const stored = await p.req("idx3", { ids: [ev70.id] });
    check(
      "tags beyond the 64 indexed ones are stored but not queryable by #t",
      r70[2] === true && first.events.length === 1 && last.events.length === 0 && stored.events[0].tags.length === 71,
      `first=${first.events.length} last=${last.events.length}`,
    );
    p.close();
  }

  console.log("deletion limits: 100 targets, ephemeral tombstones (E, H)");
  {
    const p = await open();
    const ids = Array.from({ length: 105 }, () => bytesToHex(schnorr.utils.randomPrivateKey()));
    const req = makeEvent(alice, { kind: 5, tags: ids.map((i) => ["e", i]) });
    const r = await p.publish(req);
    check("kind 5 with 105 targets -> OK true + 'ignored' note", r[2] === true && /ignored/.test(r[3]), JSON.stringify(r));
    // first 100 are tombstoned, the last 5 are not
    const dead = makeEvent(alice, { kind: 20001, content: "eph " + uniq() });
    await p.publish(makeEvent(alice, { kind: 5, tags: [["e", dead.id]] }));
    const rd = await p.publish(dead);
    check("ephemeral event with a tombstone is not broadcast (blocked)", rd[2] === false && /blocked/.test(rd[3]), JSON.stringify(rd));
    p.close();
  }

  console.log("filter handling: unknown fields, subscription size caps (F)");
  {
    const p = await open();
    const note2 = makeEvent(alice, { content: "for search " + uniq() });
    await p.publish(note2);
    const srch = await p.req("search", { ids: [note2.id], search: "anything" });
    check("unknown filter field (NIP-50 search) is ignored, not an error", srch.eose && srch.events.length === 1);
    const big = await p.req("bigsub", { "#t": ["y".repeat(5000)] });
    check("subscription over 4KB -> CLOSED 'filter too large'", /filter too large/.test(big.closed ?? ""), String(big.closed));
    let closedAt = 0;
    for (let i = 1; i <= 6; i++) {
      const r = await p.req("sz" + i, { "#t": ["y".repeat(3500) + i] });
      if (r.closed && !closedAt) closedAt = i;
    }
    check("16KB of subscriptions per connection: the 5th ~3.5KB subscription is refused", closedAt === 5, `first refused: ${closedAt}`);
    p.close();
  }

  console.log("per-source limits and frame bucket (G)");
  {
    const ip = freshIp();
    const held = [];
    for (let i = 0; i < 8; i++) held.push(await open(ip));
    let ninth = "connected";
    try {
      (await open(ip)).close();
    } catch {
      ninth = "refused";
    }
    check("9th concurrent connection from one address is refused (limit 8)", ninth === "refused");
    // totals for one address are shared across its connections
    const evs = Array.from({ length: 30 }, (_, i) => makeEvent(alice, { kind: 20001, content: "src" + i + uniq() }));
    held.slice(0, 3).forEach((c, ci) => evs.slice(ci * 10, ci * 10 + 10).forEach((e) => c.send(["EVENT", e])));
    await sleep(600);
    const okN = held.slice(0, 3).reduce((n, c) => n + c.msgs.filter((m) => m[0] === "OK" && m[2] === true).length, 0);
    check("events from several connections of one address are accepted below the per-address budget", okN === 30, `ok=${okN}`);
    held.forEach((c) => c.close());
    await sleep(300);

    const ip2 = freshIp();
    let opened = 0;
    let refused = 0;
    for (let i = 0; i < 12; i++) {
      try {
        (await open(ip2)).close();
        opened++;
      } catch {
        refused++;
      }
      await sleep(60);
    }
    check("connection attempts are limited to 10/minute per address", opened === 10 && refused === 2, `opened=${opened} refused=${refused}`);

    const f = await open();
    for (let i = 0; i < 400; i++) f.send(["CLOSE", "nothing"]);
    await sleep(1500);
    check("a flood of 400 CLOSE frames is rate limited and the connection is closed with 1008", f.closeCode === 1008, `closeCode=${f.closeCode}`);
  }
  console.log("protocol errors");
  {
    const bad = await a.req("badf", { ids: ["XYZ"] });
    check("invalid filter -> CLOSED", !!bad.closed && /invalid/.test(bad.closed), String(bad.closed));
    a.send(["REQ", "nofilter"]);
    const c = await a.wait((m) => m[0] === "CLOSED" && m[1] === "nofilter");
    check("REQ without filters -> CLOSED", !!c);
    a.ws.send("not json");
    const n = await a.wait((m) => m[0] === "NOTICE");
    check("invalid JSON -> NOTICE", !!n);
  }

  a.close();
  console.log(`\n${passed} passed, ${failed} failed`);
  process.exit(failed ? 1 : 0);
}


async function runSeed() {
  console.log("seed");
  const k = newKey();
  const c = await open();
  const t0 = Math.floor(Date.now() / 1000) - 100;
  const rep = makeEvent(k, { kind: 10002, content: "v1", created_at: t0 });
  const note = makeEvent(k, { content: "seed note " + uniq(), created_at: t0 });
  const r1 = await c.publish(rep);
  const r2 = await c.publish(note);
  check("seeded replaceable event + note", r1[2] === true && r2[2] === true);
  writeFileSync(SEED_FILE, JSON.stringify({ sk: bytesToHex(k.sk), pk: k.pk, repId: rep.id, noteId: note.id, t0 }));
  c.close();
  console.log(`\n${passed} passed, ${failed} failed`);
  process.exit(failed ? 1 : 0);
}

async function runFanout(alice) {
  console.log("fan-out budgets (relay with LIMITS_JSON maxDeliveriesPerEvent=2, socketOutboundBytesPerMinute=3000)");
  const cs = [];
  for (let i = 0; i < 6; i++) cs.push(await open());
  const c = cs[3];
  // fan-out budget: at most 2 deliveries per event (three subscribers on cs[0..2])
  for (const s of cs.slice(0, 3)) s.send(["REQ", "eph", { kinds: [20001] }]);
  await sleep(400);
  const pub = makeEvent(alice, { kind: 20001, content: "fanout " + uniq() });
  await c.publish(pub);
  await sleep(600);
  const got = cs.slice(0, 3).filter((s) => s.msgs.some((m) => m[0] === "EVENT" && m[2]?.id === pub.id)).length;
  check("one event is delivered to at most maxDeliveriesPerEvent (2) of 3 subscribers", got === 2, `delivered to ${got}`);

  // per-socket outbound quota: 3000 bytes/minute
  const slow = cs[4];
  slow.send(["REQ", "eph", { kinds: [20001] }]);
  await sleep(400);
  for (const s of cs.slice(0, 3)) s.send(["CLOSE", "eph"]);
  await sleep(300);
  for (let i = 0; i < 4; i++) await c.publish(makeEvent(alice, { kind: 20001, content: "q".repeat(1200) + i + uniq() }));
  await sleep(800);
  const received = slow.msgs.filter((m) => m[0] === "EVENT").length;
  check("slow consumer over its outbound byte quota is closed with 1013", slow.closeCode === 1013 && received >= 1 && received <= 2, `closeCode=${slow.closeCode} received=${received}`);
  cs.forEach((x) => x.close());
  console.log(`\n${passed} passed, ${failed} failed`);
  process.exit(failed ? 1 : 0);
}

async function runCapped(alice) {
  console.log("caps (relay restarted with tiny limits on the seeded state)");
  const seed = JSON.parse(readFileSync(CAPPED_FILE, "utf8"));
  const K = { sk: hexToBytes(seed.sk), pk: seed.pk };
  const cs = [];
  for (let i = 0; i < 6; i++) cs.push(await open());
  let seventh = "connected";
  try {
    (await open()).close();
  } catch {
    seventh = "refused";
  }
  check("connection beyond maxConnections (6) is refused with 503", seventh === "refused");
  const c = cs[3];

  const full = await c.publish(makeEvent(alice, { content: "no room " + uniq() }));
  check("new regular event when storage is full -> OK false 'storage is full'", full[2] === false && /storage is full/.test(full[3]), JSON.stringify(full));
  const fresh = await c.publish(makeEvent(newKey(), { kind: 10002, content: "" }));
  check("brand-new replaceable coordinate when storage is full -> OK false", fresh[2] === false && /storage is full/.test(fresh[3]), JSON.stringify(fresh));
  const newAddr = await c.publish(makeEvent(newKey(), { kind: 30078, tags: [["d", "x"]] }));
  check("brand-new addressable coordinate when storage is full -> OK false", newAddr[2] === false && /storage is full/.test(newAddr[3]), JSON.stringify(newAddr));
  const v2 = makeEvent(K, { kind: 10002, content: "v2", created_at: seed.t0 + 50 });
  const rv2 = await c.publish(v2);
  // (No REQ here: new subscription rows are refused too while the database is "full".)
  check("a real replacement (old version removed) is exempt from the cap", rv2[2] === true, JSON.stringify(rv2));
  const dup = await c.publish(v2);
  check("duplicate stays an idempotent success when full", dup[2] === true && /duplicate/.test(dup[3]), JSON.stringify(dup));
  const eph = await c.publish(makeEvent(alice, { kind: 20001, content: "eph" }));
  check("ephemeral event unaffected by full storage", eph[2] === true);
  const sub = await c.req("newsub", { kinds: [1] });
  check("new subscription row refused when storage is full", /storage is full/.test(sub.closed ?? ""), String(sub.closed));

  const del0 = await c.publish(makeEvent(alice, { kind: 5, tags: [["e", "a".repeat(64)]] }));
  check("kind 5 that deletes nothing is refused when storage is full", del0[2] === false && /storage is full/.test(del0[3]), JSON.stringify(del0));
  const del1 = await c.publish(makeEvent(K, { kind: 5, tags: [["e", seed.noteId]] }));
  check("kind 5 that frees an event is accepted when full", del1[2] === true, JSON.stringify(del1));
  const del2 = await c.publish(makeEvent(K, { kind: 5, tags: [["e", "b".repeat(64)]] }));
  check("tombstone table cap (1) reached -> new kind 5 refused", del2[2] === false && /deletion records are full/.test(del2[3]), JSON.stringify(del2));

  cs.forEach((x) => x.close());
  console.log(`\n${passed} passed, ${failed} failed`);
  process.exit(failed ? 1 : 0);
}

main().catch((e) => {
  console.error("e2e crashed:", e);
  process.exit(2);
});

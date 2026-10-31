#!/usr/bin/env node
// End-to-end checks for the relay worker.
// The suite is not idempotent: start the relay on fresh state (wrangler --persist-to <new dir>) for every run.
//   node test/e2e/run.mjs [ws://127.0.0.1:8787] [--src-header=NAME] [--seed FILE | --capped FILE]
// Each connection pretends to come from its own address by sending it in the source header
// (CF-Connecting-IP by default; start wrangler with `--var SRC_HEADER:x-e2e-src` and pass
// --src-header=x-e2e-src if the dev server overwrites CF-Connecting-IP).
// --seed FILE   : (normal relay) store a replaceable event and a note, write their keys to FILE.
// --capped FILE : (fresh relay state seeded with --seed, restarted with tiny caps) run the cap checks:
//   --var 'LIMITS_JSON:{"maxStorageBytes":1,"maxConnections":6}'
// --fanout      : (fresh relay with small budgets) delivery budgets, deletion-table eviction and write
//   budget, global-budget fairness:
//   --var 'LIMITS_JSON:{"maxDeliveriesPerEvent":2,"socketOutboundBytesPerMinute":3000,"sourceOutboundBytesPerMinute":4000,"maxOverQuotaWindows":2,"quotaWindowMs":700,"maxTombstones":3,"maxCoordCutoffs":3,"maxDeletionRowWrites":30,"globalEventWeightPerMinute":200,"maxViolations":5}'
import { readFileSync, writeFileSync } from "node:fs";
import { schnorr } from "@noble/curves/secp256k1";
import { bytesToHex, hexToBytes } from "@noble/hashes/utils";
import {
  ARGS, CAPPED_FILE, HTTP_URL, MAX_LIMIT, SEED_FILE, WS_URL,
  check, finish, freshIp, makeEvent, newKey, nextCounter, open, sleep, uniq,
} from "./lib.mjs";

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
    const p2 = await open(); // the first pull used up this socket's outbound budget, so ask on a new one
    const few = await p2.req("bytes2", { "#t": [tag], limit: 5 });
    p2.close();
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
    const xx = await p.req("xx", { "#xx": ["a"] });
    check("a multi-character tag filter is rejected, not ignored", /unsupported tag filter/.test(xx.closed ?? ""), String(xx.closed));
    const big = await p.req("bigsub", { "#t": ["y".repeat(5000)] });
    check("subscription over 4KB -> CLOSED 'filter too large'", /filter too large/.test(big.closed ?? ""), String(big.closed));
    let closedAt = 0;
    for (let i = 1; i <= 6; i++) {
      const r = await p.req("sz" + i, { "#t": ["y".repeat(3500) + i] });
      if (r.closed && !closedAt) closedAt = i;
    }
    check("16KB of subscriptions per connection: the 5th ~3.5KB subscription is refused", closedAt === 5, `first refused: ${closedAt}`);
    p.close();
    // F6: quotas count UTF-8 bytes. 1190 CJK chars = 3570 bytes each: 5 of them are ~18KB but only ~6k characters.
    const q = await open();
    let cjkRefused = 0;
    for (let i = 1; i <= 5; i++) {
      const r = await q.req("cjk" + i, { "#t": ["你".repeat(1190) + i] });
      if (r.closed && !cjkRefused) cjkRefused = i;
    }
    check("5 CJK subscriptions (~18KB of UTF-8, ~6k chars) exceed the 16KB cap: the 5th is refused", cjkRefused === 5, `first refused: ${cjkRefused}`);
    q.close();
  }

  console.log("global budget is charged after validation (H1)");
  {
    const atk = await open();
    // ~510KB frame with ~170k empty tags: weight would be ~10k if charged before validation
    const junk = '["EVENT",{"id":"' + "a".repeat(64) + '","pubkey":"' + alice.pk + '","created_at":1,"kind":1,"content":"","sig":"' + "b".repeat(128) + '","tags":[' + "[],".repeat(170000) + "[]]}]";
    let rejected = 0;
    for (let i = 0; i < 5; i++) {
      atk.ws.send(junk);
      const ok = await atk.wait((m) => m[0] === "OK", 4000);
      if (ok && ok[2] === false && /bad tags/.test(ok[3])) rejected++;
    }
    check("5 junk frames with 170k [] tags are all rejected as 'bad tags' (not rate-limited)", rejected === 5, `rejected=${rejected}`);
    const honest = await open();
    const r = await honest.publish(makeEvent(alice, { kind: 20001, content: "honest after junk " + uniq() }));
    check("an honest event from another address still passes (global budget not drained)", r[2] === true, JSON.stringify(r));
    atk.close();
    honest.close();
  }

  console.log("per-source stored-bytes quota (F3b)");
  {
    const w = await open();
    const big = "x".repeat(400_000);
    const t = Math.floor(Date.now() / 1000) - 100_000;
    let acc = 0;
    let lim = 0;
    for (let i = 0; i < 24; i++) {
      const r = await w.publish(makeEvent(bob, { content: big + i, created_at: t + i }));
      if (r[2] === true) acc++;
      else if (/rate-limited/.test(r[3])) lim++;
    }
    check("24 x 400KB from one address: stops around the 8MB/min quota", acc >= 19 && acc <= 23 && lim >= 1 && acc + lim === 24, `accepted=${acc} limited=${lim}`);
    const lim2 = (await (await fetch(HTTP_URL, { headers: { Accept: "application/nostr+json" } })).json()).limitation;
    check("NIP-11 advertises write_bytes_per_minute_per_ip", lim2.write_bytes_per_minute_per_ip === 8 * 1024 * 1024);
    w.close();
  }

  console.log("a source cannot use the global event weight alone (ephemeral events cost no stored bytes)");
  {
    const flood = await open();
    const body = "e".repeat(480_000);
    let acc = 0;
    let lim = 0;
    for (let i = 0; i < 80; i++) {
      const r = await flood.publish(makeEvent(bob, { kind: 20001, content: body + i }));
      if (r[2] === true) acc++;
      else if (/rate-limited/.test(r[3])) lim++;
    }
    check("80 x 480KB ephemeral events from one address: about 7500 weight worth is accepted, the rest refused", acc >= 55 && acc <= 75 && lim >= 5 && acc + lim === 80, `accepted=${acc} limited=${lim}`);
    const dense = await open();
    const spread = () => Array.from({ length: 256 }, () => Array(16).fill("")); // 4096 empty strings, ~13KB
    let dAcc = 0;
    let dLim = 0;
    for (let i = 0; i < 80; i++) {
      const r = await dense.publish(makeEvent(bob, { kind: 20001, content: "dense" + i, tags: spread() }));
      if (r[2] === true) dAcc++;
      else if (/rate-limited/.test(r[3])) dLim++;
    }
    check("80 tag-dense ephemeral events (weight ~132 each) from one address: it is stopped near 7500 weight, not at 80", dAcc >= 45 && dAcc <= 65 && dLim >= 15 && dAcc + dLim === 80, `accepted=${dAcc} limited=${dLim}`);
    dense.close();
    const other = await open();
    const r = await other.publish(makeEvent(alice, { kind: 20001, content: "still room " + uniq() }));
    check("another address is not affected (the global budget is not drained)", r[2] === true, JSON.stringify(r));
    flood.close();
    other.close();
  }

  console.log("history is outbound traffic: it is charged to the socket budget and cut short with 'more'");
  {
    const pub = await open();
    const tag = uniq();
    const body = "h".repeat(400_000);
    const t0 = Math.floor(Date.now() / 1000) - 5000;
    for (let i = 0; i < 6; i++) await pub.publish(makeEvent(alice, { content: body + i, tags: [["t", tag]], created_at: t0 + i }));
    const reader = await open();
    const r1 = await reader.req("h1", { "#t": [tag] });
    const r2 = await reader.req("h2", { "#t": [tag] });
    const r3 = await reader.req("h3", { "#t": [tag] });
    check("first pull of ~2.4MB of history is complete (plain EOSE)", r1.events.length === 6 && r1.hint === null, `n=${r1.events.length} hint=${JSON.stringify(r1.hint)}`);
    check("second pull on the same socket runs into the 4MB/min socket budget: fewer events and the 'more' hint", r2.events.length > 0 && r2.events.length < 6 && JSON.stringify(r2.hint) === '["more"]', `n=${r2.events.length} hint=${JSON.stringify(r2.hint)}`);
    check("third pull gets (almost) nothing and says 'more'", r3.events.length <= 1 && JSON.stringify(r3.hint) === '["more"]', `n=${r3.events.length}`);
    const live = makeEvent(alice, { kind: 20001, tags: [["t", tag]], content: "live after history " + uniq() });
    await pub.publish(live);
    const got = await reader.wait((m) => m[0] === "EVENT" && m[2]?.id === live.id, 3000);
    check("after its history pulls were cut short, the same socket still receives a matching live event", !!got);
    const fresh = await open();
    const f1 = await fresh.req("f1", { "#t": [tag] });
    check("a new socket of another address is not affected", f1.events.length === 6 && f1.hint === null);
    pub.close();
    reader.close();
    fresh.close();
  }

  console.log("long d values can be deleted by coordinate");
  {
    const p = await open();
    const d = "d".repeat(954);
    const t = Math.floor(Date.now() / 1000) - 100;
    const ev = makeEvent(alice, { kind: 30078, tags: [["d", d]], content: "long d", created_at: t });
    const r1 = await p.publish(ev);
    const del = await p.publish(makeEvent(alice, { kind: 5, tags: [["a", `30078:${alice.pk}:${d}`]], created_at: t + 1 }));
    const gone = await p.req("ld", { ids: [ev.id] });
    check("30078 with a 954-byte d is stored and its 1025-byte a coordinate deletes it", r1[2] === true && del[2] === true && gone.events.length === 0, JSON.stringify([r1, del]));
    p.close();
  }

  console.log("IPv6 /64 grouping and upgrade tokens (F1, F2)");
  {
    const lim = (await (await fetch(HTTP_URL, { headers: { Accept: "application/nostr+json" } })).json()).limitation;
    const MAXC = lim.max_connections_per_ip;
    const pfx = `2001:db8:${(nextCounter() & 0xffff).toString(16)}:${Math.floor(Math.random() * 0xffff).toString(16)}`;
    const held = [];
    for (let i = 0; i < MAXC; i++) held.push(await open(`${pfx}::${(i + 1).toString(16)}`));
    let over = "connected";
    try {
      (await open(`${pfx}:0:0:0:ffff`)).close();
    } catch {
      over = "refused";
    }
    check(`${MAXC} different addresses of one /64 share the ${MAXC}-connection limit; the next one is refused`, over === "refused");
    // Refused attempts must not burn upgrade tokens: 45 refusals + the 32 held would exceed 60 if they did.
    let refused = 0;
    for (let i = 0; i < 45; i++) {
      try {
        (await open(`${pfx}::${(0x100 + i).toString(16)}`)).close();
      } catch {
        refused++;
      }
    }
    held.pop().close();
    await sleep(300);
    let reopened = "failed";
    try {
      (await open(`${pfx}::abcd`)).close();
      reopened = "ok";
    } catch {}
    check("refused-by-concurrency attempts do not consume upgrade tokens (reconnect works after one slot frees)", refused === 45 && reopened === "ok", `refused=${refused} reopened=${reopened}`);
    held.forEach((c) => c.close());
    await sleep(300);
  }

  console.log("per-source limits and frame bucket (G)");
  {
    const lim = (await (await fetch(HTTP_URL, { headers: { Accept: "application/nostr+json" } })).json()).limitation;
    const MAXC = lim.max_connections_per_ip;
    const UPM = lim.upgrades_per_minute_per_ip;
    const ip = freshIp();
    const held = [];
    for (let i = 0; i < MAXC; i++) held.push(await open(ip));
    let ninth = "connected";
    try {
      (await open(ip)).close();
    } catch {
      ninth = "refused";
    }
    check(`connection ${MAXC + 1} from one address is refused (limit ${MAXC})`, ninth === "refused");
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
    for (let i = 0; i < UPM + 15; i++) {
      try {
        (await open(ip2)).close();
        opened++;
      } catch {
        refused++;
      }
    }
    // The bucket refills continuously (UPM per minute), so a few extra attempts may squeeze through.
    check(`connection attempts are limited to ${UPM}/minute per address`, opened >= UPM && opened <= UPM + 5 && refused >= 10, `opened=${opened} refused=${refused}`);

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
  finish();
}


async function runSeed() {
  console.log("seed");
  const k = newKey();
  const k2 = newKey();
  const c = await open();
  const t0 = Math.floor(Date.now() / 1000) - 100;
  const rep = makeEvent(k, { kind: 10002, content: "v1", created_at: t0 });
  const noteBig = makeEvent(k, { content: "seed note " + "n".repeat(5000), created_at: t0 });
  const noteSmall = makeEvent(k, { content: "s", created_at: t0 });
  const bigRep = makeEvent(k2, { kind: 10002, content: "L".repeat(20000), created_at: t0 });
  const rs = [];
  for (const e of [rep, noteBig, noteSmall, bigRep]) rs.push(await c.publish(e));
  check("seeded replaceable events and notes", rs.every((r) => r[2] === true));
  writeFileSync(SEED_FILE, JSON.stringify({ sk: bytesToHex(k.sk), pk: k.pk, sk2: bytesToHex(k2.sk), pk2: k2.pk, t0, noteBig, noteSmall }));
  c.close();
  finish();
}

async function runFanout(alice) {
  console.log("fan-out budgets, deletion eviction, global fairness (relay with the small LIMITS_JSON from the header)");
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
  for (const s of cs.slice(0, 3)) s.send(["CLOSE", "eph"]);
  await sleep(300);

  // R4: per-socket quota drops deliveries first; only a socket that stays over quota is closed.
  const slow = cs[4];
  slow.send(["REQ", "eph", { kinds: [20001] }]);
  await sleep(400);
  const burst = async (tag) => {
    for (let i = 0; i < 3; i++) await c.publish(makeEvent(alice, { kind: 20001, content: "q".repeat(900) + tag + i + uniq() }));
    await sleep(300);
  };
  await burst("a");
  const recv1 = slow.msgs.filter((m) => m[0] === "EVENT").length;
  check("over its socket quota the consumer only loses deliveries (2 of 3 delivered, still connected)", recv1 === 2 && slow.closeCode === null, `received=${recv1} closeCode=${slow.closeCode}`);
  // A drop only counts toward "sustained" when the gap since the previous drop is under one window (700ms), so
  // keep each gap near 600ms (300 inside burst + 300 here) and stay over quota for more than (N-1) windows in all.
  await sleep(300);
  await burst("b");
  await sleep(300);
  await burst("c");
  check("still over quota in a later window -> closed with 1013", slow.closeCode === 1013, `closeCode=${slow.closeCode}`);

  // R4: one source cannot use more than its share of the outbound budget (4000 bytes/min here).
  const ipC = freshIp();
  const sA = await open(ipC);
  const sB = await open(ipC);
  for (const x of [sA, sB]) x.send(["REQ", "eph", { kinds: [20001] }]);
  await sleep(400);
  for (let i = 0; i < 3; i++) await c.publish(makeEvent(alice, { kind: 20001, content: "s".repeat(900) + i + uniq() }));
  await sleep(500);
  const tot = [sA, sB].reduce((n, x) => n + x.msgs.filter((m) => m[0] === "EVENT").length, 0);
  check("two sockets of one source together receive at most its 4000-byte share (3 frames)", tot === 3 && sA.closeCode === null && sB.closeCode === null, `delivered=${tot}`);
  sA.close();
  sB.close();

  // F3c: tombstone table cap = 3 evicts the oldest records, deletions keep working.
  const p = await open();
  const evs = Array.from({ length: 5 }, (_, i) => makeEvent(alice, { content: "ev" + i + uniq() }));
  for (const e of evs) await p.publish(e);
  const dels = [];
  for (const e of evs) dels.push(await p.publish(makeEvent(alice, { kind: 5, tags: [["e", e.id]] })));
  check("5 deletions with the tombstone cap at 3: none is refused", dels.every((r) => r[2] === true), JSON.stringify(dels.map((r) => r[3])));
  const r4 = await p.publish(evs[4]);
  const r2 = await p.publish(evs[2]);
  check("newest tombstones (events 2..4) still block re-publishing", r4[2] === false && /blocked/.test(r4[3]) && r2[2] === false && /blocked/.test(r2[3]));
  const r0 = await p.publish(evs[0]);
  const r1 = await p.publish(evs[1]);
  check("oldest tombstones (events 0,1) were evicted: those events are accepted again", r0[2] === true && r1[2] === true, JSON.stringify([r0, r1]));

  // coord_cutoffs cap = 3 as well
  const k = newKey();
  const t0 = Math.floor(Date.now() / 1000) - 1000;
  for (let i = 1; i <= 4; i++) {
    const tags = [["d", "c" + i]];
    await p.publish(makeEvent(k, { kind: 30023, tags, content: "v", created_at: t0 }));
    const r = await p.publish(makeEvent(k, { kind: 5, tags: [["a", `30023:${k.pk}:c${i}`]], created_at: t0 + 10 }));
    if (r[2] !== true) check(`a-tag deletion ${i} accepted`, false, JSON.stringify(r));
  }
  const o1 = await p.publish(makeEvent(k, { kind: 30023, tags: [["d", "c1"]], content: "again", created_at: t0 + 5 }));
  const o4 = await p.publish(makeEvent(k, { kind: 30023, tags: [["d", "c4"]], content: "again", created_at: t0 + 5 }));
  const o2 = await p.publish(makeEvent(k, { kind: 30023, tags: [["d", "c2"]], content: "again", created_at: t0 + 5 }));
  check(
    "coordinate cutoffs: oldest (c1) evicted, newest (c2..c4) still enforced",
    o1[2] === true && o4[2] === false && /blocked/.test(o4[3]) && o2[2] === false,
    JSON.stringify([o1[3], o4[3], o2[3]]),
  );

  // F5: write budget 30 with cap 3 -> a 20-target request processes only the first 5 targets
  const us = Array.from({ length: 20 }, (_, i) => makeEvent(alice, { content: "u" + i + uniq() }));
  const big = await p.publish(makeEvent(alice, { kind: 5, tags: us.map((u) => ["e", u.id]) }));
  check("kind 5 with 20 targets and a 30-write budget -> OK true with an 'ignored' note", big[2] === true && /ignored/.test(big[3]), JSON.stringify(big));
  const blocked = [];
  for (const i of [4, 3, 2]) blocked.push((await p.publish(us[i]))[2] === false);
  const free = [];
  for (const i of [1, 0, 5, 12]) free.push((await p.publish(us[i]))[2] === true);
  check(
    "exactly the cap (3) newest of the 5 processed targets are tombstoned; evicted and unprocessed targets are not",
    blocked.every(Boolean) && free.every(Boolean),
    JSON.stringify({ blocked, free }),
  );
  p.close();

  // F3a: a drained global budget is not the connection's fault (maxViolations is 5 here)
  const drainers = [await open(), await open(), await open()];
  for (const d of drainers) for (let i = 0; i < 90; i++) d.send(["EVENT", makeEvent(alice, { kind: 20001, content: "drain" + i + uniq() })]);
  await drainers[2].wait((m) => m[0] === "OK" && /relay is busy/.test(m[3] ?? ""), 5000);
  const honest = await open();
  const results = [];
  for (let i = 0; i < 12; i++) results.push(await honest.publish(makeEvent(bobKey(), { kind: 20001, content: "h" + i + uniq() })));
  const busy = results.filter((r) => r[2] === false && /relay is busy/.test(r[3])).length;
  await sleep(300);
  check("global budget drained: honest events get 'relay is busy' (>= 8 of 12) but the connection is not closed", busy >= 8 && honest.closeCode === null, `busy=${busy} closeCode=${honest.closeCode}`);
  drainers.forEach((d) => d.close());
  honest.close();
  cs.forEach((x) => x.close());
  finish();
}

const bobKey = (() => {
  const k = newKey();
  return () => k;
})();

async function runCapped(alice) {
  console.log("caps (relay restarted with tiny limits on the seeded state)");
  const seed = JSON.parse(readFileSync(CAPPED_FILE, "utf8"));
  const K = { sk: hexToBytes(seed.sk), pk: seed.pk };
  const K2 = { sk: hexToBytes(seed.sk2), pk: seed.pk2 };
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
  // (No REQ here: new subscription rows are refused too while the database is "full".)
  const v2 = makeEvent(K, { kind: 10002, content: "v2", created_at: seed.t0 + 50 });
  const rv2 = await c.publish(v2);
  check("equal-size replacement (net growth 0) is allowed when full", rv2[2] === true, JSON.stringify(rv2));
  const v3 = makeEvent(K, { kind: 10002, content: "B".repeat(20000), created_at: seed.t0 + 60 });
  const rv3 = await c.publish(v3);
  check("small -> large replacement is refused when full (F4)", rv3[2] === false && /storage is full/.test(rv3[3]), JSON.stringify(rv3));
  const small2 = makeEvent(K2, { kind: 10002, content: "", created_at: seed.t0 + 10 });
  const rs2 = await c.publish(small2);
  check("large -> small replacement is allowed when full (F4)", rs2[2] === true, JSON.stringify(rs2));
  const dup = await c.publish(v2);
  check("duplicate stays an idempotent success when full", dup[2] === true && /duplicate/.test(dup[3]), JSON.stringify(dup));
  const eph = await c.publish(makeEvent(alice, { kind: 20001, content: "eph" }));
  check("ephemeral event unaffected by full storage", eph[2] === true);
  const sub = await c.req("newsub", { kinds: [1] });
  check("new subscription row refused when storage is full", /storage is full/.test(sub.closed ?? ""), String(sub.closed));

  const del0 = await c.publish(makeEvent(alice, { kind: 5, tags: [["e", "a".repeat(64)]] }));
  check("kind 5 that deletes nothing is refused when storage is full", del0[2] === false && /storage is full/.test(del0[3]), JSON.stringify(del0));
  const delSmall = await c.publish(makeEvent(K, { kind: 5, tags: [["e", seed.noteSmall.id]] }));
  check("kind 5 that frees less than it adds (tiny note) is refused when full (F4)", delSmall[2] === false && /storage is full/.test(delSmall[3]), JSON.stringify(delSmall));
  const delBig = await c.publish(makeEvent(K, { kind: 5, tags: [["e", seed.noteBig.id]] }));
  check("kind 5 that frees more than it adds (5KB note) is accepted when full (F4)", delBig[2] === true, JSON.stringify(delBig));
  const again = await c.publish(seed.noteBig);
  check("the deleted note stays blocked", again[2] === false && /blocked/.test(again[3]), JSON.stringify(again));
  cs.forEach((x) => x.close());
  finish();
}

main().catch((e) => {
  console.error("e2e crashed:", e);
  process.exit(2);
});

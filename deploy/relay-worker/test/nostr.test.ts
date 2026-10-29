import { describe, expect, it } from "vitest";
import { schnorr } from "@noble/curves/secp256k1";
import { bytesToHex } from "@noble/hashes/utils";
import { LIMITS } from "../src/config";
import { filterIsEmpty, matchAny, matchFilter, validateFilter } from "../src/filter";
import { dTag, eventId, isAddressable, isEphemeral, isReplaceable, utf8Length, validateEvent } from "../src/nostr";
import { NOW, alice, bob, signed } from "./helpers/keys";

describe("validateEvent", () => {
  it("accepts a valid signed event", () => {
    expect(validateEvent(signed(alice), NOW)).toBeNull();
  });

  it("accepts events with tags and unicode content", () => {
    const e = signed(alice, { tags: [["e", "ab"], ["p", bob.pk, "wss://x"]], content: "你好 \n\"q\"" });
    expect(validateEvent(e, NOW)).toBeNull();
  });

  it("rejects non-objects", () => {
    for (const v of [null, undefined, 1, "x"]) expect(validateEvent(v, NOW)).toMatch(/must be an object/);
  });

  it("rejects id that does not match content", () => {
    const e = signed(alice);
    e.content = "tampered";
    expect(validateEvent(e, NOW)).toBe("invalid: id does not match event content");
  });

  it("rejects a well-formed but wrong id (recomputed id check)", () => {
    const e = signed(alice);
    e.id = "0".repeat(64);
    expect(validateEvent(e, NOW)).toBe("invalid: id does not match event content");
  });

  it("rejects a bad signature (signed by another key)", () => {
    const e = signed(alice);
    const other = signed(bob, { content: "other" });
    // valid-looking sig, wrong message/key
    e.sig = other.sig;
    expect(validateEvent(e, NOW)).toBe("invalid: bad signature");
  });

  it("rejects a signature by a different key over the right id", () => {
    const e = signed(alice);
    e.sig = bytesToHex(schnorr.sign(e.id, bob.sk));
    expect(validateEvent(e, NOW)).toBe("invalid: bad signature");
  });

  it("rejects an all-zero signature without throwing", () => {
    const e = signed(alice);
    e.sig = "0".repeat(128);
    expect(validateEvent(e, NOW)).toBe("invalid: bad signature");
  });

  it("rejects a pubkey that is not a curve point without throwing", () => {
    const e = signed(alice);
    e.pubkey = "f".repeat(64);
    e.id = eventId(e);
    expect(validateEvent(e, NOW)).toBe("invalid: bad signature");
  });

  it("rejects created_at too far in the future, accepts the boundary", () => {
    const future = signed(alice, { created_at: NOW + LIMITS.maxFutureSeconds + 1 });
    expect(validateEvent(future, NOW)).toBe("invalid: created_at too far in the future");
    const edge = signed(alice, { created_at: NOW + LIMITS.maxFutureSeconds });
    expect(validateEvent(edge, NOW)).toBeNull();
  });

  it("accepts old events", () => {
    expect(validateEvent(signed(alice, { created_at: 1 }), NOW)).toBeNull();
  });

  describe("malformed fields", () => {
    const mut = (fn: (e: Record<string, unknown>) => void) => {
      const e = { ...signed(alice) } as Record<string, unknown>;
      fn(e);
      return validateEvent(e, NOW);
    };
    it("id", () => {
      expect(mut((e) => (e.id = "ABC"))).toBe("invalid: bad id");
      expect(mut((e) => (e.id = "A".repeat(64)))).toBe("invalid: bad id"); // uppercase
      expect(mut((e) => (e.id = 5))).toBe("invalid: bad id");
      expect(mut((e) => delete e.id)).toBe("invalid: bad id");
    });
    it("pubkey", () => {
      expect(mut((e) => (e.pubkey = "zz"))).toBe("invalid: bad pubkey");
      expect(mut((e) => (e.pubkey = null))).toBe("invalid: bad pubkey");
    });
    it("sig", () => {
      expect(mut((e) => (e.sig = "ab"))).toBe("invalid: bad sig");
      expect(mut((e) => (e.sig = "g".repeat(128)))).toBe("invalid: bad sig");
    });
    it("created_at", () => {
      expect(mut((e) => (e.created_at = -1))).toBe("invalid: bad created_at");
      expect(mut((e) => (e.created_at = 1.5))).toBe("invalid: bad created_at");
      expect(mut((e) => (e.created_at = "1"))).toBe("invalid: bad created_at");
      expect(mut((e) => (e.created_at = NaN))).toBe("invalid: bad created_at");
    });
    it("kind", () => {
      expect(mut((e) => (e.kind = -1))).toBe("invalid: bad kind");
      expect(mut((e) => (e.kind = 65536))).toBe("invalid: bad kind");
      expect(mut((e) => (e.kind = 1.2))).toBe("invalid: bad kind");
      expect(mut((e) => (e.kind = "1"))).toBe("invalid: bad kind");
    });
    it("content", () => {
      expect(mut((e) => (e.content = 1))).toBe("invalid: bad content");
      expect(mut((e) => delete e.content)).toBe("invalid: bad content");
    });
    it("tags", () => {
      expect(mut((e) => (e.tags = "x"))).toBe("invalid: bad tags");
      expect(mut((e) => (e.tags = [["e", 1]]))).toBe("invalid: bad tags");
      expect(mut((e) => (e.tags = ["e"]))).toBe("invalid: bad tags");
      expect(mut((e) => (e.tags = [null]))).toBe("invalid: bad tags");
      expect(mut((e) => (e.tags = Array.from({ length: LIMITS.maxEventTags + 1 }, () => ["t"])))).toBe("invalid: bad tags");
    });
  });
});

describe("kind classes", () => {
  it("ephemeral is [20000, 30000)", () => {
    expect(isEphemeral(19999)).toBe(false);
    expect(isEphemeral(20000)).toBe(true);
    expect(isEphemeral(29999)).toBe(true);
    expect(isEphemeral(30000)).toBe(false);
  });
  it("replaceable: 0, 3, [10000, 20000)", () => {
    for (const k of [0, 3, 10000, 10002, 19999]) expect(isReplaceable(k)).toBe(true);
    for (const k of [1, 2, 4, 9999, 20000]) expect(isReplaceable(k)).toBe(false);
  });
  it("addressable is [30000, 40000)", () => {
    expect(isAddressable(29999)).toBe(false);
    expect(isAddressable(30000)).toBe(true);
    expect(isAddressable(39999)).toBe(true);
    expect(isAddressable(40000)).toBe(false);
  });
  it("classes are mutually exclusive", () => {
    for (const k of [0, 1, 3, 5, 10002, 20001, 30023, 65535]) {
      const n = [isEphemeral(k), isReplaceable(k), isAddressable(k)].filter(Boolean).length;
      expect(n).toBeLessThanOrEqual(1);
    }
  });
  it("dTag returns first d value or empty string", () => {
    expect(dTag(signed(alice, { kind: 30023, tags: [["d", "a"], ["d", "b"]] }))).toBe("a");
    expect(dTag(signed(alice, { kind: 30023, tags: [["e", "a"]] }))).toBe("");
    expect(dTag(signed(alice, { kind: 30023, tags: [["d"]] }))).toBe("");
  });
});

describe("tags must have at least one string (#8)", () => {
  it("rejects an empty tag array", () => {
    expect(validateEvent(signed(alice, { tags: [[]] }), NOW)).toBe("invalid: bad tags");
    expect(validateEvent(signed(alice, { tags: [["t"], []] }), NOW)).toBe("invalid: bad tags");
  });
  it("accepts a single-element tag", () => {
    expect(validateEvent(signed(alice, { tags: [["t"]] }), NOW)).toBeNull();
  });
});

describe("utf8Length (#12)", () => {
  it("counts bytes, not UTF-16 units", () => {
    expect(utf8Length("abc")).toBe(3);
    expect(utf8Length("你好")).toBe(6);
    expect(utf8Length("😀")).toBe(4);
  });
});

describe("tag element limits", () => {
  it("rejects a tag with more than 16 strings and a string over 1024 UTF-8 bytes, accepts the boundary", () => {
    const wide = (n: number) => [Array.from({ length: n }, (_, i) => "v" + i)];
    expect(validateEvent(signed(alice, { tags: wide(16) }), NOW)).toBeNull();
    expect(validateEvent(signed(alice, { tags: wide(17) }), NOW)).toBe("invalid: bad tags");
    expect(validateEvent(signed(alice, { tags: [["t", "a".repeat(1024)]] }), NOW)).toBeNull();
    expect(validateEvent(signed(alice, { tags: [["t", "a".repeat(1025)]] }), NOW)).toBe("invalid: bad tags");
    expect(validateEvent(signed(alice, { tags: [["t", "你".repeat(341)]] }), NOW)).toBeNull(); // 1023 bytes
    expect(validateEvent(signed(alice, { tags: [["t", "你".repeat(342)]] }), NOW)).toBe("invalid: bad tags"); // 1026 bytes
  });
});

describe("coordinates in a tags", () => {
  it("an a tag may be 130 bytes longer than other tag strings, so a long d value can be addressed", () => {
    const coord = (n: number) => `30078:${alice.pk}:${"d".repeat(n)}`;
    expect(coord(954).length).toBe(1025);
    expect(validateEvent(signed(alice, { kind: 5, tags: [["a", coord(954)]] }), NOW)).toBeNull();
    expect(validateEvent(signed(alice, { kind: 5, tags: [["a", "x".repeat(1154)]] }), NOW)).toBeNull();
    expect(validateEvent(signed(alice, { kind: 5, tags: [["a", "x".repeat(1155)]] }), NOW)).toBe("invalid: bad tags");
    expect(validateEvent(signed(alice, { kind: 5, tags: [["e", "x".repeat(1025)]] }), NOW)).toBe("invalid: bad tags");
    expect(validateEvent(signed(alice, { kind: 5, tags: [["a", "x", "y".repeat(1025)]] }), NOW)).toBe("invalid: bad tags");
  });
});

describe("limits are an argument", () => {
  const small = { maxEventTags: 2, maxTagElements: 2, maxTagElementBytes: 4, maxFutureSeconds: 10 };
  it("validateEvent applies the limits it is given", () => {
    expect(validateEvent(signed(alice, { tags: [["a"], ["b"], ["c"]] }), NOW, small)).toBe("invalid: bad tags");
    expect(validateEvent(signed(alice, { tags: [["a", "b", "c"]] }), NOW, small)).toBe("invalid: bad tags");
    expect(validateEvent(signed(alice, { tags: [["t", "bcdef"]] }), NOW, small)).toBe("invalid: bad tags");
    expect(validateEvent(signed(alice, { created_at: NOW + 11 }), NOW, small)).toBe("invalid: created_at too far in the future");
    expect(validateEvent(signed(alice, { tags: [["a", "bcd"]] }), NOW, small)).toBeNull();
  });
});

describe("event limits", () => {
  it("events may have 256 tags, not 257", () => {
    expect(LIMITS.maxEventTags).toBe(256);
    const ok = signed(alice, { tags: Array.from({ length: 256 }, (_, i) => ["t", String(i)]) });
    expect(validateEvent(ok, NOW)).toBeNull();
    const many = signed(alice, { tags: Array.from({ length: 257 }, (_, i) => ["t", String(i)]) });
    expect(validateEvent(many, NOW)).toBe("invalid: bad tags");
  });
});

describe("validateFilter", () => {
  it("accepts valid filters", () => {
    expect(validateFilter({})).toBeNull();
    expect(
      validateFilter({ ids: ["ab"], authors: [alice.pk], kinds: [1, 2], since: 1, until: 2, limit: 5, "#e": ["x"], "#p": [] }),
    ).toBeNull();
  });
  it("rejects non-objects and arrays", () => {
    expect(validateFilter(null)).toMatch(/must be an object/);
    expect(validateFilter([])).toMatch(/must be an object/);
    expect(validateFilter("x")).toMatch(/must be an object/);
  });
  it("rejects bad ids/authors", () => {
    expect(validateFilter({ ids: "ab" })).toBe("invalid: bad ids");
    expect(validateFilter({ ids: ["XY"] })).toBe("invalid: bad ids");
    expect(validateFilter({ ids: [""] })).toBe("invalid: bad ids");
    expect(validateFilter({ authors: ["a".repeat(65)] })).toBe("invalid: bad authors");
    expect(validateFilter({ authors: [1] })).toBe("invalid: bad authors");
  });
  it("rejects bad kinds/since/until/limit", () => {
    expect(validateFilter({ kinds: [1.5] })).toBe("invalid: bad kinds");
    expect(validateFilter({ kinds: "1" })).toBe("invalid: bad kinds");
    expect(validateFilter({ since: "1" })).toBe("invalid: bad since");
    expect(validateFilter({ until: 1.5 })).toBe("invalid: bad until");
    expect(validateFilter({ limit: null })).toBe("invalid: bad limit");
  });
  it("rejects bad tag filters", () => {
    expect(validateFilter({ "#e": "x" })).toBe("invalid: bad #e");
    expect(validateFilter({ "#e": [1] })).toBe("invalid: bad #e");
  });
});

describe("matchFilter", () => {
  const e = signed(alice, { kind: 1, created_at: 1000, tags: [["e", "evt1"], ["p", bob.pk], ["t", "x"]] });

  it("empty filter matches everything", () => {
    expect(matchFilter({}, e)).toBe(true);
  });
  it("ids: exact and prefix", () => {
    expect(matchFilter({ ids: [e.id] }, e)).toBe(true);
    expect(matchFilter({ ids: [e.id.slice(0, 8)] }, e)).toBe(true);
    expect(matchFilter({ ids: ["zzzz"] }, e)).toBe(false);
    expect(matchFilter({ ids: [] }, e)).toBe(false);
    const wrong = (e.id[0] === "0" ? "1" : "0") + e.id.slice(1, 8);
    expect(matchFilter({ ids: [wrong] }, e)).toBe(false);
  });
  it("authors: exact and prefix", () => {
    expect(matchFilter({ authors: [alice.pk] }, e)).toBe(true);
    expect(matchFilter({ authors: [alice.pk.slice(0, 6)] }, e)).toBe(true);
    expect(matchFilter({ authors: [bob.pk] }, e)).toBe(false);
    expect(matchFilter({ authors: [bob.pk, alice.pk] }, e)).toBe(true);
  });
  it("kinds", () => {
    expect(matchFilter({ kinds: [1] }, e)).toBe(true);
    expect(matchFilter({ kinds: [0, 1] }, e)).toBe(true);
    expect(matchFilter({ kinds: [2] }, e)).toBe(false);
    expect(matchFilter({ kinds: [] }, e)).toBe(false);
  });
  it("since/until are inclusive", () => {
    expect(matchFilter({ since: 1000 }, e)).toBe(true);
    expect(matchFilter({ since: 1001 }, e)).toBe(false);
    expect(matchFilter({ until: 1000 }, e)).toBe(true);
    expect(matchFilter({ until: 999 }, e)).toBe(false);
    expect(matchFilter({ since: 1000, until: 1000 }, e)).toBe(true);
    expect(matchFilter({ since: 0 }, e)).toBe(true);
  });
  it("#e / #p tag filters", () => {
    expect(matchFilter({ "#e": ["evt1"] }, e)).toBe(true);
    expect(matchFilter({ "#e": ["evt2", "evt1"] }, e)).toBe(true);
    expect(matchFilter({ "#e": ["evt2"] }, e)).toBe(false);
    expect(matchFilter({ "#p": [bob.pk] }, e)).toBe(true);
    expect(matchFilter({ "#p": [alice.pk] }, e)).toBe(false);
    expect(matchFilter({ "#t": ["x"] }, e)).toBe(true);
    expect(matchFilter({ "#d": ["x"] }, e)).toBe(false);
    expect(matchFilter({ "#e": [] }, e)).toBe(false);
  });
  it("tag filter does not match by tag value under a different tag name", () => {
    expect(matchFilter({ "#p": ["evt1"] }, e)).toBe(false);
  });
  it("all conditions are ANDed", () => {
    expect(matchFilter({ kinds: [1], authors: [alice.pk], "#e": ["evt1"], since: 5, until: 2000 }, e)).toBe(true);
    expect(matchFilter({ kinds: [1], authors: [alice.pk], "#e": ["nope"] }, e)).toBe(false);
  });
  it("limit is ignored by matching", () => {
    expect(matchFilter({ limit: 0 }, e)).toBe(true);
  });
  it("matchAny is an OR across filters", () => {
    expect(matchAny([{ kinds: [9] }, { kinds: [1] }], e)).toBe(true);
    expect(matchAny([{ kinds: [9] }, { authors: [bob.pk] }], e)).toBe(false);
    expect(matchAny([], e)).toBe(false);
  });
});

describe("empty and oversized filters (#7)", () => {
  it("filterIsEmpty detects empty ids/authors/kinds/#x lists", () => {
    expect(filterIsEmpty({ ids: [] })).toBe(true);
    expect(filterIsEmpty({ authors: [] })).toBe(true);
    expect(filterIsEmpty({ kinds: [] })).toBe(true);
    expect(filterIsEmpty({ "#e": [] })).toBe(true);
    expect(filterIsEmpty({ kinds: [1], "#p": [] })).toBe(true);
    expect(filterIsEmpty({})).toBe(false);
    expect(filterIsEmpty({ kinds: [1], authors: ["ab"], "#e": ["x"] })).toBe(false);
  });
  it("validateFilter rejects more than 90 bindings", () => {
    const kinds = (n: number) => Array.from({ length: n }, (_, i) => i);
    expect(validateFilter({ kinds: kinds(90) })).toBeNull();
    expect(validateFilter({ kinds: kinds(91) })).toBe("invalid: filter too large");
    // values of several lists are summed, a tag key also costs one binding for its name
    expect(validateFilter({ kinds: kinds(50), authors: Array(41).fill("ab") })).toBe("invalid: filter too large");
    expect(validateFilter({ "#e": Array(89).fill("x") })).toBeNull();
    expect(validateFilter({ "#e": Array(90).fill("x") })).toBe("invalid: filter too large");
  });
});

describe("filter bindings", () => {
  it("ids/authors cost two bindings each (BETWEEN lo AND hi)", () => {
    expect(validateFilter({ authors: Array(45).fill("ab") })).toBeNull();
    expect(validateFilter({ authors: Array(46).fill("ab") })).toBe("invalid: filter too large");
    expect(validateFilter({ ids: Array(30).fill("ab"), authors: Array(16).fill("ab") })).toBe("invalid: filter too large");
  });
});

describe("tag filter keys and unknown keys", () => {
  it("rejects a multi-character tag filter instead of silently widening the filter", () => {
    expect(validateFilter({ "#xx": ["a"] })).toBe("invalid: unsupported tag filter");
    expect(validateFilter({ "#": ["a"] })).toBe("invalid: unsupported tag filter");
    expect(validateFilter({ "#1": ["a"] })).toBe("invalid: unsupported tag filter");
    expect(validateFilter({ "#e": ["a"], "#P": ["b"] })).toBeNull();
  });
  it("ignores other unknown keys (NIP-01), and matching does not look at them", () => {
    expect(validateFilter({ search: "hello", foo: 1, kinds: [1] })).toBeNull();
    expect(matchFilter({ search: "hello" } as never, signed(alice))).toBe(true);
  });
  it("validateFilter applies the binding limit it is given", () => {
    expect(validateFilter({ kinds: [1, 2, 3] }, { maxFilterBindings: 2 })).toBe("invalid: filter too large");
    expect(validateFilter({ kinds: [1, 2] }, { maxFilterBindings: 2 })).toBeNull();
  });
});

import { describe, expect, it, vi } from "vitest";
import { LIMITS, loadLimits } from "../src/config";

describe("loadLimits", () => {
  it("returns the defaults without overrides", () => {
    expect(loadLimits()).toEqual(LIMITS);
    expect(loadLimits("")).toEqual(LIMITS);
  });
  it("applies numeric overrides for known keys only", () => {
    const lim = loadLimits('{"maxStorageBytes":1,"nope":5,"maxTombstones":"x","maxConnections":null}');
    expect(lim.maxStorageBytes).toBe(1);
    expect(lim.maxTombstones).toBe(LIMITS.maxTombstones);
    expect(lim.maxConnections).toBe(LIMITS.maxConnections);
    expect("nope" in lim).toBe(false);
  });
  it("rejects fractions, huge values, zero for maxLimit and unknown keys, and reports them once", () => {
    const log = vi.spyOn(console, "error").mockImplementation(() => {});
    const lim = loadLimits('{"maxStorageBytes":1.5,"maxTombstones":1e100,"maxLimit":0,"maxEventTags":-1,"nope":1,"maxSubRows":7}');
    expect(lim).toEqual({ ...LIMITS, maxSubRows: 7 });
    expect(log).toHaveBeenCalledTimes(1);
    log.mockRestore();
  });
  it("accepts the boundaries: maxLimit 1, maxIndexedTags 0, 1e9, and the default storage size", () => {
    const lim = loadLimits(`{"maxLimit":1,"maxIndexedTags":0,"maxConnections":1000000000,"maxStorageBytes":${LIMITS.maxStorageBytes}}`);
    expect([lim.maxLimit, lim.maxIndexedTags, lim.maxConnections, lim.maxStorageBytes]).toEqual([1, 0, 1e9, LIMITS.maxStorageBytes]);
    expect(loadLimits('{"maxConnections":1000000001}').maxConnections).toBe(LIMITS.maxConnections);
  });
  it("ignores inherited keys and negative numbers", () => {
    const log = vi.spyOn(console, "error").mockImplementation(() => {});
    const lim = loadLimits('{"toString":5,"__proto__":{"maxStorageBytes":1},"maxTombstones":-1}');
    expect(lim).toEqual(LIMITS);
    log.mockRestore();
  });
  it("ignores malformed JSON", () => {
    const log = vi.spyOn(console, "error").mockImplementation(() => {});
    expect(loadLimits("{")).toEqual(LIMITS);
    expect(log).toHaveBeenCalled();
    log.mockRestore();
  });
  it("does not modify the shared defaults", () => {
    loadLimits('{"maxStorageBytes":1}');
    expect(LIMITS.maxStorageBytes).toBe(5 * 1024 * 1024 * 1024);
  });
});

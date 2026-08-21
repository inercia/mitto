/**
 * Tests for beadsGoneCache.js (mitto-msv).
 *
 * Pure module semantics — no fetch, no hooks, no DOM. The negative cache is
 * consumed by useLinkedBeadPhase, app.js's header status effect,
 * SessionPanel.js's side-panel effect, and beadsPreload.js; its correctness
 * (per-workspace isolation, case normalization, verdict persistence across
 * hypothetical cache invalidations) directly underwrites the poll-storm fix,
 * so it earns dedicated coverage independent of any caller.
 */

import {
  describe,
  test,
  expect,
  beforeEach,
} from "./testing/testGlobals.js";

let mod;

beforeEach(async () => {
  mod = await import("./beadsGoneCache.js");
  mod._resetBeadsGoneCache();
});

describe("beadsGoneCache — falsy input guards", () => {
  test("isGone returns false when workingDir or id is falsy", () => {
    expect(mod.isGone("", "mitto-aaa")).toBe(false);
    expect(mod.isGone(null, "mitto-aaa")).toBe(false);
    expect(mod.isGone(undefined, "mitto-aaa")).toBe(false);
    expect(mod.isGone("/tmp/wsA", "")).toBe(false);
    expect(mod.isGone("/tmp/wsA", null)).toBe(false);
    expect(mod.isGone("/tmp/wsA", undefined)).toBe(false);
  });

  test("markGone is a no-op when workingDir or id is falsy", () => {
    mod.markGone("", "mitto-aaa");
    mod.markGone(null, "mitto-aaa");
    mod.markGone("/tmp/wsA", "");
    mod.markGone("/tmp/wsA", null);
    // Nothing leaked into the cache — a real lookup still returns false.
    expect(mod.isGone("/tmp/wsA", "mitto-aaa")).toBe(false);
  });

  test("clearGone is a no-op when workingDir or id is falsy or unknown", () => {
    // Prime the cache with a real entry first so we can prove the unrelated
    // clears do not touch it.
    mod.markGone("/tmp/wsA", "mitto-aaa");
    mod.clearGone("", "mitto-aaa");
    mod.clearGone("/tmp/wsA", "");
    mod.clearGone("/tmp/wsB", "mitto-aaa"); // unknown workingDir
    mod.clearGone("/tmp/wsA", "mitto-zzz"); // unknown id
    expect(mod.isGone("/tmp/wsA", "mitto-aaa")).toBe(true);
  });
});

describe("beadsGoneCache — markGone / isGone / clearGone semantics", () => {
  test("markGone flips isGone to true; clearGone flips it back", () => {
    expect(mod.isGone("/tmp/wsA", "mitto-aaa")).toBe(false);
    mod.markGone("/tmp/wsA", "mitto-aaa");
    expect(mod.isGone("/tmp/wsA", "mitto-aaa")).toBe(true);
    mod.clearGone("/tmp/wsA", "mitto-aaa");
    expect(mod.isGone("/tmp/wsA", "mitto-aaa")).toBe(false);
  });

  test("markGone is idempotent", () => {
    mod.markGone("/tmp/wsA", "mitto-aaa");
    mod.markGone("/tmp/wsA", "mitto-aaa");
    mod.markGone("/tmp/wsA", "mitto-aaa");
    expect(mod.isGone("/tmp/wsA", "mitto-aaa")).toBe(true);
    // A single clearGone flips it off — proving no duplicate entries linger.
    mod.clearGone("/tmp/wsA", "mitto-aaa");
    expect(mod.isGone("/tmp/wsA", "mitto-aaa")).toBe(false);
  });

  test("clearGone on the last id in a workspace drops the workspace bucket", () => {
    // Not user-observable directly, but exercised so that later markGone in
    // the same workspace still works after the bucket is auto-collapsed.
    mod.markGone("/tmp/wsA", "mitto-aaa");
    mod.clearGone("/tmp/wsA", "mitto-aaa");
    mod.markGone("/tmp/wsA", "mitto-bbb");
    expect(mod.isGone("/tmp/wsA", "mitto-bbb")).toBe(true);
    expect(mod.isGone("/tmp/wsA", "mitto-aaa")).toBe(false);
  });
});

describe("beadsGoneCache — key normalization", () => {
  test("ids are matched case-insensitively (bd IDs are lowercased on write)", () => {
    mod.markGone("/tmp/wsA", "Mitto-AaA");
    expect(mod.isGone("/tmp/wsA", "mitto-aaa")).toBe(true);
    expect(mod.isGone("/tmp/wsA", "MITTO-AAA")).toBe(true);
    // clearGone honors the same normalization.
    mod.clearGone("/tmp/wsA", "MITTO-AAA");
    expect(mod.isGone("/tmp/wsA", "mitto-aaa")).toBe(false);
  });

  test("workingDir is matched exactly (paths are already canonical)", () => {
    // Rationale: workspace working_dir values arrive from the backend as
    // absolute canonical paths, so case-sensitive equality is the right
    // discriminator here — mistakenly lowercasing the path would collide
    // /Users/Alvaro and /users/alvaro on case-insensitive filesystems.
    mod.markGone("/tmp/wsA", "mitto-aaa");
    expect(mod.isGone("/tmp/WSA", "mitto-aaa")).toBe(false);
    expect(mod.isGone("/tmp/wsA", "mitto-aaa")).toBe(true);
  });
});

describe("beadsGoneCache — per-workspace isolation", () => {
  test("marking an id gone in wsA does NOT shadow the same id in wsB", () => {
    mod.markGone("/tmp/wsA", "mitto-aaa");
    expect(mod.isGone("/tmp/wsA", "mitto-aaa")).toBe(true);
    expect(mod.isGone("/tmp/wsB", "mitto-aaa")).toBe(false);
  });

  test("clearGone in one workspace leaves the other untouched", () => {
    mod.markGone("/tmp/wsA", "mitto-aaa");
    mod.markGone("/tmp/wsB", "mitto-aaa");
    mod.clearGone("/tmp/wsA", "mitto-aaa");
    expect(mod.isGone("/tmp/wsA", "mitto-aaa")).toBe(false);
    expect(mod.isGone("/tmp/wsB", "mitto-aaa")).toBe(true);
  });
});

describe("beadsGoneCache — _resetBeadsGoneCache (test-only helper)", () => {
  test("wipes every workspace bucket", () => {
    mod.markGone("/tmp/wsA", "mitto-aaa");
    mod.markGone("/tmp/wsA", "mitto-bbb");
    mod.markGone("/tmp/wsB", "mitto-aaa");
    mod._resetBeadsGoneCache();
    expect(mod.isGone("/tmp/wsA", "mitto-aaa")).toBe(false);
    expect(mod.isGone("/tmp/wsA", "mitto-bbb")).toBe(false);
    expect(mod.isGone("/tmp/wsB", "mitto-aaa")).toBe(false);
  });
});

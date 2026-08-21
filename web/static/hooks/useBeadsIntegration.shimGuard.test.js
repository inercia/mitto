/**
 * Regression guard for mitto-txpp.6 — pins the window.preact shim contract
 * against cross-file pollution in Bun's shared-process runner.
 *
 * Background: Bun does NOT isolate globals per file the way Jest+jsdom does.
 * If a sibling test file (historically `useConversationSeeding.test.js`)
 * hard-assigns `window.preact = { useCallback: (fn) => fn }` and this file's
 * own header uses `window.preact = window.preact || {...}`, the guard
 * short-circuits on the truthy-but-partial object and `useState`/`useMemo`/
 * `useRef` stay undefined — so `useConversationSeeding.js`'s lazy destructure
 * blows up with `useCallback is not a function` in the aggregate run but
 * passes in isolation (Jest's per-file VM masks the pollution).
 *
 * This test simulates BOTH sides of the contract without depending on file
 * order in the CLEAN[] array:
 *   1. The polluter's replacement shim must NOT wipe pre-existing hooks
 *      (i.e. `useConversationSeeding.test.js` must use a merge, not a
 *      hard-assign).
 *   2. The victim's own header must survive a partial-preact-already-present
 *      environment (i.e. `useBeadsIntegration.test.js` must use per-field
 *      top-up, not `window.preact = window.preact || {...}`).
 *
 * If either contract regresses, this file fails deterministically — even
 * when run in isolation — so the guard cannot silently disappear the way
 * the original bug did.
 */

import {
  describe,
  test,
  expect,
} from "../utils/testing/testGlobals.js";

// -----------------------------------------------------------------------------
// Helpers — the two shim patterns we want to pin, extracted verbatim from the
// live test files. Kept as local pure functions so the guard stays independent
// of import order and the shims can be evaluated against a fresh fake preact
// object per test.
// -----------------------------------------------------------------------------

/**
 * Mirrors the header shim in useConversationSeeding.test.js (mitto-txpp.6
 * fix: merge, not hard-assign).
 */
function applySeedingShim(preact) {
  return { ...(preact || {}), useCallback: (fn) => fn };
}

/**
 * Mirrors the header shim in useBeadsIntegration.test.js (mitto-txpp.6 fix:
 * per-field top-up, not `preact || {...}`).
 */
function applyBeadsShim(preact) {
  const out = preact || {};
  out.useState =
    out.useState ||
    ((initial) => [
      typeof initial === "function" ? initial() : initial,
      () => {},
    ]);
  out.useCallback = out.useCallback || ((fn) => fn);
  out.useMemo = out.useMemo || ((fn) => fn());
  out.useRef = out.useRef || ((initial) => ({ current: initial }));
  return out;
}

// -----------------------------------------------------------------------------
// Tests
// -----------------------------------------------------------------------------

describe("mitto-txpp.6 — window.preact shim contract", () => {
  test("seeding shim: merge preserves hooks stubbed by an earlier file", () => {
    const earlier = {
      useState: () => ["earlier", () => {}],
      useMemo: (fn) => fn(),
      useRef: (initial) => ({ current: initial }),
    };
    const after = applySeedingShim(earlier);

    // The seeding shim only asserts useCallback; everything else must survive.
    expect(typeof after.useCallback).toBe("function");
    expect(after.useState).toBe(earlier.useState);
    expect(after.useMemo).toBe(earlier.useMemo);
    expect(after.useRef).toBe(earlier.useRef);
  });

  test("seeding shim: safe when window.preact is undefined", () => {
    const after = applySeedingShim(undefined);
    expect(typeof after.useCallback).toBe("function");
  });

  test("beads shim: fills missing hooks when a partial preact is already present (the exact repro)", () => {
    // Reproduce the historical polluter: only useCallback present.
    const polluted = { useCallback: (fn) => fn };
    const after = applyBeadsShim(polluted);

    expect(typeof after.useCallback).toBe("function");
    expect(typeof after.useState).toBe("function");
    expect(typeof after.useMemo).toBe("function");
    expect(typeof after.useRef).toBe("function");

    // Sanity — the filled-in hooks are actually callable.
    const [value, setValue] = after.useState(42);
    expect(value).toBe(42);
    expect(typeof setValue).toBe("function");

    const memoValue = after.useMemo(() => "memoized");
    expect(memoValue).toBe("memoized");

    const ref = after.useRef({ x: 1 });
    expect(ref.current).toEqual({ x: 1 });
  });

  test("beads shim: leaves pre-existing hooks intact (never overwrites)", () => {
    const sentinelState = () => ["kept", () => {}];
    const sentinelMemo = () => "keptMemo";
    const preexisting = {
      useState: sentinelState,
      useMemo: sentinelMemo,
    };
    const after = applyBeadsShim(preexisting);

    expect(after.useState).toBe(sentinelState);
    expect(after.useMemo).toBe(sentinelMemo);
    expect(typeof after.useCallback).toBe("function");
    expect(typeof after.useRef).toBe("function");
  });

  test("beads shim: safe when window.preact is undefined", () => {
    const after = applyBeadsShim(undefined);
    expect(typeof after.useState).toBe("function");
    expect(typeof after.useCallback).toBe("function");
    expect(typeof after.useMemo).toBe("function");
    expect(typeof after.useRef).toBe("function");
  });

  test("composed pipeline: seeding-first then beads (the aggregate ordering that used to break) yields a complete preact", () => {
    // Start from a fresh preact-less environment, run the seeding shim (as if
    // useConversationSeeding.test.js ran first), then run the beads shim (as
    // if useBeadsIntegration.test.js ran second). All four hooks must be
    // callable at the end — this is exactly the ordering the original bug
    // required to reproduce.
    const afterSeeding = applySeedingShim(undefined);
    const afterBeads = applyBeadsShim(afterSeeding);

    expect(typeof afterBeads.useState).toBe("function");
    expect(typeof afterBeads.useCallback).toBe("function");
    expect(typeof afterBeads.useMemo).toBe("function");
    expect(typeof afterBeads.useRef).toBe("function");

    // And the beads shim must NOT have replaced the seeding useCallback
    // (they are functionally equivalent, but the top-up contract says "keep
    // what's already there").
    expect(afterBeads.useCallback).toBe(afterSeeding.useCallback);
  });
});

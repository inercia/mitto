/**
 * Tests for useVisibleInterval.js (mitto-2fx.2).
 *
 * The hook destructures `useEffect` and `useRef` from `window.preact` at
 * module-load time, so we install a minimal stub for both on window.preact
 * before the first import: the stub records every registered effect and
 * exposes a mutable ref implementation. We then invoke each effect body
 * ourselves, drive interval timers with jest fake timers, and dispatch
 * `visibilitychange` events on `document` to exercise the arm/disarm paths.
 *
 * Acceptance criteria pinned by these tests (from mitto-2fx.2):
 *   - While `document.visibilityState !== "visible"`, the interval must NOT
 *     fire, even if the hook is enabled.
 *   - On each transition to visible + enabled, the callback fires ONCE
 *     immediately (catch-up on wake), then arms `setInterval(cb, intervalMs)`.
 *   - On hide/disable/unmount, the interval is cleared and the
 *     `visibilitychange` listener is removed.
 */

import {
  describe,
  test,
  expect,
  beforeEach,
  afterEach,
  jest,
} from "../utils/testing/testGlobals.js";

// Minimal environment. jsdom already gives us `document` and `window`, but we
// need to control document.visibilityState — jsdom's `visibilityState` is a
// getter, so we redefine the property so tests can flip it.
global.window = global.window || {};

let _visibility = "visible";
Object.defineProperty(document, "visibilityState", {
  configurable: true,
  get: () => _visibility,
});
function setVisibility(v) {
  _visibility = v;
  document.dispatchEvent(new Event("visibilitychange"));
}

// Install stubs for the two Preact hooks the module destructures at
// module-load time. `useEffect` records (cb, deps) so tests can invoke the
// effect body manually; `useRef` returns a real mutable object so the
// callback-capture pattern in useVisibleInterval works correctly.
let currentEffects = [];
window.preact = {
  ...(window.preact || {}),
  useEffect: (cb, deps) => {
    currentEffects.push({ cb, deps });
  },
  useRef: (initial) => ({ current: initial }),
};

// Cleanups from every mount in the current test — ran in afterEach so the
// document-level visibilitychange listener registered by the source module
// does not leak across tests.
let pendingCleanups = [];

async function loadHook() {
  currentEffects = [];
  const mod = await import("./useVisibleInterval.js");
  return { useVisibleInterval: mod.useVisibleInterval };
}

beforeEach(() => {
  jest.useFakeTimers();
  _visibility = "visible";
  pendingCleanups = [];
});

afterEach(() => {
  for (const c of pendingCleanups) {
    try {
      c && c();
    } catch (_) {
      // ignore
    }
  }
  pendingCleanups = [];
  jest.useRealTimers();
  jest.restoreAllMocks();
});

// Helper: mount the hook, then run each registered effect body and collect
// the returned cleanup functions. useVisibleInterval registers TWO effects
// (one to keep the callback ref fresh, one to manage the interval).
async function mount(cb, intervalMs, options) {
  const { useVisibleInterval } = await loadHook();
  useVisibleInterval(cb, intervalMs, options);
  const cleanups = [];
  for (const { cb: effectCb } of currentEffects) {
    const ret = effectCb();
    if (typeof ret === "function") cleanups.push(ret);
  }
  const cleanupAll = () => {
    for (const c of cleanups) {
      try {
        c();
      } catch (_) {
        /* ignore */
      }
    }
  };
  pendingCleanups.push(cleanupAll);
  return { cleanupAll };
}

describe("useVisibleInterval — arm while visible + enabled", () => {
  test("fires callback once immediately on arm (catch-up on mount)", async () => {
    const cb = jest.fn();
    await mount(cb, 1000);

    // The arm() path fires the callback once synchronously before scheduling
    // the interval — this is the "catch up to wall-clock on wake" behavior.
    expect(cb).toHaveBeenCalledTimes(1);
  });

  test("fires callback repeatedly at the interval while visible", async () => {
    const cb = jest.fn();
    await mount(cb, 1000);
    expect(cb).toHaveBeenCalledTimes(1); // immediate catch-up

    jest.advanceTimersByTime(1000);
    expect(cb).toHaveBeenCalledTimes(2);
    jest.advanceTimersByTime(3000);
    expect(cb).toHaveBeenCalledTimes(5);
  });
});

describe("useVisibleInterval — hidden state does not fire", () => {
  test("no immediate catch-up when mounted while hidden", async () => {
    _visibility = "hidden";
    const cb = jest.fn();
    await mount(cb, 1000);

    // Neither the arm() catch-up nor the interval should have fired.
    expect(cb).toHaveBeenCalledTimes(0);
    jest.advanceTimersByTime(5000);
    expect(cb).toHaveBeenCalledTimes(0);
  });

  test("does not fire on visibilitychange to a non-visible state", async () => {
    const cb = jest.fn();
    await mount(cb, 1000);
    expect(cb).toHaveBeenCalledTimes(1); // catch-up on arm

    setVisibility("hidden");
    // Interval cleared; no further ticks even after time passes.
    jest.advanceTimersByTime(5000);
    expect(cb).toHaveBeenCalledTimes(1);
  });
});

describe("useVisibleInterval — visibility transitions", () => {
  test("fires once immediately on transition hidden → visible, then ticks at interval", async () => {
    _visibility = "hidden";
    const cb = jest.fn();
    await mount(cb, 1000);
    expect(cb).toHaveBeenCalledTimes(0);

    setVisibility("visible");
    // One catch-up call the moment we become visible again.
    expect(cb).toHaveBeenCalledTimes(1);

    jest.advanceTimersByTime(1000);
    expect(cb).toHaveBeenCalledTimes(2);
    jest.advanceTimersByTime(2000);
    expect(cb).toHaveBeenCalledTimes(4);
  });

  test("hide→show cycle re-arms cleanly (no duplicate intervals)", async () => {
    const cb = jest.fn();
    await mount(cb, 1000);
    expect(cb).toHaveBeenCalledTimes(1); // arm catch-up

    setVisibility("hidden");
    setVisibility("visible"); // catch-up again (+1)
    expect(cb).toHaveBeenCalledTimes(2);

    // If a stale interval had leaked, we'd see 2 calls per tick.
    jest.advanceTimersByTime(1000);
    expect(cb).toHaveBeenCalledTimes(3);
    jest.advanceTimersByTime(1000);
    expect(cb).toHaveBeenCalledTimes(4);
  });
});

describe("useVisibleInterval — enabled=false", () => {
  test("does not arm when enabled is false", async () => {
    const cb = jest.fn();
    await mount(cb, 1000, { enabled: false });

    // The interval effect returns undefined immediately when disabled,
    // so no listener is registered and no catch-up fires.
    expect(cb).toHaveBeenCalledTimes(0);
    jest.advanceTimersByTime(5000);
    expect(cb).toHaveBeenCalledTimes(0);

    // Even a visibility transition must not fire the callback, since no
    // visibilitychange listener was registered when disabled.
    setVisibility("hidden");
    setVisibility("visible");
    jest.advanceTimersByTime(5000);
    expect(cb).toHaveBeenCalledTimes(0);
  });
});

describe("useVisibleInterval — cleanup", () => {
  test("clears the interval and removes the visibilitychange listener on unmount", async () => {
    const cb = jest.fn();
    const { cleanupAll } = await mount(cb, 1000);
    expect(cb).toHaveBeenCalledTimes(1); // arm catch-up

    cleanupAll();

    // No further interval ticks after unmount.
    jest.advanceTimersByTime(5000);
    expect(cb).toHaveBeenCalledTimes(1);

    // And the visibilitychange listener is gone — a transition to visible
    // must NOT restart the interval.
    setVisibility("hidden");
    setVisibility("visible");
    jest.advanceTimersByTime(5000);
    expect(cb).toHaveBeenCalledTimes(1);
  });
});

describe("useVisibleInterval — throwing callback", () => {
  test("interval keeps ticking when the callback throws", async () => {
    let n = 0;
    const cb = jest.fn(() => {
      n += 1;
      throw new Error("boom");
    });
    await mount(cb, 1000);
    // Catch-up call executed and swallowed its throw.
    expect(n).toBe(1);

    jest.advanceTimersByTime(1000);
    expect(n).toBe(2);
    jest.advanceTimersByTime(1000);
    expect(n).toBe(3);
  });
});

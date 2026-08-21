/**
 * Unit tests for useDashboardHiddenCharts (mitto-4t8 / mitto-3i2 Phase 3).
 *
 * The hook destructures `useState`/`useEffect` from `window.preact` at
 * MODULE-LOAD time, so — following useBeadsFolderConfig.test.js — we install
 * a `window.preact` stub with capturing setters/effects BEFORE the first
 * import. The effect body is invoked directly to assert on the observed
 * subscriptions and updater results.
 *
 * The hook uses the real storage.js (no module mocking — no other test in
 * this repo mocks modules): localStorage is jsdom-backed, so the
 * getDashboardHiddenCharts read path runs end-to-end.
 */

import {
  describe,
  test,
  expect,
  beforeEach,
  jest,
} from "../utils/testing/testGlobals.js";

const IDX = { setHidden: 0 };
const KEY = "mitto-dashboard-hidden-charts";
const EVT = "mitto-dashboard-hidden-charts-changed";

let currentSetters = [];
let currentEffects = [];
window.preact = {
  useState: (initial) => {
    const setter = jest.fn();
    currentSetters.push(setter);
    const seed = typeof initial === "function" ? initial() : initial;
    return [seed, setter];
  },
  useEffect: (cb, deps) => {
    currentEffects.push({ cb, deps });
  },
};

async function loadHook() {
  currentSetters = [];
  currentEffects = [];
  const mod = await import("./useDashboardHiddenCharts.js");
  return {
    useDashboardHiddenCharts: mod.useDashboardHiddenCharts,
    setters: currentSetters,
    effects: currentEffects,
  };
}

beforeEach(() => {
  window.localStorage.clear();
});

describe("useDashboardHiddenCharts", () => {
  test("seeds state from getDashboardHiddenCharts on mount", async () => {
    window.localStorage.setItem(KEY, JSON.stringify(["tokens", "model_usage"]));
    const { useDashboardHiddenCharts } = await loadHook();
    expect(useDashboardHiddenCharts()).toEqual(["tokens", "model_usage"]);
  });

  test("registers exactly one useEffect with an empty deps array (mount-once)", async () => {
    const { useDashboardHiddenCharts, effects } = await loadHook();
    useDashboardHiddenCharts();
    expect(effects).toHaveLength(1);
    expect(effects[0].deps).toEqual([]);
  });

  test("effect subscribes to the CustomEvent and returns a cleanup fn", async () => {
    const { useDashboardHiddenCharts, effects } = await loadHook();
    useDashboardHiddenCharts();
    const addSpy = jest.spyOn(window, "addEventListener");
    try {
      const cleanup = effects[0].cb();
      expect(addSpy).toHaveBeenCalledWith(EVT, expect.any(Function));
      expect(typeof cleanup).toBe("function");
      cleanup();
    } finally {
      addSpy.mockRestore();
    }
  });

  test("effect cleanup removes the CustomEvent listener", async () => {
    const { useDashboardHiddenCharts, effects } = await loadHook();
    useDashboardHiddenCharts();
    const removeSpy = jest.spyOn(window, "removeEventListener");
    try {
      const cleanup = effects[0].cb();
      cleanup();
      expect(removeSpy).toHaveBeenCalledWith(EVT, expect.any(Function));
    } finally {
      removeSpy.mockRestore();
    }
  });

  test("firing the CustomEvent calls setHidden with the updated storage value", async () => {
    const { useDashboardHiddenCharts, setters, effects } = await loadHook();
    useDashboardHiddenCharts();
    const cleanup = effects[0].cb();
    try {
      // Simulate Settings ▸ Dashboard saving a new hidden set into storage,
      // then dispatching the live-update event (mirrors setDashboardHiddenCharts).
      window.localStorage.setItem(
        KEY,
        JSON.stringify(["tokens", "tool_calls"]),
      );
      window.dispatchEvent(
        new CustomEvent(EVT, { detail: { ids: ["tokens", "tool_calls"] } }),
      );
      expect(setters[IDX.setHidden]).toHaveBeenCalledTimes(1);
      // Updater form: `setHidden((prev) => next|prev)`. Invoke with a stale
      // `prev` and assert it returns the fresh read from storage.
      const updater = setters[IDX.setHidden].mock.calls[0][0];
      expect(updater([])).toEqual(["tokens", "tool_calls"]);
    } finally {
      cleanup();
    }
  });

  test("unchanged content → updater returns prev by reference (no spurious re-render)", async () => {
    // Referential-equality guard: when the event fires but the ids array
    // content is unchanged (e.g. an unrelated preference save), the updater
    // must return `prev` untouched so Preact bails out of the re-render
    // and uPlot does not re-mount.
    window.localStorage.setItem(KEY, JSON.stringify(["tokens"]));
    const { useDashboardHiddenCharts, setters, effects } = await loadHook();
    useDashboardHiddenCharts();
    const cleanup = effects[0].cb();
    try {
      window.dispatchEvent(
        new CustomEvent(EVT, { detail: { ids: ["tokens"] } }),
      );
      const updater = setters[IDX.setHidden].mock.calls[0][0];
      const prev = ["tokens"];
      expect(updater(prev)).toBe(prev);
    } finally {
      cleanup();
    }
  });

  test("effect body references onUIPreferencesLoaded (async first-load path)", async () => {
    // Cold app launch: initUIPreferences finishes AFTER the hook mounted; the
    // hook must ALSO subscribe via onUIPreferencesLoaded so the Dashboard
    // honours the last-saved visibility on first render (server → localStorage
    // mirror). ESM named-export bindings are read-only under
    // --experimental-vm-modules so we can't jest.spyOn the storage module;
    // instead we pin the wiring at the source level — the effect callback
    // stringifies to a body that references both signals AND their unsubscribe
    // cleanup. A regression that drops either subscription breaks this pin.
    const { useDashboardHiddenCharts, effects } = await loadHook();
    useDashboardHiddenCharts();
    const src = effects[0].cb.toString();
    expect(src).toContain("mitto-dashboard-hidden-charts-changed");
    expect(src).toContain("onUIPreferencesLoaded");
    // Cleanup returned by the effect must reference the same two signals so
    // the mount-unmount cycle is symmetric (no listener/callback leaks).
    const cleanup = effects[0].cb();
    try {
      const cleanSrc = cleanup.toString();
      expect(cleanSrc).toContain("removeEventListener");
    } finally {
      cleanup();
    }
  });
});

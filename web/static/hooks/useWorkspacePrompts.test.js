/**
 * Tests for useWorkspacePrompts.js (mitto-8x9).
 *
 * The hook destructures useState/useEffect/useRef/useCallback/useMemo from
 * window.preact at module-load time, so — mirroring the pattern used by
 * useVisibleInterval.test.js and useBeadsIntegration.test.js — we install
 * pass-through stubs before importing: useCallback/useMemo just invoke their
 * function, useState returns [initial, noop], useRef returns a real mutable
 * ref, and useEffect records (cb, deps) so tests can run the effect bodies
 * themselves and drive them with fake timers.
 *
 * Focus: the mitto:prompts_changed debounce (mitto-8x9 acceptance criterion
 * "a burst of prompts_changed / mcp_tools_available events collapses to a
 * single force refresh"). fetchWorkspacePrompts is exercised end-to-end
 * through the real promptsCache module against a mocked global.fetch, so the
 * assertions are on actual network-call counts, not a mocked cache API.
 */

import {
  describe,
  test,
  expect,
  beforeEach,
  afterEach,
  jest,
} from "../utils/testing/testGlobals.js";
import { invalidateWorkspacePromptsCache } from "../utils/promptsCache.js";

global.window = global.window || {};

let currentEffects = [];
window.preact = {
  ...(window.preact || {}),
  useState: (initial) => [
    typeof initial === "function" ? initial() : initial,
    () => {},
  ],
  useCallback: (fn) => fn,
  useMemo: (fn) => fn(),
  useRef: (initial) => ({ current: initial }),
  useEffect: (cb, deps) => {
    currentEffects.push({ cb, deps });
  },
};
window.mittoApiPrefix = "";

function makeHeaders() {
  return { get: () => null };
}

function immediateOkFetch(body = { prompts: [], migrated: [] }) {
  return jest.fn(() =>
    Promise.resolve({
      status: 200,
      ok: true,
      headers: makeHeaders(),
      json: () => Promise.resolve(body),
    }),
  );
}

let pendingCleanups = [];

async function loadHook() {
  currentEffects = [];
  const mod = await import("./useWorkspacePrompts.js");
  return mod.useWorkspacePrompts;
}

/** Mount the hook and run every registered effect body, collecting cleanups. */
async function mount(deps) {
  const useWorkspacePrompts = await loadHook();
  const bundle = useWorkspacePrompts(deps);
  const cleanups = [];
  for (const { cb } of currentEffects) {
    const ret = cb();
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
  return { bundle, cleanupAll };
}

beforeEach(() => {
  jest.useFakeTimers();
  invalidateWorkspacePromptsCache();
  pendingCleanups = [];
});

afterEach(() => {
  for (const c of pendingCleanups) {
    try {
      c();
    } catch (_) {
      /* ignore */
    }
  }
  pendingCleanups = [];
  jest.useRealTimers();
  jest.restoreAllMocks();
});

describe("mitto:prompts_changed debounce", () => {
  test("a burst of events within the debounce window triggers exactly one fetch", async () => {
    global.fetch = immediateOkFetch({ prompts: [{ name: "a" }], migrated: [] });

    await mount({ workingDir: "/w", activeSessionId: "s1" });
    // The mount-time workspace-change effect already issued its own forced
    // fetch; reset the spy so only the debounce path below is measured.
    global.fetch = immediateOkFetch({ prompts: [{ name: "a" }], migrated: [] });

    window.dispatchEvent(new Event("mitto:prompts_changed"));
    window.dispatchEvent(new Event("mitto:prompts_changed"));
    window.dispatchEvent(new Event("mitto:prompts_changed"));

    // Still within the 250ms debounce window — no fetch yet.
    expect(global.fetch).not.toHaveBeenCalled();

    jest.advanceTimersByTime(251);
    await Promise.resolve();
    await Promise.resolve();

    expect(global.fetch).toHaveBeenCalledTimes(1);
  });

  test("each new event resets the debounce timer (trailing-edge, not leading)", async () => {
    global.fetch = immediateOkFetch({ prompts: [] });
    await mount({ workingDir: "/w", activeSessionId: "s1" });
    global.fetch = immediateOkFetch({ prompts: [] });

    window.dispatchEvent(new Event("mitto:prompts_changed"));
    jest.advanceTimersByTime(200); // not yet elapsed
    window.dispatchEvent(new Event("mitto:prompts_changed")); // resets the timer
    jest.advanceTimersByTime(200); // 400ms since first event, but only 200ms since reset
    expect(global.fetch).not.toHaveBeenCalled();

    jest.advanceTimersByTime(51); // 251ms since the reset
    await Promise.resolve();
    await Promise.resolve();
    expect(global.fetch).toHaveBeenCalledTimes(1);
  });

  test("separate bursts (spaced beyond the debounce window) each trigger their own fetch", async () => {
    global.fetch = immediateOkFetch({ prompts: [] });
    await mount({ workingDir: "/w", activeSessionId: "s1" });
    global.fetch = immediateOkFetch({ prompts: [] });

    window.dispatchEvent(new Event("mitto:prompts_changed"));
    jest.advanceTimersByTime(251);
    await Promise.resolve();
    await Promise.resolve();
    expect(global.fetch).toHaveBeenCalledTimes(1);

    window.dispatchEvent(new Event("mitto:prompts_changed"));
    jest.advanceTimersByTime(251);
    await Promise.resolve();
    await Promise.resolve();
    expect(global.fetch).toHaveBeenCalledTimes(2);
  });

  test("refresh is forced (force=true), never masked by the TTL cache", async () => {
    // Prime the cache for this exact key so a non-forced call would be a
    // cache hit; the debounced refresh must bypass it regardless.
    global.fetch = immediateOkFetch({ prompts: [{ name: "stale" }] });
    await mount({ workingDir: "/w", activeSessionId: "s1" });
    // Populate the cache via the workspace-change effect (also force=true),
    // which already ran once during mount.
    expect(global.fetch).toHaveBeenCalledTimes(1);

    global.fetch = immediateOkFetch({ prompts: [{ name: "updated" }] });
    window.dispatchEvent(new Event("mitto:prompts_changed"));
    jest.advanceTimersByTime(251);
    await Promise.resolve();
    await Promise.resolve();

    // A forced refresh always hits the network, even though the TTL window
    // from the mount-time fetch has not expired.
    expect(global.fetch).toHaveBeenCalledTimes(1);
  });

  test("cleanup on unmount clears the pending debounce timer and removes the listener", async () => {
    global.fetch = immediateOkFetch({ prompts: [] });
    const { cleanupAll } = await mount({
      workingDir: "/w",
      activeSessionId: "s1",
    });
    // Consume the mount-time forced fetch call so only the debounce path is
    // being measured below.
    global.fetch = immediateOkFetch({ prompts: [] });

    window.dispatchEvent(new Event("mitto:prompts_changed"));
    cleanupAll();

    jest.advanceTimersByTime(1000);
    await Promise.resolve();
    await Promise.resolve();

    expect(global.fetch).not.toHaveBeenCalled();
  });

  test("no workingDir → event is a no-op (no timer armed, no fetch)", async () => {
    global.fetch = jest.fn();
    await mount({ workingDir: null, activeSessionId: "s1" });

    window.dispatchEvent(new Event("mitto:prompts_changed"));
    jest.advanceTimersByTime(1000);
    await Promise.resolve();

    expect(global.fetch).not.toHaveBeenCalled();
  });
});

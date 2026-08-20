/**
 * Tests for useMCPInitState.js (mitto-8fm).
 *
 * The hook destructures `useState`/`useEffect`/`useCallback`/`useRef` from
 * `window.preact` at module-load time, so we install a stub before the first
 * import. `useState` is backed by a single shared, mutable variable (not a
 * fresh value per call) so that mutations performed through a setter — from
 * any "render" — are visible to a later render's read of the state; this
 * mirrors the real Preact contract (setState updates are visible on the next
 * render) without needing a full renderer. `useCallback` is a passthrough
 * (identity/memoization isn't part of this hook's observable behavior);
 * `useRef` returns a fresh mutable box each call, which is fine because the
 * hook body unconditionally overwrites `.current` on every invocation.
 *
 * `getMCPInitState`/`clearMCPInit` returned by a given "render" only see
 * state as of THAT render for reads (statesRef.current is snapshotted at
 * call time), so tests re-invoke the hook (`freshApi()`) after an event to
 * get a binding that reflects the mutation — this matches how a real
 * consumer (app.js's useMemo) re-derives mcpInitState on every re-render.
 * Writes (event listeners, clearMCPInit) always route through the shared
 * setState and are correct regardless of which render's closure holds them.
 */

import {
  describe,
  test,
  expect,
  beforeEach,
  afterEach,
  jest,
} from "../utils/testing/testGlobals.js";

global.window = global.window || {};

let sharedState;
let stateInitialized;
let currentEffects = [];

window.preact = {
  ...(window.preact || {}),
  useState: (initial) => {
    if (!stateInitialized) {
      sharedState = typeof initial === "function" ? initial() : initial;
      stateInitialized = true;
    }
    const setState = (updater) => {
      sharedState =
        typeof updater === "function" ? updater(sharedState) : updater;
    };
    return [sharedState, setState];
  },
  useRef: (initial) => ({ current: initial }),
  useCallback: (fn) => fn,
  useEffect: (cb, deps) => {
    currentEffects.push({ cb, deps });
  },
};

let pendingCleanups = [];

async function loadHook() {
  const mod = await import("./useMCPInitState.js");
  return mod.useMCPInitState;
}

// Mounts the hook AND registers its window event listeners (run once). Use
// this exactly once per test to install listeners; use `rerender()` after to
// get a fresh, up-to-date `getMCPInitState`/`clearMCPInit` binding.
async function mount() {
  sharedState = undefined;
  stateInitialized = false;
  currentEffects = [];
  const useMCPInitState = await loadHook();
  const api = useMCPInitState();
  const cleanups = currentEffects
    .map(({ cb }) => cb())
    .filter((ret) => typeof ret === "function");
  pendingCleanups.push(...cleanups);
  return api;
}

// Re-invokes the hook WITHOUT re-registering listeners (avoids duplicate
// window listeners) to get an API bound to the current shared state.
async function rerender() {
  currentEffects = [];
  const useMCPInitState = await loadHook();
  return useMCPInitState();
}

beforeEach(() => {
  pendingCleanups = [];
});

afterEach(() => {
  for (const c of pendingCleanups) {
    try {
      c();
    } catch (_) {
      // ignore
    }
  }
  pendingCleanups = [];
  jest.useRealTimers();
  jest.restoreAllMocks();
});

describe("useMCPInitState — initial state", () => {
  test("getMCPInitState returns null when nothing has happened", async () => {
    const api = await mount();
    expect(api.getMCPInitState("ws1", "/a")).toBeNull();
  });

  test("getMCPInitState returns null when both identifiers are falsy", async () => {
    const api = await mount();
    expect(api.getMCPInitState(null, null)).toBeNull();
    expect(api.getMCPInitState(undefined, "")).toBeNull();
  });
});

describe("useMCPInitState — mitto:mcp_initializing", () => {
  test("sets initializing=true, keyed by workspace_uuid", async () => {
    await mount();
    window.dispatchEvent(
      new CustomEvent("mitto:mcp_initializing", {
        detail: { workspace_uuid: "ws1", working_dir: "/a" },
      }),
    );
    const api = await rerender();
    const state = api.getMCPInitState("ws1", "/a");
    expect(state).toMatchObject({
      initializing: true,
      timedOutAt: null,
      servers: [],
    });
    expect(typeof state.firstSeenAt).toBe("number");
  });

  test("falls back to working_dir as the key when workspace_uuid is absent", async () => {
    await mount();
    window.dispatchEvent(
      new CustomEvent("mitto:mcp_initializing", {
        detail: { working_dir: "/a" },
      }),
    );
    const api = await rerender();
    expect(api.getMCPInitState(null, "/a")).toMatchObject({
      initializing: true,
    });
    // A lookup that supplies a UUID (even for the same working_dir) resolves
    // to a DIFFERENT key than the working_dir-only key the event was stored
    // under — documents the single-key (not two-step) lookup behavior.
    expect(api.getMCPInitState("some-other-uuid", "/a")).toBeNull();
  });

  test("ignores events with no detail", async () => {
    await mount();
    expect(() =>
      window.dispatchEvent(new Event("mitto:mcp_initializing")),
    ).not.toThrow();
    const api = await rerender();
    expect(api.getMCPInitState(null, null)).toBeNull();
  });

  test("ignores events with no usable identifiers", async () => {
    await mount();
    window.dispatchEvent(
      new CustomEvent("mitto:mcp_initializing", { detail: {} }),
    );
    const api = await rerender();
    expect(api.getMCPInitState(null, null)).toBeNull();
  });
});

describe("useMCPInitState — mitto:mcp_init_timed_out", () => {
  test("sets a persistent timed-out entry naming the failed servers", async () => {
    await mount();
    window.dispatchEvent(
      new CustomEvent("mitto:mcp_init_timed_out", {
        detail: {
          workspace_uuid: "ws1",
          working_dir: "/a",
          mcp_servers: ["snow-cmr-automation", "splunk-mcp-ap"],
        },
      }),
    );
    const api = await rerender();
    const state = api.getMCPInitState("ws1", "/a");
    expect(state).toMatchObject({
      initializing: false,
      servers: ["snow-cmr-automation", "splunk-mcp-ap"],
    });
    expect(typeof state.timedOutAt).toBe("number");
  });

  test("defaults servers to [] when mcp_servers is absent (mitto-m8nx AC2 fallback)", async () => {
    await mount();
    window.dispatchEvent(
      new CustomEvent("mitto:mcp_init_timed_out", {
        detail: { workspace_uuid: "ws1", working_dir: "/a" },
      }),
    );
    const api = await rerender();
    expect(api.getMCPInitState("ws1", "/a").servers).toEqual([]);
  });

  test("transitions an initializing entry to timed-out for the same key", async () => {
    await mount();
    window.dispatchEvent(
      new CustomEvent("mitto:mcp_initializing", {
        detail: { workspace_uuid: "ws1", working_dir: "/a" },
      }),
    );
    window.dispatchEvent(
      new CustomEvent("mitto:mcp_init_timed_out", {
        detail: { workspace_uuid: "ws1", working_dir: "/a", mcp_servers: [] },
      }),
    );
    const api = await rerender();
    const state = api.getMCPInitState("ws1", "/a");
    expect(state.initializing).toBe(false);
    expect(state.timedOutAt).not.toBeNull();
  });

  test("a fresh mcp_initializing restarts the cycle after a timeout", async () => {
    await mount();
    window.dispatchEvent(
      new CustomEvent("mitto:mcp_init_timed_out", {
        detail: {
          workspace_uuid: "ws1",
          working_dir: "/a",
          mcp_servers: ["x"],
        },
      }),
    );
    window.dispatchEvent(
      new CustomEvent("mitto:mcp_initializing", {
        detail: { workspace_uuid: "ws1", working_dir: "/a" },
      }),
    );
    const api = await rerender();
    const state = api.getMCPInitState("ws1", "/a");
    expect(state).toMatchObject({
      initializing: true,
      timedOutAt: null,
      servers: [],
    });
  });

  test("ignores events with no detail", async () => {
    await mount();
    expect(() =>
      window.dispatchEvent(new Event("mitto:mcp_init_timed_out")),
    ).not.toThrow();
  });
});

describe("useMCPInitState — clearMCPInit", () => {
  test("removes an existing entry", async () => {
    const api = await mount();
    window.dispatchEvent(
      new CustomEvent("mitto:mcp_initializing", {
        detail: { workspace_uuid: "ws1", working_dir: "/a" },
      }),
    );
    expect((await rerender()).getMCPInitState("ws1", "/a")).not.toBeNull();

    api.clearMCPInit("ws1", "/a");
    expect((await rerender()).getMCPInitState("ws1", "/a")).toBeNull();
  });

  test("no-ops when the key is not present", async () => {
    const api = await mount();
    expect(() => api.clearMCPInit("nope", "/nowhere")).not.toThrow();
  });

  test("no-ops when both identifiers are falsy", async () => {
    const api = await mount();
    expect(() => api.clearMCPInit(null, null)).not.toThrow();
  });
});

describe("useMCPInitState — safety sweep", () => {
  test("does not clear a recent entry", async () => {
    jest.useFakeTimers();
    const nowSpy = jest.spyOn(Date, "now");
    let time = 0;
    nowSpy.mockImplementation(() => time);

    await mount();
    window.dispatchEvent(
      new CustomEvent("mitto:mcp_initializing", {
        detail: { workspace_uuid: "ws1", working_dir: "/a" },
      }),
    );

    time += 30 * 1000; // one sweep tick, well under the 10-minute cap
    jest.advanceTimersByTime(30 * 1000);

    const api = await rerender();
    expect(api.getMCPInitState("ws1", "/a")).not.toBeNull();
  });

  test("clears an entry once it exceeds the 10-minute safety cap", async () => {
    jest.useFakeTimers();
    const nowSpy = jest.spyOn(Date, "now");
    let time = 0;
    nowSpy.mockImplementation(() => time);

    await mount();
    window.dispatchEvent(
      new CustomEvent("mitto:mcp_initializing", {
        detail: { workspace_uuid: "ws1", working_dir: "/a" },
      }),
    );

    // Advance past the 10-minute cap, then let a sweep tick (30s) observe it.
    time += 11 * 60 * 1000;
    jest.advanceTimersByTime(11 * 60 * 1000);

    const api = await rerender();
    expect(api.getMCPInitState("ws1", "/a")).toBeNull();
  });
});

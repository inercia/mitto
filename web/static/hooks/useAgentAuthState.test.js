/**
 * Tests for useAgentAuthState.js (mitto-3du).
 *
 * Mirrors useMCPInitState.test.js's harness: `useState` is backed by a single
 * shared, mutable variable so setter mutations from any "render" are visible
 * on a later render's read (matching Preact's real contract without a full
 * renderer); `useCallback` is a passthrough; `useRef` returns a fresh box
 * each call since the hook body unconditionally overwrites `.current`.
 *
 * `getAgentAuthState`/`getAgentAuthStateForWorkingDir` returned by a given
 * "render" only see state as of THAT render (statesRef.current is
 * snapshotted at call time), so tests re-invoke the hook (`rerender()`)
 * after an event to get a binding reflecting the mutation.
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
  const mod = await import("./useAgentAuthState.js");
  return mod.useAgentAuthState;
}

async function mount() {
  sharedState = undefined;
  stateInitialized = false;
  currentEffects = [];
  const useAgentAuthState = await loadHook();
  const api = useAgentAuthState();
  const cleanups = currentEffects
    .map(({ cb }) => cb())
    .filter((ret) => typeof ret === "function");
  pendingCleanups.push(...cleanups);
  return api;
}

async function rerender() {
  currentEffects = [];
  const useAgentAuthState = await loadHook();
  return useAgentAuthState();
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

describe("useAgentAuthState — initial state", () => {
  test("getAgentAuthState returns null when nothing has happened", async () => {
    const api = await mount();
    expect(api.getAgentAuthState("ws1", "/a")).toBeNull();
  });

  test("getAgentAuthState returns null when both identifiers are falsy", async () => {
    const api = await mount();
    expect(api.getAgentAuthState(null, null)).toBeNull();
    expect(api.getAgentAuthState(undefined, "")).toBeNull();
  });

  test("getAgentAuthStateForWorkingDir returns null for an unknown working_dir", async () => {
    const api = await mount();
    expect(api.getAgentAuthStateForWorkingDir("/nowhere")).toBeNull();
    expect(api.getAgentAuthStateForWorkingDir(null)).toBeNull();
  });
});

describe("useAgentAuthState — mitto:agent_auth_required", () => {
  test("sets required=true, keyed by workspace_uuid", async () => {
    await mount();
    window.dispatchEvent(
      new CustomEvent("mitto:agent_auth_required", {
        detail: { workspace_uuid: "ws1", working_dir: "/a" },
      }),
    );
    const api = await rerender();
    const state = api.getAgentAuthState("ws1", "/a");
    expect(state).toMatchObject({
      required: true,
      workspaceUUID: "ws1",
      workingDir: "/a",
    });
    expect(typeof state.firstSeenAt).toBe("number");
  });

  test("falls back to working_dir as the key when workspace_uuid is absent", async () => {
    await mount();
    window.dispatchEvent(
      new CustomEvent("mitto:agent_auth_required", {
        detail: { working_dir: "/a" },
      }),
    );
    const api = await rerender();
    expect(api.getAgentAuthState(null, "/a")).toMatchObject({
      required: true,
    });
    // A lookup that supplies a different UUID resolves to a different key
    // than the working_dir-only key the event was stored under.
    expect(api.getAgentAuthState("some-other-uuid", "/a")).toBeNull();
  });

  test("getAgentAuthStateForWorkingDir finds the entry regardless of its key", async () => {
    await mount();
    window.dispatchEvent(
      new CustomEvent("mitto:agent_auth_required", {
        detail: { workspace_uuid: "ws1", working_dir: "/a" },
      }),
    );
    const api = await rerender();
    // Folder-group lookup: keyed by workspace_uuid internally, found by
    // scanning stored working_dir values (mitto-3du decision #3).
    expect(api.getAgentAuthStateForWorkingDir("/a")).toMatchObject({
      required: true,
    });
    expect(api.getAgentAuthStateForWorkingDir("/other")).toBeNull();
  });

  test("ignores events with no detail", async () => {
    await mount();
    expect(() =>
      window.dispatchEvent(new Event("mitto:agent_auth_required")),
    ).not.toThrow();
    const api = await rerender();
    expect(api.getAgentAuthState(null, null)).toBeNull();
  });

  test("ignores events with no usable identifiers", async () => {
    await mount();
    window.dispatchEvent(
      new CustomEvent("mitto:agent_auth_required", { detail: {} }),
    );
    const api = await rerender();
    expect(api.getAgentAuthStateForWorkingDir(null)).toBeNull();
  });
});

describe("useAgentAuthState — mitto:agent_auth_cleared", () => {
  test("removes an existing required entry", async () => {
    await mount();
    window.dispatchEvent(
      new CustomEvent("mitto:agent_auth_required", {
        detail: { workspace_uuid: "ws1", working_dir: "/a" },
      }),
    );
    expect(
      (await rerender()).getAgentAuthState("ws1", "/a"),
    ).not.toBeNull();

    window.dispatchEvent(
      new CustomEvent("mitto:agent_auth_cleared", {
        detail: { workspace_uuid: "ws1", working_dir: "/a" },
      }),
    );
    expect((await rerender()).getAgentAuthState("ws1", "/a")).toBeNull();
  });

  test("no-ops for a key that was never required", async () => {
    await mount();
    expect(() =>
      window.dispatchEvent(
        new CustomEvent("mitto:agent_auth_cleared", {
          detail: { workspace_uuid: "nope", working_dir: "/nowhere" },
        }),
      ),
    ).not.toThrow();
    const api = await rerender();
    expect(api.getAgentAuthState("nope", "/nowhere")).toBeNull();
  });

  test("ignores events with no detail", async () => {
    await mount();
    expect(() =>
      window.dispatchEvent(new Event("mitto:agent_auth_cleared")),
    ).not.toThrow();
  });
});

describe("useAgentAuthState — clearAgentAuth", () => {
  test("removes an existing entry", async () => {
    const api = await mount();
    window.dispatchEvent(
      new CustomEvent("mitto:agent_auth_required", {
        detail: { workspace_uuid: "ws1", working_dir: "/a" },
      }),
    );
    expect(
      (await rerender()).getAgentAuthState("ws1", "/a"),
    ).not.toBeNull();

    api.clearAgentAuth("ws1", "/a");
    expect((await rerender()).getAgentAuthState("ws1", "/a")).toBeNull();
  });

  test("no-ops when the key is not present", async () => {
    const api = await mount();
    expect(() => api.clearAgentAuth("nope", "/nowhere")).not.toThrow();
  });

  test("no-ops when both identifiers are falsy", async () => {
    const api = await mount();
    expect(() => api.clearAgentAuth(null, null)).not.toThrow();
  });
});

describe("useAgentAuthState — safety sweep", () => {
  test("does not clear a recent entry", async () => {
    jest.useFakeTimers();
    const nowSpy = jest.spyOn(Date, "now");
    let time = 0;
    nowSpy.mockImplementation(() => time);

    await mount();
    window.dispatchEvent(
      new CustomEvent("mitto:agent_auth_required", {
        detail: { workspace_uuid: "ws1", working_dir: "/a" },
      }),
    );

    time += 60 * 1000; // one sweep tick, well under the 1-hour cap
    jest.advanceTimersByTime(60 * 1000);

    const api = await rerender();
    expect(api.getAgentAuthState("ws1", "/a")).not.toBeNull();
  });

  test("clears an entry once it exceeds the 1-hour safety cap", async () => {
    jest.useFakeTimers();
    const nowSpy = jest.spyOn(Date, "now");
    let time = 0;
    nowSpy.mockImplementation(() => time);

    await mount();
    window.dispatchEvent(
      new CustomEvent("mitto:agent_auth_required", {
        detail: { workspace_uuid: "ws1", working_dir: "/a" },
      }),
    );

    // Advance past the 1-hour cap, then let a sweep tick (60s) observe it.
    time += 61 * 60 * 1000;
    jest.advanceTimersByTime(61 * 60 * 1000);

    const api = await rerender();
    expect(api.getAgentAuthState("ws1", "/a")).toBeNull();
  });
});

/**
 * Unit tests for useWorkspacesStore.js (mitto-sus.11): the hook wrappers
 * around stores/workspacesStore.js (global) and stores/configOptionsStore.js
 * (per-session).
 *
 * useWorkspacesStore.js destructures `useState`/`useEffect` from
 * `window.preact` at module-load time, so (following useQueue.test.js /
 * useSessionsStore.test.js) window.preact is stubbed with a small
 * call-index-based hook engine BEFORE importing the module. Each test uses
 * its own `instance` object so mounting several "components" doesn't share
 * state -- `render(instance, hookFn, ...args)` re-invokes the hook body
 * against that instance and returns the current value, mirroring how a
 * real re-render would surface a subscribe callback's setState mutation.
 */

import {
  describe,
  test,
  expect,
  beforeEach,
} from "../utils/testing/testGlobals.js";

import {
  setWorkspaces,
  setAcpServers,
  _resetWorkspacesStoreForTests,
} from "../stores/workspacesStore.js";
import {
  setConfigOptions,
  _resetConfigOptionsStoreForTests,
} from "../stores/configOptionsStore.js";

global.window = global.window || {};

function depsEqual(a, b) {
  if (!Array.isArray(a) || !Array.isArray(b) || a.length !== b.length) {
    return false;
  }
  return a.every((v, i) => Object.is(v, b[i]));
}

let current;

window.preact = {
  ...(window.preact || {}),
  useState: (initial) => {
    const inst = current;
    if (!inst.stateInitialized) {
      inst.state = typeof initial === "function" ? initial() : initial;
      inst.stateInitialized = true;
    }
    const setState = (next) => {
      inst.state = typeof next === "function" ? next(inst.state) : next;
    };
    return [inst.state, setState];
  },
  useEffect: (cb, deps) => {
    const inst = current;
    const changed = !inst.effectRan || !depsEqual(inst.effectDeps, deps);
    if (changed) {
      if (typeof inst.cleanup === "function") inst.cleanup();
      inst.cleanup = cb() || null;
      inst.effectDeps = deps;
      inst.effectRan = true;
    }
  },
};

function makeInstance() {
  return {
    state: undefined,
    stateInitialized: false,
    effectRan: false,
    effectDeps: undefined,
    cleanup: null,
  };
}

function render(inst, hookFn, ...args) {
  current = inst;
  const result = hookFn(...args);
  current = null;
  return result;
}

async function loadHooks() {
  return import("./useWorkspacesStore.js");
}

beforeEach(() => {
  _resetWorkspacesStoreForTests();
  _resetConfigOptionsStoreForTests();
});

describe("useWorkspacesStore hooks (mitto-sus.11) > initial mount", () => {
  test("useWorkspaces / useAcpServers return [] before any fetch", async () => {
    const { useWorkspaces, useAcpServers } = await loadHooks();
    const w = makeInstance();
    const a = makeInstance();
    expect(render(w, useWorkspaces)).toEqual([]);
    expect(render(a, useAcpServers)).toEqual([]);
  });

  test("useConfigOptions returns [] for a falsy or unset session id", async () => {
    const { useConfigOptions } = await loadHooks();
    const unset = makeInstance();
    const falsy = makeInstance();
    expect(render(unset, useConfigOptions, "s1")).toEqual([]);
    expect(render(falsy, useConfigOptions, null)).toEqual([]);
  });

  test("mounting after the stores are already populated returns the current value immediately", async () => {
    setWorkspaces([{ uuid: "w1" }]);
    setConfigOptions("s1", [{ id: "model" }]);
    const { useWorkspaces, useConfigOptions } = await loadHooks();
    const w = makeInstance();
    const c = makeInstance();
    expect(render(w, useWorkspaces)).toEqual([{ uuid: "w1" }]);
    expect(render(c, useConfigOptions, "s1")).toEqual([{ id: "model" }]);
  });
});

describe("useWorkspacesStore hooks (mitto-sus.11) > live updates via subscription", () => {
  test("a later setWorkspaces is reflected on the next render", async () => {
    const { useWorkspaces } = await loadHooks();
    const inst = makeInstance();
    render(inst, useWorkspaces);

    setWorkspaces([{ uuid: "w1" }]);

    expect(render(inst, useWorkspaces)).toEqual([{ uuid: "w1" }]);
  });

  test("setAcpServers does not affect a useWorkspaces subscriber's value", async () => {
    const { useWorkspaces } = await loadHooks();
    const inst = makeInstance();
    render(inst, useWorkspaces);

    setAcpServers([{ name: "mock-acp" }]);

    expect(render(inst, useWorkspaces)).toEqual([]);
  });

  test("a setConfigOptions for a DIFFERENT session id does not affect this hook's value", async () => {
    const { useConfigOptions } = await loadHooks();
    const inst = makeInstance();
    render(inst, useConfigOptions, "s1");

    setConfigOptions("s2", [{ id: "model" }]);

    expect(render(inst, useConfigOptions, "s1")).toEqual([]);
  });
});

describe("useWorkspacesStore hooks (mitto-sus.11) > session-id switch resubscribes cleanly", () => {
  test("switching sessionId tears down the old subscription and adopts the new session's value", async () => {
    const { useConfigOptions } = await loadHooks();
    setConfigOptions("s1", [{ id: "model", current_value: "sonnet" }]);
    setConfigOptions("s2", [{ id: "model", current_value: "opus" }]);
    const inst = makeInstance();

    render(inst, useConfigOptions, "s1");
    expect(inst.state).toEqual([{ id: "model", current_value: "sonnet" }]);

    render(inst, useConfigOptions, "s2");
    expect(inst.state).toEqual([{ id: "model", current_value: "opus" }]);

    // Further updates to the now-abandoned session s1 must not leak in.
    setConfigOptions("s1", [{ id: "model", current_value: "haiku" }]);
    expect(inst.state).toEqual([{ id: "model", current_value: "opus" }]);
  });

  test("switching from a falsy id to a real one resets to [] then adopts the session's value", async () => {
    const { useConfigOptions } = await loadHooks();
    setConfigOptions("s1", [{ id: "model" }]);
    const inst = makeInstance();

    render(inst, useConfigOptions, null);
    expect(inst.state).toEqual([]);

    render(inst, useConfigOptions, "s1");
    expect(inst.state).toEqual([{ id: "model" }]);
  });
});

describe("useWorkspacesStore hooks (mitto-sus.11) > unmount stops notifications", () => {
  test("calling the effect cleanup unsubscribes; later store writes no longer mutate state", async () => {
    const { useWorkspaces } = await loadHooks();
    const inst = makeInstance();
    render(inst, useWorkspaces);

    inst.cleanup();
    setWorkspaces([{ uuid: "w1" }]);

    expect(inst.state).toEqual([]);
  });
});

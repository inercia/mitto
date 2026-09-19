/**
 * Unit tests for useSessionsStore.js (mitto-sus.7): the per-slice hook
 * wrappers around stores/sessionsStore.js.
 *
 * useSessionsStore.js destructures `useState`/`useEffect` from
 * `window.preact` at module-load time, so (following useAgentAuthState.
 * test.js / useSwipeToDelete.test.js) window.preact is stubbed with a small
 * call-index-based hook engine BEFORE importing the module. Each test uses
 * its own `instance` object so mounting several "components" (e.g. two
 * different session ids, or simulating unmount) doesn't share state --
 * `render(instance, hookFn, ...args)` re-invokes the hook body against that
 * instance and returns the current value, mirroring how a real re-render
 * would surface a setState mutation made by a store's subscribe callback.
 */

import {
  describe,
  test,
  expect,
  beforeEach,
} from "../utils/testing/testGlobals.js";

import { replaceAll, _resetSessionsStoreForTests } from "../stores/sessionsStore.js";

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
  return import("./useSessionsStore.js");
}

beforeEach(() => {
  _resetSessionsStoreForTests();
});

describe("useSessionsStore hooks (mitto-sus.7) > initial mount", () => {
  test("useActiveSessionMessages returns [] when the session has no messages yet", async () => {
    const { useActiveSessionMessages } = await loadHooks();
    const inst = makeInstance();
    expect(render(inst, useActiveSessionMessages, "s1")).toEqual([]);
  });

  test("useSessionSummary/useSessionInfo/useSessionKeepalive return null for a falsy session id", async () => {
    const { useSessionSummary, useSessionInfo, useSessionKeepalive } =
      await loadHooks();
    const s = makeInstance();
    const i = makeInstance();
    const k = makeInstance();
    expect(render(s, useSessionSummary, null)).toBeNull();
    expect(render(i, useSessionInfo, null)).toBeNull();
    expect(render(k, useSessionKeepalive, null)).toBeNull();
  });

  test("mounting after the store is already populated returns the current value immediately", async () => {
    replaceAll({ s1: { messages: ["a", "b"] } });
    const { useActiveSessionMessages } = await loadHooks();
    const inst = makeInstance();
    expect(render(inst, useActiveSessionMessages, "s1")).toEqual(["a", "b"]);
  });
});

describe("useSessionsStore hooks (mitto-sus.7) > live updates via subscription", () => {
  test("a later replaceAll for the same session is reflected on the next render", async () => {
    const { useActiveSessionMessages } = await loadHooks();
    const inst = makeInstance();
    render(inst, useActiveSessionMessages, "s1");

    replaceAll({ s1: { messages: ["new"] } });

    expect(render(inst, useActiveSessionMessages, "s1")).toEqual(["new"]);
  });

  test("a replaceAll for a DIFFERENT session id does not affect this hook's value", async () => {
    const { useActiveSessionMessages } = await loadHooks();
    const inst = makeInstance();
    render(inst, useActiveSessionMessages, "s1");

    replaceAll({ s1: { messages: [] }, s2: { messages: ["other"] } });

    expect(render(inst, useActiveSessionMessages, "s1")).toEqual([]);
  });
});

describe("useSessionsStore hooks (mitto-sus.7) > session-id switch resubscribes cleanly", () => {
  test("switching sessionId tears down the old subscription and adopts the new session's value", async () => {
    const { useActiveSessionMessages } = await loadHooks();
    replaceAll({ s1: { messages: ["s1-msg"] }, s2: { messages: ["s2-msg"] } });
    const inst = makeInstance();

    render(inst, useActiveSessionMessages, "s1");
    expect(inst.state).toEqual(["s1-msg"]);

    // Simulate the active conversation changing.
    render(inst, useActiveSessionMessages, "s2");
    expect(inst.state).toEqual(["s2-msg"]);

    // Further updates to the now-abandoned session s1 must not leak in.
    replaceAll({
      s1: { messages: ["s1-updated"] },
      s2: { messages: ["s2-msg"] },
    });
    expect(inst.state).toEqual(["s2-msg"]);

    // But updates to the newly-active s2 still flow through.
    replaceAll({
      s1: { messages: ["s1-updated"] },
      s2: { messages: ["s2-updated"] },
    });
    expect(inst.state).toEqual(["s2-updated"]);
  });
});

describe("useSessionsStore hooks (mitto-sus.7) > unmount stops notifications", () => {
  test("calling the effect cleanup unsubscribes; later store writes no longer mutate state", async () => {
    const { useActiveSessionMessages } = await loadHooks();
    const inst = makeInstance();
    render(inst, useActiveSessionMessages, "s1");

    inst.cleanup();
    replaceAll({ s1: { messages: ["after-unmount"] } });

    expect(inst.state).toEqual([]);
  });
});

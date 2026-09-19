/**
 * Unit tests for useQueue.js (mitto-sus.11): the per-slice hook wrappers
 * around stores/queueStore.js.
 *
 * useQueue.js destructures `useState`/`useEffect` from `window.preact` at
 * module-load time, so (following useSessionsStore.test.js) window.preact
 * is stubbed with a small call-index-based hook engine BEFORE importing the
 * module. Each test uses its own `instance` object so mounting several
 * "components" doesn't share state -- `render(instance, hookFn, ...args)`
 * re-invokes the hook body against that instance and returns the current
 * value, mirroring how a real re-render would surface a subscribe
 * callback's setState mutation.
 */

import {
  describe,
  test,
  expect,
  beforeEach,
} from "../utils/testing/testGlobals.js";

import {
  setMessages,
  setLength,
  setConfig,
  DEFAULT_QUEUE_CONFIG,
  _resetQueueStoreForTests,
} from "../stores/queueStore.js";

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
  return import("./useQueue.js");
}

beforeEach(() => {
  _resetQueueStoreForTests();
});

describe("useQueue hooks (mitto-sus.11) > initial mount", () => {
  test("useQueueMessages returns [] and useQueueLength returns 0 for an unset session", async () => {
    const { useQueueMessages, useQueueLength } = await loadHooks();
    const m = makeInstance();
    const l = makeInstance();
    expect(render(m, useQueueMessages, "s1")).toEqual([]);
    expect(render(l, useQueueLength, "s1")).toBe(0);
  });

  test("useQueueConfig returns the default config for an unset session", async () => {
    const { useQueueConfig } = await loadHooks();
    const inst = makeInstance();
    expect(render(inst, useQueueConfig, "s1")).toEqual(DEFAULT_QUEUE_CONFIG);
  });

  test("hooks return undefined-derived defaults for a falsy session id", async () => {
    const { useQueueMessages, useQueueLength, useQueueConfig } =
      await loadHooks();
    const m = makeInstance();
    const l = makeInstance();
    const c = makeInstance();
    expect(render(m, useQueueMessages, null)).toEqual([]);
    expect(render(l, useQueueLength, null)).toBe(0);
    expect(render(c, useQueueConfig, null)).toEqual(DEFAULT_QUEUE_CONFIG);
  });

  test("mounting after the store is already populated returns the current value immediately", async () => {
    setMessages("s1", [{ id: "q1" }]);
    const { useQueueMessages } = await loadHooks();
    const inst = makeInstance();
    expect(render(inst, useQueueMessages, "s1")).toEqual([{ id: "q1" }]);
  });
});

describe("useQueue hooks (mitto-sus.11) > live updates via subscription", () => {
  test("a later setLength for the same session is reflected on the next render", async () => {
    const { useQueueLength } = await loadHooks();
    const inst = makeInstance();
    render(inst, useQueueLength, "s1");

    setLength("s1", 3);

    expect(render(inst, useQueueLength, "s1")).toBe(3);
  });

  test("a setLength for a DIFFERENT session id does not affect this hook's value", async () => {
    const { useQueueLength } = await loadHooks();
    const inst = makeInstance();
    render(inst, useQueueLength, "s1");

    setLength("s2", 9);

    expect(render(inst, useQueueLength, "s1")).toBe(0);
  });

  test("setConfig updates useQueueConfig's live value", async () => {
    const { useQueueConfig } = await loadHooks();
    const inst = makeInstance();
    render(inst, useQueueConfig, "s1");

    const next = { enabled: false, max_size: 20, delay_seconds: 5 };
    setConfig("s1", next);

    expect(render(inst, useQueueConfig, "s1")).toEqual(next);
  });
});

describe("useQueue hooks (mitto-sus.11) > session-id switch resubscribes cleanly", () => {
  test("switching sessionId tears down the old subscription and adopts the new session's value", async () => {
    const { useQueueLength } = await loadHooks();
    setLength("s1", 1);
    setLength("s2", 2);
    const inst = makeInstance();

    render(inst, useQueueLength, "s1");
    expect(inst.state).toBe(1);

    render(inst, useQueueLength, "s2");
    expect(inst.state).toBe(2);

    // Further updates to the now-abandoned session s1 must not leak in.
    setLength("s1", 99);
    expect(inst.state).toBe(2);
  });
});

describe("useQueue hooks (mitto-sus.11) > unmount stops notifications", () => {
  test("calling the effect cleanup unsubscribes; later store writes no longer mutate state", async () => {
    const { useQueueLength } = await loadHooks();
    const inst = makeInstance();
    render(inst, useQueueLength, "s1");

    inst.cleanup();
    setLength("s1", 42);

    expect(inst.state).toBe(0);
  });
});

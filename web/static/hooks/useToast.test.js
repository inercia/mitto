/**
 * Unit tests for useToast.js (mitto-sus.11): the hook wrappers around
 * stores/notificationsStore.js's toast stack.
 *
 * `useToast()` returns the module-level `showToast`/`dismissToast`
 * functions directly (no useState/useEffect involved) -- these must be the
 * exact same function references on every call, which is what lets callers
 * pass them into a `useCallback` dep array without ever forcing a
 * recreation. `useToasts()` is the one hook that actually subscribes to
 * the live list, so it follows the small call-index-based hook engine used
 * by useSessionsStore.test.js / useAgentAuthState.test.js.
 */

import {
  describe,
  test,
  expect,
  beforeEach,
} from "../utils/testing/testGlobals.js";

import {
  showToast,
  dismissToast,
  _resetNotificationsStoreForTests,
} from "../stores/notificationsStore.js";

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

async function loadHook() {
  return import("./useToast.js");
}

beforeEach(() => {
  _resetNotificationsStoreForTests();
});

describe("useToast (mitto-sus.11) > useToast()", () => {
  test("returns the module-level showToast/dismissToast function references", async () => {
    const { useToast } = await loadHook();
    const { showToast: hookShow, dismissToast: hookDismiss } = useToast();

    expect(hookShow).toBe(showToast);
    expect(hookDismiss).toBe(dismissToast);
  });

  test("returns the exact same references across repeated calls (stable identity)", async () => {
    const { useToast } = await loadHook();
    const first = useToast();
    const second = useToast();

    expect(second.showToast).toBe(first.showToast);
    expect(second.dismissToast).toBe(first.dismissToast);
  });
});

describe("useToast (mitto-sus.11) > useToasts()", () => {
  test("returns [] before any toast is shown", async () => {
    const { useToasts } = await loadHook();
    const inst = makeInstance();

    expect(render(inst, useToasts)).toEqual([]);
  });

  test("reflects a toast shown before mount", async () => {
    showToast({ title: "pre-mount" });
    const { useToasts } = await loadHook();
    const inst = makeInstance();

    const toasts = render(inst, useToasts);

    expect(toasts).toHaveLength(1);
    expect(toasts[0]).toMatchObject({ title: "pre-mount" });
  });

  test("a later showToast is reflected on the next render via the subscription", async () => {
    const { useToasts } = await loadHook();
    const inst = makeInstance();
    render(inst, useToasts);

    showToast({ title: "after-mount" });

    const toasts = render(inst, useToasts);
    expect(toasts).toHaveLength(1);
    expect(toasts[0]).toMatchObject({ title: "after-mount" });
  });

  test("dismissing a toast is reflected on the next render", async () => {
    const { useToasts } = await loadHook();
    const inst = makeInstance();
    render(inst, useToasts);

    const id = showToast({ title: "to-dismiss" });
    render(inst, useToasts);
    dismissToast(id);

    expect(render(inst, useToasts)).toEqual([]);
  });

  test("unmount (effect cleanup) unsubscribes; later showToast calls no longer mutate state", async () => {
    const { useToasts } = await loadHook();
    const inst = makeInstance();
    render(inst, useToasts);

    inst.cleanup();
    showToast({ title: "post-unmount" });

    expect(inst.state).toEqual([]);
  });
});

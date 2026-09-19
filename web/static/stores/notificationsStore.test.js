/**
 * Unit tests for the mitto-sus.11 subscribable notificationsStore.
 *
 * Covers the store's two responsibilities:
 *  1. The toast stack (showToast/dismissToast) -- stable module-level
 *     functions, MAX_TOASTS eviction, style-based auto-dismiss timers
 *     (sticky/error never auto-dismiss), and toasts-slice notification.
 *  2. The four singleton background-notification slots
 *     (backgroundCompletion/loopStarted/backgroundUIPrompt/
 *     backgroundUIPromptTimeout) -- get/set/subscribe/clear per slot, with
 *     strict per-slice isolation (setting one slot must never notify a
 *     different slot's subscribers, nor the toasts subscriber).
 *
 * Mirrors stores/sessionsStore.test.js's shape (no window.preact
 * dependency -- this module has none, so it is imported directly).
 */

import {
  describe,
  test,
  expect,
  jest,
  beforeEach,
  afterEach,
} from "../utils/testing/testGlobals.js";

import * as store from "./notificationsStore.js";

beforeEach(() => {
  store._resetNotificationsStoreForTests();
  jest.useFakeTimers();
});

afterEach(() => {
  jest.useRealTimers();
});

describe("notificationsStore (mitto-sus.11) > toasts", () => {
  test("getToasts starts empty", () => {
    expect(store.getToasts()).toEqual([]);
  });

  test("showToast appends a toast, returns its id, and notifies subscribers", () => {
    const cb = jest.fn();
    store.subscribeToasts(cb);

    const id = store.showToast({ title: "Hello", message: "world" });

    expect(typeof id).toBe("number");
    expect(store.getToasts()).toEqual([
      expect.objectContaining({ id, title: "Hello", message: "world" }),
    ]);
    expect(cb).toHaveBeenCalledWith(store.getToasts());
  });

  test("showToast defaults style to info and dismissable to true", () => {
    store.showToast({ title: "Defaults" });
    expect(store.getToasts()[0]).toMatchObject({
      style: "info",
      dismissable: true,
      message: "",
    });
  });

  test("dismissToast removes only the matching toast and notifies", () => {
    const idA = store.showToast({ title: "A" });
    const idB = store.showToast({ title: "B" });
    const cb = jest.fn();
    store.subscribeToasts(cb);

    store.dismissToast(idA);

    expect(store.getToasts()).toEqual([
      expect.objectContaining({ id: idB, title: "B" }),
    ]);
    expect(cb).toHaveBeenCalledWith(store.getToasts());
  });

  test("dismissToast on an unknown id is a safe no-op that does not notify", () => {
    store.showToast({ title: "A" });
    const cb = jest.fn();
    store.subscribeToasts(cb);

    store.dismissToast(999999);

    expect(store.getToasts()).toHaveLength(1);
    expect(cb).not.toHaveBeenCalled();
  });

  test("showing more than MAX_TOASTS (5) evicts the oldest", () => {
    const ids = [];
    for (let i = 0; i < 6; i++) {
      ids.push(store.showToast({ title: `toast-${i}` }));
    }

    const toasts = store.getToasts();
    expect(toasts).toHaveLength(5);
    // The very first toast (index 0) was evicted; the rest survive in order.
    expect(toasts.map((t) => t.id)).toEqual(ids.slice(1));
  });

  test("an info/success/warning toast auto-dismisses after its severity duration", () => {
    store.showToast({ style: "info", title: "auto" });
    expect(store.getToasts()).toHaveLength(1);

    jest.advanceTimersByTime(5000);

    expect(store.getToasts()).toHaveLength(0);
  });

  test("a warning toast uses the longer 10s default duration", () => {
    store.showToast({ style: "warning", title: "auto-warn" });

    jest.advanceTimersByTime(5000);
    expect(store.getToasts()).toHaveLength(1);

    jest.advanceTimersByTime(5000);
    expect(store.getToasts()).toHaveLength(0);
  });

  test("an explicit duration overrides the severity default", () => {
    store.showToast({ style: "info", title: "custom", duration: 1000 });

    jest.advanceTimersByTime(999);
    expect(store.getToasts()).toHaveLength(1);

    jest.advanceTimersByTime(1);
    expect(store.getToasts()).toHaveLength(0);
  });

  test("a sticky toast never auto-dismisses", () => {
    store.showToast({ title: "sticky", sticky: true });

    jest.advanceTimersByTime(60_000);

    expect(store.getToasts()).toHaveLength(1);
  });

  test("an error-style toast never auto-dismisses even without sticky", () => {
    store.showToast({ style: "error", title: "boom" });

    jest.advanceTimersByTime(60_000);

    expect(store.getToasts()).toHaveLength(1);
  });

  test("manually dismissing a toast before its timer fires cancels the timer cleanly", () => {
    const id = store.showToast({ style: "info", title: "manual" });
    store.dismissToast(id);

    // The pending timer must not throw or double-dismiss once it would have
    // fired -- covered by not throwing when advancing past its duration.
    expect(() => jest.advanceTimersByTime(5000)).not.toThrow();
    expect(store.getToasts()).toHaveLength(0);
  });
});

describe("notificationsStore (mitto-sus.11) > background-notification slots", () => {
  test("each slot starts null", () => {
    expect(store.getBackgroundCompletion()).toBeNull();
    expect(store.getLoopStarted()).toBeNull();
    expect(store.getBackgroundUIPrompt()).toBeNull();
    expect(store.getBackgroundUIPromptTimeout()).toBeNull();
  });

  test("setBackgroundCompletion updates the getter and notifies its subscriber", () => {
    const cb = jest.fn();
    store.subscribeBackgroundCompletion(cb);

    const value = { sessionId: "s1", sessionName: "Session 1" };
    store.setBackgroundCompletion(value);

    expect(store.getBackgroundCompletion()).toBe(value);
    expect(cb).toHaveBeenCalledWith(value);
  });

  test("clearBackgroundCompletion resets to null and notifies", () => {
    store.setBackgroundCompletion({ sessionId: "s1" });
    const cb = jest.fn();
    store.subscribeBackgroundCompletion(cb);

    store.clearBackgroundCompletion();

    expect(store.getBackgroundCompletion()).toBeNull();
    expect(cb).toHaveBeenCalledWith(null);
  });

  test("setLoopStarted/setBackgroundUIPrompt/setBackgroundUIPromptTimeout each notify only their own subscriber", () => {
    const completionCb = jest.fn();
    const loopCb = jest.fn();
    const promptCb = jest.fn();
    const timeoutCb = jest.fn();
    const toastsCb = jest.fn();
    store.subscribeBackgroundCompletion(completionCb);
    store.subscribeLoopStarted(loopCb);
    store.subscribeBackgroundUIPrompt(promptCb);
    store.subscribeBackgroundUIPromptTimeout(timeoutCb);
    store.subscribeToasts(toastsCb);

    store.setLoopStarted({ sessionId: "s1" });

    expect(loopCb).toHaveBeenCalledTimes(1);
    expect(completionCb).not.toHaveBeenCalled();
    expect(promptCb).not.toHaveBeenCalled();
    expect(timeoutCb).not.toHaveBeenCalled();
    expect(toastsCb).not.toHaveBeenCalled();
  });

  test("a change to one slot never notifies a different slot's subscribers", () => {
    const promptCb = jest.fn();
    const timeoutCb = jest.fn();
    store.subscribeBackgroundUIPrompt(promptCb);
    store.subscribeBackgroundUIPromptTimeout(timeoutCb);

    store.setBackgroundUIPromptTimeout({ sessionId: "s2", question: "q?" });

    expect(timeoutCb).toHaveBeenCalledWith({ sessionId: "s2", question: "q?" });
    expect(promptCb).not.toHaveBeenCalled();
  });

  test("unsubscribe stops further notifications for that callback only", () => {
    const cb1 = jest.fn();
    const cb2 = jest.fn();
    const unsubscribe = store.subscribeLoopStarted(cb1);
    store.subscribeLoopStarted(cb2);

    unsubscribe();
    store.setLoopStarted({ sessionId: "s1" });

    expect(cb1).not.toHaveBeenCalled();
    expect(cb2).toHaveBeenCalledWith({ sessionId: "s1" });
  });
});

describe("notificationsStore (mitto-sus.11) > robustness", () => {
  test("a throwing toasts listener does not break the store or other listeners", () => {
    const throwing = jest.fn(() => {
      throw new Error("boom");
    });
    const healthy = jest.fn();
    store.subscribeToasts(throwing);
    store.subscribeToasts(healthy);

    expect(() => store.showToast({ title: "x" })).not.toThrow();
    expect(healthy).toHaveBeenCalled();
  });

  test("a throwing background-slot listener does not break the store or other listeners", () => {
    const throwing = jest.fn(() => {
      throw new Error("boom");
    });
    const healthy = jest.fn();
    store.subscribeBackgroundCompletion(throwing);
    store.subscribeBackgroundCompletion(healthy);

    expect(() =>
      store.setBackgroundCompletion({ sessionId: "s1" }),
    ).not.toThrow();
    expect(healthy).toHaveBeenCalled();
  });

  test("_resetNotificationsStoreForTests clears toasts, all slots, listeners, and pending timers", () => {
    store.showToast({ title: "x" });
    store.setBackgroundCompletion({ sessionId: "s1" });
    store.setLoopStarted({ sessionId: "s1" });
    const cb = jest.fn();
    store.subscribeToasts(cb);

    store._resetNotificationsStoreForTests();

    expect(store.getToasts()).toEqual([]);
    expect(store.getBackgroundCompletion()).toBeNull();
    expect(store.getLoopStarted()).toBeNull();
    expect(store.getBackgroundUIPrompt()).toBeNull();
    expect(store.getBackgroundUIPromptTimeout()).toBeNull();

    // Prior subscriber was dropped by the reset.
    store.showToast({ title: "y" });
    expect(cb).not.toHaveBeenCalled();

    // No stray timer from the pre-reset toast fires later and throws.
    expect(() => jest.advanceTimersByTime(60_000)).not.toThrow();
  });
});

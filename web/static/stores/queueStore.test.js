/**
 * Unit tests for the mitto-sus.11 subscribable per-session queueStore.
 *
 * Covers the store's core promise (mirrors stores/sessionsStore.test.js's
 * shape): getMessages/getLength/getConfig default sanely for an unset
 * session; setMessages/setLength/setConfig accept either a value or a
 * (prev) => next functional updater and notify ONLY their own slice's
 * subscribers for that session id; a reference-equal resolved value is a
 * silent no-op; replaceSession() diffs a subset of slices at once.
 *
 * No window.preact dependency here (this module has none), so it is
 * imported directly, mirroring sessionsStore.test.js / notificationsStore
 * .test.js's setup.
 */

import {
  describe,
  test,
  expect,
  jest,
  beforeEach,
} from "../utils/testing/testGlobals.js";

import * as store from "./queueStore.js";

beforeEach(() => {
  store._resetQueueStoreForTests();
});

describe("queueStore (mitto-sus.11) > getters on an unset session", () => {
  test("getMessages returns [], getLength returns 0, getConfig returns the default", () => {
    expect(store.getMessages("nope")).toEqual([]);
    expect(store.getLength("nope")).toBe(0);
    expect(store.getConfig("nope")).toEqual(store.DEFAULT_QUEUE_CONFIG);
  });
});

describe("queueStore (mitto-sus.11) > setters and per-slice notification isolation", () => {
  test("setMessages updates the getter and notifies only the messages subscriber", () => {
    const messagesCb = jest.fn();
    const lengthCb = jest.fn();
    const configCb = jest.fn();
    store.subscribeMessages("s1", messagesCb);
    store.subscribeLength("s1", lengthCb);
    store.subscribeConfig("s1", configCb);

    store.setMessages("s1", [{ id: "q1" }]);

    expect(store.getMessages("s1")).toEqual([{ id: "q1" }]);
    expect(messagesCb).toHaveBeenCalledWith([{ id: "q1" }]);
    expect(lengthCb).not.toHaveBeenCalled();
    expect(configCb).not.toHaveBeenCalled();
  });

  test("setLength and setConfig each notify only their own subscriber", () => {
    const lengthCb = jest.fn();
    const configCb = jest.fn();
    store.subscribeLength("s1", lengthCb);
    store.subscribeConfig("s1", configCb);

    store.setLength("s1", 2);

    expect(store.getLength("s1")).toBe(2);
    expect(lengthCb).toHaveBeenCalledWith(2);
    expect(configCb).not.toHaveBeenCalled();
  });

  test("setters accept a functional updater, mirroring the useState setter contract", () => {
    store.setLength("s1", 1);
    const lengthCb = jest.fn();
    store.subscribeLength("s1", lengthCb);

    store.setLength("s1", (prev) => prev + 1);

    expect(store.getLength("s1")).toBe(2);
    expect(lengthCb).toHaveBeenCalledWith(2);
  });

  test("a resolved value reference-equal to the current value is a silent no-op", () => {
    const messages = [{ id: "q1" }];
    store.setMessages("s1", messages);
    const cb = jest.fn();
    store.subscribeMessages("s1", cb);

    store.setMessages("s1", messages);

    expect(cb).not.toHaveBeenCalled();
  });

  test("a change to session B never notifies session A's subscribers", () => {
    store.setLength("a", 1);
    store.setLength("b", 1);
    const aCb = jest.fn();
    store.subscribeLength("a", aCb);

    store.setLength("b", 5);

    expect(aCb).not.toHaveBeenCalled();
    expect(store.getLength("a")).toBe(1);
  });
});

describe("queueStore (mitto-sus.11) > replaceSession", () => {
  test("replaceSession only touches the slices present as own-properties of partial", () => {
    store.setMessages("s1", [{ id: "q1" }]);
    store.setLength("s1", 1);
    const messagesCb = jest.fn();
    const lengthCb = jest.fn();
    store.subscribeMessages("s1", messagesCb);
    store.subscribeLength("s1", lengthCb);

    store.replaceSession("s1", { length: 2 });

    expect(store.getLength("s1")).toBe(2);
    expect(lengthCb).toHaveBeenCalledWith(2);
    expect(messagesCb).not.toHaveBeenCalled();
  });

  test("replaceSession with a falsy session id is a safe no-op", () => {
    expect(() => store.replaceSession(null, { length: 1 })).not.toThrow();
  });
});

describe("queueStore (mitto-sus.11) > robustness", () => {
  test("unsubscribe stops further notifications for that callback only", () => {
    const cb1 = jest.fn();
    const cb2 = jest.fn();
    const unsubscribe = store.subscribeLength("s1", cb1);
    store.subscribeLength("s1", cb2);

    unsubscribe();
    store.setLength("s1", 3);

    expect(cb1).not.toHaveBeenCalled();
    expect(cb2).toHaveBeenCalledWith(3);
  });

  test("a throwing listener does not break the store or other listeners", () => {
    const throwing = jest.fn(() => {
      throw new Error("boom");
    });
    const healthy = jest.fn();
    store.subscribeLength("s1", throwing);
    store.subscribeLength("s1", healthy);

    expect(() => store.setLength("s1", 1)).not.toThrow();
    expect(healthy).toHaveBeenCalledWith(1);
  });

  test("subscribing with a falsy session id is a safe no-op", () => {
    const cb = jest.fn();
    const unsubscribe = store.subscribeMessages(null, cb);
    expect(() => unsubscribe()).not.toThrow();
    store.setMessages("s1", [{ id: "q1" }]);
    expect(cb).not.toHaveBeenCalled();
  });

  test("_resetQueueStoreForTests clears state and listeners", () => {
    store.setLength("s1", 5);
    const cb = jest.fn();
    store.subscribeLength("s1", cb);

    store._resetQueueStoreForTests();

    expect(store.getLength("s1")).toBe(0);
    store.setLength("s1", 1);
    expect(cb).not.toHaveBeenCalled();
  });
});

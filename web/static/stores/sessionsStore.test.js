/**
 * Unit tests for the mitto-sus.7 subscribable sessionsStore.
 *
 * Covers the store's core promise: replaceAll() diffs each touched session's
 * four slices (messages/summary/info/keepalive) independently and notifies
 * ONLY the subscribers of a slice whose value actually changed -- a chunk
 * that only touches session A's messages must never wake session A's
 * summary/info/keepalive subscribers, nor any subscriber of session B.
 *
 * No window.preact dependency here (this module has none), so it is
 * imported directly, mirroring draftStore's test setup in ChatInput.test.js.
 */

import {
  describe,
  test,
  expect,
  jest,
  beforeEach,
} from "../utils/testing/testGlobals.js";

import * as store from "./sessionsStore.js";

beforeEach(() => {
  store._resetSessionsStoreForTests();
});

describe("sessionsStore (mitto-sus.7) > getters on an unset session", () => {
  test("getMessages returns [] and getSummary/getInfo/getKeepalive return null", () => {
    expect(store.getMessages("nope")).toEqual([]);
    expect(store.getSummary("nope")).toBeNull();
    expect(store.getInfo("nope")).toBeNull();
    expect(store.getKeepalive("nope")).toBeNull();
  });
});

describe("sessionsStore (mitto-sus.7) > replaceAll populates getters", () => {
  test("getters reflect the values passed to replaceAll", () => {
    store.replaceAll({
      s1: {
        messages: ["hi"],
        info: { name: "S1", working_dir: "/a" },
        isStreaming: true,
        lastSeq: 3,
        isRunning: true,
      },
    });
    expect(store.getMessages("s1")).toEqual(["hi"]);
    expect(store.getInfo("s1")).toEqual({ name: "S1", working_dir: "/a" });
    expect(store.getSummary("s1")).toMatchObject({
      name: "S1",
      working_dir: "/a",
      isStreaming: true,
    });
    expect(store.getKeepalive("s1")).toEqual({ lastSeq: 3, isRunning: true });
  });
});

describe("sessionsStore (mitto-sus.7) > per-slice notification isolation", () => {
  // NOTE: replaceAll() diffs `messages`/`info` by REFERENCE (matching how
  // useWebSocket.js's setSessions((prev) => ({ ...prev, [id]: next })) keeps
  // every *untouched* session's nested object at the exact same reference
  // across renders — only the touched session gets a new object). These
  // tests reuse the same array/object reference across replaceAll() calls
  // wherever a slice is meant to be "untouched", mirroring that real
  // calling convention; a fresh literal on every call would (correctly)
  // register as a change regardless of its content.

  test("a messages-only chunk notifies only the messages subscriber, not summary/info/keepalive", () => {
    const info = { name: "S1" };
    store.replaceAll({ s1: { messages: ["one"], info, lastSeq: 1 } });

    const messagesCb = jest.fn();
    const summaryCb = jest.fn();
    const infoCb = jest.fn();
    const keepaliveCb = jest.fn();
    store.subscribeMessages("s1", messagesCb);
    store.subscribeSummary("s1", summaryCb);
    store.subscribeInfo("s1", infoCb);
    store.subscribeKeepalive("s1", keepaliveCb);

    store.replaceAll({ s1: { messages: ["one", "two"], info, lastSeq: 1 } });

    expect(messagesCb).toHaveBeenCalledWith(["one", "two"]);
    expect(summaryCb).not.toHaveBeenCalled();
    expect(infoCb).not.toHaveBeenCalled();
    expect(keepaliveCb).not.toHaveBeenCalled();
  });

  test("a summary-field change notifies the summary subscriber even when messages is untouched", () => {
    const messages = ["one"];
    store.replaceAll({ s1: { messages, info: { name: "S1" } } });
    const messagesCb = jest.fn();
    const summaryCb = jest.fn();
    store.subscribeMessages("s1", messagesCb);
    store.subscribeSummary("s1", summaryCb);

    store.replaceAll({ s1: { messages, info: { pinned: true } } });

    expect(messagesCb).not.toHaveBeenCalled();
    expect(summaryCb).toHaveBeenCalledWith(
      expect.objectContaining({ pinned: true }),
    );
  });

  test("a change to session B never notifies session A's subscribers", () => {
    const aMessages = ["a1"];
    store.replaceAll({ a: { messages: aMessages }, b: { messages: ["b1"] } });
    const aCb = jest.fn();
    store.subscribeMessages("a", aCb);

    store.replaceAll({
      a: { messages: aMessages },
      b: { messages: ["b2"] },
    });

    expect(aCb).not.toHaveBeenCalled();
  });

  test("a new info object reference with identical summary fields still fires info but not summary", () => {
    store.replaceAll({ s1: { messages: [], info: { name: "S1" } } });
    const infoCb = jest.fn();
    const summaryCb = jest.fn();
    store.subscribeInfo("s1", infoCb);
    store.subscribeSummary("s1", summaryCb);

    // Same summary-relevant content, but a brand-new object reference (as a
    // fresh WS payload always is).
    store.replaceAll({ s1: { messages: [], info: { name: "S1" } } });

    expect(infoCb).toHaveBeenCalledTimes(1);
    expect(summaryCb).not.toHaveBeenCalled();
  });
});

describe("sessionsStore (mitto-sus.7) > removal, unsubscribe, and robustness", () => {
  test("a session id absent from a later replaceAll clears its slices and getters", () => {
    store.replaceAll({ s1: { messages: ["hi"] } });
    const messagesCb = jest.fn();
    store.subscribeMessages("s1", messagesCb);

    store.replaceAll({});

    expect(messagesCb).toHaveBeenCalledWith([]);
    expect(store.getMessages("s1")).toEqual([]);
    expect(store.getSummary("s1")).toBeNull();
  });

  test("unsubscribe stops further notifications for that callback only", () => {
    store.replaceAll({ s1: { messages: [] } });
    const cb1 = jest.fn();
    const cb2 = jest.fn();
    const unsubscribe = store.subscribeMessages("s1", cb1);
    store.subscribeMessages("s1", cb2);

    unsubscribe();
    store.replaceAll({ s1: { messages: ["x"] } });

    expect(cb1).not.toHaveBeenCalled();
    expect(cb2).toHaveBeenCalledWith(["x"]);
  });

  test("a throwing listener does not break the store or other listeners", () => {
    store.replaceAll({ s1: { messages: [] } });
    const throwing = jest.fn(() => {
      throw new Error("boom");
    });
    const healthy = jest.fn();
    store.subscribeMessages("s1", throwing);
    store.subscribeMessages("s1", healthy);

    expect(() => store.replaceAll({ s1: { messages: ["x"] } })).not.toThrow();
    expect(healthy).toHaveBeenCalledWith(["x"]);
  });

  test("subscribing with a falsy session id is a safe no-op", () => {
    const cb = jest.fn();
    const unsubscribe = store.subscribeMessages(null, cb);
    expect(() => unsubscribe()).not.toThrow();
    store.replaceAll({ s1: { messages: ["x"] } });
    expect(cb).not.toHaveBeenCalled();
  });
});

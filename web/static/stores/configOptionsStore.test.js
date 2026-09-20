/**
 * Unit tests for the mitto-sus.11 subscribable per-session configOptionsStore.
 *
 * Covers the store's core promise -- this is the one that fixes a REAL bug
 * (see the module's own header comment): getConfigOptions defaults to []
 * for an unset session; setConfigOptions notifies only that session's
 * subscribers; a reference-equal resolved value (the common case -- an
 * unrelated `info` touch that leaves `config_options` at the same array
 * reference) is a SILENT no-op, which is the actual fix over the old
 * `useMemo` that recomputed (and looked "changed") on every touch; a
 * falsy session id is a safe no-op for both get/set/subscribe.
 *
 * No window.preact dependency here (this module has none), so it is
 * imported directly, mirroring queueStore.test.js/workspacesStore
 * .test.js's setup.
 */

import {
  describe,
  test,
  expect,
  jest,
  beforeEach,
} from "../utils/testing/testGlobals.js";

import * as store from "./configOptionsStore.js";

beforeEach(() => {
  store._resetConfigOptionsStoreForTests();
});

describe("configOptionsStore (mitto-sus.11) > getters on an unset session", () => {
  test("getConfigOptions returns [] for a session that was never set", () => {
    expect(store.getConfigOptions("nope")).toEqual([]);
  });
});

describe("configOptionsStore (mitto-sus.11) > setConfigOptions", () => {
  test("updates the getter and notifies that session's subscribers", () => {
    const cb = jest.fn();
    store.subscribeConfigOptions("s1", cb);

    const options = [{ id: "model", current_value: "sonnet" }];
    store.setConfigOptions("s1", options);

    expect(store.getConfigOptions("s1")).toEqual(options);
    expect(cb).toHaveBeenCalledWith(options);
  });

  test("a change to session B never notifies session A's subscribers", () => {
    const aCb = jest.fn();
    store.subscribeConfigOptions("a", aCb);

    store.setConfigOptions("b", [{ id: "model" }]);

    expect(aCb).not.toHaveBeenCalled();
    expect(store.getConfigOptions("a")).toEqual([]);
  });

  test("a resolved value reference-equal to the current value is a SILENT no-op -- the actual bug fix", () => {
    const options = [{ id: "model", current_value: "sonnet" }];
    store.setConfigOptions("s1", options);
    const cb = jest.fn();
    store.subscribeConfigOptions("s1", cb);

    // Mirrors an unrelated `info` touch that reuses the same config_options
    // array reference (the common case per useWSConfigOptions.js's effect).
    store.setConfigOptions("s1", options);

    expect(cb).not.toHaveBeenCalled();
  });

  test("a nullish value resolves to [] and still notifies when it actually changes", () => {
    store.setConfigOptions("s1", [{ id: "model" }]);
    const cb = jest.fn();
    store.subscribeConfigOptions("s1", cb);

    store.setConfigOptions("s1", null);

    expect(store.getConfigOptions("s1")).toEqual([]);
    expect(cb).toHaveBeenCalledWith([]);
  });

  test("setConfigOptions with a falsy session id is a safe no-op", () => {
    expect(() => store.setConfigOptions(null, [{ id: "model" }])).not.toThrow();
    expect(store.getConfigOptions(null)).toEqual([]);
  });
});

describe("configOptionsStore (mitto-sus.11) > robustness", () => {
  test("subscribing with a falsy session id is a safe no-op", () => {
    const cb = jest.fn();
    const unsubscribe = store.subscribeConfigOptions(null, cb);
    expect(() => unsubscribe()).not.toThrow();
    store.setConfigOptions("s1", [{ id: "model" }]);
    expect(cb).not.toHaveBeenCalled();
  });

  test("unsubscribe stops further notifications for that callback only", () => {
    const cb1 = jest.fn();
    const cb2 = jest.fn();
    const unsubscribe = store.subscribeConfigOptions("s1", cb1);
    store.subscribeConfigOptions("s1", cb2);

    unsubscribe();
    store.setConfigOptions("s1", [{ id: "model" }]);

    expect(cb1).not.toHaveBeenCalled();
    expect(cb2).toHaveBeenCalledWith([{ id: "model" }]);
  });

  test("unsubscribing the last listener for a session cleans up the listeners map entry", () => {
    const cb = jest.fn();
    const unsubscribe = store.subscribeConfigOptions("s1", cb);

    unsubscribe();

    // No throw and no stale entry -- setting again should behave exactly
    // like a first-time set for this session (still notifies future subs).
    expect(() => store.setConfigOptions("s1", [{ id: "model" }])).not.toThrow();
    const cb2 = jest.fn();
    store.subscribeConfigOptions("s1", cb2);
    store.setConfigOptions("s1", [{ id: "mode" }]);
    expect(cb2).toHaveBeenCalledWith([{ id: "mode" }]);
  });

  test("a throwing listener does not break the store or other listeners", () => {
    const throwing = jest.fn(() => {
      throw new Error("boom");
    });
    const healthy = jest.fn();
    store.subscribeConfigOptions("s1", throwing);
    store.subscribeConfigOptions("s1", healthy);

    expect(() =>
      store.setConfigOptions("s1", [{ id: "model" }]),
    ).not.toThrow();
    expect(healthy).toHaveBeenCalledWith([{ id: "model" }]);
  });

  test("_resetConfigOptionsStoreForTests clears state and listeners", () => {
    store.setConfigOptions("s1", [{ id: "model" }]);
    const cb = jest.fn();
    store.subscribeConfigOptions("s1", cb);

    store._resetConfigOptionsStoreForTests();

    expect(store.getConfigOptions("s1")).toEqual([]);
    store.setConfigOptions("s1", [{ id: "mode" }]);
    expect(cb).not.toHaveBeenCalled();
  });
});

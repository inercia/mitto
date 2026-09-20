/**
 * Unit tests for the mitto-sus.11 subscribable global workspacesStore.
 *
 * Unlike queueStore.js/configOptionsStore.js, this store is GLOBAL (not
 * per-session): getWorkspaces/getAcpServers default to [] before any fetch;
 * setWorkspaces/setAcpServers each notify only their OWN slice's
 * subscribers; a reference-equal resolved value is a silent no-op; a
 * nullish value resolves to [] (mirrors the WS payload's `data.workspaces
 * || []` shape).
 *
 * No window.preact dependency here (this module has none), so it is
 * imported directly, mirroring queueStore.test.js/notificationsStore
 * .test.js's setup.
 */

import {
  describe,
  test,
  expect,
  jest,
  beforeEach,
} from "../utils/testing/testGlobals.js";

import * as store from "./workspacesStore.js";

beforeEach(() => {
  store._resetWorkspacesStoreForTests();
});

describe("workspacesStore (mitto-sus.11) > getters before any fetch", () => {
  test("getWorkspaces and getAcpServers both start as []", () => {
    expect(store.getWorkspaces()).toEqual([]);
    expect(store.getAcpServers()).toEqual([]);
  });
});

describe("workspacesStore (mitto-sus.11) > setters and per-slice notification isolation", () => {
  test("setWorkspaces updates the getter and notifies only the workspaces subscriber", () => {
    const workspacesCb = jest.fn();
    const acpServersCb = jest.fn();
    store.subscribeWorkspaces(workspacesCb);
    store.subscribeAcpServers(acpServersCb);

    const workspaces = [{ uuid: "w1", working_dir: "/tmp/w1" }];
    store.setWorkspaces(workspaces);

    expect(store.getWorkspaces()).toEqual(workspaces);
    expect(workspacesCb).toHaveBeenCalledWith(workspaces);
    expect(acpServersCb).not.toHaveBeenCalled();
  });

  test("setAcpServers updates the getter and notifies only the acpServers subscriber", () => {
    const workspacesCb = jest.fn();
    const acpServersCb = jest.fn();
    store.subscribeWorkspaces(workspacesCb);
    store.subscribeAcpServers(acpServersCb);

    const acpServers = [{ name: "mock-acp" }];
    store.setAcpServers(acpServers);

    expect(store.getAcpServers()).toEqual(acpServers);
    expect(acpServersCb).toHaveBeenCalledWith(acpServers);
    expect(workspacesCb).not.toHaveBeenCalled();
  });

  test("a resolved value reference-equal to the current value is a silent no-op", () => {
    const workspaces = [{ uuid: "w1" }];
    store.setWorkspaces(workspaces);
    const cb = jest.fn();
    store.subscribeWorkspaces(cb);

    store.setWorkspaces(workspaces);

    expect(cb).not.toHaveBeenCalled();
  });

  test("a nullish value resolves to [] and still notifies (mirrors `data.workspaces || []`)", () => {
    store.setWorkspaces([{ uuid: "w1" }]);
    const cb = jest.fn();
    store.subscribeWorkspaces(cb);

    store.setWorkspaces(null);

    expect(store.getWorkspaces()).toEqual([]);
    expect(cb).toHaveBeenCalledWith([]);
  });

  test("setting acpServers never notifies the workspaces subscriber and vice versa, repeatedly", () => {
    const workspacesCb = jest.fn();
    const acpServersCb = jest.fn();
    store.subscribeWorkspaces(workspacesCb);
    store.subscribeAcpServers(acpServersCb);

    store.setAcpServers([{ name: "a" }]);
    store.setWorkspaces([{ uuid: "w1" }]);
    store.setAcpServers([{ name: "b" }]);

    expect(workspacesCb).toHaveBeenCalledTimes(1);
    expect(acpServersCb).toHaveBeenCalledTimes(2);
  });
});

describe("workspacesStore (mitto-sus.11) > robustness", () => {
  test("unsubscribe stops further notifications for that callback only", () => {
    const cb1 = jest.fn();
    const cb2 = jest.fn();
    const unsubscribe = store.subscribeWorkspaces(cb1);
    store.subscribeWorkspaces(cb2);

    unsubscribe();
    store.setWorkspaces([{ uuid: "w1" }]);

    expect(cb1).not.toHaveBeenCalled();
    expect(cb2).toHaveBeenCalledWith([{ uuid: "w1" }]);
  });

  test("a throwing listener does not break the store or other listeners", () => {
    const throwing = jest.fn(() => {
      throw new Error("boom");
    });
    const healthy = jest.fn();
    store.subscribeWorkspaces(throwing);
    store.subscribeWorkspaces(healthy);

    expect(() => store.setWorkspaces([{ uuid: "w1" }])).not.toThrow();
    expect(healthy).toHaveBeenCalledWith([{ uuid: "w1" }]);
  });

  test("_resetWorkspacesStoreForTests clears state and listeners", () => {
    store.setWorkspaces([{ uuid: "w1" }]);
    store.setAcpServers([{ name: "a" }]);
    const cb = jest.fn();
    store.subscribeWorkspaces(cb);

    store._resetWorkspacesStoreForTests();

    expect(store.getWorkspaces()).toEqual([]);
    expect(store.getAcpServers()).toEqual([]);
    store.setWorkspaces([{ uuid: "w2" }]);
    expect(cb).not.toHaveBeenCalled();
  });
});

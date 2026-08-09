/**
 * Tests for useIssueLabels.js (mitto-7gta.17 slice S3 Test phase).
 *
 * The hook had no prior test file (mitto-90f.7 PR-12 extracted it verbatim
 * with no accompanying tests), so this is dedicated new coverage for the
 * migration onto getSdkClient(). Covers fetchAllLabels (GET
 * /api/issues/labels), mutateLabel (POST .../labels), and the
 * fetchDepsRef bridge mutateLabel uses to trigger a full issue refresh.
 *
 * Harness mirrors useIssueDependencies.test.js: `useState` is backed by a
 * per-test cell array (indexed by call order) so a setter invoked mid-test
 * is visible on a subsequent re-render; `useCallback`/`useEffect` are
 * identity/capture stubs; `useRef` persists like useState.
 */

import {
  describe,
  test,
  expect,
  jest,
} from "../../../utils/testing/testGlobals.js";
import { fakeResponse } from "../../../sdk/testing/fake-server.js";

global.window = global.window || {};
window.mittoApiPrefix = "";
if (typeof document === "undefined") {
  global.document = { cookie: "" };
}

let cells;
let cellIdx;
let currentEffects;
window.preact = {
  useState: (initial) => {
    const i = cellIdx++;
    if (!(i in cells)) cells[i] = initial;
    const setState = (v) => {
      cells[i] = typeof v === "function" ? v(cells[i]) : v;
    };
    return [cells[i], setState];
  },
  useRef: (initial) => {
    const i = cellIdx++;
    if (!(i in cells)) cells[i] = { current: initial };
    return cells[i];
  },
  useCallback: (fn) => fn,
  useEffect: (cb, deps) => {
    currentEffects.push({ cb, deps });
  },
};

async function flush() {
  for (let i = 0; i < 10; i++) await Promise.resolve();
}

let hookMod;
async function render(args) {
  cellIdx = 0;
  currentEffects = [];
  // Cache-busting query: useBeadsDetailPanel.test.js transitively imports the
  // bare "./useIssueLabels.js" path (via useBeadsDetailPanel.js's own static
  // import) under a DIFFERENT window.preact stub. Without a distinct query
  // string here, ESM's per-path module cache would hand this file the OTHER
  // test file's already-evaluated module — whose captured
  // useState/useRef/useCallback are bound to that file's `cells` array, not
  // this file's — silently breaking every cross-render assertion.
  hookMod = hookMod || (await import("./useIssueLabels.js?slice-s3-test"));
  return hookMod.useIssueLabels(args);
}

function freshMount() {
  cells = [];
  cellIdx = 0;
  currentEffects = [];
  global.document.cookie = "mitto_csrf=test-token";
}

function baseArgs(overrides = {}) {
  return {
    data: { id: "mitto-abc" },
    workingDir: "/tmp/wsA",
    showToast: jest.fn(),
    fetchDepsRef: { current: null },
    onUpdated: jest.fn(),
    isOpen: false,
    creating: false,
    ...overrides,
  };
}

describe("useIssueLabels — fetchAllLabels effect", () => {
  test("fires GET /api/issues/labels when open and not creating", async () => {
    freshMount();
    global.fetch = jest.fn(() =>
      Promise.resolve(
        fakeResponse({ body: [{ label: "planned", count: 3 }, "sdk"] }),
      ),
    );
    const bag = await render(baseArgs({ isOpen: true, creating: false }));
    await currentEffects[0].cb();
    await flush();

    expect(global.fetch).toHaveBeenCalledTimes(1);
    const [url] = global.fetch.mock.calls[0];
    expect(String(url)).toContain("/api/issues/labels");
    expect(String(url)).toContain(encodeURIComponent("/tmp/wsA"));
    // Re-render to read the populated allLabels state.
    const bag2 = await render(baseArgs({ isOpen: true, creating: false }));
    expect(bag2.allLabels).toEqual(["planned", "sdk"]);
    void bag;
  });

  test("does not fire while creating", async () => {
    freshMount();
    global.fetch = jest.fn();
    await render(baseArgs({ isOpen: true, creating: true }));
    await currentEffects[0].cb();
    await flush();
    expect(global.fetch).not.toHaveBeenCalled();
  });

  test("failure is non-fatal (swallowed, no throw)", async () => {
    freshMount();
    global.fetch = jest.fn(() =>
      Promise.resolve(fakeResponse({ status: 500 })),
    );
    await render(baseArgs({ isOpen: true, creating: false }));
    expect(() => currentEffects[0].cb()).not.toThrow();
    await flush();
  });
});

describe("useIssueLabels — mutateLabel", () => {
  test("add: POSTs {label, action}, toasts, refreshes deps, and re-fetches suggestions", async () => {
    freshMount();
    global.fetch = jest.fn(() =>
      Promise.resolve(fakeResponse({ status: 204 })),
    );
    const showToast = jest.fn();
    const onUpdated = jest.fn();
    const fetchDepsRef = { current: jest.fn(() => Promise.resolve()) };
    const bag = await render(baseArgs({ showToast, onUpdated, fetchDepsRef }));

    const ok = await bag.mutateLabel("add", "  urgent  ");
    await flush();

    expect(ok).toBe(true);
    const [url, init] = global.fetch.mock.calls[0];
    expect(String(url)).toContain("/api/issues/mitto-abc/labels");
    expect(init.method).toBe("POST");
    expect(JSON.parse(init.body)).toEqual({ label: "urgent", action: "add" });
    expect(showToast).toHaveBeenCalledWith({
      style: "success",
      title: 'Added label "urgent"',
    });
    expect(fetchDepsRef.current).toHaveBeenCalledWith(false);
    expect(onUpdated).toHaveBeenCalledTimes(1);
    // action === "add" also re-fetches the workspace label suggestions.
    expect(global.fetch).toHaveBeenCalledTimes(2);
    expect(String(global.fetch.mock.calls[1][0])).toContain(
      "/api/issues/labels",
    );
  });

  test("remove: does NOT re-fetch label suggestions", async () => {
    freshMount();
    global.fetch = jest.fn(() =>
      Promise.resolve(fakeResponse({ status: 204 })),
    );
    const bag = await render(baseArgs());

    await bag.mutateLabel("remove", "urgent");
    await flush();

    expect(global.fetch).toHaveBeenCalledTimes(1);
    expect(JSON.parse(global.fetch.mock.calls[0][1].body)).toEqual({
      label: "urgent",
      action: "remove",
    });
  });

  test("failure: error toast, no fetchDepsRef call, no onUpdated, returns false", async () => {
    freshMount();
    global.fetch = jest.fn(() =>
      Promise.resolve(fakeResponse({ status: 500 })),
    );
    const showToast = jest.fn();
    const onUpdated = jest.fn();
    const fetchDepsRef = { current: jest.fn() };
    const bag = await render(baseArgs({ showToast, onUpdated, fetchDepsRef }));

    const ok = await bag.mutateLabel("add", "urgent");
    await flush();

    expect(ok).toBe(false);
    expect(showToast).toHaveBeenCalledWith({
      style: "error",
      title: "Request failed with status 500",
    });
    expect(fetchDepsRef.current).not.toHaveBeenCalled();
    expect(onUpdated).not.toHaveBeenCalled();
  });

  test("no-op when the label is blank after trimming", async () => {
    freshMount();
    global.fetch = jest.fn();
    const bag = await render(baseArgs());
    const ok = await bag.mutateLabel("add", "   ");
    expect(ok).toBe(false);
    expect(global.fetch).not.toHaveBeenCalled();
  });

  test("tolerates a missing fetchDepsRef.current (bridge not wired yet)", async () => {
    freshMount();
    global.fetch = jest.fn(() =>
      Promise.resolve(fakeResponse({ status: 204 })),
    );
    const bag = await render(baseArgs({ fetchDepsRef: { current: null } }));
    await expect(bag.mutateLabel("remove", "urgent")).resolves.toBe(true);
  });
});

describe("useIssueLabels — handleAddLabel", () => {
  test("no-op when newLabel is blank (initial state)", async () => {
    freshMount();
    global.fetch = jest.fn();
    const bag = await render(baseArgs());
    await bag.handleAddLabel();
    expect(global.fetch).not.toHaveBeenCalled();
  });

  test("trims newLabel, adds it, and clears the input on success", async () => {
    freshMount();
    global.fetch = jest.fn(() =>
      Promise.resolve(fakeResponse({ status: 204 })),
    );
    const args = baseArgs();
    let bag = await render(args);
    bag.setNewLabel("  urgent  ");
    bag = await render(args);

    await bag.handleAddLabel();
    await flush();

    expect(JSON.parse(global.fetch.mock.calls[0][1].body)).toEqual({
      label: "urgent",
      action: "add",
    });
    bag = await render(args);
    expect(bag.newLabel).toBe("");
  });
});

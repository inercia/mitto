/**
 * Tests for useIssueLabels.js (mitto-7gta.17 slice S3 Test phase).
 *
 * The hook had no prior test file (mitto-90f.7 PR-12 extracted it verbatim
 * with no accompanying tests), so this is dedicated new coverage for the
 * migration onto getSdkClient(). Covers fetchAllLabels (GET
 * /api/issues/labels), the in-memory staging helpers (addLabelLocal /
 * removeLabelLocal / labelsDirty), and the persistLabels reconciler that diffs
 * the working set against the baseline and issues the POST .../labels calls on
 * Save.
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

describe("useIssueLabels — in-memory staging", () => {
  test("addLabelLocal stages a trimmed label with no network call and marks dirty", async () => {
    freshMount();
    global.fetch = jest.fn();
    const args = baseArgs();
    let bag = await render(args);
    // Seed the baseline via the authoritative setter so dirty starts false.
    bag.setLabels(["a"]);
    bag = await render(args);
    expect(bag.labels).toEqual(["a"]);
    expect(bag.labelsDirty).toBe(false);

    bag.addLabelLocal("  b  ");
    bag = await render(args);
    expect(bag.labels).toEqual(["a", "b"]);
    expect(bag.labelsDirty).toBe(true);
    expect(global.fetch).not.toHaveBeenCalled();
  });

  test("addLabelLocal ignores blanks and duplicates", async () => {
    freshMount();
    global.fetch = jest.fn();
    const args = baseArgs();
    let bag = await render(args);
    bag.setLabels(["a"]);
    bag = await render(args);
    bag.addLabelLocal("   ");
    bag.addLabelLocal("a");
    bag = await render(args);
    expect(bag.labels).toEqual(["a"]);
    expect(bag.labelsDirty).toBe(false);
  });

  test("removeLabelLocal stages a removal and marks dirty (no network)", async () => {
    freshMount();
    global.fetch = jest.fn();
    const args = baseArgs();
    let bag = await render(args);
    bag.setLabels(["a", "b"]);
    bag = await render(args);
    bag.removeLabelLocal("a");
    bag = await render(args);
    expect(bag.labels).toEqual(["b"]);
    expect(bag.labelsDirty).toBe(true);
    expect(global.fetch).not.toHaveBeenCalled();
  });

  test("labelsDirty stays false in create mode even when sets differ", async () => {
    freshMount();
    global.fetch = jest.fn();
    const args = baseArgs({ creating: true });
    let bag = await render(args);
    bag.addLabelLocal("x");
    bag = await render(args);
    expect(bag.labelsDirty).toBe(false);
  });
});

describe("useIssueLabels — persistLabels", () => {
  test("diffs baseline vs working set: issues remove + add, advances baseline, re-fetches suggestions", async () => {
    freshMount();
    global.fetch = jest.fn(() =>
      Promise.resolve(fakeResponse({ status: 204 })),
    );
    const args = baseArgs();
    let bag = await render(args);
    bag.setLabels(["keep", "drop"]);
    bag = await render(args);
    bag.removeLabelLocal("drop");
    bag.addLabelLocal("new");
    bag = await render(args);
    expect(bag.labelsDirty).toBe(true);

    const ok = await bag.persistLabels();
    await flush();
    expect(ok).toBe(true);

    const bodies = global.fetch.mock.calls
      .map((c) => c[1] && c[1].body)
      .filter(Boolean)
      .map((b) => JSON.parse(b));
    expect(bodies).toContainEqual({ label: "drop", action: "remove" });
    expect(bodies).toContainEqual({ label: "new", action: "add" });
    // An add also refreshes the workspace label suggestions (GET, no body).
    expect(
      global.fetch.mock.calls.some(
        (c) =>
          String(c[0]).includes("/api/issues/labels") &&
          !(c[1] && c[1].body),
      ),
    ).toBe(true);

    // Baseline advanced to the working set, so the panel is no longer dirty.
    bag = await render(args);
    expect(bag.labelsDirty).toBe(false);
  });

  test("no-op returns true with no network call when not dirty", async () => {
    freshMount();
    global.fetch = jest.fn();
    const args = baseArgs();
    let bag = await render(args);
    bag.setLabels(["a"]);
    bag = await render(args);
    const ok = await bag.persistLabels();
    expect(ok).toBe(true);
    expect(global.fetch).not.toHaveBeenCalled();
  });

  test("failure: error toast, returns false, baseline unchanged (stays dirty)", async () => {
    freshMount();
    global.fetch = jest.fn(() =>
      Promise.resolve(fakeResponse({ status: 500 })),
    );
    const showToast = jest.fn();
    const args = baseArgs({ showToast });
    let bag = await render(args);
    bag.setLabels(["a"]);
    bag = await render(args);
    bag.addLabelLocal("b");
    bag = await render(args);

    const ok = await bag.persistLabels();
    await flush();

    expect(ok).toBe(false);
    expect(showToast).toHaveBeenCalledWith({
      style: "error",
      title: "Request failed with status 500",
    });
    bag = await render(args);
    expect(bag.labelsDirty).toBe(true);
  });
});

describe("useIssueLabels — handleAddLabel", () => {
  test("no-op when newLabel is blank (initial state)", async () => {
    freshMount();
    global.fetch = jest.fn();
    const bag = await render(baseArgs());
    bag.handleAddLabel();
    expect(global.fetch).not.toHaveBeenCalled();
  });

  test("trims newLabel, stages it in-memory, and clears the input (no network)", async () => {
    freshMount();
    global.fetch = jest.fn();
    const args = baseArgs();
    let bag = await render(args);
    bag.setNewLabel("  urgent  ");
    bag = await render(args);

    bag.handleAddLabel();
    bag = await render(args);

    expect(bag.labels).toContain("urgent");
    expect(bag.newLabel).toBe("");
    expect(global.fetch).not.toHaveBeenCalled();
  });
});

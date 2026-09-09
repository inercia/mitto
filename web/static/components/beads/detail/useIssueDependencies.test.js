/**
 * Tests for useIssueDependencies.js (mitto-7gta.17 slice S3 Test phase).
 *
 * The hook had no prior test file (mitto-90f.7 PR-17 extracted it verbatim
 * with no accompanying tests), so this is dedicated new coverage for the
 * migration onto getSdkClient(), mirroring the precedent set by S2's
 * folder-config hook tests. Covers fetchDeps (GET /api/issues/{id}), the
 * in-memory staging helpers (addDepLocal / removeDepLocal / changeDepTypeLocal
 * / depsDirty), and the persistDeps reconciler that diffs the working set
 * against the baseline and issues the POST .../dependencies calls on Save
 * (remove, add, and a type change as remove + re-add).
 *
 * Harness: `useState`/`useCallback` are destructured from `window.preact` at
 * module-load time. `useCallback` is stubbed as an identity function (no
 * memoization needed for a single-render-per-test harness); `useState` is
 * backed by a small per-test cell array indexed by call order so a setter
 * invoked mid-test is visible to a subsequent re-render — needed for
 * handleAddDep, whose guard reads `newDepId`/`depsBusy` from closed-over
 * state rather than accepting them as arguments.
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
window.preact = {
  useState: (initial) => {
    const i = cellIdx++;
    if (!(i in cells)) cells[i] = initial;
    const setState = (v) => {
      cells[i] = typeof v === "function" ? v(cells[i]) : v;
    };
    return [cells[i], setState];
  },
  useCallback: (fn) => fn,
};

async function flush() {
  for (let i = 0; i < 10; i++) await Promise.resolve();
}

let hookMod;
async function render(args) {
  cells = cells || [];
  cellIdx = 0;
  // Cache-busting query: useBeadsDetailPanel.test.js transitively imports the
  // bare "./useIssueDependencies.js" path (via useBeadsDetailPanel.js's own
  // static import) under a DIFFERENT window.preact stub. Without a distinct
  // query string here, ESM's per-path module cache would hand this file the
  // OTHER test file's already-evaluated module — whose captured
  // useState/useCallback are bound to that file's `cells` array, not this
  // file's — silently breaking every cross-render assertion.
  hookMod =
    hookMod || (await import("./useIssueDependencies.js?slice-s3-test"));
  return hookMod.useIssueDependencies(args);
}

function freshMount() {
  cells = [];
  cellIdx = 0;
  // Mutating requests (POST/PUT/PATCH/DELETE) go through browserCookieAuth,
  // which fetches a CSRF token first when no `mitto_csrf` cookie is present
  // (sdk/auth/browser-cookie.js). Pre-seed the cookie so every test's fetch
  // call count reflects only the resource calls under test, matching the
  // convention established by useFolderProcessorsConfig.test.js etc.
  global.document.cookie = "mitto_csrf=test-token";
}

function baseArgs(overrides = {}) {
  return {
    data: { id: "mitto-abc" },
    allIssues: [],
    creating: false,
    workingDir: "/tmp/wsA",
    showToast: jest.fn(),
    fetchDepsRef: { current: null },
    setLabels: jest.fn(),
    setComments: jest.fn(),
    setNotes: jest.fn(),
    setViewDraft: jest.fn(),
    ...overrides,
  };
}

describe("useIssueDependencies — fetchDeps", () => {
  test("success populates deps/labels/comments/notes from the SDK response", async () => {
    freshMount();
    global.fetch = jest.fn(() =>
      Promise.resolve(
        fakeResponse({
          body: {
            dependencies: [{ id: "mitto-x", type: "blocks" }],
            labels: ["planned"],
            comments: [{ text: "hi" }],
            notes: "some notes",
          },
        }),
      ),
    );
    const setLabels = jest.fn();
    const setComments = jest.fn();
    const setNotes = jest.fn();
    const setViewDraft = jest.fn();
    const bag = await render(
      baseArgs({ setLabels, setComments, setNotes, setViewDraft }),
    );

    await bag.fetchDeps(true);
    await flush();

    expect(global.fetch).toHaveBeenCalledTimes(1);
    const [url] = global.fetch.mock.calls[0];
    expect(String(url)).toContain("/api/issues/mitto-abc");
    expect(String(url)).toContain(encodeURIComponent("/tmp/wsA"));
    expect(setLabels).toHaveBeenCalledWith(["planned"]);
    expect(setComments).toHaveBeenCalledWith([{ text: "hi" }]);
    expect(setNotes).toHaveBeenCalledWith("some notes");
    // seedDraftNotes=true also seeds viewDraft.notes via the functional updater.
    expect(setViewDraft).toHaveBeenCalledTimes(1);
    expect(setViewDraft.mock.calls[0][0]({})).toEqual({ notes: "some notes" });
  });

  test("failure clears deps/labels/comments/notes instead of throwing", async () => {
    freshMount();
    global.fetch = jest.fn(() =>
      Promise.resolve(fakeResponse({ status: 500 })),
    );
    const setLabels = jest.fn();
    const setComments = jest.fn();
    const setNotes = jest.fn();
    const bag = await render(baseArgs({ setLabels, setComments, setNotes }));

    await expect(bag.fetchDeps(false)).resolves.toBeUndefined();
    await flush();

    expect(setLabels).toHaveBeenCalledWith([]);
    expect(setComments).toHaveBeenCalledWith([]);
    expect(setNotes).toHaveBeenCalledWith("");
  });

  test("wires fetchDepsRef.current to fetchDeps for the labels/comments bridge", async () => {
    freshMount();
    const fetchDepsRef = { current: null };
    const bag = await render(baseArgs({ fetchDepsRef }));
    expect(fetchDepsRef.current).toBe(bag.fetchDeps);
  });
});

describe("useIssueDependencies — in-memory staging", () => {
  test("addDepLocal stages a trimmed edge (title/status from allIssues), marks dirty, no network", async () => {
    freshMount();
    global.fetch = jest.fn();
    const args = baseArgs({
      allIssues: [{ id: "mitto-x", title: "X issue", status: "open" }],
    });
    let bag = await render(args);
    // Seed the baseline via the authoritative setter so dirty starts false.
    bag.setDeps([
      { id: "mitto-a", title: "A", status: "open", dependency_type: "blocks" },
    ]);
    bag = await render(args);
    expect(bag.deps.map((d) => d.id)).toEqual(["mitto-a"]);
    expect(bag.depsDirty).toBe(false);

    bag.addDepLocal("  mitto-x  ", "related");
    bag = await render(args);
    expect(bag.deps.map((d) => d.id)).toEqual(["mitto-a", "mitto-x"]);
    expect(bag.deps.find((d) => d.id === "mitto-x")).toEqual({
      id: "mitto-x",
      title: "X issue",
      status: "open",
      dependency_type: "related",
    });
    expect(bag.depsDirty).toBe(true);
    expect(global.fetch).not.toHaveBeenCalled();
  });

  test("addDepLocal ignores blanks and duplicate ids", async () => {
    freshMount();
    global.fetch = jest.fn();
    const args = baseArgs();
    let bag = await render(args);
    bag.setDeps([{ id: "mitto-a", dependency_type: "blocks" }]);
    bag = await render(args);
    bag.addDepLocal("   ", "blocks");
    bag.addDepLocal("mitto-a", "related");
    bag = await render(args);
    expect(bag.deps.map((d) => d.id)).toEqual(["mitto-a"]);
    expect(bag.depsDirty).toBe(false);
  });

  test("removeDepLocal stages a removal and marks dirty (no network)", async () => {
    freshMount();
    global.fetch = jest.fn();
    const args = baseArgs();
    let bag = await render(args);
    bag.setDeps([
      { id: "mitto-a", dependency_type: "blocks" },
      { id: "mitto-b", dependency_type: "blocks" },
    ]);
    bag = await render(args);
    bag.removeDepLocal("mitto-a");
    bag = await render(args);
    expect(bag.deps.map((d) => d.id)).toEqual(["mitto-b"]);
    expect(bag.depsDirty).toBe(true);
    expect(global.fetch).not.toHaveBeenCalled();
  });

  test("changeDepTypeLocal restages the edge type and marks dirty (no network)", async () => {
    freshMount();
    global.fetch = jest.fn();
    const args = baseArgs();
    let bag = await render(args);
    bag.setDeps([{ id: "mitto-a", dependency_type: "blocks" }]);
    bag = await render(args);
    bag.changeDepTypeLocal("mitto-a", "related");
    bag = await render(args);
    expect(bag.deps[0].dependency_type).toBe("related");
    expect(bag.depsDirty).toBe(true);
    expect(global.fetch).not.toHaveBeenCalled();
  });

  test("depsDirty stays false in create mode even when sets differ", async () => {
    freshMount();
    global.fetch = jest.fn();
    const args = baseArgs({ creating: true });
    let bag = await render(args);
    bag.addDepLocal("mitto-x", "blocks");
    bag = await render(args);
    expect(bag.depsDirty).toBe(false);
  });
});

describe("useIssueDependencies — persistDeps", () => {
  test("diffs baseline vs working: remove, add, and type-change (remove+re-add); advances baseline", async () => {
    freshMount();
    global.fetch = jest.fn(() =>
      Promise.resolve(fakeResponse({ status: 204 })),
    );
    const args = baseArgs();
    let bag = await render(args);
    bag.setDeps([
      { id: "keep", dependency_type: "blocks" },
      { id: "drop", dependency_type: "blocks" },
      { id: "retype", dependency_type: "blocks" },
    ]);
    bag = await render(args);
    bag.removeDepLocal("drop");
    bag.changeDepTypeLocal("retype", "related");
    bag.addDepLocal("new", "parent-child");
    bag = await render(args);
    expect(bag.depsDirty).toBe(true);

    const ok = await bag.persistDeps();
    await flush();
    expect(ok).toBe(true);

    const bodies = global.fetch.mock.calls
      .map((c) => c[1] && c[1].body)
      .filter(Boolean)
      .map((b) => JSON.parse(b));
    // "drop" removed outright.
    expect(bodies).toContainEqual({ depends_on: "drop", action: "remove" });
    // "retype" is a remove followed by a re-add with the new type.
    expect(bodies).toContainEqual({ depends_on: "retype", action: "remove" });
    expect(bodies).toContainEqual({
      depends_on: "retype",
      type: "related",
      action: "add",
    });
    // "new" added.
    expect(bodies).toContainEqual({
      depends_on: "new",
      type: "parent-child",
      action: "add",
    });
    // "keep" is untouched — no POST references it.
    expect(bodies.some((b) => b.depends_on === "keep")).toBe(false);

    // Baseline advanced to the working set, so the panel is no longer dirty.
    bag = await render(args);
    expect(bag.depsDirty).toBe(false);
  });

  test("no-op returns true with no network call when not dirty", async () => {
    freshMount();
    global.fetch = jest.fn();
    const args = baseArgs();
    let bag = await render(args);
    bag.setDeps([{ id: "a", dependency_type: "blocks" }]);
    bag = await render(args);
    const ok = await bag.persistDeps();
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
    bag.setDeps([{ id: "a", dependency_type: "blocks" }]);
    bag = await render(args);
    bag.addDepLocal("b", "blocks");
    bag = await render(args);

    const ok = await bag.persistDeps();
    await flush();

    expect(ok).toBe(false);
    expect(showToast).toHaveBeenCalledWith({
      style: "error",
      title: "Request failed with status 500",
    });
    bag = await render(args);
    expect(bag.depsDirty).toBe(true);
  });
});

describe("useIssueDependencies — handleAddDep", () => {
  test("no-op when newDepId is blank (initial state)", async () => {
    freshMount();
    global.fetch = jest.fn();
    const bag = await render(baseArgs());
    bag.handleAddDep();
    expect(global.fetch).not.toHaveBeenCalled();
  });

  test("trims newDepId, stages it in-memory with the chosen type, and clears the draft (no network)", async () => {
    freshMount();
    global.fetch = jest.fn();
    const args = baseArgs({
      allIssues: [{ id: "mitto-dep2", title: "Dep 2", status: "open" }],
    });
    let bag = await render(args);
    // Simulate choosing a type and typing into the add-dep draft, then a
    // re-render so the next handleAddDep closure reads the updated state.
    bag.setNewDepType("related");
    bag.setNewDepId("  mitto-dep2  ");
    bag = await render(args);

    bag.handleAddDep();
    bag = await render(args);

    const added = bag.deps.find((d) => d.id === "mitto-dep2");
    expect(added).toBeTruthy();
    expect(added.dependency_type).toBe("related");
    // Draft is cleared after staging; a subsequent render observes it.
    expect(bag.newDepId).toBe("");
    expect(global.fetch).not.toHaveBeenCalled();
  });
});

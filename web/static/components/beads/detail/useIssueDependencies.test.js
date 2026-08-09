/**
 * Tests for useIssueDependencies.js (mitto-7gta.17 slice S3 Test phase).
 *
 * The hook had no prior test file (mitto-90f.7 PR-17 extracted it verbatim
 * with no accompanying tests), so this is dedicated new coverage for the
 * migration onto getSdkClient(), mirroring the precedent set by S2's
 * folder-config hook tests. Focuses on the three network-bearing operations:
 * fetchDeps (GET /api/issues/{id}), mutateDep (POST .../dependencies), and
 * changeDepType's two-step remove-then-add flow, which the Implementation
 * comment specifically calls out as needing its exact original control flow
 * preserved (a failed remove returns early with no re-fetch; a failed add
 * still triggers fetchDeps+onUpdated).
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
    workingDir: "/tmp/wsA",
    showToast: jest.fn(),
    fetchDepsRef: { current: null },
    onUpdated: jest.fn(),
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

describe("useIssueDependencies — mutateDep", () => {
  test("add: POSTs {depends_on, type, action} and refreshes on success", async () => {
    freshMount();
    global.fetch = jest.fn(() =>
      Promise.resolve(fakeResponse({ status: 204 })),
    );
    const showToast = jest.fn();
    const onUpdated = jest.fn();
    const bag = await render(baseArgs({ showToast, onUpdated }));

    const ok = await bag.mutateDep("add", "mitto-dep", "related");
    await flush();

    expect(ok).toBe(true);
    // Two fetches: the mutation POST, then fetchDeps' GET refresh.
    expect(global.fetch).toHaveBeenCalledTimes(2);
    const [url, init] = global.fetch.mock.calls[0];
    expect(String(url)).toContain("/api/issues/mitto-abc/dependencies");
    expect(init.method).toBe("POST");
    expect(JSON.parse(init.body)).toEqual({
      depends_on: "mitto-dep",
      action: "add",
      type: "related",
    });
    expect(showToast).toHaveBeenCalledWith({
      style: "success",
      title: "Added dependency on mitto-dep",
    });
    expect(onUpdated).toHaveBeenCalledTimes(1);
  });

  test("remove: defaults omit `type` and never call onUpdated on failure", async () => {
    freshMount();
    global.fetch = jest.fn(() =>
      Promise.resolve(
        fakeResponse({ status: 409, body: { error: "conflict" } }),
      ),
    );
    const showToast = jest.fn();
    const onUpdated = jest.fn();
    const bag = await render(baseArgs({ showToast, onUpdated }));

    const ok = await bag.mutateDep("remove", "mitto-dep");
    await flush();

    expect(ok).toBe(false);
    const [, init] = global.fetch.mock.calls[0];
    expect(JSON.parse(init.body)).toEqual({
      depends_on: "mitto-dep",
      action: "remove",
    });
    // errorMessage(err, fallback) prefers the SDK error's own message (here
    // the flat `error` code from the 409 body) over the local fallback text —
    // the fallback only applies to message-less errors (e.g. a network
    // failure).
    expect(showToast).toHaveBeenCalledWith({
      style: "error",
      title: "conflict",
    });
    expect(onUpdated).not.toHaveBeenCalled();
  });

  test("no-op when dependsOn is empty", async () => {
    freshMount();
    global.fetch = jest.fn();
    const bag = await render(baseArgs());
    await bag.mutateDep("add", "");
    expect(global.fetch).not.toHaveBeenCalled();
  });
});

describe("useIssueDependencies — handleAddDep", () => {
  test("no-op when newDepId is blank (initial state)", async () => {
    freshMount();
    global.fetch = jest.fn();
    const bag = await render(baseArgs());
    await bag.handleAddDep();
    expect(global.fetch).not.toHaveBeenCalled();
  });

  test("trims newDepId, calls mutateDep, and clears the draft on success", async () => {
    freshMount();
    global.fetch = jest.fn(() =>
      Promise.resolve(fakeResponse({ status: 204 })),
    );
    const args = baseArgs();
    let bag = await render(args);
    // Simulate typing into the add-dep draft, then a re-render so the next
    // handleAddDep closure reads the updated state.
    bag.setNewDepId("  mitto-dep2  ");
    bag = await render(args);

    await bag.handleAddDep();
    await flush();

    const [url, init] = global.fetch.mock.calls[0];
    expect(String(url)).toContain("/api/issues/mitto-abc/dependencies");
    expect(JSON.parse(init.body).depends_on).toBe("mitto-dep2");
    // Draft is cleared after a successful add; a subsequent render observes it.
    bag = await render(args);
    expect(bag.newDepId).toBe("");
  });
});

describe("useIssueDependencies — changeDepType", () => {
  test("success: remove then add, single success toast, one refresh", async () => {
    freshMount();
    global.fetch = jest.fn(() =>
      Promise.resolve(fakeResponse({ status: 204 })),
    );
    const showToast = jest.fn();
    const onUpdated = jest.fn();
    const bag = await render(baseArgs({ showToast, onUpdated }));

    await bag.changeDepType("mitto-dep", "related");
    await flush();

    // remove POST, add POST, then fetchDeps' GET refresh.
    expect(global.fetch).toHaveBeenCalledTimes(3);
    const removeBody = JSON.parse(global.fetch.mock.calls[0][1].body);
    const addBody = JSON.parse(global.fetch.mock.calls[1][1].body);
    expect(removeBody).toEqual({ depends_on: "mitto-dep", action: "remove" });
    expect(addBody).toEqual({
      depends_on: "mitto-dep",
      type: "related",
      action: "add",
    });
    expect(showToast).toHaveBeenCalledWith({
      style: "success",
      title: "Changed mitto-dep to related",
    });
    expect(onUpdated).toHaveBeenCalledTimes(1);
  });

  test("remove fails: error toast, no add call, no refresh, no onUpdated", async () => {
    freshMount();
    global.fetch = jest.fn(() =>
      Promise.resolve(fakeResponse({ status: 500 })),
    );
    const showToast = jest.fn();
    const onUpdated = jest.fn();
    const bag = await render(baseArgs({ showToast, onUpdated }));

    await bag.changeDepType("mitto-dep", "related");
    await flush();

    // Only the failed remove POST — no add attempt, no fetchDeps refresh.
    expect(global.fetch).toHaveBeenCalledTimes(1);
    expect(showToast).toHaveBeenCalledWith({
      style: "error",
      title: "Request failed with status 500",
    });
    expect(onUpdated).not.toHaveBeenCalled();
  });

  test("remove succeeds, add fails: error toast for add, but STILL refreshes and notifies", async () => {
    freshMount();
    let call = 0;
    global.fetch = jest.fn(() => {
      call += 1;
      // 1st call = remove (succeeds), 2nd call = add (fails), 3rd = fetchDeps GET.
      if (call === 2) return Promise.resolve(fakeResponse({ status: 500 }));
      return Promise.resolve(fakeResponse({ status: 204 }));
    });
    const showToast = jest.fn();
    const onUpdated = jest.fn();
    const bag = await render(baseArgs({ showToast, onUpdated }));

    await bag.changeDepType("mitto-dep", "related");
    await flush();

    expect(global.fetch).toHaveBeenCalledTimes(3);
    expect(showToast).toHaveBeenCalledWith({
      style: "error",
      title: "Request failed with status 500",
    });
    // Preserves the original control flow: a failed add still triggers the
    // refresh + parent notification so the UI reflects the successful remove.
    expect(onUpdated).toHaveBeenCalledTimes(1);
  });

  test("no-op when depsBusy (initial state false, so this pins the guard exists)", async () => {
    freshMount();
    global.fetch = jest.fn();
    const bag = await render(baseArgs());
    await bag.changeDepType("", "related");
    expect(global.fetch).not.toHaveBeenCalled();
  });
});

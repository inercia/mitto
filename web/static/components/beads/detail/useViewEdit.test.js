/**
 * Tests for useViewEdit.js (mitto-7gta.17 slice S3 Test phase).
 *
 * The hook had no prior test file (mitto-90f.7 PR-16 extracted it verbatim
 * with no accompanying tests). This file covers handleViewSave, the sole
 * network-bearing operation (PATCH /api/issues/{id}), migrated onto
 * getSdkClient() in slice S3. The extensive DOM-focused edit-mode
 * bookkeeping (per-field editing toggles, outside-click, focus effects,
 * markdown memo) is unrelated to the SDK migration and is out of scope here.
 *
 * Harness mirrors useCreateMode.test.js. `useMemo` recomputes on every call
 * (no memoization needed for a single-render-per-test harness); `renderMarkdown`
 * is not exercised by these tests since `creating` stays false with an empty
 * description in most cases (its output is irrelevant to handleViewSave).
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
  global.document = {
    cookie: "",
    addEventListener: () => {},
    removeEventListener: () => {},
  };
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
  useMemo: (fn) => fn(),
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
  // bare "./useViewEdit.js" path (via useBeadsDetailPanel.js's own static
  // import) under a DIFFERENT window.preact stub. Without a distinct query
  // string here, ESM's per-path module cache would hand this file the OTHER
  // test file's already-evaluated module — whose captured
  // useState/useRef/useCallback/useMemo are bound to that file's `cells`
  // array, not this file's — silently breaking every cross-render assertion.
  hookMod = hookMod || (await import("./useViewEdit.js?slice-s3-test"));
  return hookMod.useViewEdit(args);
}

// Effect index of the "seed non-notes fields whenever a different issue
// opens" effect (source order: outside-click, SEED, edit-mode-reset,
// notes-focus, title-focus, assignee-focus). Runs it explicitly since our
// stub never auto-fires effects — real Preact would run it on mount,
// populating viewDraft from `data` before any user edit.
const SEED_EFFECT_IDX = 1;

/** Mounts the hook and runs the seed effect so viewDraft mirrors `data`,
 * matching the state a real first render would reach before a save. */
async function mountSeeded(args) {
  let bag = await render(args);
  currentEffects[SEED_EFFECT_IDX].cb();
  bag = await render(args);
  return bag;
}

function freshMount() {
  cells = [];
  cellIdx = 0;
  currentEffects = [];
  global.document.cookie = "mitto_csrf=test-token";
}

const ORIGINAL_ISSUE = {
  id: "mitto-abc",
  title: "Original title",
  issue_type: "task",
  priority: 2,
  description: "Original description",
  assignee: "",
};

function baseArgs(overrides = {}) {
  return {
    data: ORIGINAL_ISSUE,
    creating: false,
    workingDir: "/tmp/wsA",
    showToast: jest.fn(),
    onUpdated: jest.fn(),
    ...overrides,
  };
}

describe("useViewEdit — handleViewSave", () => {
  test("no-op (no PATCH) when nothing in the draft differs from the original", async () => {
    freshMount();
    global.fetch = jest.fn();
    const bag = await mountSeeded(baseArgs());
    await bag.handleViewSave();
    expect(global.fetch).not.toHaveBeenCalled();
  });

  test("only the changed field(s) are sent in the PATCH body", async () => {
    freshMount();
    global.fetch = jest.fn(() =>
      Promise.resolve(fakeResponse({ status: 204 })),
    );
    const args = baseArgs();
    let bag = await mountSeeded(args);
    bag.setViewDraft((prev) => ({ ...prev, title: "New title" }));
    bag = await render(args);

    await bag.handleViewSave();
    await flush();

    expect(global.fetch).toHaveBeenCalledTimes(1);
    const [url, init] = global.fetch.mock.calls[0];
    expect(String(url)).toContain("/api/issues/mitto-abc");
    expect(init.method).toBe("PATCH");
    expect(JSON.parse(init.body)).toEqual({ title: "New title" });
  });

  test("success: toasts, exits every edit mode, records a saved baseline, and notifies", async () => {
    freshMount();
    global.fetch = jest.fn(() =>
      Promise.resolve(fakeResponse({ status: 204 })),
    );
    const showToast = jest.fn();
    const onUpdated = jest.fn();
    const args = baseArgs({ showToast, onUpdated });
    let bag = await mountSeeded(args);
    bag.setViewDraft((prev) => ({ ...prev, description: "New description" }));
    bag.setEditingDesc(true);
    bag = await render(args);

    await bag.handleViewSave();
    await flush();

    expect(showToast).toHaveBeenCalledWith({
      style: "success",
      title: "Changes saved",
    });
    expect(onUpdated).toHaveBeenCalledTimes(1);

    bag = await render(args);
    expect(bag.editingDesc).toBe(false);
    expect(bag.savingView).toBe(false);
    // The dirty check now compares against the just-saved baseline, so it
    // clears immediately without waiting for onUpdated's async re-seed.
    expect(bag.viewDirty).toBe(false);
  });

  test("notes: also updates local notes state via setNotes when included in the body", async () => {
    freshMount();
    global.fetch = jest.fn(() =>
      Promise.resolve(fakeResponse({ status: 204 })),
    );
    const args = baseArgs();
    let bag = await mountSeeded(args);
    bag.setViewDraft((prev) => ({ ...prev, notes: "new notes" }));
    bag = await render(args);

    await bag.handleViewSave();
    await flush();

    bag = await render(args);
    expect(bag.notes).toBe("new notes");
  });

  test("failure: error toast, savingView reset, no baseline recorded (still dirty)", async () => {
    freshMount();
    global.fetch = jest.fn(() =>
      Promise.resolve(fakeResponse({ status: 500 })),
    );
    const showToast = jest.fn();
    const onUpdated = jest.fn();
    const args = baseArgs({ showToast, onUpdated });
    let bag = await mountSeeded(args);
    bag.setViewDraft((prev) => ({ ...prev, title: "New title" }));
    bag = await render(args);

    await bag.handleViewSave();
    await flush();

    expect(showToast).toHaveBeenCalledWith({
      style: "error",
      title: "Request failed with status 500",
    });
    expect(onUpdated).not.toHaveBeenCalled();

    bag = await render(args);
    expect(bag.savingView).toBe(false);
    expect(bag.viewDirty).toBe(true);
  });

  test("no-op when data has no id (e.g. panel closing/create mode)", async () => {
    freshMount();
    global.fetch = jest.fn();
    const args = baseArgs({ data: null });
    const bag = await mountSeeded(args);
    bag.setViewDraft((prev) => ({ ...prev, title: "New title" }));
    await bag.handleViewSave();
    expect(global.fetch).not.toHaveBeenCalled();
  });
});

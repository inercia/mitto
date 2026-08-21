/**
 * Tests for useIssueComments.js (mitto-7gta.17 slice S3 Test phase).
 *
 * The hook had no prior test file (mitto-90f.7 PR-13 extracted it verbatim
 * with no accompanying tests), so this is dedicated new coverage for the
 * migration onto getSdkClient(). Covers handleCommentBlur, the sole
 * network-bearing operation (POST /api/issues/{id}/comments), plus its
 * fetchDepsRef bridge and the empty-draft close-without-request short
 * circuit.
 *
 * Harness mirrors useIssueLabels.test.js.
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
  useRef: (initial) => {
    const i = cellIdx++;
    if (!(i in cells)) cells[i] = { current: initial };
    return cells[i];
  },
  useCallback: (fn) => fn,
  useEffect: () => {},
};

async function flush() {
  for (let i = 0; i < 10; i++) await Promise.resolve();
}

let hookMod;
async function render(args) {
  cellIdx = 0;
  // Cache-busting query: useBeadsDetailPanel.test.js transitively imports the
  // bare "./useIssueComments.js" path (via useBeadsDetailPanel.js's own
  // static import) under a DIFFERENT window.preact stub. Without a distinct
  // query string here, ESM's per-path module cache would hand this file the
  // OTHER test file's already-evaluated module — whose captured
  // useState/useRef/useCallback are bound to that file's `cells` array, not
  // this file's — silently breaking every cross-render assertion.
  hookMod = hookMod || (await import("./useIssueComments.js?slice-s3-test"));
  return hookMod.useIssueComments(args);
}

function freshMount() {
  cells = [];
  cellIdx = 0;
  global.document.cookie = "mitto_csrf=test-token";
}

function baseArgs(overrides = {}) {
  return {
    data: { id: "mitto-abc" },
    workingDir: "/tmp/wsA",
    showToast: jest.fn(),
    fetchDepsRef: { current: null },
    onUpdated: jest.fn(),
    ...overrides,
  };
}

describe("useIssueComments — handleCommentBlur", () => {
  test("empty draft closes the editor without issuing a request", async () => {
    freshMount();
    global.fetch = jest.fn();
    const args = baseArgs();
    let bag = await render(args);
    bag.setCommentDraft("   ");
    bag = await render(args);

    await bag.handleCommentBlur();
    expect(global.fetch).not.toHaveBeenCalled();
    bag = await render(args);
    expect(bag.addingComment).toBe(false);
  });

  test("posts the trimmed text, toasts, clears the draft, refreshes deps, and notifies", async () => {
    freshMount();
    global.fetch = jest.fn(() =>
      Promise.resolve(fakeResponse({ status: 204 })),
    );
    const showToast = jest.fn();
    const onUpdated = jest.fn();
    const fetchDepsRef = { current: jest.fn(() => Promise.resolve()) };
    const args = baseArgs({ showToast, onUpdated, fetchDepsRef });
    let bag = await render(args);
    bag.setCommentDraft("  looks good  ");
    bag = await render(args);

    await bag.handleCommentBlur();
    await flush();

    expect(global.fetch).toHaveBeenCalledTimes(1);
    const [url, init] = global.fetch.mock.calls[0];
    expect(String(url)).toContain("/api/issues/mitto-abc/comments");
    expect(init.method).toBe("POST");
    expect(JSON.parse(init.body)).toEqual({ text: "looks good" });
    expect(showToast).toHaveBeenCalledWith({
      style: "success",
      title: "Comment added",
    });
    expect(fetchDepsRef.current).toHaveBeenCalledWith(false);
    expect(onUpdated).toHaveBeenCalledTimes(1);

    bag = await render(args);
    expect(bag.commentDraft).toBe("");
    expect(bag.savingComment).toBe(false);
    expect(bag.addingComment).toBe(false);
  });

  test("failure: error toast, editor still closes, no fetchDepsRef/onUpdated call", async () => {
    freshMount();
    global.fetch = jest.fn(() =>
      Promise.resolve(fakeResponse({ status: 500 })),
    );
    const showToast = jest.fn();
    const onUpdated = jest.fn();
    const fetchDepsRef = { current: jest.fn() };
    const args = baseArgs({ showToast, onUpdated, fetchDepsRef });
    let bag = await render(args);
    bag.setCommentDraft("looks good");
    bag = await render(args);

    await bag.handleCommentBlur();
    await flush();

    expect(showToast).toHaveBeenCalledWith({
      style: "error",
      title: "Request failed with status 500",
    });
    expect(fetchDepsRef.current).not.toHaveBeenCalled();
    expect(onUpdated).not.toHaveBeenCalled();

    bag = await render(args);
    expect(bag.addingComment).toBe(false);
    expect(bag.savingComment).toBe(false);
  });

  test("tolerates a missing fetchDepsRef.current (bridge not wired yet)", async () => {
    freshMount();
    global.fetch = jest.fn(() =>
      Promise.resolve(fakeResponse({ status: 204 })),
    );
    const args = baseArgs({ fetchDepsRef: { current: null } });
    let bag = await render(args);
    bag.setCommentDraft("hi");
    bag = await render(args);
    await expect(bag.handleCommentBlur()).resolves.toBeUndefined();
  });
});

describe("useIssueComments — startAddComment", () => {
  test("opens the editor with an empty draft", async () => {
    freshMount();
    const args = baseArgs();
    let bag = await render(args);
    bag.setCommentDraft("leftover");
    bag = await render(args);

    bag.startAddComment();
    bag = await render(args);
    expect(bag.addingComment).toBe(true);
    expect(bag.commentDraft).toBe("");
  });

  test("no-op while a save is already in flight (savingComment=true)", async () => {
    freshMount();
    const args = baseArgs();
    let bag = await render(args);
    bag.setSavingComment(true);
    bag.setAddingComment(false);
    bag = await render(args);

    bag.startAddComment();
    bag = await render(args);
    expect(bag.addingComment).toBe(false);
  });
});

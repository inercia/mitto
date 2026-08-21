/**
 * Tests for useBeadsIntegration.js
 *
 * Two flavors:
 *   1. Pure unit tests on `buildBeadsPromptToast` — one payload per branch.
 *   2. An integration-flavor test that invokes `handleRunBeadsListPrompt` with
 *      a stubbed `newSession` returning {sessionId, reused: true} and asserts
 *      that `showToast` is called with the reused-branch payload (the exact
 *      test the bead's spec asks for: "stubs newSession to return
 *      {session_id, reused: true} and asserts the toast was rendered").
 */

// testGlobals.js re-exports the lifecycle globals and `jest` from whichever
// runner is active (Jest under Node ESM, bun:test under Bun), so a single
// import works under both runners.
import {
  describe,
  test,
  expect,
  afterEach,
  jest,
} from "../utils/testing/testGlobals.js";
import {
  buildBeadsPromptToast,
  useBeadsIntegration,
} from "./useBeadsIntegration.js";
import { invalidateWorkspacePromptsCache } from "../utils/promptsCache.js";

// Provide a minimal window.preact stub so the hook's lazy destructure works.
// useBeadsIntegration reads useState/useCallback/useMemo/useRef from
// window.preact lazily inside its body (see the file's header comment), so
// pass-through stubs are enough for these tests.
//
// Per-field top-up (not `window.preact = window.preact || {...}`) so a partial
// stub left by an earlier test file under Bun's shared-process runner does not
// short-circuit the guard and leave hooks undefined (mitto-txpp.6).
global.window = global.window || {};
window.preact = window.preact || {};
window.preact.useState =
  window.preact.useState ||
  ((initial) => [
    typeof initial === "function" ? initial() : initial,
    () => {},
  ]);
window.preact.useCallback = window.preact.useCallback || ((fn) => fn);
window.preact.useMemo = window.preact.useMemo || ((fn) => fn());
window.preact.useRef =
  window.preact.useRef || ((initial) => ({ current: initial }));
window.mittoApiPrefix = "";

if (typeof document === "undefined") {
  global.document = { cookie: "" };
} else {
  Object.defineProperty(document, "cookie", {
    value: "",
    writable: true,
    configurable: true,
  });
}

afterEach(() => {
  jest.restoreAllMocks();
  invalidateWorkspacePromptsCache();
});

describe("buildBeadsPromptToast", () => {
  test("not reused (per-issue) → success + 'Started ... for {issueId}'", () => {
    const toast = buildBeadsPromptToast({
      result: { sessionId: "s-new", reused: false },
      promptName: "Review",
      issueId: "mitto-abc",
      activeSessionId: "s-current",
    });
    expect(toast).toEqual({
      style: "success",
      title: 'Started "Review" for mitto-abc',
      duration: 3000,
    });
  });

  test("not reused (no issueId) → success + 'Started \"{promptName}\"'", () => {
    const toast = buildBeadsPromptToast({
      result: { sessionId: "s-new", reused: false },
      promptName: "Pull tasks",
    });
    expect(toast).toEqual({
      style: "success",
      title: 'Started "Pull tasks"',
      duration: 3000,
    });
  });

  test("reused into different session (per-issue) → info + 'Continued in existing conversation for {issueId}'", () => {
    const toast = buildBeadsPromptToast({
      result: { sessionId: "s-other", reused: true },
      promptName: "Review",
      issueId: "mitto-abc",
      activeSessionId: "s-current",
    });
    expect(toast).toEqual({
      style: "info",
      title: "Continued in existing conversation for mitto-abc",
      duration: 3000,
    });
  });

  test("reused into different session (no issueId) → info + 'Continued in existing \"{promptName}\" conversation'", () => {
    const toast = buildBeadsPromptToast({
      result: { sessionId: "s-other", reused: true },
      promptName: "Pull tasks",
      activeSessionId: "s-current",
    });
    expect(toast).toEqual({
      style: "info",
      title: 'Continued in existing "Pull tasks" conversation',
      duration: 3000,
    });
  });

  test("reused into the current session → info + 'Prompt enqueued into current conversation'", () => {
    const toast = buildBeadsPromptToast({
      result: { sessionId: "s-current", reused: true },
      promptName: "Review",
      issueId: "mitto-abc",
      activeSessionId: "s-current",
    });
    expect(toast).toEqual({
      style: "info",
      title: "Prompt enqueued into current conversation",
      duration: 3000,
    });
  });

  test("reused into the current session (no issueId) → same no-op wording", () => {
    const toast = buildBeadsPromptToast({
      result: { sessionId: "s-current", reused: true },
      promptName: "Pull tasks",
      activeSessionId: "s-current",
    });
    expect(toast.title).toBe("Prompt enqueued into current conversation");
    expect(toast.style).toBe("info");
  });

  test("reused but activeSessionId unknown → falls back to 'Continued in existing ...' (not no-op)", () => {
    const toast = buildBeadsPromptToast({
      result: { sessionId: "s-other", reused: true },
      promptName: "Review",
      issueId: "mitto-abc",
      // activeSessionId omitted
    });
    expect(toast.style).toBe("info");
    expect(toast.title).toBe(
      "Continued in existing conversation for mitto-abc",
    );
  });

  test("reused missing / falsy → treated as not reused", () => {
    const toast = buildBeadsPromptToast({
      result: { sessionId: "s-new" },
      promptName: "Review",
      issueId: "mitto-abc",
    });
    expect(toast.style).toBe("success");
    expect(toast.title).toBe('Started "Review" for mitto-abc');
  });

  test("duration is always 3000ms regardless of branch", () => {
    const branches = [
      { result: { sessionId: "a", reused: false }, promptName: "P" },
      { result: { sessionId: "a", reused: true }, promptName: "P" },
      {
        result: { sessionId: "a", reused: true },
        promptName: "P",
        activeSessionId: "a",
      },
    ];
    for (const args of branches) {
      expect(buildBeadsPromptToast(args).duration).toBe(3000);
    }
  });
});

// =============================================================================
// useBeadsIntegration — handleRunBeadsListPrompt wiring (integration flavor)
// =============================================================================

/**
 * Instantiate the hook with pass-through preact stubs and a stubbed
 * `newSession`; return its handler bundle plus the showToast spy.
 */
function mountHookForListPrompt({ newSessionImpl, activeSessionId }) {
  const showToast = jest.fn();
  const bundle = useBeadsIntegration({
    allSessions: [],
    workspaces: [{ working_dir: "/w", is_default: true, acp_server: "acp" }],
    newSession: newSessionImpl,
    showToast,
    switchSession: jest.fn(),
    setMainView: jest.fn(),
    setShowSidebar: jest.fn(),
    setShowSidePanel: jest.fn(),
    setSidePanelTab: jest.fn(),
    activeSessionId,
    // onOpenLoopDialog and onOpenPromptParamDialog left undefined so the
    // handler takes the direct path (no dialogs).
  });
  return { showToast, bundle };
}

describe("useBeadsIntegration — handleRunBeadsListPrompt reused branch", () => {
  test("newSession returns reused:true into a different session → showToast called with info + 'Continued in existing ...' payload", async () => {
    const newSession = jest
      .fn()
      .mockResolvedValue({ sessionId: "s-other", reused: true });
    const { showToast, bundle } = mountHookForListPrompt({
      newSessionImpl: newSession,
      activeSessionId: "s-current",
    });

    await bundle.handleRunBeadsListPrompt({ name: "Pull tasks" }, "/w");

    expect(newSession).toHaveBeenCalledTimes(1);
    expect(showToast).toHaveBeenCalledTimes(1);
    expect(showToast).toHaveBeenCalledWith({
      style: "info",
      title: 'Continued in existing "Pull tasks" conversation',
      duration: 3000,
    });
  });

  test("newSession returns reused:true into the CURRENT session → showToast called with 'Prompt enqueued into current conversation'", async () => {
    const newSession = jest
      .fn()
      .mockResolvedValue({ sessionId: "s-current", reused: true });
    const { showToast, bundle } = mountHookForListPrompt({
      newSessionImpl: newSession,
      activeSessionId: "s-current",
    });

    await bundle.handleRunBeadsListPrompt({ name: "Pull tasks" }, "/w");

    expect(showToast).toHaveBeenCalledWith({
      style: "info",
      title: "Prompt enqueued into current conversation",
      duration: 3000,
    });
  });

  test("newSession returns reused:false → showToast called with success 'Started ...' payload (regression: not-reused branch preserved)", async () => {
    const newSession = jest
      .fn()
      .mockResolvedValue({ sessionId: "s-new", reused: false });
    const { showToast, bundle } = mountHookForListPrompt({
      newSessionImpl: newSession,
      activeSessionId: "s-current",
    });

    await bundle.handleRunBeadsListPrompt({ name: "Pull tasks" }, "/w");

    expect(showToast).toHaveBeenCalledWith({
      style: "success",
      title: 'Started "Pull tasks"',
      duration: 3000,
    });
  });

  test("newSession fails (no sessionId) → error toast, not the reused branch", async () => {
    const newSession = jest.fn().mockResolvedValue({ error: "boom" });
    const { showToast, bundle } = mountHookForListPrompt({
      newSessionImpl: newSession,
      activeSessionId: "s-current",
    });

    await bundle.handleRunBeadsListPrompt({ name: "Pull tasks" }, "/w");

    expect(showToast).toHaveBeenCalledTimes(1);
    const arg = showToast.mock.calls[0][0];
    expect(arg.style).toBe("error");
  });
});

// =============================================================================
// fetchBeadsPromptsForWorkspace / fetchBeadsListPromptsForWorkspace (mitto-8x9)
// =============================================================================
//
// Both fetchers were routed through the shared promptsCache module so bursts
// of beads-row/list menu opens coalesce into one /api/workspace-prompts
// request. Exercised end-to-end against a mocked global.fetch (not a mocked
// cache) so the assertions cover the real coalescing behavior.

function immediateOkFetch(body = { prompts: [], migrated: [] }) {
  return jest.fn(() =>
    Promise.resolve({
      status: 200,
      ok: true,
      headers: { get: () => null },
      json: () => Promise.resolve(body),
    }),
  );
}

function mountBundle() {
  return useBeadsIntegration({
    allSessions: [],
    workspaces: [{ working_dir: "/w", is_default: true, acp_server: "acp" }],
    newSession: jest.fn(),
    showToast: jest.fn(),
    switchSession: jest.fn(),
    setMainView: jest.fn(),
    setShowSidebar: jest.fn(),
    setShowSidePanel: jest.fn(),
    setSidePanelTab: jest.fn(),
    activeSessionId: "s-current",
  });
}

describe("fetchBeadsPromptsForWorkspace", () => {
  test("maps issue fields to item_* params and keeps only beadsIssues-menu prompts", async () => {
    global.fetch = immediateOkFetch({
      prompts: [
        { name: "Review", menus: "beadsIssues" },
        { name: "Not for beads", menus: "prompts" },
      ],
    });
    const bundle = mountBundle();

    const result = await bundle.fetchBeadsPromptsForWorkspace("/w", {
      id: "mitto-1",
      status: "open",
      issue_type: "task",
      priority: 2,
      labels: ["frontend", "performance"],
    });

    expect(result.map((p) => p.name)).toEqual(["Review"]);
    const url = global.fetch.mock.calls[0][0];
    expect(url).toContain("working_dir=");
    expect(url).toContain("item_kind=beadsIssue");
    expect(url).toContain("item_id=mitto-1");
    expect(url).toContain("item_status=open");
    expect(url).toContain("item_priority=2");
    expect(url).toContain("item_labels=frontend%2Cperformance");
  });

  test("two calls with identical workingDir+issue coalesce into one HTTP request", async () => {
    global.fetch = immediateOkFetch({ prompts: [] });
    const bundle = mountBundle();
    const issue = { id: "mitto-1", status: "open", issue_type: "task" };

    const p1 = bundle.fetchBeadsPromptsForWorkspace("/w", issue);
    const p2 = bundle.fetchBeadsPromptsForWorkspace("/w", issue);
    await Promise.all([p1, p2]);

    expect(global.fetch).toHaveBeenCalledTimes(1);
  });

  test("no workingDir → [] without calling fetch", async () => {
    global.fetch = jest.fn();
    const bundle = mountBundle();

    const result = await bundle.fetchBeadsPromptsForWorkspace(null, {
      id: "mitto-1",
    });

    expect(result).toEqual([]);
    expect(global.fetch).not.toHaveBeenCalled();
  });
});

describe("fetchBeadsListPromptsForWorkspace", () => {
  test("sends enabled_context=workspace with no item_* params, keeps only beadsList-menu prompts", async () => {
    global.fetch = immediateOkFetch({
      prompts: [
        { name: "Triage", menus: "beadsList" },
        { name: "Per-issue only", menus: "beadsIssues" },
      ],
    });
    const bundle = mountBundle();

    const result = await bundle.fetchBeadsListPromptsForWorkspace("/w");

    expect(result.map((p) => p.name)).toEqual(["Triage"]);
    const url = global.fetch.mock.calls[0][0];
    expect(url).toContain("enabled_context=workspace");
    expect(url).not.toContain("item_kind");
  });

  test("the list-button call site and loadShortcuts call site share one cached response", async () => {
    global.fetch = immediateOkFetch({ prompts: [] });
    const bundle = mountBundle();

    await bundle.fetchBeadsListPromptsForWorkspace("/w"); // e.g. list button
    await bundle.fetchBeadsListPromptsForWorkspace("/w"); // e.g. loadShortcuts

    expect(global.fetch).toHaveBeenCalledTimes(1);
  });
});

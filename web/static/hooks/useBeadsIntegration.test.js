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

import { jest } from "@jest/globals";
import {
  buildBeadsPromptToast,
  useBeadsIntegration,
} from "./useBeadsIntegration.js";

// Provide a minimal window.preact stub so the hook's lazy destructure works.
// useBeadsIntegration reads useState/useCallback/useMemo/useRef from
// window.preact lazily inside its body (see the file's header comment), so
// pass-through stubs are enough for these tests.
global.window = global.window || {};
window.preact = window.preact || {
  useState: (initial) => [
    typeof initial === "function" ? initial() : initial,
    () => {},
  ],
  useCallback: (fn) => fn,
  useMemo: (fn) => fn(),
  useRef: (initial) => ({ current: initial }),
};
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

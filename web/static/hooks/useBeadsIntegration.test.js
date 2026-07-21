/**
 * Tests for useBeadsIntegration.js
 *
 * Covers the pure buildBeadsPromptToast helper, which produces the toast payload
 * for the four handler call sites (per-issue and list, each with and without a
 * loop dialog) plus the app.js Tasks quick-launch site.
 */

import { buildBeadsPromptToast } from "./useBeadsIntegration.js";

// Provide a minimal window.preact stub so the module-level destructure doesn't throw.
global.window = global.window || {};
window.preact = window.preact || {
  useState: () => [undefined, () => {}],
  useCallback: (fn) => fn,
  useMemo: (fn) => fn(),
  useRef: () => ({ current: undefined }),
};
window.mittoApiPrefix = "";

if (typeof document === "undefined") {
  global.document = { cookie: "" };
}

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

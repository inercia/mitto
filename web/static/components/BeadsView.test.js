/**
 * Unit tests for BeadsView response-parsing logic.
 *
 * Tests cover readBeadsResponse: the defensive helper that reads a fetch
 * Response as text and only then attempts JSON.parse, so a non-JSON body
 * (e.g. a plain-text 403 from the old localhost gate) never triggers Safari's
 * cryptic "The string did not match the expected pattern." error.
 *
 * Helpers are imported from ../utils/beads.js (framework-free module extracted
 * in mitto-90f.3 E-1) so the tests exercise the real implementation instead of
 * a local duplicate that used to drift out of sync.
 */

import {
  readBeadsResponse,
  matchesSearch,
  computeEffectiveStreamingSet,
  taskTitleBackground,
  CLEANUP_PROGRESS_TOAST_INTERVAL_MS,
} from "../utils/beads.js";
// Namespaced import so a missing named export (e.g. isBeadsSchemaSkew before the
// mitto-n5mw fix lands) does NOT cause a module-load SyntaxError that would
// take the whole test file down — the missing helpers just show up as
// `undefined` on the namespace object.
import * as beadsUtils from "../utils/beads.js";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";

// =============================================================================
// readBeadsResponse logic
// =============================================================================

/**
 * Build a minimal mock fetch Response whose text() resolves to `body`.
 */
function mockResponse(body, status = 200) {
  return {
    status,
    text: () => Promise.resolve(body),
  };
}

describe("readBeadsResponse", () => {
  describe("valid JSON bodies", () => {
    test("parses a JSON object body", async () => {
      const res = mockResponse('{"id":"abc-1","title":"Hello"}');
      const data = await readBeadsResponse(res);
      expect(data).toEqual({ id: "abc-1", title: "Hello" });
    });

    test("parses a JSON array body (the list endpoint shape)", async () => {
      const res = mockResponse('[{"id":"abc-1"},{"id":"abc-2"}]');
      const data = await readBeadsResponse(res);
      expect(Array.isArray(data)).toBe(true);
      expect(data).toHaveLength(2);
      expect(data[0].id).toBe("abc-1");
    });

    test("passes through a JSON error object unchanged", async () => {
      const res = mockResponse('{"error":"bd not found"}', 200);
      const data = await readBeadsResponse(res);
      expect(data).toEqual({ error: "bd not found" });
    });

    test("parses an empty JSON array", async () => {
      const res = mockResponse("[]");
      const data = await readBeadsResponse(res);
      expect(data).toEqual([]);
    });
  });

  describe("non-JSON bodies become an error object", () => {
    test("plain-text 403 body is surfaced as { error: <text> }", async () => {
      const res = mockResponse(
        "This endpoint is only available from localhost\n",
        403,
      );
      const data = await readBeadsResponse(res);
      expect(data.error).toBe("This endpoint is only available from localhost");
    });

    test("HTML error page is surfaced as { error: <text> } (not thrown)", async () => {
      const res = mockResponse("<html><body>500</body></html>", 500);
      const data = await readBeadsResponse(res);
      expect(typeof data.error).toBe("string");
      expect(data.error).toContain("<html>");
    });

    test("does not throw on invalid JSON", async () => {
      const res = mockResponse("Unexpected token W", 200);
      await expect(readBeadsResponse(res)).resolves.toBeDefined();
    });
  });

  describe("empty and whitespace bodies fall back to an HTTP-status error", () => {
    test("empty body falls back to the HTTP status", async () => {
      const res = mockResponse("", 502);
      const data = await readBeadsResponse(res);
      expect(data.error).toBe("Request failed (HTTP 502)");
    });

    test("whitespace-only body falls back to the HTTP status", async () => {
      const res = mockResponse("   \n  ", 504);
      const data = await readBeadsResponse(res);
      expect(data.error).toBe("Request failed (HTTP 504)");
    });
  });

  // Coverage for the canonical nested error envelope produced by the Go web
  // handlers (see rule 11-web-backend-errors.md): {error:{code,message,details}}.
  // readBeadsResponse must flatten it to the {error:"<message>", code, stderr,
  // details} shape the beads UI consumers already expect. The previous local
  // duplicate of readBeadsResponse in this test file did NOT implement this
  // branch, so this coverage was silently missing (mitto-90f.3 E-2).
  describe("canonical nested error envelope is flattened", () => {
    test("bd-failure envelope with stderr is flattened to flat shape", async () => {
      const body = JSON.stringify({
        error: {
          code: "bd_failed",
          message: "bd exited 1",
          details: { stderr: "issue not found: mitto-xyz\n" },
        },
      });
      const res = mockResponse(body, 500);
      const data = await readBeadsResponse(res);
      expect(data.error).toBe("bd exited 1");
      expect(data.code).toBe("bd_failed");
      expect(data.stderr).toBe("issue not found: mitto-xyz\n");
      expect(data.details).toEqual({ stderr: "issue not found: mitto-xyz\n" });
    });

    test("validation-error envelope without stderr yields undefined stderr", async () => {
      const body = JSON.stringify({
        error: { code: "invalid_request", message: "title is required" },
      });
      const res = mockResponse(body, 400);
      const data = await readBeadsResponse(res);
      expect(data.error).toBe("title is required");
      expect(data.code).toBe("invalid_request");
      expect(data.stderr).toBeUndefined();
      expect(data.details).toBeUndefined();
    });

    test("envelope with no message falls back to HTTP status text", async () => {
      const body = JSON.stringify({ error: { code: "unknown" } });
      const res = mockResponse(body, 503);
      const data = await readBeadsResponse(res);
      expect(data.error).toBe("Request failed (HTTP 503)");
      expect(data.code).toBe("unknown");
    });

    test("flat {error:'<string>'} envelope is NOT rewritten (passthrough)", async () => {
      // Regression guard: only object-valued .error triggers normalization;
      // legacy string-valued .error must be returned untouched so existing
      // consumers keep working.
      const res = mockResponse('{"error":"bd not found"}', 200);
      const data = await readBeadsResponse(res);
      expect(data).toEqual({ error: "bd not found" });
      expect(data.code).toBeUndefined();
    });
  });
});

// =============================================================================
// matchesSearch logic — beads list search filtering
// =============================================================================

describe("matchesSearch", () => {
  const issue = {
    id: "mitto-3bx",
    title: "Beads Search Filtering",
    owner: "saurin@adobe.com",
    description: "Implement smart filtering in the beads list view search box.",
  };

  describe("empty queries match everything", () => {
    test("empty string matches", () => {
      expect(matchesSearch(issue, "")).toBe(true);
    });
    test("null / undefined matches", () => {
      expect(matchesSearch(issue, null)).toBe(true);
      expect(matchesSearch(issue, undefined)).toBe(true);
    });
    test("whitespace-only matches", () => {
      expect(matchesSearch(issue, "   \t  ")).toBe(true);
    });
  });

  describe("id matching", () => {
    test("exact id matches", () => {
      expect(matchesSearch(issue, "mitto-3bx")).toBe(true);
    });
    test("id is case-insensitive", () => {
      expect(matchesSearch(issue, "MITTO-3BX")).toBe(true);
    });
    test("partial id substring matches", () => {
      expect(matchesSearch(issue, "3bx")).toBe(true);
    });
    test("non-matching id returns false", () => {
      expect(matchesSearch(issue, "mitto-9zz")).toBe(false);
    });
  });

  describe("title matching", () => {
    test("single title word matches", () => {
      expect(matchesSearch(issue, "filtering")).toBe(true);
    });
    test("title is case-insensitive", () => {
      expect(matchesSearch(issue, "BEADS")).toBe(true);
    });
    test("title substring matches", () => {
      expect(matchesSearch(issue, "filt")).toBe(true);
    });
  });

  describe("description (body) matching", () => {
    test("body word matches when not in title", () => {
      expect(matchesSearch(issue, "smart")).toBe(true);
    });
    test("body substring matches", () => {
      expect(matchesSearch(issue, "view search")).toBe(true);
    });
    test("missing description does not throw", () => {
      const bare = { id: "x-1", title: "hi" };
      expect(matchesSearch(bare, "hi")).toBe(true);
      expect(matchesSearch(bare, "nope")).toBe(false);
    });
  });

  describe("owner matching is preserved", () => {
    test("owner email matches", () => {
      expect(matchesSearch(issue, "saurin")).toBe(true);
    });
  });

  describe("multi-word AND semantics", () => {
    test("all tokens must match (one in title, one in body)", () => {
      expect(matchesSearch(issue, "beads smart")).toBe(true);
    });
    test("returns false when any token is unmatched", () => {
      expect(matchesSearch(issue, "beads zzznope")).toBe(false);
    });
    test("tokens may match different fields (id + title)", () => {
      expect(matchesSearch(issue, "3bx filtering")).toBe(true);
    });
    test("extra whitespace between tokens is ignored", () => {
      expect(matchesSearch(issue, "   beads    smart  ")).toBe(true);
    });
  });

  describe("non-matching queries", () => {
    test("unrelated word returns false", () => {
      expect(matchesSearch(issue, "frontend")).toBe(false);
    });
  });
});

// =============================================================================
// "prompts" upstream: argument-free prompt filtering logic
// =============================================================================

/**
 * Duplicated filter from WorkspacesDialog.js / loadBeadsUpstreamPrompts for testing.
 * Keep in sync with implementation: filters to enabled AND parameter-free prompts.
 */
function filterArgumentFreePrompts(prompts) {
  return prompts.filter(
    (p) => p.enabled !== false && (!p.parameters || p.parameters.length === 0),
  );
}

describe("filterArgumentFreePrompts (prompts upstream picker)", () => {
  const basePrompts = [
    { name: "sync-tasks", enabled: true, parameters: [] },
    { name: "pull-issues", enabled: true, parameters: undefined },
    { name: "create-issue", enabled: true, parameters: [{ name: "title" }] },
    { name: "disabled-prompt", enabled: false, parameters: [] },
    { name: "disabled-param", enabled: false, parameters: [{ name: "type" }] },
    { name: "no-fields-at-all", enabled: true },
  ];

  test("includes prompts with empty parameters array", () => {
    const result = filterArgumentFreePrompts(basePrompts);
    expect(result.map((p) => p.name)).toContain("sync-tasks");
  });

  test("includes prompts with undefined parameters", () => {
    const result = filterArgumentFreePrompts(basePrompts);
    expect(result.map((p) => p.name)).toContain("pull-issues");
  });

  test("includes prompts with no parameters field", () => {
    const result = filterArgumentFreePrompts(basePrompts);
    expect(result.map((p) => p.name)).toContain("no-fields-at-all");
  });

  test("excludes prompts that have parameters (has required args)", () => {
    const result = filterArgumentFreePrompts(basePrompts);
    expect(result.map((p) => p.name)).not.toContain("create-issue");
  });

  test("excludes prompts where enabled === false", () => {
    const result = filterArgumentFreePrompts(basePrompts);
    expect(result.map((p) => p.name)).not.toContain("disabled-prompt");
    expect(result.map((p) => p.name)).not.toContain("disabled-param");
  });

  test("treats enabled: undefined as enabled (included)", () => {
    const prompt = { name: "no-enabled-field", parameters: [] };
    const result = filterArgumentFreePrompts([prompt]);
    expect(result).toHaveLength(1);
    expect(result[0].name).toBe("no-enabled-field");
  });

  test("returns empty array when no prompts pass the filter", () => {
    const allParameterized = [
      { name: "a", enabled: true, parameters: [{ name: "x" }] },
      { name: "b", enabled: false, parameters: [] },
    ];
    expect(filterArgumentFreePrompts(allParameterized)).toHaveLength(0);
  });

  test("returns all argument-free enabled prompts when all qualify", () => {
    const all = [
      { name: "x", enabled: true, parameters: [] },
      { name: "y", enabled: true },
    ];
    expect(filterArgumentFreePrompts(all)).toHaveLength(2);
  });
});

// =============================================================================
// "prompts" upstream: button disabled logic
// =============================================================================

/**
 * Mirrors the disable condition used in BeadsView for the "prompts" upstream buttons.
 * A button is disabled when its prompt name is empty OR onLaunchPrompt is absent.
 */
function isPromptButtonDisabled(promptName, onLaunchPrompt) {
  return !promptName || !onLaunchPrompt;
}

describe("prompts upstream button disabled logic", () => {
  const launcher = () => {};

  test("disabled when promptName is empty string", () => {
    expect(isPromptButtonDisabled("", launcher)).toBe(true);
  });

  test("disabled when promptName is undefined", () => {
    expect(isPromptButtonDisabled(undefined, launcher)).toBe(true);
  });

  test("disabled when onLaunchPrompt is absent (no prop wired)", () => {
    expect(isPromptButtonDisabled("my-prompt", undefined)).toBe(true);
  });

  test("disabled when both promptName and launcher are absent", () => {
    expect(isPromptButtonDisabled("", undefined)).toBe(true);
  });

  test("enabled when both promptName and onLaunchPrompt are present", () => {
    expect(isPromptButtonDisabled("sync-tasks", launcher)).toBe(false);
  });
});

// =============================================================================
// "prompts" upstream: onLaunchPrompt call convention
// =============================================================================

describe("onLaunchPrompt call convention", () => {
  /**
   * Simulates what the Pull/Push/Sync buttons do when clicked with a configured prompt:
   *   onLaunchPrompt(action, promptName)
   * — no arguments object, no loop, no acpServer (handled by handler in app.js).
   */
  function simulateButtonClick(action, promptName, onLaunchPrompt) {
    if (!promptName || !onLaunchPrompt) return;
    onLaunchPrompt(action, promptName);
  }

  /** Minimal call spy without jest.fn() (file uses ESM without @jest/globals import). */
  function makeSpy() {
    const calls = [];
    const spy = (...args) => calls.push(args);
    spy.calls = calls;
    spy.callCount = () => calls.length;
    spy.lastCall = () => calls[calls.length - 1];
    return spy;
  }

  test("pull button calls launcher with 'pull' action and the configured promptName", () => {
    const launcher = makeSpy();
    simulateButtonClick("pull", "sync-issues", launcher);
    expect(launcher.callCount()).toBe(1);
    expect(launcher.lastCall()).toEqual(["pull", "sync-issues"]);
  });

  test("push button calls launcher with 'push' action", () => {
    const launcher = makeSpy();
    simulateButtonClick("push", "push-tasks", launcher);
    expect(launcher.lastCall()).toEqual(["push", "push-tasks"]);
  });

  test("sync button calls launcher with 'sync' action", () => {
    const launcher = makeSpy();
    simulateButtonClick("sync", "full-sync", launcher);
    expect(launcher.lastCall()).toEqual(["sync", "full-sync"]);
  });

  test("button does NOT call launcher when promptName is empty", () => {
    const launcher = makeSpy();
    simulateButtonClick("pull", "", launcher);
    expect(launcher.callCount()).toBe(0);
  });

  test("button does NOT call launcher when onLaunchPrompt is absent", () => {
    // Nothing to assert — just ensure it doesn't throw
    expect(() =>
      simulateButtonClick("pull", "my-prompt", undefined),
    ).not.toThrow();
  });

  test("launcher is NOT called with an arguments object (argument-free)", () => {
    const launcher = makeSpy();
    simulateButtonClick("sync", "sync-prompt", launcher);
    // Must have exactly 2 args: action + promptName (no args/loop object)
    expect(launcher.lastCall()).toHaveLength(2);
  });
});

// =============================================================================
// Cleanup progress-toast throttle/replace logic
// =============================================================================

// The throttle harness mirrors handleCleanup's start toast and the onProgress
// handler in BeadsView.js. `now` is injected (rather than Date.now()) so the
// throttle window can be exercised deterministically. The interval constant
// itself is imported from utils/beads.js (mitto-90f.3 E-3).

function makeCleanupHarness({ workingDir = "/w" } = {}) {
  const refs = { cleanupToastId: null, lastCleanupToastAt: 0 };

  let nextToastId = 0;
  const showToast = (opts) => {
    showToast.calls.push(opts);
    return ++nextToastId;
  };
  showToast.calls = [];
  showToast.count = () => showToast.calls.length;
  showToast.last = () => showToast.calls[showToast.calls.length - 1];
  showToast.countByStyle = (style) =>
    showToast.calls.filter((c) => c.style === style).length;

  const dismissToast = (id) => dismissToast.ids.push(id);
  dismissToast.ids = [];
  dismissToast.count = () => dismissToast.ids.length;
  dismissToast.last = () => dismissToast.ids[dismissToast.ids.length - 1];

  const fetchList = () => {
    fetchList.count += 1;
  };
  fetchList.count = 0;

  const setCleaningUp = (v) => setCleaningUp.values.push(v);
  setCleaningUp.values = [];
  const setCleanupProgress = (v) => setCleanupProgress.values.push(v);
  setCleanupProgress.values = [];

  const clearProgressToast = () => {
    if (refs.cleanupToastId != null && dismissToast) {
      dismissToast(refs.cleanupToastId);
    }
    refs.cleanupToastId = null;
  };

  // Mirrors handleCleanup's "background job started" branch.
  const start = (total, now) => {
    setCleanupProgress({ deleted: 0, total });
    refs.lastCleanupToastAt = now;
    refs.cleanupToastId = showToast
      ? showToast({
          style: "info",
          title: `Removing ${total} closed issue${total === 1 ? "" : "s"}…`,
          sticky: true,
        })
      : null;
  };

  // Mirrors the onProgress event handler.
  const onProgress = (detail, now) => {
    const d = detail || {};
    if (d.working_dir !== workingDir) return;
    if (d.error) {
      clearProgressToast();
      showToast &&
        showToast({
          style: "error",
          title: d.error || "Failed to clean up issues",
        });
      setCleaningUp(false);
      setCleanupProgress(null);
      fetchList();
      return;
    }
    const deleted = d.deleted || 0;
    const total = d.total || 0;
    setCleanupProgress({ deleted, total });
    if (d.done) {
      clearProgressToast();
      showToast &&
        showToast({
          style: "success",
          title: `Removed ${deleted} closed issue${deleted === 1 ? "" : "s"}`,
        });
      setCleaningUp(false);
      setCleanupProgress(null);
      fetchList();
      return;
    }
    if (
      showToast &&
      now - refs.lastCleanupToastAt >= CLEANUP_PROGRESS_TOAST_INTERVAL_MS
    ) {
      refs.lastCleanupToastAt = now;
      clearProgressToast();
      refs.cleanupToastId = showToast({
        style: "info",
        title: `Removing closed issues… ${deleted}/${total}`,
        sticky: true,
      });
    }
  };

  return {
    refs,
    workingDir,
    showToast,
    dismissToast,
    fetchList,
    setCleaningUp,
    setCleanupProgress,
    start,
    onProgress,
  };
}

describe("cleanup progress toast — start", () => {
  test("shows an immediate sticky info toast and records its id + timestamp", () => {
    const h = makeCleanupHarness();
    h.start(120, 1000);
    expect(h.showToast.count()).toBe(1);
    expect(h.showToast.last()).toEqual({
      style: "info",
      title: "Removing 120 closed issues…",
      sticky: true,
    });
    expect(h.refs.cleanupToastId).toBe(1);
    expect(h.refs.lastCleanupToastAt).toBe(1000);
    expect(h.dismissToast.count()).toBe(0);
  });

  test("singular pluralization for a single closed issue", () => {
    const h = makeCleanupHarness();
    h.start(1, 1000);
    expect(h.showToast.last().title).toBe("Removing 1 closed issue…");
  });
});

describe("cleanup progress toast — throttle", () => {
  test("an update within the throttle window shows no new toast", () => {
    const h = makeCleanupHarness();
    h.start(120, 1000);
    h.onProgress({ working_dir: "/w", deleted: 25, total: 120 }, 3999); // 2999ms later
    expect(h.showToast.countByStyle("info")).toBe(1); // still just the start toast
    expect(h.dismissToast.count()).toBe(0);
    expect(h.refs.cleanupToastId).toBe(1);
    expect(h.refs.lastCleanupToastAt).toBe(1000);
  });

  test("an update at exactly the interval boundary shows (>= comparison)", () => {
    const h = makeCleanupHarness();
    h.start(120, 1000);
    h.onProgress({ working_dir: "/w", deleted: 50, total: 120 }, 4000); // exactly 3000ms later
    expect(h.showToast.countByStyle("info")).toBe(2);
    expect(h.refs.lastCleanupToastAt).toBe(4000);
  });

  test("each update throttles from the last shown time, not from start", () => {
    const h = makeCleanupHarness();
    h.start(120, 1000);
    h.onProgress({ working_dir: "/w", deleted: 50, total: 120 }, 4000); // shows (id 2)
    h.onProgress({ working_dir: "/w", deleted: 75, total: 120 }, 6000); // only 2000ms later → skip
    expect(h.showToast.countByStyle("info")).toBe(2);
    h.onProgress({ working_dir: "/w", deleted: 90, total: 120 }, 7000); // 3000ms later → shows
    expect(h.showToast.countByStyle("info")).toBe(3);
    expect(h.refs.lastCleanupToastAt).toBe(7000);
  });

  test("first mid-flight progress (no prior start) shows immediately", () => {
    const h = makeCleanupHarness();
    h.onProgress({ working_dir: "/w", deleted: 25, total: 50 }, 5000);
    expect(h.showToast.countByStyle("info")).toBe(1);
    expect(h.dismissToast.count()).toBe(0); // nothing to replace yet
    expect(h.refs.cleanupToastId).toBe(1);
    expect(h.refs.lastCleanupToastAt).toBe(5000);
  });

  test("events for a different working dir are ignored", () => {
    const h = makeCleanupHarness();
    h.start(120, 1000);
    h.onProgress({ working_dir: "/other", deleted: 60, total: 120 }, 9999);
    expect(h.showToast.count()).toBe(1); // only the start toast
    expect(h.dismissToast.count()).toBe(0);
    expect(h.refs.cleanupToastId).toBe(1);
    expect(h.refs.lastCleanupToastAt).toBe(1000);
  });
});

describe("cleanup progress toast — replace in place", () => {
  test("a throttled update dismisses the previous toast before showing the new one", () => {
    const h = makeCleanupHarness();
    h.start(120, 1000); // toast id 1
    h.onProgress({ working_dir: "/w", deleted: 50, total: 120 }, 4000); // replace
    expect(h.dismissToast.count()).toBe(1);
    expect(h.dismissToast.last()).toBe(1); // dismissed the start toast
    expect(h.showToast.last()).toEqual({
      style: "info",
      title: "Removing closed issues… 50/120",
      sticky: true,
    });
    expect(h.refs.cleanupToastId).toBe(2); // tracks the new live toast
  });
});

describe("cleanup progress toast — terminal outcomes reset state", () => {
  test("done dismisses the live toast, shows a success toast, and clears the ref", () => {
    const h = makeCleanupHarness();
    h.start(120, 1000);
    h.onProgress({ working_dir: "/w", deleted: 50, total: 120 }, 4000); // live toast id 2
    h.onProgress(
      { working_dir: "/w", deleted: 120, total: 120, done: true },
      5000,
    );
    expect(h.dismissToast.last()).toBe(2);
    expect(h.showToast.countByStyle("success")).toBe(1);
    expect(h.showToast.last().title).toBe("Removed 120 closed issues");
    expect(h.refs.cleanupToastId).toBeNull();
    expect(h.fetchList.count).toBe(1);
    expect(h.setCleaningUp.values).toContain(false);
  });

  test("done with no live toast does not call dismiss (null guard)", () => {
    const h = makeCleanupHarness();
    h.onProgress({ working_dir: "/w", deleted: 0, total: 0, done: true }, 5000);
    expect(h.dismissToast.count()).toBe(0);
    expect(h.showToast.countByStyle("success")).toBe(1);
    expect(h.showToast.last().title).toBe("Removed 0 closed issues");
  });

  test("done with a single deleted issue uses singular pluralization", () => {
    const h = makeCleanupHarness();
    h.onProgress({ working_dir: "/w", deleted: 1, total: 1, done: true }, 5000);
    expect(h.showToast.last().title).toBe("Removed 1 closed issue");
  });

  test("error dismisses the live toast, shows an error toast, and clears the ref", () => {
    const h = makeCleanupHarness();
    h.start(120, 1000); // live toast id 1
    h.onProgress({ working_dir: "/w", error: "bd exploded" }, 2000);
    expect(h.dismissToast.last()).toBe(1);
    expect(h.showToast.countByStyle("error")).toBe(1);
    expect(h.showToast.last().title).toBe("bd exploded");
    expect(h.refs.cleanupToastId).toBeNull();
    expect(h.fetchList.count).toBe(1);
  });
});

// =============================================================================
// BeadsIssueView in-viewer navigation history stack (mitto-qluh.1)
// =============================================================================
//
// The stack lives in BeadsIssueView (BeadsView.js:275-389) as two useState
// values — `history` (array of issue IDs) and `pos` (index) — mutated by
// three callbacks: `handleSelectIssue`, `goBack`, `goForward`, plus a reset
// effect that runs when the external `issueId`/`selectNonce` prop changes.
//
// Because BeadsView.js reads `window.preact` at module load, the component
// itself cannot be imported under jsdom (see the header comment on
// PromptParameterDialog.test.js for the project-wide precedent). We therefore
// mirror the pure state transitions here as small reducer helpers and exercise
// them directly — the same pattern the rest of this file uses. If the stack
// logic in BeadsView.js changes, these helpers must be updated to match.
// =============================================================================

/**
 * Mirrors the initial state seeded in BeadsIssueView when the component mounts
 * or the external issueId/selectNonce prop changes:
 *   const [history, setHistory] = useState([issueId]);
 *   const [pos,     setPos]     = useState(0);
 */
function makeInitialHistory(issueId) {
  return { history: [issueId], pos: 0 };
}

/**
 * Mirrors handleSelectIssue in BeadsView.js:372-381: clicking a related id
 * truncates any forward entries, pushes the new id, and advances `pos`. A
 * click on the id already showing is a no-op (does not add a duplicate step).
 * `depObj` mimics the shape passed in from BeadsDetailPanelBody call sites
 * (dependencies / parent / subtasks): `{id: string}` — missing/empty id is
 * a no-op.
 */
function selectIssue(state, depObj) {
  const id = depObj && depObj.id;
  if (!id) return state;
  if (id === state.history[state.pos]) return state;
  return {
    history: [...state.history.slice(0, state.pos + 1), id],
    pos: state.pos + 1,
  };
}

/** Mirrors goBack in BeadsView.js:383-385 (clamped at 0). */
function goBack(state) {
  return { ...state, pos: state.pos > 0 ? state.pos - 1 : state.pos };
}

/** Mirrors goForward in BeadsView.js:387-389 (clamped at history.length-1). */
function goForward(state) {
  return {
    ...state,
    pos: state.pos < state.history.length - 1 ? state.pos + 1 : state.pos,
  };
}

/** Derived flags exposed to the panel (BeadsView.js:278-279). */
function canGoBack(state) {
  return state.pos > 0;
}
function canGoForward(state) {
  return state.pos < state.history.length - 1;
}

/** The `currentIssueId` derived value (BeadsView.js:277). */
function currentId(state) {
  return state.history[state.pos];
}

// mitto-qluh.2 — the visible Back/Forward buttons in PanelBody's bottom bar
// are pure JSX derived from the state exercised above (canGoBack/canGoForward
// and the goBack/goForward callbacks). No reducer-level assertion is added
// here; button rendering / click wiring will be covered by Playwright in a
// follow-up bead.

describe("BeadsIssueView history stack — initial state", () => {
  test("seeds a single-entry stack with pos=0", () => {
    const s = makeInitialHistory("mitto-aaa");
    expect(s.history).toEqual(["mitto-aaa"]);
    expect(s.pos).toBe(0);
    expect(currentId(s)).toBe("mitto-aaa");
  });

  test("canGoBack and canGoForward are both false at the root", () => {
    const s = makeInitialHistory("mitto-aaa");
    expect(canGoBack(s)).toBe(false);
    expect(canGoForward(s)).toBe(false);
  });
});

describe("BeadsIssueView history stack — handleSelectIssue push", () => {
  test("a single navigation pushes the id and advances pos", () => {
    let s = makeInitialHistory("mitto-aaa");
    s = selectIssue(s, { id: "mitto-bbb" });
    expect(s.history).toEqual(["mitto-aaa", "mitto-bbb"]);
    expect(s.pos).toBe(1);
    expect(currentId(s)).toBe("mitto-bbb");
  });

  test("several sequential navigations extend the stack in order", () => {
    let s = makeInitialHistory("mitto-aaa");
    s = selectIssue(s, { id: "mitto-bbb" });
    s = selectIssue(s, { id: "mitto-ccc" });
    s = selectIssue(s, { id: "mitto-ddd" });
    expect(s.history).toEqual([
      "mitto-aaa",
      "mitto-bbb",
      "mitto-ccc",
      "mitto-ddd",
    ]);
    expect(s.pos).toBe(3);
    expect(currentId(s)).toBe("mitto-ddd");
    expect(canGoBack(s)).toBe(true);
    expect(canGoForward(s)).toBe(false);
  });

  test("clicking the same id as the current entry is a no-op", () => {
    const s0 = makeInitialHistory("mitto-aaa");
    const s1 = selectIssue(s0, { id: "mitto-aaa" });
    expect(s1).toBe(s0); // identity preserved (no state change)
  });

  test("clicking the current id after navigation is still a no-op", () => {
    let s = makeInitialHistory("mitto-aaa");
    s = selectIssue(s, { id: "mitto-bbb" });
    const before = s;
    s = selectIssue(s, { id: "mitto-bbb" });
    expect(s).toBe(before);
    expect(s.history).toEqual(["mitto-aaa", "mitto-bbb"]);
    expect(s.pos).toBe(1);
  });

  test("a depObj without an id is ignored (missing / empty / falsy)", () => {
    const s = makeInitialHistory("mitto-aaa");
    expect(selectIssue(s, {})).toBe(s);
    expect(selectIssue(s, { id: "" })).toBe(s);
    expect(selectIssue(s, { id: null })).toBe(s);
    expect(selectIssue(s, null)).toBe(s);
    expect(selectIssue(s, undefined)).toBe(s);
  });
});

describe("BeadsIssueView history stack — goBack / goForward", () => {
  test("goBack retraces the previous entry and updates canGo* flags", () => {
    let s = makeInitialHistory("mitto-aaa");
    s = selectIssue(s, { id: "mitto-bbb" });
    s = selectIssue(s, { id: "mitto-ccc" });
    // At mitto-ccc, pos=2.
    s = goBack(s);
    expect(currentId(s)).toBe("mitto-bbb");
    expect(s.pos).toBe(1);
    expect(canGoBack(s)).toBe(true);
    expect(canGoForward(s)).toBe(true);
  });

  test("goBack all the way to the root disables further Back", () => {
    let s = makeInitialHistory("mitto-aaa");
    s = selectIssue(s, { id: "mitto-bbb" });
    s = selectIssue(s, { id: "mitto-ccc" });
    s = goBack(s);
    s = goBack(s);
    expect(currentId(s)).toBe("mitto-aaa");
    expect(s.pos).toBe(0);
    expect(canGoBack(s)).toBe(false);
    expect(canGoForward(s)).toBe(true);
  });

  test("goBack at pos=0 is a no-op (clamped, does not go negative)", () => {
    const s0 = makeInitialHistory("mitto-aaa");
    const s1 = goBack(s0);
    expect(s1.pos).toBe(0);
    expect(currentId(s1)).toBe("mitto-aaa");
  });

  test("goForward retraces the discarded direction after a goBack", () => {
    let s = makeInitialHistory("mitto-aaa");
    s = selectIssue(s, { id: "mitto-bbb" });
    s = selectIssue(s, { id: "mitto-ccc" });
    s = goBack(s); // now at mitto-bbb
    s = goForward(s);
    expect(currentId(s)).toBe("mitto-ccc");
    expect(s.pos).toBe(2);
    expect(canGoBack(s)).toBe(true);
    expect(canGoForward(s)).toBe(false);
  });

  test("goForward at the end of history is a no-op (clamped)", () => {
    let s = makeInitialHistory("mitto-aaa");
    s = selectIssue(s, { id: "mitto-bbb" });
    // pos=1, length=2 → canGoForward is false.
    const before = s;
    s = goForward(s);
    expect(s.pos).toBe(before.pos);
    expect(currentId(s)).toBe("mitto-bbb");
    expect(canGoForward(s)).toBe(false);
  });
});

describe("BeadsIssueView history stack — forward-branch truncation", () => {
  test("selecting a new id after a goBack truncates the forward chain", () => {
    let s = makeInitialHistory("mitto-aaa");
    s = selectIssue(s, { id: "mitto-bbb" });
    s = selectIssue(s, { id: "mitto-ccc" });
    s = selectIssue(s, { id: "mitto-ddd" });
    // history: [aaa, bbb, ccc, ddd], pos=3
    s = goBack(s); // pos=2 (ccc)
    s = goBack(s); // pos=1 (bbb)
    // Selecting a new branch here should discard [ccc, ddd] and push eee.
    s = selectIssue(s, { id: "mitto-eee" });
    expect(s.history).toEqual(["mitto-aaa", "mitto-bbb", "mitto-eee"]);
    expect(s.pos).toBe(2);
    expect(currentId(s)).toBe("mitto-eee");
    // Forward is no longer available: ccc/ddd were discarded on the branch.
    expect(canGoForward(s)).toBe(false);
    expect(canGoBack(s)).toBe(true);
  });

  test("goBack-then-select at pos=0 replaces the tail entirely", () => {
    let s = makeInitialHistory("mitto-aaa");
    s = selectIssue(s, { id: "mitto-bbb" });
    s = selectIssue(s, { id: "mitto-ccc" });
    s = goBack(s); // pos=1
    s = goBack(s); // pos=0 (root)
    // A fresh branch at the root: history becomes [aaa, zzz].
    s = selectIssue(s, { id: "mitto-zzz" });
    expect(s.history).toEqual(["mitto-aaa", "mitto-zzz"]);
    expect(s.pos).toBe(1);
    expect(canGoForward(s)).toBe(false);
  });

  test("selecting the current id after a goBack still no-ops (no truncation)", () => {
    let s = makeInitialHistory("mitto-aaa");
    s = selectIssue(s, { id: "mitto-bbb" });
    s = selectIssue(s, { id: "mitto-ccc" });
    // history: [aaa, bbb, ccc], pos=2
    s = goBack(s); // now at bbb, pos=1, forward=[ccc] preserved
    const before = s;
    // Re-clicking bbb (the current entry) must NOT truncate the forward chain.
    s = selectIssue(s, { id: "mitto-bbb" });
    expect(s).toBe(before);
    expect(s.history).toEqual(["mitto-aaa", "mitto-bbb", "mitto-ccc"]);
    expect(canGoForward(s)).toBe(true);
  });
});

describe("BeadsIssueView history stack — external prop reset", () => {
  test("re-opening from a new external issueId starts a single-entry stack", () => {
    // Mirrors the reset effect at BeadsView.js:299-302, which fires whenever
    // the external issueId or selectNonce changes: setHistory([issueId]);
    // setPos(0). We express it here by rebuilding the initial state.
    let s = makeInitialHistory("mitto-aaa");
    s = selectIssue(s, { id: "mitto-bbb" });
    s = selectIssue(s, { id: "mitto-ccc" });
    // External navigation event: user follows a beads link to mitto-zzz.
    s = makeInitialHistory("mitto-zzz");
    expect(s.history).toEqual(["mitto-zzz"]);
    expect(s.pos).toBe(0);
    expect(currentId(s)).toBe("mitto-zzz");
    expect(canGoBack(s)).toBe(false);
    expect(canGoForward(s)).toBe(false);
  });

  test("selectNonce-triggered reset with the SAME issueId still clears history", () => {
    // selectNonce is used to force a reset even when the id is unchanged
    // (BeadsView.js line 302 deps: [issueId, selectNonce]). Simulate by
    // rebuilding initial state with the same id.
    let s = makeInitialHistory("mitto-aaa");
    s = selectIssue(s, { id: "mitto-bbb" });
    s = selectIssue(s, { id: "mitto-ccc" });
    expect(s.history).toHaveLength(3);
    s = makeInitialHistory("mitto-aaa");
    expect(s.history).toEqual(["mitto-aaa"]);
    expect(s.pos).toBe(0);
  });
});

// =============================================================================
// BeadsView (main tasks-list flavor) in-panel navigation history stack
// =============================================================================
//
// The main BeadsView owns a simpler in-panel navigation stack than
// BeadsIssueView: no browser-history integration, and the "closed" state is
// modeled with `pos = -1` and an empty `history`. Mutators mirrored below:
//   - listClickReducer(state, issue)   — a click on a list row (selectIssue).
//   - panelSelectReducer(state, depObj) — a dep/subtask/parent click inside
//                                         the panel (handlePanelSelectIssue).
//   - goBackReducer(state) / goForwardReducer(state) — bottom-bar chevrons.
//
// Same jsdom limitation as above (BeadsView.js reads window.preact at module
// load) means we exercise these as pure reducer helpers rather than driving
// the real component. If the stack logic in BeadsView.js changes, these
// helpers must be updated to match.
// =============================================================================

/** Initial state when the panel is closed: empty history, pos=-1. */
function makeClosedPanel() {
  return { history: [], pos: -1 };
}

/**
 * Mirrors selectIssue in BeadsView.js: a list-row click either opens the
 * clicked issue (resetting the stack to a single root entry) or toggles the
 * panel closed if the same issue is already showing.
 */
function listClickReducer(state, issue) {
  const currentId = state.pos >= 0 ? state.history[state.pos] : null;
  if (currentId && currentId === issue.id) {
    return makeClosedPanel();
  }
  return { history: [issue.id], pos: 0 };
}

/**
 * Mirrors handlePanelSelectIssue in BeadsView.js: an in-panel dep/subtask
 * click truncates any forward entries, pushes the new id, and advances pos.
 * A click matching the current id is a no-op. A falsy id is ignored.
 */
function panelSelectReducer(state, depObj) {
  const id = depObj && depObj.id;
  if (!id) return state;
  if (state.pos >= 0 && state.history[state.pos] === id) return state;
  const newHistory = [...state.history.slice(0, state.pos + 1), id];
  return { history: newHistory, pos: newHistory.length - 1 };
}

/** Mirrors goBack in BeadsView.js (clamped at 0; no-op when pos<=0). */
function goBackReducer(state) {
  if (state.pos <= 0) return state;
  return { ...state, pos: state.pos - 1 };
}

/** Mirrors goForward in BeadsView.js (clamped at history.length-1). */
function goForwardReducer(state) {
  if (state.pos < 0 || state.pos >= state.history.length - 1) return state;
  return { ...state, pos: state.pos + 1 };
}

/** Derived flags exposed to the panel. */
function panelCanGoBack(state) {
  return state.pos > 0;
}
function panelCanGoForward(state) {
  return state.pos >= 0 && state.pos < state.history.length - 1;
}
function panelCurrentId(state) {
  return state.pos >= 0 ? state.history[state.pos] : null;
}

describe("BeadsView panel history — initial state", () => {
  test("closed panel has empty history and pos=-1", () => {
    const s = makeClosedPanel();
    expect(s.history).toEqual([]);
    expect(s.pos).toBe(-1);
    expect(panelCurrentId(s)).toBeNull();
    expect(panelCanGoBack(s)).toBe(false);
    expect(panelCanGoForward(s)).toBe(false);
  });
});

describe("BeadsView panel history — list-row click", () => {
  test("opening from the list resets history to [id]/pos=0", () => {
    const s = listClickReducer(makeClosedPanel(), { id: "mitto-aaa" });
    expect(s.history).toEqual(["mitto-aaa"]);
    expect(s.pos).toBe(0);
    expect(panelCurrentId(s)).toBe("mitto-aaa");
    expect(panelCanGoBack(s)).toBe(false);
    expect(panelCanGoForward(s)).toBe(false);
  });

  test("opening a different issue from the list resets the stack", () => {
    // User has clicked into aaa, drilled into bbb via a dep, then clicks
    // ccc in the list — the panel should reset to a fresh [ccc]/pos=0 stack.
    let s = listClickReducer(makeClosedPanel(), { id: "mitto-aaa" });
    s = panelSelectReducer(s, { id: "mitto-bbb" });
    expect(s.history).toEqual(["mitto-aaa", "mitto-bbb"]);
    s = listClickReducer(s, { id: "mitto-ccc" });
    expect(s.history).toEqual(["mitto-ccc"]);
    expect(s.pos).toBe(0);
    expect(panelCanGoBack(s)).toBe(false);
    expect(panelCanGoForward(s)).toBe(false);
  });

  test("clicking the same list issue again toggles the panel closed", () => {
    let s = listClickReducer(makeClosedPanel(), { id: "mitto-aaa" });
    s = listClickReducer(s, { id: "mitto-aaa" });
    expect(s.history).toEqual([]);
    expect(s.pos).toBe(-1);
    expect(panelCurrentId(s)).toBeNull();
  });

  test("toggle-close ignores prior in-panel navigation state", () => {
    // Even with a multi-entry stack, re-clicking whatever is CURRENTLY shown
    // in the list closes the panel entirely.
    let s = listClickReducer(makeClosedPanel(), { id: "mitto-aaa" });
    s = panelSelectReducer(s, { id: "mitto-bbb" });
    // The panel currently shows bbb (pos=1). Re-clicking bbb in the list
    // toggles closed because the list-row check compares to currentId.
    s = listClickReducer(s, { id: "mitto-bbb" });
    expect(s.history).toEqual([]);
    expect(s.pos).toBe(-1);
  });
});

describe("BeadsView panel history — in-panel dep click", () => {
  test("dep click pushes and advances pos", () => {
    let s = listClickReducer(makeClosedPanel(), { id: "mitto-aaa" });
    s = panelSelectReducer(s, { id: "mitto-bbb" });
    expect(s.history).toEqual(["mitto-aaa", "mitto-bbb"]);
    expect(s.pos).toBe(1);
    expect(panelCurrentId(s)).toBe("mitto-bbb");
    expect(panelCanGoBack(s)).toBe(true);
    expect(panelCanGoForward(s)).toBe(false);
  });

  test("several sequential dep clicks extend the stack", () => {
    let s = listClickReducer(makeClosedPanel(), { id: "mitto-aaa" });
    s = panelSelectReducer(s, { id: "mitto-bbb" });
    s = panelSelectReducer(s, { id: "mitto-ccc" });
    s = panelSelectReducer(s, { id: "mitto-ddd" });
    expect(s.history).toEqual([
      "mitto-aaa",
      "mitto-bbb",
      "mitto-ccc",
      "mitto-ddd",
    ]);
    expect(s.pos).toBe(3);
    expect(panelCurrentId(s)).toBe("mitto-ddd");
  });

  test("dep click matching the current id is a no-op", () => {
    let s = listClickReducer(makeClosedPanel(), { id: "mitto-aaa" });
    s = panelSelectReducer(s, { id: "mitto-bbb" });
    const before = s;
    s = panelSelectReducer(s, { id: "mitto-bbb" });
    expect(s).toBe(before);
    expect(s.history).toEqual(["mitto-aaa", "mitto-bbb"]);
    expect(s.pos).toBe(1);
  });

  test("dep click after goBack truncates the forward chain", () => {
    let s = listClickReducer(makeClosedPanel(), { id: "mitto-aaa" });
    s = panelSelectReducer(s, { id: "mitto-bbb" });
    s = panelSelectReducer(s, { id: "mitto-ccc" });
    s = panelSelectReducer(s, { id: "mitto-ddd" });
    // history: [aaa, bbb, ccc, ddd], pos=3
    s = goBackReducer(s); // pos=2 (ccc)
    s = goBackReducer(s); // pos=1 (bbb)
    // Branch at bbb: discards [ccc, ddd] and pushes eee.
    s = panelSelectReducer(s, { id: "mitto-eee" });
    expect(s.history).toEqual(["mitto-aaa", "mitto-bbb", "mitto-eee"]);
    expect(s.pos).toBe(2);
    expect(panelCurrentId(s)).toBe("mitto-eee");
    expect(panelCanGoForward(s)).toBe(false);
    expect(panelCanGoBack(s)).toBe(true);
  });

  test("dep click with missing/empty/falsy id is ignored", () => {
    const s = listClickReducer(makeClosedPanel(), { id: "mitto-aaa" });
    expect(panelSelectReducer(s, {})).toBe(s);
    expect(panelSelectReducer(s, { id: "" })).toBe(s);
    expect(panelSelectReducer(s, { id: null })).toBe(s);
    expect(panelSelectReducer(s, null)).toBe(s);
    expect(panelSelectReducer(s, undefined)).toBe(s);
  });
});

describe("BeadsView panel history — goBack / goForward", () => {
  test("goBack decrements pos and updates canGo* flags", () => {
    let s = listClickReducer(makeClosedPanel(), { id: "mitto-aaa" });
    s = panelSelectReducer(s, { id: "mitto-bbb" });
    s = panelSelectReducer(s, { id: "mitto-ccc" });
    // pos=2 (ccc)
    s = goBackReducer(s);
    expect(panelCurrentId(s)).toBe("mitto-bbb");
    expect(s.pos).toBe(1);
    expect(panelCanGoBack(s)).toBe(true);
    expect(panelCanGoForward(s)).toBe(true);
  });

  test("goBack at pos=0 is a no-op (does not go negative)", () => {
    const s0 = listClickReducer(makeClosedPanel(), { id: "mitto-aaa" });
    const s1 = goBackReducer(s0);
    expect(s1).toBe(s0);
    expect(s1.pos).toBe(0);
    expect(panelCurrentId(s1)).toBe("mitto-aaa");
  });

  test("goForward increments pos and retraces the forward chain", () => {
    let s = listClickReducer(makeClosedPanel(), { id: "mitto-aaa" });
    s = panelSelectReducer(s, { id: "mitto-bbb" });
    s = panelSelectReducer(s, { id: "mitto-ccc" });
    s = goBackReducer(s); // pos=1 (bbb)
    s = goForwardReducer(s);
    expect(panelCurrentId(s)).toBe("mitto-ccc");
    expect(s.pos).toBe(2);
    expect(panelCanGoBack(s)).toBe(true);
    expect(panelCanGoForward(s)).toBe(false);
  });

  test("goForward at the end of history is a no-op (clamped)", () => {
    let s = listClickReducer(makeClosedPanel(), { id: "mitto-aaa" });
    s = panelSelectReducer(s, { id: "mitto-bbb" });
    const before = s;
    s = goForwardReducer(s);
    expect(s).toBe(before);
    expect(s.pos).toBe(1);
    expect(panelCanGoForward(s)).toBe(false);
  });

  test("goBack/goForward on a closed panel are no-ops", () => {
    const closed = makeClosedPanel();
    expect(goBackReducer(closed)).toBe(closed);
    expect(goForwardReducer(closed)).toBe(closed);
    expect(panelCanGoBack(closed)).toBe(false);
    expect(panelCanGoForward(closed)).toBe(false);
  });
});

describe("BeadsView panel history — canGoBack / canGoForward derivations", () => {
  test("root entry: both false", () => {
    const s = listClickReducer(makeClosedPanel(), { id: "mitto-aaa" });
    expect(panelCanGoBack(s)).toBe(false);
    expect(panelCanGoForward(s)).toBe(false);
  });

  test("middle of a stack after goBack: both true", () => {
    let s = listClickReducer(makeClosedPanel(), { id: "mitto-aaa" });
    s = panelSelectReducer(s, { id: "mitto-bbb" });
    s = panelSelectReducer(s, { id: "mitto-ccc" });
    s = goBackReducer(s);
    expect(panelCanGoBack(s)).toBe(true);
    expect(panelCanGoForward(s)).toBe(true);
  });

  test("tail of a stack: only back is true", () => {
    let s = listClickReducer(makeClosedPanel(), { id: "mitto-aaa" });
    s = panelSelectReducer(s, { id: "mitto-bbb" });
    expect(panelCanGoBack(s)).toBe(true);
    expect(panelCanGoForward(s)).toBe(false);
  });
});

// =============================================================================
// mitto-qluh.3 — computePopstateAction (browser History API integration)
// =============================================================================
//
// Mirrors the pure decision helper exported from BeadsView.js:
//   export function computePopstateAction(newState, ourKey, currentPos, historyLen)
// BeadsView.js cannot be imported under jsdom (it reads `window.preact` at
// module load, see the "Duplicated helpers" convention in this file), so the
// helper's logic is duplicated verbatim below and exercised directly. If the
// implementation in BeadsView.js changes, this copy must be updated.
function computePopstateAction(newState, ourKey, currentPos, historyLen) {
  if (!newState || newState.__mittoBeadsKey !== ourKey) {
    return { kind: "close" };
  }
  const raw = newState.__mittoBeadsPos;
  const numeric =
    typeof raw === "number" && Number.isFinite(raw) ? raw : currentPos;
  const upper = historyLen > 0 ? historyLen - 1 : 0;
  const clamped = Math.max(0, Math.min(upper, numeric));
  const delta = clamped - currentPos;
  if (delta === 0) return { kind: "noop" };
  return { kind: "setPos", pos: clamped, delta };
}

describe("computePopstateAction — direction & clamping", () => {
  const KEY = "test-key-abc";

  test("forward navigation returns setPos with the target pos", () => {
    const state = { __mittoBeadsKey: KEY, __mittoBeadsPos: 2 };
    const action = computePopstateAction(state, KEY, 0, 3);
    expect(action.kind).toBe("setPos");
    expect(action.pos).toBe(2);
    expect(action.delta).toBe(2);
  });

  test("back navigation returns setPos with the target pos", () => {
    const state = { __mittoBeadsKey: KEY, __mittoBeadsPos: 0 };
    const action = computePopstateAction(state, KEY, 1, 2);
    expect(action.kind).toBe("setPos");
    expect(action.pos).toBe(0);
    expect(action.delta).toBe(-1);
  });

  test("same pos is a no-op", () => {
    const state = { __mittoBeadsKey: KEY, __mittoBeadsPos: 1 };
    const action = computePopstateAction(state, KEY, 1, 3);
    expect(action.kind).toBe("noop");
  });

  test("null state closes the overlay (popped past our anchor)", () => {
    const action = computePopstateAction(null, KEY, 0, 1);
    expect(action.kind).toBe("close");
  });

  test("undefined state closes the overlay", () => {
    const action = computePopstateAction(undefined, KEY, 0, 1);
    expect(action.kind).toBe("close");
  });

  test("mismatched key closes the overlay (foreign popstate)", () => {
    const state = { __mittoBeadsKey: "other-key", __mittoBeadsPos: 1 };
    const action = computePopstateAction(state, KEY, 0, 2);
    expect(action.kind).toBe("close");
  });

  test("missing key on state closes the overlay", () => {
    const state = { __mittoBeadsPos: 1 };
    const action = computePopstateAction(state, KEY, 0, 2);
    expect(action.kind).toBe("close");
  });

  test("pos > historyLen-1 is clamped to historyLen-1", () => {
    const state = { __mittoBeadsKey: KEY, __mittoBeadsPos: 99 };
    const action = computePopstateAction(state, KEY, 0, 3);
    expect(action.kind).toBe("setPos");
    expect(action.pos).toBe(2);
    expect(action.delta).toBe(2);
  });

  test("negative pos is clamped to 0", () => {
    const state = { __mittoBeadsKey: KEY, __mittoBeadsPos: -5 };
    const action = computePopstateAction(state, KEY, 2, 3);
    expect(action.kind).toBe("setPos");
    expect(action.pos).toBe(0);
    expect(action.delta).toBe(-2);
  });

  test("non-numeric pos falls back to currentPos (yields noop)", () => {
    const state = { __mittoBeadsKey: KEY, __mittoBeadsPos: "banana" };
    const action = computePopstateAction(state, KEY, 1, 3);
    expect(action.kind).toBe("noop");
  });

  test("empty history (historyLen=0) clamps upper bound to 0", () => {
    const state = { __mittoBeadsKey: KEY, __mittoBeadsPos: 4 };
    const action = computePopstateAction(state, KEY, 0, 0);
    expect(action.kind).toBe("noop");
  });

  test("preserves extra state fields (spread-first invariant is caller-side)", () => {
    // The helper does not read/emit unrelated fields; it only branches on
    // __mittoBeadsKey and __mittoBeadsPos. Confirm foreign fields do not
    // affect the decision.
    const state = {
      unrelated: "value",
      __mittoBeadsKey: KEY,
      __mittoBeadsPos: 1,
    };
    const action = computePopstateAction(state, KEY, 0, 2);
    expect(action.kind).toBe("setPos");
    expect(action.pos).toBe(1);
  });
});

// =============================================================================
// mitto-zbfq — BeadsIssueView single-Drawer mount across loading → loaded
// =============================================================================
//
// BeadsIssueView renders TWO different top-level components depending on
// whether /api/issues/{id} has resolved: a raw <${Drawer}> placeholder while
// loading, then <${BeadsDetailPanel}> (which itself renders a separate Drawer
// via BeadsDetailPanelBody) once the issue arrives. Because the two branches
// mount two DIFFERENT top-level components, Preact's diff unmounts the
// placeholder Drawer and mounts a fresh Drawer for the loaded state — the
// fresh Drawer replays its slide-in transition, which the user perceives as
// a second panel opening on top of the first (the reported flicker /
// double-animation on opening a beads issue from a conversation link).
//
// A pure DOM/render test of BeadsView is impractical here (BeadsView.js
// imports window.preact globals at module load time — see the "Duplicated
// helpers" convention in Dashboard.test.js / Message.test.js), so this
// reproduction is a structural source-code assertion against BeadsView.js.
// It fails today and will pass once BeadsIssueView is refactored to render
// a single stable <Drawer> across both loading and loaded states (option 1
// in the mitto-zbfq investigation: teach BeadsDetailPanel(Body) to render
// its Drawer for data === null too, then drop the placeholder branch here).

const __filename_bv = fileURLToPath(import.meta.url);
const __dirname_bv = dirname(__filename_bv);
const BEADS_VIEW_PATH = resolve(__dirname_bv, "BeadsView.js");

describe("task label title backgrounds (mitto-ggs6)", () => {
  const mappings = [
    { label: "needs-human", color: "#ef4444" },
    { label: "blocked", color: "#f59e0b" },
  ];

  test("first configured matching label wins regardless of issue label order", () => {
    expect(
      taskTitleBackground({ labels: ["blocked", "needs-human"] }, mappings),
    ).toBe("#ef4444");
  });

  test("matching is exact and missing labels remain uncolored", () => {
    expect(
      taskTitleBackground({ labels: ["needs-human-review"] }, mappings),
    ).toBe("");
    expect(taskTitleBackground({}, mappings)).toBe("");
  });

  test("removing the matched task label or mapping immediately removes the color", () => {
    const issue = { labels: ["needs-human"] };
    expect(taskTitleBackground(issue, mappings)).toBe("#ef4444");
    expect(taskTitleBackground({ labels: [] }, mappings)).toBe("");
    expect(taskTitleBackground(issue, mappings.slice(1))).toBe("");
  });

  test("BeadsView applies the derived color to the whole card, not the title span", () => {
    const source = readFileSync(BEADS_VIEW_PATH, "utf8");
    // The title span must no longer carry an inline background color.
    expect(source).not.toMatch(
      /<span\s+data-testid="beads-issue-title"\s+style=/,
    );
    // The row wrapper (BeadsIssueRow's div.list-row) must apply the label
    // color as its background, taking precedence over bgTone/hover.
    expect(source).toMatch(
      /class="list-row[\s\S]{0,200}style="transform: translateX\(\$\{swipeOffset\}px\);\$\{labelBackground/,
    );
  });

  test("an open BeadsView refetches mappings when the global event arrives", () => {
    const source = readFileSync(BEADS_VIEW_PATH, "utf8");
    expect(source).toMatch(
      /const handler = \(\) => loadTaskLabelColors\(\);\s*const folderHandler[\s\S]{0,300}window\.addEventListener\("mitto:task_label_colors_updated", handler\)/,
    );
    expect(source).toMatch(
      /loadTaskLabelColors[\s\S]*?getSdkClient\(\)\s*\.taskLabelColors\.getGlobal\(\)/,
    );
  });

  test("mitto-m5f.3: an open BeadsView refetches mappings when the folder event arrives, scoped by working_dir", () => {
    const source = readFileSync(BEADS_VIEW_PATH, "utf8");
    // The folder-scoped listener must exist and be scoped: it only refetches
    // when the event's working_dir matches this view's own workingDir (or is
    // absent), so an unrelated folder's edit does not trigger a refetch here.
    expect(source).toMatch(
      /window\.addEventListener\(\s*"mitto:folder_task_label_colors_updated",\s*folderHandler,?\s*\)/,
    );
    expect(source).toMatch(
      /const folderHandler = \(e\) => \{\s*const dir = e\?\.detail\?\.working_dir;\s*if \(!dir \|\| dir === workingDir\) loadTaskLabelColors\(\);/,
    );
    // Merge must fetch folder (when workingDir is set) + global in parallel
    // and combine via mergeTaskLabelColors (folder-first precedence).
    expect(source).toMatch(
      /getSdkClient\(\)\s*\.taskLabelColors\.getFolder\(\{ working_dir: workingDir \}\)/,
    );
    expect(source).toMatch(
      /setTaskLabelColors\(\s*mergeTaskLabelColors\(folderData\?\.entries, globalData\?\.entries\),?\s*\)/,
    );
  });
});

describe("mitto-zbfq: BeadsIssueView single Drawer mount across load", () => {
  const source = readFileSync(BEADS_VIEW_PATH, "utf8");

  // Isolate the BeadsIssueView function body. The function is declared with
  // `function BeadsIssueView(` and the return block we care about starts at
  // `return html\``. We slice from the declaration to the next top-level
  // `function ` declaration to keep the search scoped.
  function extractBeadsIssueViewSource() {
    const startMarker = "function BeadsIssueView(";
    const startIdx = source.indexOf(startMarker);
    expect(startIdx).toBeGreaterThan(-1);
    // Find the next top-level function declaration after this one.
    const afterStart = source.indexOf(
      "\nfunction ",
      startIdx + startMarker.length,
    );
    const endIdx = afterStart === -1 ? source.length : afterStart;
    return source.slice(startIdx, endIdx);
  }

  test("emits exactly one top-level Drawer site (placeholder + loaded share one mount)", () => {
    const body = extractBeadsIssueViewSource();

    // Count JSX Drawer opens: both `<${Drawer}` and `<${BeadsDetailPanel}`
    // count as "mounts a Drawer" from Preact's perspective, because
    // BeadsDetailPanel → BeadsDetailPanelBody → <${Drawer}> at the top of
    // its body (see beads/detail/PanelBody.js). A stable-mount refactor
    // must fold the placeholder into BeadsDetailPanel(Body) so there is
    // exactly ONE Drawer site in BeadsIssueView.
    const drawerSites = (body.match(/<\$\{Drawer\}/g) || []).length;
    const panelSites = (body.match(/<\$\{BeadsDetailPanel\}/g) || []).length;
    const totalDrawerSites = drawerSites + panelSites;

    expect(totalDrawerSites).toBe(1);
  });

  test("has no `!h.data` early-return-null gate in BeadsDetailPanel", () => {
    // The other half of the bug: BeadsDetailPanel returns null when
    // h.data is falsy (BeadsView.js line ~208: `if (!h.creating && !h.data)
    // return null;`), which forces callers to render a separate placeholder.
    // The fix removes that gate so BeadsDetailPanel(Body) can render its
    // own loading skeleton inside a single, stable Drawer.
    const startMarker = "function BeadsDetailPanel(";
    const startIdx = source.indexOf(startMarker);
    expect(startIdx).toBeGreaterThan(-1);
    const afterStart = source.indexOf(
      "\nfunction ",
      startIdx + startMarker.length,
    );
    const endIdx = afterStart === -1 ? source.length : afterStart;
    const body = source.slice(startIdx, endIdx);

    // The buggy gate — collapse whitespace so line breaks / formatting do not
    // hide it from the assertion.
    const collapsed = body.replace(/\s+/g, " ");
    expect(collapsed).not.toMatch(
      /if\s*\(\s*!\s*h\.creating\s*&&\s*!\s*h\.data\s*\)\s*return\s+null/,
    );
  });
});

// =============================================================================
// mitto-n5mw: write handlers must not swallow beads_schema_skew (409)
// =============================================================================
//
// Only fetchList routes `data.code === "beads_schema_skew"` into
// setSchemaSkew(...) — every other write handler in BeadsView.js falls through
// to a plain error toast whose title carries the migration message but offers
// no path to SchemaSkewDialog. This reproduction has two layers:
//
//   1. Contract: utils/beads.js must export isBeadsSchemaSkew() and
//      toSchemaSkewState() so every write handler can share one branch.
//   2. Wiring:   each write handler in BeadsView.js must call the helper on
//      its error branch (source-inspection assertion, matching the existing
//      mitto-zbfq test pattern — a full DOM/render test is impractical here
//      because BeadsView imports window.preact globals at module load time).
//
// Both layers fail today and must pass once the fix lands.

describe("mitto-n5mw: write handlers must not swallow beads_schema_skew (409)", () => {
  const source = readFileSync(BEADS_VIEW_PATH, "utf8");

  // Slice a named handler body from the file so the assertions are scoped to
  // its error branch instead of the whole 3303-line component.
  function extractHandlerBody(marker) {
    const startIdx = source.indexOf(marker);
    expect(startIdx).toBeGreaterThan(-1);
    // Handler bodies are ~30–110 lines; slice a generous window and stop at
    // the next top-level `const handle` / `function ` declaration so we do
    // not bleed into the next handler.
    const afterStart = source.indexOf(
      "\n  const handle",
      startIdx + marker.length,
    );
    const afterFn = source.indexOf("\nfunction ", startIdx + marker.length);
    const candidates = [afterStart, afterFn].filter((i) => i > startIdx);
    const endIdx = candidates.length ? Math.min(...candidates) : source.length;
    return source.slice(startIdx, endIdx);
  }

  describe("utils/beads.js exports the shared schema-skew helpers", () => {
    test("isBeadsSchemaSkew is exported", () => {
      expect(typeof beadsUtils.isBeadsSchemaSkew).toBe("function");
    });

    test("toSchemaSkewState is exported", () => {
      expect(typeof beadsUtils.toSchemaSkewState).toBe("function");
    });

    test("isBeadsSchemaSkew detects the canonical 409 code", () => {
      const data = {
        error: "The beads database needs migration",
        code: "beads_schema_skew",
        details: { db_path: "/x/.beads", hint: "run bd migrate", options: [] },
      };
      expect(beadsUtils.isBeadsSchemaSkew(data)).toBe(true);
    });

    test("isBeadsSchemaSkew returns false for unrelated errors", () => {
      expect(
        beadsUtils.isBeadsSchemaSkew({ error: "boom", code: "bd_failed" }),
      ).toBe(false);
      expect(beadsUtils.isBeadsSchemaSkew({ error: "boom" })).toBe(false);
      expect(beadsUtils.isBeadsSchemaSkew(null)).toBe(false);
      expect(beadsUtils.isBeadsSchemaSkew(undefined)).toBe(false);
    });

    test("toSchemaSkewState maps the flattened envelope into SchemaSkewDialog state", () => {
      const data = {
        error: "The beads database at /x/.beads needs migration",
        code: "beads_schema_skew",
        details: {
          db_path: "/x/.beads",
          hint: "run bd migrate",
          options: ["allow_migrate_from_ui"],
          allow_migrate_from_ui: false,
        },
      };
      expect(beadsUtils.toSchemaSkewState(data)).toEqual({
        message: "The beads database at /x/.beads needs migration",
        dbPath: "/x/.beads",
        hint: "run bd migrate",
        options: ["allow_migrate_from_ui"],
        allowMigrate: false,
        databaseMode: "shared",
      });
    });

    test("toSchemaSkewState tolerates missing details", () => {
      const state = beadsUtils.toSchemaSkewState({
        error: "msg",
        code: "beads_schema_skew",
      });
      expect(state.message).toBe("msg");
      expect(state.dbPath).toBe("");
      expect(state.hint).toBe("");
      expect(state.options).toEqual([]);
      expect(state.allowMigrate).toBe(true);
      expect(state.databaseMode).toBe("shared");
    });
  });

  describe("BeadsDetailPanelWrapper write handlers route schema_skew to onSchemaSkew", () => {
    // The wrapper cannot reach setSchemaSkew directly (it lives in the parent
    // BeadsView), so the fix threads a callback prop (e.g. onSchemaSkew(data))
    // through to it. Every error branch must gate on isBeadsSchemaSkew and
    // call that callback before falling back to the plain toast.

    test("handleToggleStatus (wrapper) branches on isBeadsSchemaSkew", () => {
      // The wrapper's handleToggleStatus lives before the main-view one; its
      // body appears first in the file. We slice just that first occurrence.
      const idx = source.indexOf("const handleToggleStatus = useCallback");
      expect(idx).toBeGreaterThan(-1);
      const nextIdx = source.indexOf("\n  const handle", idx + 30);
      const body = source.slice(idx, nextIdx > idx ? nextIdx : idx + 4000);
      expect(body).toMatch(/isBeadsSchemaSkew\s*\(/);
    });

    test("handleToggleDefer (wrapper) branches on isBeadsSchemaSkew", () => {
      const idx = source.indexOf("const handleToggleDefer = useCallback");
      expect(idx).toBeGreaterThan(-1);
      const nextIdx = source.indexOf("\n  const ", idx + 30);
      const body = source.slice(idx, nextIdx > idx ? nextIdx : idx + 4000);
      expect(body).toMatch(/isBeadsSchemaSkew\s*\(/);
    });

    test("confirmDeleteIssue (wrapper) branches on isBeadsSchemaSkew", () => {
      // Two confirmDeleteIssue definitions exist (wrapper + main view). The
      // wrapper's is the FIRST occurrence.
      const idx = source.indexOf("const confirmDeleteIssue = useCallback");
      expect(idx).toBeGreaterThan(-1);
      const nextIdx = source.indexOf("\n  const ", idx + 30);
      const body = source.slice(idx, nextIdx > idx ? nextIdx : idx + 4000);
      expect(body).toMatch(/isBeadsSchemaSkew\s*\(/);
    });
  });

  describe("Main BeadsView write handlers route schema_skew to setSchemaSkew", () => {
    // These handlers all live inside the main BeadsView function and have
    // setSchemaSkew / setShowMigrateDialog in scope, so they can populate the
    // dialog directly.

    test("handleCleanup branches on isBeadsSchemaSkew", () => {
      const body = extractHandlerBody("const handleCleanup = useCallback");
      expect(body).toMatch(/isBeadsSchemaSkew\s*\(/);
    });

    test("main-view confirmDeleteIssue branches on isBeadsSchemaSkew (and does not just accumulate closeFailed / childDeleteFailed on 409)", () => {
      // The second occurrence of confirmDeleteIssue is the main-view one.
      const first = source.indexOf("const confirmDeleteIssue = useCallback");
      expect(first).toBeGreaterThan(-1);
      const second = source.indexOf(
        "const confirmDeleteIssue = useCallback",
        first + 1,
      );
      expect(second).toBeGreaterThan(-1);
      const nextIdx = source.indexOf("\n  const ", second + 30);
      const body = source.slice(
        second,
        nextIdx > second ? nextIdx : second + 8000,
      );
      // Guard on the parent delete branch.
      expect(body).toMatch(/isBeadsSchemaSkew\s*\(/);
      // Child loops must also honour schema_skew — either by bailing early or
      // by calling the helper. Look for at least two mentions inside the body
      // (parent branch + at least one child loop bail).
      const hits = body.match(/isBeadsSchemaSkew\s*\(/g) || [];
      expect(hits.length).toBeGreaterThanOrEqual(2);
    });

    test("main-view handleToggleStatus branches on isBeadsSchemaSkew", () => {
      const first = source.indexOf("const handleToggleStatus = useCallback");
      const second = source.indexOf(
        "const handleToggleStatus = useCallback",
        first + 1,
      );
      expect(second).toBeGreaterThan(-1);
      const nextIdx = source.indexOf("\n  const ", second + 30);
      const body = source.slice(
        second,
        nextIdx > second ? nextIdx : second + 4000,
      );
      expect(body).toMatch(/isBeadsSchemaSkew\s*\(/);
    });

    test("main-view handleToggleDefer branches on isBeadsSchemaSkew", () => {
      const first = source.indexOf("const handleToggleDefer = useCallback");
      const second = source.indexOf(
        "const handleToggleDefer = useCallback",
        first + 1,
      );
      expect(second).toBeGreaterThan(-1);
      const nextIdx = source.indexOf("\n  const ", second + 30);
      const body = source.slice(
        second,
        nextIdx > second ? nextIdx : second + 4000,
      );
      expect(body).toMatch(/isBeadsSchemaSkew\s*\(/);
    });

    test("handleAddDependencyEdge branches on isBeadsSchemaSkew", () => {
      const body = extractHandlerBody(
        "const handleAddDependencyEdge = useCallback",
      );
      expect(body).toMatch(/isBeadsSchemaSkew\s*\(/);
    });
  });
});

// =============================================================================
// mitto-erry: SchemaSkewDialog copy consolidation + kill-switch UX
// =============================================================================
//
// The bead flipped `web.beads.allow_migrate_from_ui` from opt-in to default-on
// kill-switch and consolidated the risk copy directly into the ack-checkbox
// label (previously a stand-alone amber banner duplicated the same warning).
// These source-scanning assertions parallel the mitto-zbfq / mitto-n5mw pattern
// established above.

describe("mitto-erry: SchemaSkewDialog copy consolidation + kill-switch UX", () => {
  const source = readFileSync(BEADS_VIEW_PATH, "utf8");

  // Slice the SchemaSkewDialog function body so the assertions are scoped to
  // it rather than the whole 3400-line component.
  function extractSchemaSkewDialogSource() {
    const startMarker = "function SchemaSkewDialog(";
    const startIdx = source.indexOf(startMarker);
    expect(startIdx).toBeGreaterThan(-1);
    const afterStart = source.indexOf(
      "\nfunction ",
      startIdx + startMarker.length,
    );
    const endIdx = afterStart === -1 ? source.length : afterStart;
    return source.slice(startIdx, endIdx);
  }

  test("ack-checkbox label carries the 'designated migrator' + '#4259' consolidated copy", () => {
    const body = extractSchemaSkewDialogSource();
    // Locate the ack-checkbox and slice the enclosing <label> so we assert the
    // copy lives next to the checkbox itself, not somewhere else in the dialog.
    const checkboxIdx = body.indexOf('data-testid="schema-skew-ack-checkbox"');
    expect(checkboxIdx).toBeGreaterThan(-1);
    const labelStart = body.lastIndexOf("<label", checkboxIdx);
    expect(labelStart).toBeGreaterThan(-1);
    const labelEnd = body.indexOf("</label>", checkboxIdx);
    expect(labelEnd).toBeGreaterThan(-1);
    const labelBlock = body.slice(labelStart, labelEnd);
    expect(labelBlock).toMatch(/designated migrator/i);
    expect(labelBlock).toMatch(/#4259/);
  });

  test("stand-alone amber warning banner is gone (copy lives in the ack label only)", () => {
    const body = extractSchemaSkewDialogSource();
    // A stand-alone banner would repeat the "designated migrator clone" copy
    // outside the ack-checkbox <label>. The kill-switch error text uses a
    // different phrasing ("designated clone") so the JSX-rendered risk copy
    // must appear exactly once — inside the ack label — and never outside it.
    const jsxHits = body.match(/designated migrator clone/gi) || [];
    expect(jsxHits.length).toBe(1);
    // And the sole hit must be scoped to the ack-checkbox <label>.
    const checkboxIdx = body.indexOf('data-testid="schema-skew-ack-checkbox"');
    const labelStart = body.lastIndexOf("<label", checkboxIdx);
    const labelEnd = body.indexOf("</label>", checkboxIdx);
    const labelBlock = body.slice(labelStart, labelEnd);
    expect(labelBlock).toMatch(/designated migrator clone/i);
  });

  test("migrate_from_ui_disabled branch copy names the kill-switch flag", () => {
    // Whole-file scan is fine here — the branch lives inside SchemaSkewDialog's
    // handleConfirm error handler and cites the flag by name so admins can find
    // it in their config.
    expect(source).toMatch(/migrate_from_ui_disabled/);
    expect(source).toMatch(/web\.beads\.allow_migrate_from_ui/);
  });

  test("forward schema skew cannot offer or enable the migration action", () => {
    const body = extractSchemaSkewDialogSource();
    expect(body).toMatch(
      /allowMigrate\s*&&\s*\(isLocalMode \|\| mode === "adopt" \|\| ackChecked\)/,
    );
    expect(body).toMatch(/showConfirm=\$\{allowMigrate\}/);
    expect(body).toMatch(/isRunning \|\| !allowMigrate/);
    expect(body).toMatch(/"Beads recovery required"/);
    const confirmDialogSource = readFileSync(
      resolve(__dirname_bv, "ConfirmDialog.js"),
      "utf8",
    );
    expect(confirmDialogSource).toMatch(
      /showConfirm\s*&&[\s\S]{0,200}<button[\s\S]{0,300}data-testid="confirm-dialog-confirm"/,
    );
    expect(source).toMatch(
      /schemaSkew\.allowMigrate\s*&&[\s\S]{0,300}data-testid="beads-run-migration-btn"/,
    );
  });
});

describe("mitto-wx5t.3: local schema remediation stays local-only", () => {
  const source = readFileSync(BEADS_VIEW_PATH, "utf8");
  const start = source.indexOf("function SchemaSkewDialog(");
  const end = source.indexOf("\nfunction ", start + 1);
  const body = source.slice(start, end === -1 ? source.length : end);

  test("filters remote-backed options and renders only available options", () => {
    expect(body).toMatch(
      /options\.filter\(\(opt\) => !isLocalMode \|\| opt\.mode === "migrate"\)/,
    );
    expect(body).toMatch(/availableOptions\.map\(/);
    expect(body).not.toMatch(/\$\{options\.map\(/);
  });

  test("forces an in-place migrate request even if selected mode is stale", () => {
    expect(body).toMatch(/mode: isLocalMode \? "migrate" : mode/);
  });
});

// =============================================================================
// mitto-vc2m: SchemaSkewDialog.handleConfirm must use secureFetch (CSRF header)
// =============================================================================
//
// The original bug: SchemaSkewDialog.handleConfirm called
//   authFetch(endpoints.beads.migrate(), { method: "POST", ... })
// which drops the X-CSRF-Token header, so the middleware rejects the request
// with 403 (has_header=false has_cookie=true). Symptom on mobile: "Run
// migration" dies with a 403 toast and never reaches the backend.
//
// mitto-7gta.17 slice S3: handleConfirm was migrated onto the SDK client
// (getSdkClient().issues.migrate()). This structurally closes the whole "picked
// the wrong fetch helper" bug class — browserCookieAuth (sdk/auth/browser-cookie.js)
// applies the X-CSRF-Token header unconditionally to every mutating request the
// SDK makes, so there is no authFetch/secureFetch choice left to get wrong. This
// source-scan assertion now pins the migrate call to the SDK and guards against a
// future regression back to a raw authFetch/secureFetch call.
//
// Source-scan style parallels the mitto-erry / mitto-n5mw / mitto-zbfq blocks
// above — a full DOM render test is impractical because BeadsView imports
// window.preact globals at module load time.

describe("mitto-vc2m: SchemaSkewDialog migrate call must use the SDK client (CSRF)", () => {
  const source = readFileSync(BEADS_VIEW_PATH, "utf8");

  function extractSchemaSkewDialogSource() {
    const startMarker = "function SchemaSkewDialog(";
    const startIdx = source.indexOf(startMarker);
    expect(startIdx).toBeGreaterThan(-1);
    const afterStart = source.indexOf(
      "\nfunction ",
      startIdx + startMarker.length,
    );
    const endIdx = afterStart === -1 ? source.length : afterStart;
    return source.slice(startIdx, endIdx);
  }

  test("handleConfirm calls getSdkClient().issues.migrate(), never authFetch/secureFetch", () => {
    const body = extractSchemaSkewDialogSource();
    // Locate the migrate call site. Collapse whitespace so line breaks between
    // the client accessor and the resource method do not hide it.
    const collapsed = body.replace(/\s+/g, " ");

    // Positive: the migrate POST must go through the SDK client, whose auth
    // adapter applies X-CSRF-Token unconditionally.
    expect(collapsed).toMatch(/getSdkClient\(\)\.issues\.migrate\s*\(/);

    // Negative: neither raw fetch helper may reappear on this call site —
    // authFetch omits X-CSRF-Token entirely (the exact 403 shape reported in
    // mitto-vc2m), and secureFetch would be a regression back to a bespoke
    // fetch call the SDK migration was meant to retire.
    expect(collapsed).not.toMatch(/\bauthFetch\s*\(/);
    expect(collapsed).not.toMatch(/\bsecureFetch\s*\(/);
  });
});

// =============================================================================
// mitto-cq2n.1: SchemaSkewDialog must render details.stderr, not just the
// generic error wrapper message
// =============================================================================
//
// The original bug: when POST /api/beads/migrate failed, handleConfirm's
// catch branch called `setErrorMsg(beadsErrorFrom(err, ...).error)` — only
// the flattened `.error` (the generic "bd exited with non-zero status: exit
// status N" wrapper) — and never read `.stderr`, silently discarding the
// actionable diagnostic the backend had already captured and returned in
// `error.details.stderr` (e.g. a `bd dolt push` remote/auth failure). The fix
// renders both the primary message and the raw (possibly multi-line) stderr.
//
// Source-scan style parallels the mitto-erry / mitto-vc2m blocks above.

describe("mitto-cq2n.1: SchemaSkewDialog renders both errorMsg and details.stderr", () => {
  const source = readFileSync(BEADS_VIEW_PATH, "utf8");

  function extractSchemaSkewDialogSource() {
    const startMarker = "function SchemaSkewDialog(";
    const startIdx = source.indexOf(startMarker);
    expect(startIdx).toBeGreaterThan(-1);
    const afterStart = source.indexOf(
      "\nfunction ",
      startIdx + startMarker.length,
    );
    const endIdx = afterStart === -1 ? source.length : afterStart;
    return source.slice(startIdx, endIdx);
  }

  test("handleConfirm's catch branch captures beadsErrorFrom(err).stderr into state", () => {
    const body = extractSchemaSkewDialogSource();
    const collapsed = body.replace(/\s+/g, " ");
    // The non-kill-switch branch must read the flattened error object once
    // (so both .error and .stderr come from the same beadsErrorFrom() call)
    // rather than re-deriving errorMsg via a bare `.error` accessor chain
    // that has no sibling `.stderr` read anywhere nearby.
    expect(collapsed).toMatch(
      /beadsErrorFrom\(\s*err\s*,\s*["'][^"']*["']\s*\)/,
    );
    expect(collapsed).toMatch(/\.stderr\b/);
    // Must be plumbed into a piece of state that is distinct from errorMsg
    // (i.e. not silently discarded) — look for a state setter call whose
    // name is not setErrorMsg but is invoked with a `.stderr` derived value.
    expect(collapsed).toMatch(/setErrorStderr\s*\(/);
  });

  test("dialog resets stderr state whenever it reopens or a new attempt starts", () => {
    const body = extractSchemaSkewDialogSource();
    // The stale-state-leak class of bug (mitto-erry's own useEffect reset
    // pattern) applies equally to the new stderr state: it must be cleared
    // both on reopen (isOpen effect) and at the start of every handleConfirm
    // attempt, mirroring how errorMsg is already reset in both places.
    const setStderrCalls = (body.match(/setErrorStderr\(\s*""\s*\)/g) || [])
      .length;
    expect(setStderrCalls).toBeGreaterThanOrEqual(2);
  });

  test("dialog renders details.stderr in a distinct, multiline-preserving block", () => {
    const body = extractSchemaSkewDialogSource();
    const collapsed = body.replace(/\s+/g, " ");
    // A dedicated testid must exist for the stderr block, distinct from the
    // primary error message's testid.
    expect(body).toMatch(/data-testid="schema-skew-dialog-error-stderr"/);
    expect(body).toMatch(/data-testid="schema-skew-dialog-error"/);
    // Multiline output must be preserved (whitespace-pre-wrap or a <pre>
    // element), not collapsed the way `break-all` alone would visually
    // squash it.
    expect(collapsed).toMatch(/whitespace-pre-wrap|<pre\b/);
  });
});

// =============================================================================
// mitto-vqf — renderIssueRow bgTone: tint whole row when a linked conversation
// is prompting on the bead.
// =============================================================================
//
// Mirrors the `bgTone` ternary inside renderIssueRow (BeadsView.js ~L2501–2507):
// a 4-way branch over (isSelected × isStreamingIssue). BeadsView.js cannot be
// imported under jsdom (reads window.preact at module load — same limitation
// documented above), so this file mirrors the pure computation as a small
// helper and additionally reads the JS/CSS source to guard against silent
// drift between the mirror and the real code.
//
// If the branch structure in BeadsView.js changes, `computeBgTone` below and
// the source-guard regexes must be updated to match.

/**
 * Mirrors the `bgTone` expression in renderIssueRow. Returns the same class
 * string the component composes for the `list-row` element background.
 */
function computeBgTone(isSelected, isStreamingIssue) {
  return isSelected
    ? isStreamingIssue
      ? "bg-mitto-surface-3/30 beads-row-streaming"
      : "bg-mitto-surface-3/30"
    : isStreamingIssue
      ? "beads-row-streaming hover:bg-red-600"
      : "bg-mitto-surface-3/20 hover:bg-red-600";
}

describe("renderIssueRow bgTone — 4-way branch over (isSelected × isStreamingIssue)", () => {
  test("not-selected, not-streaming: base surface tint + hover-red", () => {
    const tone = computeBgTone(false, false);
    expect(tone).toBe("bg-mitto-surface-3/20 hover:bg-red-600");
    expect(tone).not.toContain("beads-row-streaming");
  });

  test("not-selected, streaming: streaming class + hover-red (hover still reachable)", () => {
    const tone = computeBgTone(false, true);
    expect(tone).toContain("beads-row-streaming");
    expect(tone).toContain("hover:bg-red-600");
    // Base surface utility is intentionally dropped on streaming rows so the
    // .beads-row-streaming CSS rule owns the background without competing
    // with `bg-mitto-surface-3/20`.
    expect(tone).not.toContain("bg-mitto-surface-3/20");
  });

  test("selected, not-streaming: selection surface, no streaming class, no hover-red", () => {
    const tone = computeBgTone(true, false);
    expect(tone).toBe("bg-mitto-surface-3/30");
    expect(tone).not.toContain("beads-row-streaming");
    expect(tone).not.toContain("hover:bg-red-600");
  });

  test("selected + streaming: selection surface AND streaming class combine", () => {
    // Per plan: selected+streaming keeps the selection tint AND the blue
    // streaming class so the two signals combine legibly (the selection
    // accent border is applied separately via borderTone, not part of bgTone).
    const tone = computeBgTone(true, true);
    expect(tone).toContain("bg-mitto-surface-3/30");
    expect(tone).toContain("beads-row-streaming");
    // Selected rows never take hover-red (matches non-streaming selected).
    expect(tone).not.toContain("hover:bg-red-600");
  });

  test("truthy isSelected values (object) behave like true (mirrors `selectedIssue && …`)", () => {
    // In BeadsView.js, `isSelected` is `selectedIssue && selectedIssue.id === issue.id`
    // — falsy when no selection, truthy (boolean true) when matched. Guard the
    // helper against a truthy-but-non-boolean caller passing the raw expression.
    const tone = computeBgTone({ id: "mitto-aaa" }, true);
    expect(tone).toContain("bg-mitto-surface-3/30");
    expect(tone).toContain("beads-row-streaming");
  });

  test("streaming class is present iff isStreamingIssue is true (both selected states)", () => {
    for (const sel of [true, false]) {
      expect(computeBgTone(sel, true)).toContain("beads-row-streaming");
      expect(computeBgTone(sel, false)).not.toContain("beads-row-streaming");
    }
  });
});

describe("renderIssueRow bgTone — source guard (mitto-vqf)", () => {
  // Read the real BeadsView.js/styles-v2.css from disk and assert the
  // implementation still matches the mirror + spec, so a future refactor
  // that drops the streaming branch or renames the class trips a test.
  const here = dirname(fileURLToPath(import.meta.url));
  const beadsViewSource = readFileSync(resolve(here, "BeadsView.js"), "utf8");
  const stylesSource = readFileSync(
    resolve(here, "..", "styles-v2.css"),
    "utf8",
  );

  test("BeadsView.js still branches bgTone on isStreamingIssue", () => {
    // The bgTone expression must still be a 4-way branch that includes the
    // streaming class on both selected and non-selected paths.
    expect(beadsViewSource).toMatch(/const bgTone = isSelected/);
    // Both branches (selected+streaming and non-selected+streaming) must
    // apply the shared semantic class.
    const streamingBranchCount = (
      beadsViewSource.match(/beads-row-streaming/g) || []
    ).length;
    expect(streamingBranchCount).toBeGreaterThanOrEqual(2);
    // Non-selected streaming path keeps hover-red reachable.
    expect(beadsViewSource).toContain('"beads-row-streaming hover:bg-red-600"');
    // Selected+streaming path combines surface tint with the streaming class.
    expect(beadsViewSource).toContain(
      '"bg-mitto-surface-3/30 beads-row-streaming"',
    );
  });

  test("styles-v2.css defines .beads-row-streaming in BOTH light and dark themes", () => {
    // Per-theme rules are required so the tint reads correctly in both
    // modes (spec: "must look good and stay readable in BOTH light and
    // dark modes"). Use flexible whitespace so a future reformat does not
    // trip these guards.
    expect(stylesSource).toMatch(
      /\.light\s+\.beads-row-streaming\s*\{[^}]*background:[^;}]+;?[^}]*\}/,
    );
    expect(stylesSource).toMatch(
      /\.dark\s+\.beads-row-streaming\s*\{[^}]*background:[^;}]+;?[^}]*\}/,
    );
  });

  test("streaming tint uses the in-progress blue family, not a new hue", () => {
    // Spec: "Use a color from Mitto's existing palette … Do NOT introduce a
    // new hue." The rule must reference blue-500 (rgb 59,130,246 — same
    // family as the beadsInProgressPulse keyframe) rather than any other
    // color name/token.
    const lightMatch = stylesSource.match(
      /\.light\s+\.beads-row-streaming\s*\{[^}]+\}/,
    );
    const darkMatch = stylesSource.match(
      /\.dark\s+\.beads-row-streaming\s*\{[^}]+\}/,
    );
    expect(lightMatch).not.toBeNull();
    expect(darkMatch).not.toBeNull();
    expect(lightMatch[0]).toMatch(/rgba\(\s*59\s*,\s*130\s*,\s*246\s*,/);
    expect(darkMatch[0]).toMatch(/rgba\(\s*59\s*,\s*130\s*,\s*246\s*,/);
  });

  test("hover-red rule is still defined earlier in the file with !important, so hover wins on streaming rows", () => {
    // Spec regression guard: on a non-selected streaming row, hover-red
    // must still be reachable. The hover-red rule sits earlier in the file
    // and carries !important; higher specificity + !important beats the
    // single-class .beads-row-streaming.
    const hoverIdx = stylesSource.indexOf(".hover\\:bg-red-600:hover");
    const streamingIdx = stylesSource.indexOf(".beads-row-streaming");
    expect(hoverIdx).toBeGreaterThan(-1);
    expect(streamingIdx).toBeGreaterThan(-1);
    // Hover rule must be defined before the streaming rule (source order
    // matters when specificity ties — but here specificity already favors
    // the compound hover selector; this guard also protects the
    // documented placement in the file).
    expect(hoverIdx).toBeLessThan(streamingIdx);
    // Both light and dark hover-red rules use !important.
    expect(stylesSource).toMatch(
      /\.light\s+\.hover\\:bg-red-600:hover[\s\S]*?!important/,
    );
    expect(stylesSource).toMatch(
      /\.dark\s+\.hover\\:bg-red-600:hover[\s\S]*?!important/,
    );
  });
});

// =============================================================================
// mitto-19j: BeadsIssueView drawer must listen for mitto:beads_changed so the
// single-issue drawer refreshes when the on-disk bead changes externally
// (agent `bd close`/`bd update`, sibling Mitto session, `bd` CLI, `git pull`,
// `bd dolt pull`). The outer list view already wires this at BeadsView.js
// L1502-1511; the drawer branch was missed and silently ages out.
//
// The fix is a single effect in BeadsIssueView that adds a
// `mitto:beads_changed` window listener scoped by `working_dir` and bumps
// `refreshNonce` (which already re-fires the /api/issues/{id} fetch at L525
// and the sibling /api/issues list fetch at L551).
//
// Mirrors the source-inspection convention used for mitto-zbfq / mitto-n5mw
// above: a pure DOM/render test of BeadsView is impractical because
// BeadsView.js imports window.preact globals at module load time.

describe("mitto-19j: BeadsIssueView listens for mitto:beads_changed", () => {
  const source = readFileSync(BEADS_VIEW_PATH, "utf8");

  // Isolate the BeadsIssueView function body so the assertion does not
  // accidentally match the outer BeadsView list-view listener at L1502
  // (which is not the code under test here).
  function extractBeadsIssueViewSource() {
    const startMarker = "function BeadsIssueView(";
    const startIdx = source.indexOf(startMarker);
    expect(startIdx).toBeGreaterThan(-1);
    const afterStart = source.indexOf(
      "\nfunction ",
      startIdx + startMarker.length,
    );
    const endIdx = afterStart === -1 ? source.length : afterStart;
    return source.slice(startIdx, endIdx);
  }

  test("BeadsIssueView registers a mitto:beads_changed window listener", () => {
    const body = extractBeadsIssueViewSource();

    // The listener must be registered inside BeadsIssueView (not just at the
    // outer BeadsView list-view). Accept both `addEventListener` and
    // `window.addEventListener` spellings so the test does not couple to
    // formatting choices.
    expect(body).toMatch(
      /(window\.)?addEventListener\(\s*["']mitto:beads_changed["']/,
    );
  });

  test("BeadsIssueView listener is scoped by working_dir (no cross-workspace refetch)", () => {
    const body = extractBeadsIssueViewSource();

    // Acceptance criterion: cross-workspace events must NOT trigger a
    // refetch. The listener must therefore consult `working_dirs` from the
    // event detail and gate on the current `workingDir`.
    expect(body).toMatch(/working_dirs/);
    // And it must funnel back into the refresh mechanism the two data-fetch
    // effects already depend on (refreshNonce).
    expect(body).toMatch(/setRefreshNonce/);
  });
});

// =============================================================================
// mitto-0qn: computeEffectiveStreamingSet — extends the streaming-issue set
// with every ancestor reached by walking issue.parent upward from any seed, so
// an epic row tints blue when any of its transitive descendants is currently
// prompting (visible even when the group is collapsed).
// =============================================================================
//
// Covers the pure helper in utils/beads.js. A sibling source-guard describe
// block below asserts BeadsView's renderIssueRow reads `effectiveStreamingSet`
// (not the raw prop `issueStreamingSet`) so the wiring cannot silently
// regress.

describe("computeEffectiveStreamingSet (mitto-0qn)", () => {
  test("empty streaming set → empty result (never mutates input)", () => {
    const issues = [{ id: "a" }, { id: "b", parent: "a" }];
    const seed = new Set();
    const out = computeEffectiveStreamingSet(issues, seed);
    expect(out).toBeInstanceOf(Set);
    expect(out.size).toBe(0);
    // Input untouched.
    expect(seed.size).toBe(0);
    // Fresh Set instance is returned, not the same reference.
    expect(out).not.toBe(seed);
  });

  test("null / undefined streaming set → empty result", () => {
    expect(computeEffectiveStreamingSet([{ id: "a" }], null).size).toBe(0);
    expect(computeEffectiveStreamingSet([{ id: "a" }], undefined).size).toBe(0);
  });

  test("leaf-only, no parent → set unchanged", () => {
    const issues = [{ id: "leaf" }];
    const seed = new Set(["leaf"]);
    const out = computeEffectiveStreamingSet(issues, seed);
    expect([...out].sort()).toEqual(["leaf"]);
    // Original set instance is not mutated.
    expect([...seed].sort()).toEqual(["leaf"]);
  });

  test("direct parent → parent added to the set", () => {
    const issues = [{ id: "epic1" }, { id: "task1", parent: "epic1" }];
    const out = computeEffectiveStreamingSet(issues, new Set(["task1"]));
    expect([...out].sort()).toEqual(["epic1", "task1"]);
  });

  test("2-deep grandparent chain → both ancestors added", () => {
    const issues = [
      { id: "epic1" },
      { id: "epic2", parent: "epic1" },
      { id: "task1", parent: "epic2" },
    ];
    const out = computeEffectiveStreamingSet(issues, new Set(["task1"]));
    expect([...out].sort()).toEqual(["epic1", "epic2", "task1"]);
  });

  test("cycle A→B→A terminates and tints both nodes exactly once", () => {
    const issues = [
      { id: "a", parent: "b" },
      { id: "b", parent: "a" },
    ];
    const out = computeEffectiveStreamingSet(issues, new Set(["a"]));
    expect([...out].sort()).toEqual(["a", "b"]);
  });

  test("missing parent id in issues list → walk stops cleanly", () => {
    // task1 references an epic that does not exist in `issues`. The parent id
    // is still added to the effective set (the tint is a UI concern; the
    // ancestor id is what the row read compares against), but the walk stops
    // there without throwing.
    const issues = [{ id: "task1", parent: "ghost-epic" }];
    const out = computeEffectiveStreamingSet(issues, new Set(["task1"]));
    expect([...out].sort()).toEqual(["ghost-epic", "task1"]);
  });

  test("two disjoint trees, streaming in only one → other tree untouched", () => {
    const issues = [
      { id: "epicA" },
      { id: "taskA", parent: "epicA" },
      { id: "epicB" },
      { id: "taskB", parent: "epicB" },
    ];
    const out = computeEffectiveStreamingSet(issues, new Set(["taskA"]));
    expect([...out].sort()).toEqual(["epicA", "taskA"]);
    expect(out.has("epicB")).toBe(false);
    expect(out.has("taskB")).toBe(false);
  });

  test("multiple seeds share ancestor → ancestor tinted once, both leaves included", () => {
    const issues = [
      { id: "epic1" },
      { id: "task1", parent: "epic1" },
      { id: "task2", parent: "epic1" },
    ];
    const out = computeEffectiveStreamingSet(
      issues,
      new Set(["task1", "task2"]),
    );
    expect([...out].sort()).toEqual(["epic1", "task1", "task2"]);
  });

  test("empty/undefined issues list still returns the seed set", () => {
    const out = computeEffectiveStreamingSet([], new Set(["only"]));
    expect([...out].sort()).toEqual(["only"]);
    const out2 = computeEffectiveStreamingSet(undefined, new Set(["only"]));
    expect([...out2].sort()).toEqual(["only"]);
  });
});

describe("renderIssueRow effective-streaming-set — source guard (mitto-0qn)", () => {
  // Guard against silent regression: renderIssueRow's isStreamingIssue read
  // must consult the memoized `effectiveStreamingSet` (which includes
  // ancestors), NOT the raw `issueStreamingSet` prop (leaves only). If a
  // future refactor swaps the read back to the raw prop, the ancestor-tint
  // acceptance criterion silently breaks — this test trips first.
  const source = readFileSync(BEADS_VIEW_PATH, "utf8");

  test("BeadsView.js imports computeEffectiveStreamingSet from utils/beads.js", () => {
    expect(source).toMatch(/computeEffectiveStreamingSet/);
    // The import must land in the utils/beads.js import block, not from an
    // unrelated module.
    expect(source).toMatch(
      /import\s*\{[\s\S]*?computeEffectiveStreamingSet[\s\S]*?\}\s*from\s*["']\.\.\/utils\/beads\.js["']/,
    );
  });

  test("BeadsView.js memoizes effectiveStreamingSet with correct deps", () => {
    // The memo must derive from both `issues` and `issueStreamingSet` so the
    // tint recomputes when either the graph or the base streaming set
    // changes (spec: tint clears within one render after streaming stops).
    expect(source).toMatch(
      /const\s+effectiveStreamingSet\s*=\s*useMemo\s*\(\s*\(\s*\)\s*=>\s*computeEffectiveStreamingSet\s*\(\s*issues\s*,\s*issueStreamingSet\s*\)/,
    );
    expect(source).toMatch(
      /computeEffectiveStreamingSet\s*\(\s*issues\s*,\s*issueStreamingSet\s*\)\s*,\s*\[\s*issues\s*,\s*issueStreamingSet\s*\]/,
    );
  });

  test("renderIssueRow reads effectiveStreamingSet.has(issue.id), NOT the raw prop", () => {
    // The read that drives isStreamingIssue must consult the effective set so
    // ancestor epic rows are tinted.
    expect(source).toMatch(
      /const\s+isStreamingIssue\s*=\s*effectiveStreamingSet\.has\(\s*issue\.id\s*\)/,
    );
    // And it must NOT read the raw prop `issueStreamingSet.has(...)` anywhere
    // inside renderIssueRow — that would silently regress the acceptance
    // criterion (the raw prop contains only leaves).
    expect(source).not.toMatch(/issueStreamingSet\.has\s*\(/);
  });
});

// =============================================================================
// mitto-9vh: BeadsIssueView must stop polling deleted issues after the first 404
// =============================================================================
//
// Bug: when an issue was deleted externally (agent bd close, sibling Mitto
// session, git pull), the drawer's fetch effect would 404, surface the error,
// but never mark the id as "gone". Any subsequent mitto:beads_changed event
// (fired for any .beads/ filesystem change) bumped refreshNonce, re-running
// the effect and re-issuing the same GET /api/issues/<id> -> 404. Observed:
// 589 x 404 for mitto-46k in 8h from a single stale drawer, across all
// connected clients (local + 3 mobile IPs).
//
// Fix (frontend-only):
//   1. BeadsIssueView tracks 404'd ids in a per-instance goneIdsRef (Set).
//   2. The fetch effect short-circuits any id already in the set.
//   3. A 404 response adds the id to the set and sets loadError to
//      {message, gone: true} (skips the toast — external deletion is expected).
//   4. PanelBody's isLoading branch normalizes loadError (string | object) and
//      suppresses the Retry button when gone=true (retrying would just 404).
//
// A full E2E test that opens the drawer, deletes the issue, fires
// mitto:beads_changed, and counts network requests lives in
// tests/ui/specs/beads.spec.ts ("stops polling the drawer's issue after the
// first 404"). This file's job is to lock the source structure so the fix
// cannot silently regress via a later refactor — matching the mitto-zbfq /
// mitto-n5mw pattern already established in this file.

describe("mitto-9vh: BeadsIssueView stops polling deleted issues", () => {
  const source = readFileSync(BEADS_VIEW_PATH, "utf8");

  // Isolate the BeadsIssueView function body so assertions do not leak into
  // sibling components. Same slicing pattern as the mitto-zbfq test above.
  function extractBeadsIssueViewSource() {
    const startMarker = "function BeadsIssueView(";
    const startIdx = source.indexOf(startMarker);
    expect(startIdx).toBeGreaterThan(-1);
    const afterStart = source.indexOf(
      "\nfunction ",
      startIdx + startMarker.length,
    );
    const endIdx = afterStart === -1 ? source.length : afterStart;
    return source.slice(startIdx, endIdx);
  }

  test("declares a goneIdsRef useRef(new Set()) inside BeadsIssueView", () => {
    // The gate needs a per-instance, mutation-safe container. A ref (not
    // state) is deliberate: mutating the Set must NOT trigger a re-render —
    // the guard is consulted inside the fetch effect whose deps
    // (refreshNonce/currentIssueId) already re-run on external changes.
    const body = extractBeadsIssueViewSource();
    const collapsed = body.replace(/\s+/g, " ");
    expect(collapsed).toMatch(
      /const\s+goneIdsRef\s*=\s*useRef\s*\(\s*new\s+Set\s*\(\s*\)\s*\)/,
    );
  });

  test("fetch effect short-circuits ids already in goneIdsRef before issuing a request", () => {
    // The guard must run BEFORE the SDK call, so refreshNonce bumps on a
    // known-gone id become a cheap no-op instead of another 404. Without
    // this, mitto:beads_changed re-fires the same GET forever (589 x 404
    // observed). mitto-7gta.17 slice S3: the fetch site is now
    // getSdkClient().issues.show(...), not authFetch(endpoints.issues.show(...)).
    const body = extractBeadsIssueViewSource();
    const collapsed = body.replace(/\s+/g, " ");
    // Grab the guard site and the fetch site, then assert the guard's index
    // is BEFORE the issues.show() call inside the effect.
    const guardIdx = collapsed.search(
      /if\s*\(\s*goneIdsRef\.current\.has\s*\(\s*currentIssueId\s*\)\s*\)/,
    );
    expect(guardIdx).toBeGreaterThan(-1);
    const fetchRe =
      /getSdkClient\s*\(\s*\)\s*\.issues\.show\s*\(\s*currentIssueId/;
    const fetchMatch = collapsed.match(fetchRe);
    expect(fetchMatch).not.toBeNull();
    expect(guardIdx).toBeLessThan(collapsed.indexOf(fetchMatch[0]));
  });

  test("404 response adds the id to goneIdsRef so future refreshNonce bumps no-op", () => {
    // The recovery half of the fix: when the fetch discovers a 404, the id
    // must be memoized so the guard above will skip it next time. If this
    // add() is missing, the guard is unreachable and the bug returns.
    // mitto-7gta.17 slice S3: the SDK throws on non-2xx instead of returning
    // a raw Response, so the 404 branch is now `isNotFoundError(err)` inside
    // the catch block, not a `res.status === 404` check.
    const body = extractBeadsIssueViewSource();
    const collapsed = body.replace(/\s+/g, " ");
    // The add() lives inside the isNotFoundError(err) branch. Assert both
    // pieces exist in that order.
    const statusIdx = collapsed.search(/isNotFoundError\s*\(\s*err\s*\)/);
    expect(statusIdx).toBeGreaterThan(-1);
    const addRe = /goneIdsRef\.current\.add\s*\(\s*currentIssueId\s*\)/;
    const addMatch = collapsed.match(addRe);
    expect(addMatch).not.toBeNull();
    expect(collapsed.indexOf(addMatch[0])).toBeGreaterThan(statusIdx);
  });

  test("404 branch sets loadError to a {gone: true} object so PanelBody can suppress Retry", () => {
    // The visible half: without the gone flag, PanelBody renders a Retry
    // button that would just re-404. The object shape distinguishes the
    // deleted-issue case from transient errors (which stay as strings and
    // keep the Retry button).
    const body = extractBeadsIssueViewSource();
    const collapsed = body.replace(/\s+/g, " ");
    // Two setLoadError({..., gone: true}) sites — one in the guard, one in
    // the 404 branch. Both matter: the guard preserves the notice across
    // refreshNonce bumps, the 404 branch installs it the first time.
    const goneSites = collapsed.match(
      /setLoadError\s*\(\s*\{[^}]*gone\s*:\s*true[^}]*\}\s*\)/g,
    );
    expect(goneSites).not.toBeNull();
    expect(goneSites.length).toBeGreaterThanOrEqual(2);
  });

  test("404 branch does NOT call showToast — external deletion must not spam every client", () => {
    // The bug produced 589 x 404 across 4 IPs (local + 3 mobile) in 8h.
    // Toasting each one would have quadrupled the UX pain. Isolate the 404
    // branch specifically — the transient-error branch (the rest of the
    // catch block) legitimately DOES toast, so a whole-effect scan would
    // false-positive. mitto-7gta.17 slice S3: the branch guard is now
    // isNotFoundError(err), not res.status === 404.
    const body = extractBeadsIssueViewSource();
    // Extract from "isNotFoundError(err)" up to the branch's closing
    // `return;` so we only look inside that branch's body. The 404 branch
    // ends with a bare `return;` (early-exit); the sibling catch-all code
    // that legitimately calls showToast lives strictly after that boundary.
    const idx = body.search(/isNotFoundError\s*\(\s*err\s*\)/);
    expect(idx).toBeGreaterThan(-1);
    const rest = body.slice(idx);
    const endMatch = rest.match(/\breturn\s*;/);
    expect(endMatch).not.toBeNull();
    const branchBody = rest.slice(0, endMatch.index + endMatch[0].length);
    expect(branchBody).not.toMatch(/showToast\s*\(/);
  });
});

// =============================================================================
// mitto-9vh: BeadsDetailPanelBody must suppress Retry when loadError.gone
// =============================================================================
//
// Consumer half of the fix: PanelBody's isLoading branch renders loadError
// via a small alert with an optional Retry button. The bug fix widens
// loadError to `string | {message, gone: true}`; PanelBody must normalize
// the shape (so `[object Object]` never leaks to the UI) AND drop the Retry
// button when gone=true (retrying would just re-404 the deleted issue).

describe("mitto-9vh: BeadsDetailPanelBody suppresses Retry on gone loadError", () => {
  const PANEL_BODY_PATH = resolve(
    __dirname_bv,
    "beads",
    "detail",
    "PanelBody.js",
  );
  const source = readFileSync(PANEL_BODY_PATH, "utf8");

  test("normalizes loadError to a string message (handles both string and object shapes)", () => {
    // Without the normalization, an object loadError renders as
    // "[object Object]" inside the alert <span>. The fix computes an errMsg
    // local that reads .message when loadError is an object, else the raw
    // string.
    const collapsed = source.replace(/\s+/g, " ");
    expect(collapsed).toMatch(
      /const\s+errMsg\s*=\s*loadError\s*&&\s*typeof\s+loadError\s*===\s*"object"\s*\?\s*loadError\.message\s*:\s*loadError/,
    );
  });

  test("computes errGone from the loadError.gone flag", () => {
    // The flag drives Retry suppression below. Must be a coerced boolean
    // (!!) so a truthy-but-non-boolean payload is safe.
    const collapsed = source.replace(/\s+/g, " ");
    expect(collapsed).toMatch(
      /const\s+errGone\s*=\s*!!\s*\(\s*loadError\s*&&\s*typeof\s+loadError\s*===\s*"object"\s*&&\s*loadError\.gone\s*\)/,
    );
  });

  test("Retry button render is gated on `onRetry && !errGone`, not `onRetry` alone", () => {
    // The regression bar: a naive `onRetry ? Retry : null` would still show
    // Retry for a deleted issue, whose only purpose would be to re-404. The
    // gate must also consult errGone.
    const collapsed = source.replace(/\s+/g, " ");
    expect(collapsed).toMatch(/onRetry\s*&&\s*!\s*errGone/);
    // And the pre-fix ternary — `onRetry ? html\`<button` — must NOT exist
    // as the ONLY guard anywhere in the file. Allow the phrase in comments,
    // but the JSX site must include the errGone conjunction.
    // (Structural: the two-clause form above IS the JSX site; the negative
    // assertion here would false-positive on the errGone form itself, so
    // asserting the presence of the correct form is sufficient.)
  });

  test("alert renders errMsg (the normalized string), not raw loadError", () => {
    // If PanelBody kept rendering ${loadError} directly, an object shape
    // would print "[object Object]" — regressing the user-visible message.
    // Assert the span reads errMsg.
    const collapsed = source.replace(/\s+/g, " ");
    expect(collapsed).toMatch(/<span>\s*\$\{\s*errMsg\s*\}\s*<\/span>/);
    // And it must NOT render the raw ${loadError} anywhere in the isLoading
    // alert body. Scope to just the isLoading branch: from "if (isLoading)"
    // to the first "return html`" that closes it.
    const isLoadingIdx = source.indexOf("if (isLoading)");
    expect(isLoadingIdx).toBeGreaterThan(-1);
    // Take a bounded window (roughly the length of the loading skeleton
    // block; PanelBody.js line ~98-170 in the current implementation).
    const branch = source.slice(isLoadingIdx, isLoadingIdx + 3000);
    expect(branch).not.toMatch(/<span>\s*\$\{\s*loadError\s*\}\s*<\/span>/);
  });
});

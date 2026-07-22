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
    pos:
      state.pos < state.history.length - 1 ? state.pos + 1 : state.pos,
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
    const afterStart = source.indexOf("\nfunction ", startIdx + startMarker.length);
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
    const afterStart = source.indexOf("\nfunction ", startIdx + startMarker.length);
    const endIdx = afterStart === -1 ? source.length : afterStart;
    const body = source.slice(startIdx, endIdx);

    // The buggy gate — collapse whitespace so line breaks / formatting do not
    // hide it from the assertion.
    const collapsed = body.replace(/\s+/g, " ");
    expect(collapsed).not.toMatch(/if\s*\(\s*!\s*h\.creating\s*&&\s*!\s*h\.data\s*\)\s*return\s+null/);
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
    const afterStart = source.indexOf("\n  const handle", startIdx + marker.length);
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
      expect(beadsUtils.isBeadsSchemaSkew({ error: "boom", code: "bd_failed" })).toBe(false);
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
        },
      };
      expect(beadsUtils.toSchemaSkewState(data)).toEqual({
        message: "The beads database at /x/.beads needs migration",
        dbPath: "/x/.beads",
        hint: "run bd migrate",
        options: ["allow_migrate_from_ui"],
      });
    });

    test("toSchemaSkewState tolerates missing details", () => {
      const state = beadsUtils.toSchemaSkewState({ error: "msg", code: "beads_schema_skew" });
      expect(state.message).toBe("msg");
      expect(state.dbPath).toBe("");
      expect(state.hint).toBe("");
      expect(state.options).toEqual([]);
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
      const second = source.indexOf("const confirmDeleteIssue = useCallback", first + 1);
      expect(second).toBeGreaterThan(-1);
      const nextIdx = source.indexOf("\n  const ", second + 30);
      const body = source.slice(second, nextIdx > second ? nextIdx : second + 8000);
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
      const second = source.indexOf("const handleToggleStatus = useCallback", first + 1);
      expect(second).toBeGreaterThan(-1);
      const nextIdx = source.indexOf("\n  const ", second + 30);
      const body = source.slice(second, nextIdx > second ? nextIdx : second + 4000);
      expect(body).toMatch(/isBeadsSchemaSkew\s*\(/);
    });

    test("main-view handleToggleDefer branches on isBeadsSchemaSkew", () => {
      const first = source.indexOf("const handleToggleDefer = useCallback");
      const second = source.indexOf("const handleToggleDefer = useCallback", first + 1);
      expect(second).toBeGreaterThan(-1);
      const nextIdx = source.indexOf("\n  const ", second + 30);
      const body = source.slice(second, nextIdx > second ? nextIdx : second + 4000);
      expect(body).toMatch(/isBeadsSchemaSkew\s*\(/);
    });

    test("handleAddDependencyEdge branches on isBeadsSchemaSkew", () => {
      const body = extractHandlerBody("const handleAddDependencyEdge = useCallback");
      expect(body).toMatch(/isBeadsSchemaSkew\s*\(/);
    });
  });
});

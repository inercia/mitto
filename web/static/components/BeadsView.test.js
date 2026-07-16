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
